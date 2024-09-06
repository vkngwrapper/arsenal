package defrag

import (
	"errors"
	"fmt"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"math"
)

// Algorithm identifies which defragmentation algorithm will be used for defrag passes
type Algorithm uint32

const (
	// AlgorithmFast indicates that the defragmentation run should use a "fast"
	// algorithm that is less thorough about compacting data within blocks, but requires fewer passes
	// to complete a full run.
	AlgorithmFast Algorithm = iota + 1
	// AlgorithmFull indicates that the defragmentation run should use an algorithm
	// that is somewhat slower than DefragmentationFlagAlgorithmFast but will compact memory within blocks,
	// allowing subsequent passes to compact memory across blocks to use the space that was just freed up.
	//
	// This is the default algorithm if none is specified.
	AlgorithmFull
)

var algorithmMapping = map[Algorithm]string{
	AlgorithmFast: "AlgorithmFast",
	AlgorithmFull: "AlgorithmFull",
}

func (a Algorithm) String() string {
	return algorithmMapping[a]
}

// DefragmentationStats contains basic metrics for defragmentation over time
type DefragmentationStats struct {
	// BytesMoved is the number of bytes that have been successfully relocated
	BytesMoved int
	// BytesFreed is the number of bytes that have been freed: bear in mind that relocating an allocation doesn't necessarily
	// free its memory- only if the defragmentation run completely frees up a block of memory and the
	// BlockList chooses to free it will this value increase
	BytesFreed int
	// AllocationsMoved is the number of successful relocations
	AllocationsMoved int
	// DeviceMemoryBlocksFreed is the number of memory pages that the BlockList has chosen to free as a consequence
	// of relocating allocations out of the block
	DeviceMemoryBlocksFreed int
}

func (s *DefragmentationStats) Add(stats DefragmentationStats) {
	s.BytesMoved += stats.BytesMoved
	s.BytesFreed += stats.BytesFreed
	s.AllocationsMoved += stats.AllocationsMoved
	s.DeviceMemoryBlocksFreed += stats.DeviceMemoryBlocksFreed
}

// MetadataDefragContext is the core of the defragmentation logic for memutils. One of these must be created
// and initialized for each defragmentation run, which will then consist of multiple passes
type MetadataDefragContext[T any] struct {
	// Algorithm is the defragmentation algorithm that should be used
	Algorithm Algorithm
	// Handler is a method that will be called to complete each relocation as part of BlockListCompletePass
	Handler DefragmentOperationHandler[T]
	// BlockList is the memory object this context exists to defragment
	BlockList BlockList[T]

	moves         []DefragmentationMove[T]
	ignoredAllocs int

	immovableBlockCount int

	// scratchStats exists to avoid allocating statistics objects when passing them in to be populated
	// because we pass them to an interface so the escape analyzer will get annoying about it
	scratchStats memutils.Statistics
}

// Init sets up this MetadataDefragContext to be used in a fresh defragmentation run. MetadataDefragContext can
// be reused for multiple runs, as long as this method is called prior to beginning each run, including the first
func (c *MetadataDefragContext[T]) Init() error {
	if c.BlockList == nil {
		panic("attempted to init defragmentation context without a block list")
	}

	for index := 0; index < c.BlockList.BlockCount(); index++ {
		mtData := c.BlockList.MetadataForBlock(index)
		if !mtData.SupportsRandomAccess() {
			return errors.New("attempted to defragment a BlockList that does not support random access- non-random access allocators such as Linear allocators cannot be and do not need to be defragmented")
		}
	}

	if c.Algorithm == 0 {
		c.Algorithm = AlgorithmFull
	}

	return nil
}

// BlockListCompletePass should be called after a defragmentation pass has been worked: BlockListCollectMoves
// have been called, we have copied data over for all relocation operations found, and the set of DefragmentationMove
// operations have had their operation type changed away from DefragmentationMoveCopy if necessary.
//
// This method will clean up the pass by incrementing DefragmentationStats, calling MetadataDefragContext.Handler
// for each operation, and reorganizing free blocks within the BlockList if necessary. This method will return
// any errors returned from MetadataDefragContext.Handler as a combined error with errors.Join. It will also
// return a boolean- false if there is more defragmentation work to perform, true if the run is now complete.
//
// stateIndex - the index of blockList within the list of all blockLists being defragmented
//
// blockList - The memory pool to complete defragmentation of
func (c *MetadataDefragContext[T]) BlockListCompletePass(pass *PassContext) error {
	immovableBlocks := make(map[metadata.BlockMetadata]struct{}, c.BlockList.BlockCount()-c.immovableBlockCount)

	var allErrors []error

	for i := 0; i < len(c.moves); i++ {
		move := c.moves[i]

		c.scratchStats = memutils.Statistics{}
		c.BlockList.AddStatistics(&c.scratchStats)
		prevCount := c.scratchStats.BlockCount
		prevBytes := c.scratchStats.BlockBytes

		err := c.Handler(move)
		if err != nil {
			allErrors = append(allErrors, err)
			continue
		}

		c.scratchStats = memutils.Statistics{}
		c.BlockList.AddStatistics(&c.scratchStats)
		pass.Stats.DeviceMemoryBlocksFreed += prevCount - c.scratchStats.BlockCount
		pass.Stats.BytesFreed += prevBytes - c.scratchStats.BlockBytes

		switch move.MoveOperation {
		case DefragmentationMoveIgnore:
			pass.Stats.BytesMoved -= move.Size
			pass.Stats.AllocationsMoved--
			immovableBlocks[move.SrcBlockMetadata] = struct{}{}

		case DefragmentationMoveDestroy:
			pass.Stats.BytesMoved -= move.Size
			pass.Stats.AllocationsMoved--
		}
	}

	// Move blocks with immovable allocations according to algorithm
	if len(immovableBlocks) > 0 {
		// Move to the beginning
		for block := range immovableBlocks {
			c.swapImmovableBlocks(block)
		}
	}

	c.moves = c.moves[:0]

	if len(allErrors) == 1 {
		return allErrors[0]
	}

	if len(allErrors) > 0 {
		return errors.Join(allErrors...)
	}

	return nil
}

