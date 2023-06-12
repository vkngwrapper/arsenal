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
	// Bytesfreed is the number of bytes that have been freed: bear in mind that relocating an allocation doesn't necessarily
	// free its memory- only if the defragmentation run completely frees up a block of memory and the
	// BlockList chooses to free it will this value increase
	BytesFreed int
	// AllocationsMoved is the number of successful relocations
	AllocationsMoved int
	// DeviceMemoryBlocksFreed is the number of memory pages that the BlockList has chosen to free as a consequence
	// of relocating allocations out of the block
	DeviceMemoryBlocksFreed int
}

// MetadataDefragContext is the core of the defragmentation logic for memutils. One of these must be created
// and initialized for each defragmentation run, which will then consist of multiple passses
type MetadataDefragContext[T any] struct {
	// MaxPassBytes is the maximum number of bytes to relocate in each pass. There is no guarantee that
	// this many bytes will actually be relocated in any given pass, based on how easy it is to find additional
	// relocations to fit within the budget
	MaxPassBytes int
	// MaxPassAllocations is the maximum number of relocations to perform in each pass. There is no guarantee
	// that this many allocations will actually be relocated in any givne pass, based on how easy it is to find
	// additional relocations to fit within the budget. This value is a close proxy for the number of go allocations
	// made for each pass, so managing this and MaxPassBytes can help you control memory and cpu throughput of the
	// defragmentation process.
	MaxPassAllocations int
	// Algorithm is the defragmentation algorithm that should be used
	Algorithm Algorithm
	// Handler is a method that will be called to complete each relocation as part of BlockListCompletePass
	Handler DefragmentOperationHandler[T]

	moves         []DefragmentationMove[T]
	ignoredAllocs int

	immovableBlockCount int

	globalStats  DefragmentationStats
	passStats    DefragmentationStats
	scratchStats memutils.Statistics
}

// Init sets up this MetadataDefragContext to be used in a fresh defragmentation run. MetadataDefragContext can
// be reused for multiple runs, as long as this method is called prior to beginning the run
//
// blockListCount - the number of individual BlockList objects that are being defragmented by this process
func (c *MetadataDefragContext[T]) Init(blockListCount int) {
	if c.MaxPassBytes == 0 {
		c.MaxPassBytes = math.MaxInt
	}
	if c.MaxPassAllocations == 0 {
		c.MaxPassAllocations = math.MaxInt
	}

	if c.Algorithm == 0 {
		c.Algorithm = AlgorithmFull
	}
}

