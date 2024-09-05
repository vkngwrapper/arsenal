package vam

import (
	"fmt"
	"github.com/vkngwrapper/arsenal/memutils/defrag"
	"github.com/vkngwrapper/core/v2/common"
	"golang.org/x/exp/slog"
	"math"
	"sync"
	"unsafe"
)

// DefragmentationFlags is a set of bitflags that specify behavior for the DefragmentationContext
type DefragmentationFlags uint32

const (
	// DefragmentationFlagAlgorithmFast indicates that the DefragmentationContext should use a "fast"
	// algorithm that is less thorough about compacting data within blocks, but requires fewer passes
	// to complete a full run. It is not compatible with DefragmentationFlagAlgorithmFull
	DefragmentationFlagAlgorithmFast DefragmentationFlags = 1 << iota
	// DefragmentationFlagAlgorithmFull indicates that the DefragmentationContext should use an algorithm
	// that is somewhat slower than DefragmentationFlagAlgorithmFast but will compact memory within blocks,
	// allowing subsequent passes to compact memory across blocks to use the space that was just freed up.
	//
	// This is the default algorithm if none is specified. It is not compatible with DefragmentationFlagAlgorithmFast
	DefragmentationFlagAlgorithmFull

	DefragmentationFlagAlgorithmMask = DefragmentationFlagAlgorithmFast |
		DefragmentationFlagAlgorithmFull
)

var defragmentationFlagsMapping = map[DefragmentationFlags]string{
	DefragmentationFlagAlgorithmFast: "DefragmentationFlagAlgorithmFast",
	DefragmentationFlagAlgorithmFull: "DefragmentationFlagAlgorithmFull",
}

func (f DefragmentationFlags) String() string {
	return defragmentationFlagsMapping[f]
}

// DefragmentationInfo is used to specify options for a defragmentation run when populating a
// DefragmentationContext.
type DefragmentationInfo struct {
	// Flags specifies optional DefragmentationFlags
	Flags DefragmentationFlags
	// Pool indicates a custom memory pool to defragment. This is usually nil, in which case the
	// Allocator will be defragmented
	Pool *Pool

	// MaxBytesPerPass is the maximum number of bytes to relocate in each pass. This can be used to restrict the amount of compute
	// and GPU that will be spent on a single pass of the defragmentation algorithm. If one pass is performed per
	// frame (or some other mechanism to limit throughput), then the amount of resources spent on the defragmentation
	// process can be controlled.
	MaxBytesPerPass int
	// MaxAllocationsPerPass is the maximum number of Allocation objects to relocate in each pass. Since the number
	// of relocations is almost a direct proxy for the number of go allocations made, this is an important value
	// for managing go memory throughput and CPU usage spent on the defragmentation process.
	MaxAllocationsPerPass int
}

type lockOperation struct {
	alloc     *Allocation
	waitGroup *sync.WaitGroup
}

var lockAllocationChan = make(chan lockOperation, 50)

func asyncLockMutexes() {
	for lockOp := range lockAllocationChan {
		lockOp.alloc.mapLock.Lock()
		lockOp.waitGroup.Done()
	}
}

func init() {
	for i := 0; i < 5; i++ {
		go asyncLockMutexes()
	}
}

// DefragmentationContext is an object that represents a single run of the defragmentation algorithm, although
// that run will consist of multiple passes that may be spread out over an extended period of time. This object
// is populated by Allocator.BeginDefragmentation. DefragmentationContext objects can be reused for multiple defragmentation
// runs in order to reduce allocations, if desired.
type DefragmentationContext struct {
	MaxPassBytes       int
	MaxPassAllocations int

	context           []defrag.MetadataDefragContext[Allocation]
	logger            *slog.Logger
	blockListProgress int
	pass              defrag.PassContext
	stats             defrag.DefragmentationStats
}

func (c *DefragmentationContext) init(o *DefragmentationInfo) {
	c.MaxPassBytes = o.MaxBytesPerPass
	c.MaxPassAllocations = o.MaxAllocationsPerPass

	if c.MaxPassBytes == 0 {
		c.MaxPassBytes = math.MaxInt
	}

	if c.MaxPassAllocations == 0 {
		c.MaxPassAllocations = math.MaxInt
	}

	algorithm := o.Flags & DefragmentationFlagAlgorithmMask

	for index := range c.context {
		if c.context[index].BlockList == nil {
			continue
		}

		c.context[index].Handler = c.completePassForMove

		switch algorithm {
		case DefragmentationFlagAlgorithmFast:
			c.context[index].Algorithm = defrag.AlgorithmFast
		case DefragmentationFlagAlgorithmFull:
			c.context[index].Algorithm = defrag.AlgorithmFull
		default:
			panic(fmt.Sprintf("unknown defragmentation algorithm: %s", algorithm.String()))
		}

		c.context[index].Init()
	}
}

func (c *DefragmentationContext) initForPool(pool *Pool, o *DefragmentationInfo) {
	c.context = []defrag.MetadataDefragContext[Allocation]{
		{
			BlockList: &pool.blockList,
		},
	}
	c.logger = pool.logger
	pool.blockList.incrementalSort = false
	pool.blockList.SortByFreeSize()
	c.init(o)
}

func (c *DefragmentationContext) initForAllocator(allocator *Allocator, o *DefragmentationInfo) {
	c.context = make([]defrag.MetadataDefragContext[Allocation], common.MaxMemoryTypes)
	c.logger = allocator.logger

	for index := range c.context {
		if allocator.memoryBlockLists[index] != nil {
			c.context[index].BlockList = allocator.memoryBlockLists[index]
			allocator.memoryBlockLists[index].incrementalSort = false
			allocator.memoryBlockLists[index].SortByFreeSize()
		}
	}

	c.init(o)
}