func (c *MetadataDefragContext[T]) swapImmovableBlocks(mtdata metadata.BlockMetadata) {
	c.BlockList.Lock()
	defer c.BlockList.Unlock()

	for i := c.immovableBlockCount; i < c.BlockList.BlockCount(); i++ {
		if c.BlockList.MetadataForBlock(i) == mtdata {
			c.BlockList.SwapBlocks(i, c.immovableBlockCount)
			c.immovableBlockCount++
		}
	}
}

// BlockListCollectMoves will retrieve a single pass's worth of DefragmentationMove operations to be completed.
// Those operations can be retrieved from MetadataDefragContext.Moves.
//
// stateIndex - the index of blockList within the larger set of BlockList objects being defragmented in this run
//
// blockList - the memory pool to collect relocation operations for
func (c *MetadataDefragContext[T]) BlockListCollectMoves(pass *PassContext) bool {
	c.BlockList.Lock()
	defer c.BlockList.Unlock()

	if c.BlockList.BlockCount() > 1 {
		switch c.Algorithm {
		case AlgorithmFast:
			return c.walkSuballocations(pass, c.defragFastSuballocHandler)
		case AlgorithmFull:
			return c.walkSuballocations(pass, c.defragFullSuballocHandler)
		default:
			panic(fmt.Sprintf("attempted to defragment with unknown algorithm: %s", c.Algorithm.String()))
		}
	} else if c.BlockList.BlockCount() == 1 && c.Algorithm != AlgorithmFast {
		return c.walkSuballocations(pass, c.reallocSuballocHandler)
	}

	return false
}

// Moves returns the list of relocation operations most recently collected with BlockListCollectMoves
func (c *MetadataDefragContext[T]) Moves() []DefragmentationMove[T] {
	return c.moves
}

func (c *MetadataDefragContext[T]) mustBeginAllocationList(mtdata metadata.BlockMetadata) metadata.BlockAllocationHandle {
	handle, err := mtdata.AllocationListBegin()
	if err != nil {
		panic(fmt.Sprintf("unexpected error when getting first allocation: %+v", err))
	}

	return handle
}

func (c *MetadataDefragContext[T]) mustFindNextAllocation(mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle) metadata.BlockAllocationHandle {
	handle, err := mtdata.FindNextAllocation(handle)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when getting next allocation: %+v", err))
	}

	return handle
}

func (c *MetadataDefragContext[T]) mustFindNextFreeRegionSize(mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle) int {
	size, err := mtdata.FindNextFreeRegionSize(handle)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when getting next free region size: %+v", err))
	}

	return size
}

func (c *MetadataDefragContext[T]) mustFindOffset(mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle) int {
	offset, err := mtdata.AllocationOffset(handle)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when getting allocation offset: %+v", err))
	}

	return offset
}

func (c *MetadataDefragContext[T]) getMoveData(handle metadata.BlockAllocationHandle, mtdata metadata.BlockMetadata) (MoveAllocationData[T], bool) {
	userData, err := mtdata.AllocationUserData(handle)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when retrieving allocation user data: %+v", err))
	}

	if userData == c {
		return MoveAllocationData[T]{}, true
	}

	return c.BlockList.MoveDataForUserData(userData), false
}

func (c *MetadataDefragContext[T]) allocFromBlock(blockIndex int, mtData metadata.BlockMetadata, size int, alignment uint, flags uint32, userData any, suballocType uint32, outAlloc *T) (common.VkResult, error) {
	success, currRequest, err := mtData.CreateAllocationRequest(size, alignment, false, suballocType, 0, math.MaxInt)
	if err != nil {
		return core1_0.VKErrorUnknown, err
	} else if !success {
		return core1_0.VKErrorOutOfDeviceMemory, nil
	}

	return c.BlockList.CommitDefragAllocationRequest(currRequest, blockIndex, alignment, flags, userData, suballocType, outAlloc)
}