// PopulateStats retrieves the DefragmentationStats representing defragmentation metrics for the entire current
// run so far.
//
// stats - The DefragmentationStats object whose fields are being overwritten
func (c *MetadataDefragContext[T]) PopulateStats(stats *DefragmentationStats) {
	if stats == nil {
		return
	}

	stats.DeviceMemoryBlocksFreed = c.globalStats.DeviceMemoryBlocksFreed
	stats.BytesFreed = c.globalStats.BytesFreed
	stats.BytesMoved = c.globalStats.BytesMoved
	stats.AllocationsMoved = c.globalStats.AllocationsMoved
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
func (c *MetadataDefragContext[T]) BlockListCompletePass(stateIndex int, blockList BlockList[T]) (bool, error) {
	immovableBlocks := make(map[metadata.BlockMetadata]struct{}, blockList.BlockCount())

	var allErrors []error

	for i := 0; i < len(c.moves); i++ {
		move := c.moves[i]

		c.scratchStats = memutils.Statistics{}
		blockList.AddStatistics(&c.scratchStats)
		prevCount := c.scratchStats.BlockCount
		prevBytes := c.scratchStats.BlockBytes

		err := c.Handler(move)
		if err != nil {
			allErrors = append(allErrors, err)
			continue
		}

		c.scratchStats = memutils.Statistics{}
		blockList.AddStatistics(&c.scratchStats)
		c.passStats.DeviceMemoryBlocksFreed += prevCount - c.scratchStats.BlockCount
		c.passStats.BytesFreed += prevBytes - c.scratchStats.BlockBytes

		switch move.MoveOperation {
		case DefragmentationMoveIgnore:
			c.passStats.BytesMoved -= move.Size
			c.passStats.AllocationsMoved--
			immovableBlocks[move.SrcBlockMetadata] = struct{}{}

		case DefragmentationMoveDestroy:
			c.passStats.BytesMoved -= move.Size
			c.passStats.AllocationsMoved--
		}
	}

	c.globalStats.AllocationsMoved += c.passStats.AllocationsMoved
	c.globalStats.BytesFreed += c.passStats.BytesFreed
	c.globalStats.BytesMoved += c.passStats.BytesMoved
	c.globalStats.DeviceMemoryBlocksFreed += c.passStats.DeviceMemoryBlocksFreed
	c.passStats = DefragmentationStats{}

	// Move blocks iwth immovable allocations according to algorithm
	swappedFreeBlocks := false
	if len(immovableBlocks) > 0 {
		if !swappedFreeBlocks {
			// Move to the beginning
			for blockMetadata := range immovableBlocks {
				c.swapImmovableBlocks(blockList, blockMetadata)
			}
		}
	}

	var outError error
	if len(allErrors) == 1 {
		outError = allErrors[0]
	} else if len(allErrors) > 0 {
		outError = errors.Join(allErrors...)
	}

	if len(c.moves) > 0 || swappedFreeBlocks || outError != nil {
		c.moves = c.moves[:0]
		return false, outError
	}

	return true, nil
}

func (c *MetadataDefragContext[T]) swapImmovableBlocks(blockList BlockList[T], mtdata metadata.BlockMetadata) {
	blockList.Lock()
	defer blockList.Unlock()

	for i := c.immovableBlockCount; i < blockList.BlockCount(); i++ {
		if blockList.MetadataForBlock(i) == mtdata {
			blockList.SwapBlocks(i, c.immovableBlockCount)
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
func (c *MetadataDefragContext[T]) BlockListCollectMoves(stateIndex int, blockList BlockList[T]) {
	if blockList == nil {
		panic("attempted to begin pass with a nil blockList")
	}

	blockList.Lock()
	defer blockList.Unlock()

	if blockList.BlockCount() > 1 {
		c.computeDefragmentation(blockList, stateIndex)
	} else if blockList.BlockCount() == 1 {
		c.reallocWithinBlock(blockList, 0, blockList.MetadataForBlock(0))
	}
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

func (c *MetadataDefragContext[T]) getMoveData(blockList BlockList[T], handle metadata.BlockAllocationHandle, mtdata metadata.BlockMetadata) (MoveAllocationData[T], bool) {
	userData, err := mtdata.AllocationUserData(handle)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when retrieving allocation user data: %+v", err))
	}

	if userData == c {
		return MoveAllocationData[T]{}, true
	}

	return blockList.MoveDataForUserData(userData), false
}

const defragMaxAllocsToIgnore = 16

func (c *MetadataDefragContext[T]) checkCounters(bytes int) defragCounterStatus {
	// Ignore allocation if it will exceed max size for copy
	if c.passStats.BytesMoved+bytes > c.MaxPassBytes {
		c.ignoredAllocs++
		if c.ignoredAllocs < defragMaxAllocsToIgnore {
			return defragCounterIgnore
		} else {
			return defragCounterEnd
		}
	} else {
		c.ignoredAllocs = 0
	}

	return defragCounterPass
}

func (c *MetadataDefragContext[T]) incrementCounters(bytes int) bool {
	c.passStats.BytesMoved += bytes
	c.passStats.AllocationsMoved++

	// Early return when max found
	if c.passStats.AllocationsMoved >= c.MaxPassAllocations || c.passStats.BytesMoved >= c.MaxPassBytes {
		if c.passStats.AllocationsMoved != c.MaxPassAllocations && c.passStats.BytesMoved != c.MaxPassBytes {
			panic(fmt.Sprintf("somehow passed maximum pass thresholds: bytes %d, allocs %d", c.passStats.BytesMoved, c.passStats.AllocationsMoved))
		}

		return true
	}

	return false
}

func (c *MetadataDefragContext[T]) allocFromBlock(blockList BlockList[T], blockIndex int, mtData metadata.BlockMetadata, size int, alignment uint, flags uint32, userData any, suballocType uint32, outAlloc *T) (common.VkResult, error) {
	success, currRequest, err := mtData.CreateAllocationRequest(size, alignment, false, suballocType, 0)
	if err != nil {
		return core1_0.VKErrorUnknown, err
	} else if !success {
		return core1_0.VKErrorOutOfDeviceMemory, nil
	}

	return blockList.CommitDefragAllocationRequest(currRequest, blockIndex, alignment, flags, userData, suballocType, outAlloc)
}

func (c *MetadataDefragContext[T]) allocInOtherBlock(start, end int, data *MoveAllocationData[T], blockList BlockList[T]) bool {
	for ; start < end; start++ {
		dstMetadata := blockList.MetadataForBlock(start)
		if dstMetadata.SumFreeSize() >= data.Move.Size {
			data.Move.DstTmpAllocation = blockList.CreateAlloc()
			res, err := c.allocFromBlock(blockList, start, dstMetadata,
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
				if c.incrementCounters(data.Move.Size) {
					return true
				}
				break
			}
		}
	}

	return false
}

func (c *MetadataDefragContext[T]) walkSuballocations(blockList BlockList[T], blockIndex int, mtdata metadata.BlockMetadata,
	suballocHandler func(blockList BlockList[T], blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData MoveAllocationData[T]) bool) bool {
	for handle := c.mustBeginAllocationList(mtdata); handle != metadata.NoAllocation; handle = c.mustFindNextAllocation(mtdata, handle) {
		moveData, immobile := c.getMoveData(blockList, handle, mtdata)
		if immobile {
			continue
		}

		counter := c.checkCounters(moveData.Move.Size)
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

		if suballocHandler(blockList, blockIndex, mtdata, handle, moveData) {
			return true
		}
	}

	return false
}

func (c *MetadataDefragContext[T]) allocIfLowerOffset(offset int, blockList BlockList[T], blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData *MoveAllocationData[T]) bool {
	success, allocRequest, err := mtdata.CreateAllocationRequest(
		moveData.Move.Size,
		moveData.Alignment,
		false,
		moveData.SuballocationType,
		metadata.AllocationStrategyMinOffset,
	)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when populating allocation request for defrag: %+v", err))
	}

	if success && c.mustFindOffset(mtdata, allocRequest.BlockAllocationHandle) < offset {
		moveData.Move.DstTmpAllocation = blockList.CreateAlloc()
		res, err := blockList.CommitDefragAllocationRequest(
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
			if c.incrementCounters(moveData.Move.Size) {
				return true
			}
		} else if res == core1_0.VKErrorUnknown {
			panic(fmt.Sprintf("unexpected error when commiting allocation request for defragment: %+v", err))
		}
	}

	return false
}

func (c *MetadataDefragContext[T]) reallocSuballocHandler(blockList BlockList[T], blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData MoveAllocationData[T]) bool {
	offset := c.mustFindOffset(mtdata, handle)
	if offset != 0 && mtdata.SumFreeSize() >= moveData.Move.Size {
		return c.allocIfLowerOffset(offset, blockList, blockIndex, mtdata, handle, &moveData)
	}

	return false
}

func (c *MetadataDefragContext[T]) reallocWithinBlock(blockList BlockList[T], blockIndex int, mtdata metadata.BlockMetadata) bool {
	return c.walkSuballocations(blockList, blockIndex, mtdata, c.reallocSuballocHandler)
}

func (c *MetadataDefragContext[T]) computeDefragmentation(blockList BlockList[T], stateIndex int) bool {
	switch c.Algorithm {
	case AlgorithmFast:
		return c.computeDefragmentationFast(blockList)
	case AlgorithmFull:
		return c.computeDefragmentationFull(blockList)
	default:
		panic(fmt.Sprintf("attempted to defragment with unknown algorithm: %s", c.Algorithm.String()))
	}
}

func (c *MetadataDefragContext[T]) defragFastSuballocHandler(blockList BlockList[T], blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData MoveAllocationData[T]) bool {
	return c.allocInOtherBlock(0, blockIndex, &moveData, blockList)
}

func (c *MetadataDefragContext[T]) computeDefragmentationFast(blockList BlockList[T]) bool {
	// Move only between blocks

	// Go through allocation in last blocks and try to fit them inside first ones
	for i := blockList.BlockCount() - 1; i > c.immovableBlockCount; i-- {
		mtdata := blockList.MetadataForBlock(i)

		if c.walkSuballocations(blockList, i, mtdata, c.defragFastSuballocHandler) {
			return true
		}
	}

	return false
}

func (c *MetadataDefragContext[T]) defragFullSuballocHandler(blockList BlockList[T], blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData MoveAllocationData[T]) bool {
	// Check all previous blocks for free space
	prevMoveCount := len(c.moves)
	if c.allocInOtherBlock(0, blockIndex, &moveData, blockList) {
		return true
	}

	// If no room found then realloc within block for lower offset
	offset := c.mustFindOffset(mtdata, handle)
	if prevMoveCount == len(c.moves) &&
		offset != 0 &&
		mtdata.SumFreeSize() >= moveData.Move.Size {

		if c.allocIfLowerOffset(offset, blockList, blockIndex, mtdata, handle, &moveData) {
			return true
		}
	}

	return false
}

func (c *MetadataDefragContext[T]) computeDefragmentationFull(blockList BlockList[T]) bool {
	// Go over every allocation and try to fit it in previous blocks at lowest offsets,
	// if not possible: realloc within single block to minimize offset (exclude offset == 0)

	for i := blockList.BlockCount() - 1; i > c.immovableBlockCount; i-- {
		mtdata := blockList.MetadataForBlock(i)

		if c.walkSuballocations(blockList, i, mtdata, c.defragFullSuballocHandler) {
			return true
		}
	}

	return false
}