// BeginDefragPass collects a number of relocations to be performed for a single pass of the defragmentation
// run and returns those relocations. Before returning, the Allocation objects being relocated
// (defrag.DefragmentationMove.SrcAllocation) will be write-locked, causing any device-memory-accessing
// method calls to block until EndDefragPass is called. Before calling EndDefragPass, the caller
// should copy the memory data from SrcAllocation to DstTmpAllocation and rebind any relevant resources
// (buffers, images, etc.) to the DstTmpAllocation. If it is necessary to map SrcAllocation's
// memory in order to accomplish that, then MapSourceAllocation and UnmapSourceAllocation are available for use, since Allocation.Map will
// block forever.
//
// Alternatively, defrag.DefragmentationMove.MoveOperation can be set to defrag.DefragmentationMoveIgnore or
// defrag.DefragmentationMoveDestroy instead to prevent the relocation or simply destroy SrcAllocation without
// moving it.
func (c *DefragmentationContext) BeginDefragPass() []defrag.DefragmentationMove[Allocation] {
	c.logger.Debug("DefragmentationContext::BeginDefragPass")

	c.pass = defrag.PassContext{
		MaxPassBytes:       c.MaxPassBytes,
		MaxPassAllocations: c.MaxPassAllocations,
	}

	var moves []defrag.DefragmentationMove[Allocation]

	for ; c.blockListProgress < len(c.context); c.blockListProgress++ {
		if c.context[c.blockListProgress].BlockList == nil {
			continue
		}

		if c.context[c.blockListProgress].BlockListCollectMoves(&c.pass) {
			break
		}

		moves = c.context[c.blockListProgress].Moves()

		if len(moves) > 0 {
			break
		}
	}

	var wg sync.WaitGroup
	for _, move := range moves {
		// Waiting on several goroutines that will live like 100ns is slow, so only do this async if
		// there's actually something to wait on
		if !move.SrcAllocation.mapLock.TryLock() {
			wg.Add(1)
			lockAllocationChan <- lockOperation{
				alloc:     move.SrcAllocation,
				waitGroup: &wg,
			}
		}
	}
	wg.Wait()
	return moves
}

// EndDefragPass will complete the relocation of the allocations collected in BeginDefragPass, inject
// DstTmpAllocation's data into SrcAllocation (so the old Allocation object can continue to be used),
// release the write lock on SrcAllocation, and free the old memory that was relocated.
//
// This method may return any error that Allocation.Unmap does if any of the various Unmap operations
// it performs fail. Otherwise, it will return true if the defragmentation run has ended after this pass,
// or false if additional passes are necessary.
func (c *DefragmentationContext) EndDefragPass() (bool, error) {
	c.logger.Debug("DefragmentationContext::EndDefragPass")

	if c.blockListProgress >= len(c.context) {
		return true, nil
	}

	if len(c.context[c.blockListProgress].Moves()) == 0 {
		return true, nil
	}

	err := c.context[c.blockListProgress].BlockListCompletePass(&c.pass)
	c.stats.Add(c.pass.Stats)

	return false, err
}

// Finish performs some vital cleanup duties after the last defragmentation pass has run. This
// should be called whenever EndDefragPass returns true.
func (c *DefragmentationContext) Finish(outStats *defrag.DefragmentationStats) {
	c.logger.Debug("DefragmentationContext::Finish")

	if outStats != nil {
		*outStats = c.stats
	}

	for index := range c.context {
		if c.context[index].BlockList != nil {
			blockList := c.context[index].BlockList.(*memoryBlockList)
			blockList.incrementalSort = true
		}
	}
}

func (c *DefragmentationContext) completePassForMove(move defrag.DefragmentationMove[Allocation]) error {
	switch move.MoveOperation {
	case defrag.DefragmentationMoveCopy:
		err := move.SrcAllocation.swapBlockAllocation(move.DstTmpAllocation)
		move.SrcAllocation.mapLock.Unlock()
		if err != nil {
			return err
		}

	case defrag.DefragmentationMoveDestroy:
		move.SrcAllocation.mapLock.Unlock()
		err := move.SrcAllocation.free()
		if err != nil {
			panic(fmt.Sprintf("failed to free source allocation on Destroy move: %+v", err))
		}
	default:
		move.SrcAllocation.mapLock.Unlock()
	}

	err := move.DstTmpAllocation.free()
	if err != nil {
		panic(fmt.Sprintf("failed to free temporary defrag allocation: %+v", err))
	}

	return nil
}

// MapSourceAllocation is roughly equivalent to calling Allocation.Map- however, Allocation.Map cannot be called
// on an Allocation object in the midst of being relocated as part of a defragmentation pass, because
// a write lock has been taken out on the Allocation. If it is necessary to map data as part of the relocation
// process, use this method. Because this ignores Allocation thread-safety primitives, calling this on
// an Allocation that is not currently being relocated by this DefragmentationContext is dangerous.
func (c *DefragmentationContext) MapSourceAllocation(alloc *Allocation) (unsafe.Pointer, common.VkResult, error) {
	return alloc.mapOptionalLock(false)
}

// UnmapSourceAllocation should be called after MapSourceAllocation to clean up the mapping
func (c *DefragmentationContext) UnmapSourceAllocation(alloc *Allocation) error {
	return alloc.memory.Unmap(1)
}
