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

type Algorithm uint32

const (
	AlgorithmFast Algorithm = iota + 1
	AlgorithmBalanced
	AlgorithmFull
	AlgorithmExtensive
)

var algorithmMapping = map[Algorithm]string{
	AlgorithmFast:      "AlgorithmFast",
	AlgorithmBalanced:  "AlgorithmBalanced",
	AlgorithmFull:      "AlgorithmFull",
	AlgorithmExtensive: "AlgorithmExtensive",
}

func (a Algorithm) String() string {
	return algorithmMapping[a]
}

type DefragmentationStats struct {
	BytesMoved              int
	BytesFreed              int
	AllocationsMoved        int
	DeviceMemoryBlocksFreed int
}

type MetadataDefragContext[T any] struct {
	MaxPassBytes       int
	MaxPassAllocations int
	Algorithm          Algorithm
	Handler            DefragmentOperationHandler[T]

	moves         []DefragmentationMove[T]
	ignoredAllocs int

	balancedStates      []stateBalanced[T]
	extensiveStates     []stateExtensive
	immovableBlockCount int

	globalStats  DefragmentationStats
	passStats    DefragmentationStats
	scratchStats memutils.Statistics
}

func (c *MetadataDefragContext[T]) Init(blockListCount int) {
	if c.MaxPassBytes == 0 {
		c.MaxPassBytes = math.MaxInt
	}
	if c.MaxPassAllocations == 0 {
		c.MaxPassAllocations = math.MaxInt
	}

	c.balancedStates = make([]stateBalanced[T], blockListCount)
	c.extensiveStates = make([]stateExtensive, blockListCount)
	for i := 0; i < blockListCount; i++ {
		c.balancedStates[i].AverageAllocSize = math.MaxInt
		c.extensiveStates[i].FirstFreeBlock = math.MaxInt
	}

	if c.Algorithm == 0 {
		c.Algorithm = AlgorithmBalanced
	}
}