func (c *MetadataDefragContext[T]) allocInOtherBlock(start, end int, data *MoveAllocationData[T]) bool {
	for ; start < end; start++ {
		dstMetadata := c.BlockList.MetadataForBlock(start)
		if dstMetadata.MayHaveFreeBlock(data.SuballocationType, data.Move.Size) {
			data.Move.DstTmpAllocation = c.BlockList.CreateAlloc()
			res, err := c.allocFromBlock(start, dstMetadata,
				data.Move.Size,
				data.Alignment,
				data.Flags,
				c,
				data.SuballocationType,
				data.Move.DstTmpAllocation)
			if err != nil {
				panic(fmt.Sprintf("unexpected error while allocating: %+v", err))
			} else if res == core1_0.VKSuccess {
				data.Move.DstBlockMetadata = dstMetadata
				c.moves = append(c.moves, data.Move)
				return true
			}
		}
	}

	return false
}

type walkHandler[T any] func(pass *PassContext, blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData MoveAllocationData[T]) bool

func (c *MetadataDefragContext[T]) walkSuballocations(pass *PassContext, suballocHandler walkHandler[T]) bool {
	// Go through allocation in last blocks and try to fit them inside first ones
	for blockIndex := c.BlockList.BlockCount() - 1; blockIndex > c.immovableBlockCount; blockIndex-- {
		mtdata := c.BlockList.MetadataForBlock(blockIndex)

		for handle := c.mustBeginAllocationList(mtdata); handle != metadata.NoAllocation; handle = c.mustFindNextAllocation(mtdata, handle) {
			moveData, immobile := c.getMoveData(handle, mtdata)
			if immobile {
				continue
			}

			counter := pass.checkCounters(moveData.Move.Size)
			switch counter {
			case defragCounterIgnore:
				continue
			case defragCounterEnd:
				return true
			case defragCounterPass:
				break
			default:
				panic(fmt.Sprintf("unexpected defrag counter status: %s", counter.String()))
			}

			if suballocHandler(pass, blockIndex, mtdata, handle, moveData) {
				return true
			}
		}
	}

	return false
}

func (c *MetadataDefragContext[T]) allocIfLowerOffset(offset int, blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData *MoveAllocationData[T]) bool {
	success, allocRequest, err := mtdata.CreateAllocationRequest(
		moveData.Move.Size,
		moveData.Alignment,
		false,
		moveData.SuballocationType,
		metadata.AllocationStrategyMinOffset,
		offset,
	)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when populating allocation request for defrag: %+v", err))
	}

	if success && c.mustFindOffset(mtdata, allocRequest.BlockAllocationHandle) < offset {
		moveData.Move.DstTmpAllocation = c.BlockList.CreateAlloc()
		res, err := c.BlockList.CommitDefragAllocationRequest(
			allocRequest,
			blockIndex,
			moveData.Alignment,
			moveData.Flags,
			c,
			moveData.SuballocationType,
			moveData.Move.DstTmpAllocation,
		)
		if err == nil {
			moveData.Move.DstBlockMetadata = mtdata
			c.moves = append(c.moves, moveData.Move)
			return true
		} else if res == core1_0.VKErrorUnknown {
			panic(fmt.Sprintf("unexpected error when commiting allocation request for defragment: %+v", err))
		}
	}

	return false
}

func (c *MetadataDefragContext[T]) reallocSuballocHandler(pass *PassContext, blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData MoveAllocationData[T]) bool {
	offset := c.mustFindOffset(mtdata, handle)
	if offset != 0 && mtdata.MayHaveFreeBlock(moveData.SuballocationType, moveData.Move.Size) {
		if c.allocIfLowerOffset(offset, blockIndex, mtdata, handle, &moveData) {
			return pass.incrementCounters(moveData.Move.Size)
		}
	}

	return false
}

func (c *MetadataDefragContext[T]) defragFastSuballocHandler(pass *PassContext, blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData MoveAllocationData[T]) bool {
	if blockIndex == 0 {
		return true
	}

	success := c.allocInOtherBlock(0, blockIndex, &moveData)
	if !success {
		return false
	}

	// Have we crossed our threshold for this pass?
	return pass.incrementCounters(moveData.Move.Size)
}

func (c *MetadataDefragContext[T]) defragFullSuballocHandler(pass *PassContext, blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData MoveAllocationData[T]) bool {
	// Check all previous blocks for free space
	prevMoveCount := len(c.moves)
	if blockIndex > 0 && c.allocInOtherBlock(0, blockIndex, &moveData) {
		return pass.incrementCounters(moveData.Move.Size)
	}

	// If no room found then realloc within block for lower offset
	offset := c.mustFindOffset(mtdata, handle)
	if prevMoveCount == len(c.moves) &&
		offset > 0 &&
		mtdata.MayHaveFreeBlock(moveData.SuballocationType, moveData.Move.Size) {

		if c.allocIfLowerOffset(offset, blockIndex, mtdata, handle, &moveData) {
			return pass.incrementCounters(moveData.Move.Size)
		}
	}

	return false
}