func (c *MetadataDefragContext[T]) PopulateStats(stats *DefragmentationStats) {
	if stats == nil {
		return
	}

	stats.DeviceMemoryBlocksFreed = c.globalStats.DeviceMemoryBlocksFreed
	stats.BytesFreed = c.globalStats.BytesFreed
	stats.BytesMoved = c.globalStats.BytesMoved
	stats.AllocationsMoved = c.globalStats.AllocationsMoved
}

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

		if c.Algorithm == AlgorithmExtensive {
			// Avoid unnecessary tries to allocate when new free block is available
			state := &c.extensiveStates[stateIndex]
			if state.FirstFreeBlock != math.MaxInt {
				diff := prevCount - c.scratchStats.BlockCount
				if state.FirstFreeBlock >= diff {
					state.FirstFreeBlock -= diff
					if state.FirstFreeBlock != 0 && blockList.MetadataForBlock(state.FirstFreeBlock-1).IsEmpty() {
						state.FirstFreeBlock--
					}
				} else {
					state.FirstFreeBlock = 0
				}
			}
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
		if c.Algorithm == AlgorithmExtensive {
			state := c.extensiveStates[stateIndex]

			if state.Operation != defragmentOperationCleanup {
				for blockMetadata := range immovableBlocks {
					if c.swapFreeImmovableBlocks(blockList, &state, blockMetadata) {
						swappedFreeBlocks = true
					}
				}
			}
		}

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

func (c *MetadataDefragContext[T]) swapFreeImmovableBlocks(blockList BlockList[T], state *stateExtensive, mtdata metadata.BlockMetadata) bool {
	blockList.Lock()
	defer blockList.Unlock()

	count := blockList.BlockCount() - c.immovableBlockCount
	for i := 0; i < count; i++ {
		if blockList.MetadataForBlock(i) == mtdata {
			c.immovableBlockCount++
			blockList.SwapBlocks(i, blockList.BlockCount()-c.immovableBlockCount)
			if state.FirstFreeBlock != math.MaxInt {
				if i+1 < state.FirstFreeBlock {
					state.FirstFreeBlock--
					if state.FirstFreeBlock > 2 {
						blockList.SwapBlocks(i, state.FirstFreeBlock)
					}
				}
			}
			return true
		}
	}

	return false
}

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

func (c *MetadataDefragContext[T]) allocFromBlock(blockList BlockList[T], blockIndex int, mtData metadata.BlockMetadata, size int, alignment uint, flags memutils.AllocationCreateFlags, userData any, suballocType metadata.SuballocationType, outAlloc *T) (common.VkResult, error) {
	isUpperAddress := flags&memutils.AllocationCreateUpperAddress != 0

	success, currRequest, err := mtData.CreateAllocationRequest(size, alignment, isUpperAddress, suballocType, 0)
	if err != nil {
		return core1_0.VKErrorUnknown, err
	} else if !success {
		return core1_0.VKErrorOutOfDeviceMemory, core1_0.VKErrorOutOfDeviceMemory.ToError()
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
			if err == nil {
				data.Move.DstBlockMetadata = dstMetadata
				c.moves = append(c.moves, data.Move)
				if c.incrementCounters(data.Move.Size) {
					return true
				}
				break
			} else if res == core1_0.VKErrorUnknown {
				panic(fmt.Sprintf("unexpected error while allocating: %+v", err))
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
		memutils.AllocationCreateStrategyMinOffset,
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

func (c *MetadataDefragContext[T]) moveDataToFreeBlocks(currentType metadata.SuballocationType, blockList BlockList[T], firstFreeBlock int) (texturePresent, bufferPresent, otherPresent, success bool) {
	prevMoveCount := len(c.moves)
	for i := firstFreeBlock - 1; i > 0; i-- {
		mtdata := blockList.MetadataForBlock(i)

		allocSuccess := c.walkSuballocations(blockList, i, mtdata,
			func(blockList BlockList[T], blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData MoveAllocationData[T]) bool {
				// Move only single type of resources at once
				if !metadata.IsBufferImageGranularityConflict(moveData.SuballocationType, currentType) {
					// Try to fit allocation into free blocks
					if c.allocInOtherBlock(firstFreeBlock, blockList.BlockCount(), &moveData, blockList) {
						return true
					}
				}

				if !metadata.IsBufferImageGranularityConflict(moveData.SuballocationType, metadata.SuballocationImageOptimal) {
					texturePresent = true
				} else if !metadata.IsBufferImageGranularityConflict(moveData.SuballocationType, metadata.SuballocationBuffer) {
					bufferPresent = true
				} else {
					otherPresent = true
				}

				return false
			})

		if allocSuccess {
			return texturePresent, bufferPresent, otherPresent, false
		}
	}

	return texturePresent, bufferPresent, otherPresent, prevMoveCount == len(c.moves)
}

func (c *MetadataDefragContext[T]) computeDefragmentation(blockList BlockList[T], stateIndex int) bool {
	switch c.Algorithm {
	case AlgorithmFast:
		return c.computeDefragmentationFast(blockList)
	case AlgorithmBalanced:
		return c.computeDefragmentationBalanced(blockList, stateIndex, true)
	case AlgorithmFull:
		return c.computeDefragmentationFull(blockList)
	case AlgorithmExtensive:
		return c.computeDefragemntationExtensive(blockList, stateIndex)
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

func (c *MetadataDefragContext[T]) computeDefragmentationBalanced(blockList BlockList[T], stateIndex int, update bool) bool {
	// Go over every allocation and try to fit it in previous blocks at lowest offsets,
	// if not possible: realloc within single block to minimize offset (exclude offset == 0),
	// but only if there are noticeable gaps between them

	state := &c.balancedStates[stateIndex]
	if update && state.AverageAllocSize == math.MaxInt {
		state.UpdateStatistics(blockList)
	}

	startMoveCount := len(c.moves)
	minimalFreeRegion := state.AverageFreeSize / 2

	for i := blockList.BlockCount() - 1; i > c.immovableBlockCount; i-- {
		mtdata := blockList.MetadataForBlock(i)

		var prevFreeRegionSize int

		if c.walkSuballocations(blockList, i, mtdata,
			func(blockList BlockList[T], blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData MoveAllocationData[T]) bool {
				// Check all previous blocks for free space
				prevMoveCount := len(c.moves)
				if c.allocInOtherBlock(0, i, &moveData, blockList) {
					return true
				}

				nextFreeRegionSize := c.mustFindNextFreeRegionSize(mtdata, handle)
				// If no room found then realloc within block for lower offset
				offset := c.mustFindOffset(mtdata, handle)

				// Check if realloc will make sense
				if prevMoveCount == len(c.moves) &&
					offset != 0 && mtdata.SumFreeSize() >= moveData.Move.Size &&
					(prevFreeRegionSize >= minimalFreeRegion ||
						nextFreeRegionSize >= minimalFreeRegion ||
						moveData.Move.Size <= state.AverageFreeSize ||
						moveData.Move.Size <= state.AverageAllocSize) {

					if c.allocIfLowerOffset(offset, blockList, i, mtdata, handle, &moveData) {
						return true
					}
				}

				prevFreeRegionSize = nextFreeRegionSize
				return false
			}) {
			return true
		}
	}

	// No moves performed, update statistics to current state
	if startMoveCount == len(c.moves) && !update {
		state.AverageAllocSize = math.MaxInt
		return c.computeDefragmentationBalanced(blockList, stateIndex, false)
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

func (c *MetadataDefragContext[T]) computeDefragemntationExtensive(blockList BlockList[T], stateIndex int) bool {
	// FIrst free single block, then populate it to the brim, then free another block, and so on

	//Fallback to previous algorithm since without granularity conflicts it can achieve max packing
	if blockList.BufferImageGranularity() == 1 {
		return c.computeDefragmentationFull(blockList)
	}

	state := &c.extensiveStates[stateIndex]

	switch state.Operation {
	case defragmentOperationDone:
		return false
	case defragmentOperationFindFreeBlockBuffer,
		defragmentOperationFindFreeBlockTexture,
		defragmentOperationFindFreeBlockAll:
		if state.FirstFreeBlock == 0 {
			state.Operation = defragmentOperationCleanup
			return c.computeDefragmentationFast(blockList)
		}

		// No free blocks, have to clear the last one
		last := state.FirstFreeBlock - 1
		if state.FirstFreeBlock == math.MaxInt {
			last = blockList.BlockCount() - 1
		}
		freeMetadata := blockList.MetadataForBlock(last)

		prevMoveCount := len(c.moves)
		complete := c.walkSuballocations(blockList, last, freeMetadata,
			func(blockList BlockList[T], blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData MoveAllocationData[T]) bool {
				if c.allocInOtherBlock(0, last, &moveData, blockList) {
					if prevMoveCount != len(c.moves) && c.mustFindNextAllocation(mtdata, handle) == metadata.NoAllocation {
						state.FirstFreeBlock = last
					}
					return true
				}

				return false
			})
		if complete {
			return true
		}

		if prevMoveCount == len(c.moves) {
			// Cannot perform full clear, have to move data in other blocks around
			if last != 0 {
				for i := last - 1; i > 0; i-- {
					if c.reallocWithinBlock(blockList, i, blockList.MetadataForBlock(i)) {
						return true
					}
				}
			}

			if prevMoveCount == len(c.moves) {
				// No possible reallocs within blocks, try to move them around
				return c.computeDefragmentationFast(blockList)
			}
		} else {
			switch state.Operation {
			case defragmentOperationFindFreeBlockBuffer:
				state.Operation = defragmentOperationMoveBuffers
				break
			case defragmentOperationFindFreeBlockTexture:
				state.Operation = defragmentOperationMoveTextures
				break
			case defragmentOperationFindFreeBlockAll:
				state.Operation = defragmentOperationMoveAll
				break
			default:
				panic(fmt.Sprintf("unexpected state operation in find free operation: %s", state.Operation.String()))
			}
			state.FirstFreeBlock = last
			// Nothing done, block found without reallocations, can perform another realloc in same pass
			return c.computeDefragemntationExtensive(blockList, stateIndex)
		}
	case defragmentOperationMoveTextures:
		texturePresent, bufferPresent, otherPresent, success := c.moveDataToFreeBlocks(metadata.SuballocationImageOptimal, blockList, state.FirstFreeBlock)
		if !success {
			break
		}

		if texturePresent {
			state.Operation = defragmentOperationFindFreeBlockTexture
			return c.computeDefragemntationExtensive(blockList, stateIndex)
		}

		if !bufferPresent && !otherPresent {
			state.Operation = defragmentOperationCleanup
			break
		}

		// No more textures to move, check buffers
		state.Operation = defragmentOperationMoveBuffers
		fallthrough
	case defragmentOperationMoveBuffers:
		_, bufferPresent, otherPresent, success := c.moveDataToFreeBlocks(metadata.SuballocationBuffer, blockList, state.FirstFreeBlock)
		if !success {
			break
		}

		if bufferPresent {
			state.Operation = defragmentOperationFindFreeBlockBuffer
			return c.computeDefragemntationExtensive(blockList, stateIndex)
		}

		if !otherPresent {
			state.Operation = defragmentOperationCleanup
			break
		}

		// No more buffers to move, check all others
		state.Operation = defragmentOperationMoveAll
		fallthrough
	case defragmentOperationMoveAll:
		_, _, otherPresent, success := c.moveDataToFreeBlocks(metadata.SuballocationFree, blockList, state.FirstFreeBlock)
		if success {
			if otherPresent {
				state.Operation = defragmentOperationFindFreeBlockBuffer
				return c.computeDefragemntationExtensive(blockList, stateIndex)
			}

			// Everything moved
			state.Operation = defragmentOperationCleanup
		}

		break
	}

	if state.Operation == defragmentOperationCleanup {
		// All other work done, pack data in blocks even tighter if possible
		prevMoveCount := len(c.moves)
		for i := 0; i < blockList.BlockCount(); i++ {
			if c.reallocWithinBlock(blockList, i, blockList.MetadataForBlock(i)) {
				return true
			}
		}

		if prevMoveCount == len(c.moves) {
			state.Operation = defragmentOperationDone
		}
	}

	return false
}
