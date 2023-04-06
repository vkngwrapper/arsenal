package defrag

import (
	"fmt"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"golang.org/x/exp/slog"
	"math"
)

type DefragmentationStats struct {
	BytesMoved              int
	BytesFreed              int
	AllocationsMoved        int
	DeviceMemoryBlocksFreed int
}

type MetadataDefragContext struct {
	logger *slog.Logger

	maxPassBytes       int
	maxPassAllocations int

	moves []DefragmentationMove

	ignoredAllocs int
	algorithm     DefragmentationFlags
	handler       DefragmentOperationHandler

	singleBlockList     BlockList
	blockLists          [common.MaxMemoryTypes]BlockList
	balancedStates      [common.MaxMemoryTypes]stateBalanced
	extensiveStates     [common.MaxMemoryTypes]stateExtensive
	immovableBlockCount int

	globalStats DefragmentationStats
	passStats   DefragmentationStats
}

func (c *MetadataDefragContext) init(logger *slog.Logger, o *DefragmentationInfo) {
	c.logger = logger
	c.singleBlockList = nil
	c.maxPassBytes = o.MaxBytesPerPass
	c.maxPassAllocations = o.MaxAllocationsPerPass
	if c.maxPassBytes == 0 {
		c.maxPassBytes = -1
	}
	if c.maxPassAllocations == 0 {
		c.maxPassAllocations = math.MaxInt
	}
	c.algorithm = o.Flags & DefragmentationFlagAlgorithmMask

	for i := 0; i < common.MaxMemoryTypes; i++ {
		c.blockLists[i] = nil
		c.balancedStates[i].AverageAllocSize = math.MaxInt
		c.extensiveStates[i].FirstFreeBlock = math.MaxInt
	}

	if c.algorithm == 0 {
		c.algorithm = DefragmentationFlagAlgorithmBalanced
	}
}

func (c *MetadataDefragContext) InitSingle(logger *slog.Logger, blockList BlockList, o DefragmentationInfo) {
	c.init(logger, &o)

	c.singleBlockList = blockList
}

func (c *MetadataDefragContext) InitMultiple(logger *slog.Logger, blockList [common.MaxMemoryTypes]BlockList, o DefragmentationInfo) {
	c.init(logger, &o)

	for i := 0; i < common.MaxMemoryTypes; i++ {
		c.blockLists[i] = blockList[i]
	}
}

func (c *MetadataDefragContext) ExecutePass() (common.VkResult, error) {
	c.logger.Debug("MetadataDefragContext::ExecutePass")

	var defaultBlockList BlockList

	for i := 0; i < common.MaxMemoryTypes; i++ {
		if c.blockLists[i] != nil {
			defaultBlockList = c.blockLists[i]
			break
		}
	}

	if c.singleBlockList == nil && defaultBlockList == nil {
		panic("could not find a non-nil memory block list")
	}

	if c.singleBlockList != nil {
		return c.blockListExecutePass(0, c.singleBlockList)
	}

	var isIncomplete bool
	var res common.VkResult
	var err error
	for i := 0; i < len(c.blockLists); i++ {
		if c.blockLists[i] != nil {
			res, err = c.blockListExecutePass(i, c.blockLists[i])
			if err != nil {
				return res, err
			}

			if res == core1_0.VKIncomplete {
				isIncomplete = true
			}
		}
	}

	if isIncomplete {
		return core1_0.VKIncomplete, nil
	}

	return core1_0.VKSuccess, nil
}

func (c *MetadataDefragContext) PopulateStats(stats *DefragmentationStats) {
	if stats == nil {
		return
	}

	stats.DeviceMemoryBlocksFreed = c.globalStats.DeviceMemoryBlocksFreed
	stats.BytesFreed = c.globalStats.BytesFreed
	stats.BytesMoved = c.globalStats.BytesMoved
	stats.AllocationsMoved = c.globalStats.AllocationsMoved
}

func (c *MetadataDefragContext) blockListExecutePass(index int, blockList BlockList) (common.VkResult, error) {
	c.blockListCollectMoves(index, blockList)
	immovableBlocks := make(map[metadata.BlockMetadata]struct{}, blockList.BlockCount())

	for i := 0; i < len(c.moves); i++ {
		move := c.moves[i]

		var stats memutils.Statistics
		blockList.AddStatistics(&stats)
		prevCount := stats.BlockCount
		prevBytes := stats.BlockBytes

		op := c.handler(move.SrcAllocation, move.DstTmpAllocation)

		blockList.AddStatistics(&stats)
		c.passStats.DeviceMemoryBlocksFreed += prevCount - stats.BlockCount
		c.passStats.BytesFreed += prevBytes - stats.BlockBytes

		switch op {
		case DefragmentationMoveIgnore:
			c.passStats.BytesMoved -= move.Size
			c.passStats.AllocationsMoved--
			immovableBlocks[move.SrcBlockMetadata] = struct{}{}

		case DefragmentationMoveDestroy:
			c.passStats.BytesMoved -= move.Size
			c.passStats.AllocationsMoved--
		}

		if c.algorithm == DefragmentationFlagAlgorithmExtensive {
			// Avoid unnecessary tries to allocate when new free block is available
			state := &c.extensiveStates[index]
			if state.FirstFreeBlock != math.MaxInt {
				diff := prevCount - stats.BlockCount
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
	c.globalStats.DeviceMemoryBlocksFreed += c.passStats.BytesMoved
	c.passStats = DefragmentationStats{}
	c.ignoredAllocs = 0

	// Move blocks iwth immovable allocations according to algorithm
	swappedFreeBlocks := true
	if len(immovableBlocks) > 0 {
		if c.algorithm == DefragmentationFlagAlgorithmExtensive {
			state := c.extensiveStates[index]

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

	if len(c.moves) > 0 || swappedFreeBlocks {
		c.moves = c.moves[:0]
		return core1_0.VKIncomplete, nil
	}

	return core1_0.VKSuccess, nil
}

func (c *MetadataDefragContext) swapImmovableBlocks(blockList BlockList, mtdata metadata.BlockMetadata) {
	blockList.Lock()
	defer blockList.Unlock()

	for i := c.immovableBlockCount; i < blockList.BlockCount(); i++ {
		if blockList.MetadataForBlock(i) == mtdata {
			blockList.SwapBlocks(i, c.immovableBlockCount)
			c.immovableBlockCount++
		}
	}
}

func (c *MetadataDefragContext) swapFreeImmovableBlocks(blockList BlockList, state *stateExtensive, mtdata metadata.BlockMetadata) bool {
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

func (c *MetadataDefragContext) blockListCollectMoves(index int, blockList BlockList) {
	if blockList == nil {
		panic("attempted to begin pass with a nil blockList")
	}

	blockList.Lock()
	defer blockList.Unlock()

	if blockList.BlockCount() > 1 {
		c.computeDefragmentation(blockList, index)
	} else if blockList.BlockCount() == 1 {
		c.reallocWithinBlock(blockList, index, blockList.MetadataForBlock(0))
	}
}

func (c *MetadataDefragContext) mustBeginAllocationList(mtdata metadata.BlockMetadata) metadata.BlockAllocationHandle {
	handle, err := mtdata.AllocationListBegin()
	if err != nil {
		panic(fmt.Sprintf("unexpected error when getting first allocation: %+v", err))
	}

	return handle
}

func (c *MetadataDefragContext) mustFindNextAllocation(mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle) metadata.BlockAllocationHandle {
	handle, err := mtdata.FindNextAllocation(handle)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when getting next allocation: %+v", err))
	}

	return handle
}

func (c *MetadataDefragContext) mustFindNextFreeRegionSize(mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle) int {
	size, err := mtdata.FindNextFreeRegionSize(handle)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when getting next free region size: %+v", err))
	}

	return size
}

func (c *MetadataDefragContext) mustFindOffset(mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle) int {
	offset, err := mtdata.AllocationOffset(handle)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when getting allocation offset: %+v", err))
	}

	return offset
}

func (c *MetadataDefragContext) getMoveData(blockList BlockList, handle metadata.BlockAllocationHandle, mtdata metadata.BlockMetadata) moveAllocationData {
	userData, err := mtdata.AllocationUserData(handle)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when retrieving allocation user data: %+v", err))
	}

	moveData := blockList.MoveDataForUserData(userData)
	moveData.Move.SrcBlockMetadata = mtdata
	return moveData
}

const defragMaxAllocsToIgnore = 16

func (c *MetadataDefragContext) checkCounters(bytes int) defragCounterStatus {
	// Ignore allocation if it will exceed max size for copy
	if c.passStats.BytesMoved+bytes > c.maxPassBytes {
		c.ignoredAllocs++
		if c.ignoredAllocs < defragMaxAllocsToIgnore {
			return defragCounterIgnore
		} else {
			return defragCounterEnd
		}
	}

	return defragCounterPass
}

func (c *MetadataDefragContext) incrementCounters(bytes int) bool {
	c.passStats.BytesMoved += bytes
	c.passStats.AllocationsMoved++

	// Early return when max found
	if c.passStats.AllocationsMoved >= c.maxPassAllocations || c.passStats.BytesMoved >= c.maxPassBytes {
		if c.passStats.AllocationsMoved != c.maxPassAllocations && c.passStats.BytesMoved != c.maxPassBytes {
			panic(fmt.Sprintf("somehow excited maximum pass thresholds: bytes %d, allocs %d", c.passStats.BytesMoved, c.passStats.AllocationsMoved))
		}

		return true
	}

	return false
}

func (c *MetadataDefragContext) allocFromBlock(blockList BlockList, blockIndex int, mtData metadata.BlockMetadata, size int, alignment uint, flags memutils.AllocationCreateFlags, userData any, suballocType metadata.SuballocationType, outAlloc *any) (common.VkResult, error) {
	isUpperAddress := flags&memutils.AllocationCreateUpperAddress != 0

	var currRequest metadata.AllocationRequest
	success, err := mtData.PopulateAllocationRequest(size, alignment, isUpperAddress, suballocType, 0, &currRequest)
	if err != nil {
		return core1_0.VKErrorUnknown, err
	} else if !success {
		return core1_0.VKErrorOutOfDeviceMemory, core1_0.VKErrorOutOfDeviceMemory.ToError()
	}

	return blockList.CommitAllocationRequest(&currRequest, blockIndex, alignment, flags, userData, suballocType, outAlloc)
}

func (c *MetadataDefragContext) allocInOtherBlock(start, end int, data *moveAllocationData, blockList BlockList) bool {
	for ; start < end; start++ {
		dstMetadata := blockList.MetadataForBlock(start)
		if dstMetadata.SumFreeSize() >= data.Move.Size {
			res, err := c.allocFromBlock(blockList, start, dstMetadata,
				data.Move.Size,
				data.Alignment,
				data.Flags,
				c,
				data.SuballocationType,
				&data.Move.DstTmpAllocation)
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

func (c *MetadataDefragContext) walkSuballocations(blockList BlockList, blockIndex int, mtdata metadata.BlockMetadata,
	suballocHandler func(blockList BlockList, blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData *moveAllocationData) bool) bool {
	for handle := c.mustBeginAllocationList(mtdata); handle != metadata.NoAllocation; handle = c.mustFindNextAllocation(mtdata, handle) {
		moveData := c.getMoveData(blockList, handle, mtdata)

		// Ignore newly-created allocations by defragmentation algorithm
		if moveData.Move.SrcAllocation == c {
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

		if suballocHandler(blockList, blockIndex, mtdata, handle, &moveData) {
			return true
		}
	}

	return false
}

func (c *MetadataDefragContext) allocIfLowerOffset(offset int, blockList BlockList, blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData *moveAllocationData) bool {
	var allocRequest metadata.AllocationRequest
	success, err := mtdata.PopulateAllocationRequest(
		moveData.Move.Size,
		moveData.Alignment,
		false,
		moveData.SuballocationType,
		memutils.AllocationCreateStrategyMinOffset,
		&allocRequest,
	)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when populating allocation request for defrag: %+v", err))
	}

	if success && c.mustFindOffset(mtdata, handle) < offset {
		res, err := blockList.CommitAllocationRequest(
			&allocRequest,
			blockIndex,
			moveData.Alignment,
			moveData.Flags,
			c,
			moveData.SuballocationType,
			&moveData.Move.DstTmpAllocation,
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

func (c *MetadataDefragContext) reallocSuballocHandler(blockList BlockList, blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData *moveAllocationData) bool {
	offset := c.mustFindOffset(mtdata, handle)
	if offset != 0 && mtdata.SumFreeSize() >= moveData.Move.Size {
		return c.allocIfLowerOffset(offset, blockList, blockIndex, mtdata, handle, moveData)
	}

	return false
}

func (c *MetadataDefragContext) reallocWithinBlock(blockList BlockList, blockIndex int, mtdata metadata.BlockMetadata) bool {
	return c.walkSuballocations(blockList, blockIndex, mtdata, c.reallocSuballocHandler)
}

func (c *MetadataDefragContext) moveDataToFreeBlocks(currentType metadata.SuballocationType, blockList BlockList, firstFreeBlock int) (texturePresent, bufferPresent, otherPresent, success bool) {
	prevMoveCount := len(c.moves)
	for i := firstFreeBlock - 1; i > 0; i-- {
		mtdata := blockList.MetadataForBlock(i)

		allocSuccess := c.walkSuballocations(blockList, i, mtdata,
			func(blockList BlockList, blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData *moveAllocationData) bool {
				// Move only single type of resources at once
				if !metadata.IsBufferImageGranularityConflict(moveData.SuballocationType, currentType) {
					// Try to fit allocation into free blocks
					if c.allocInOtherBlock(firstFreeBlock, blockList.BlockCount(), moveData, blockList) {
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

func (c *MetadataDefragContext) computeDefragmentation(blockList BlockList, index int) bool {
	switch c.algorithm {
	case DefragmentationFlagAlgorithmFast:
		return c.computeDefragmentationFast(blockList)
	case DefragmentationFlagAlgorithmBalanced:
		return c.computeDefragmentationBalanced(blockList, index, true)
	case DefragmentationFlagAlgorithmFull:
		return c.computeDefragmentationFull(blockList)
	case DefragmentationFlagAlgorithmExtensive:
		return c.computeDefragemntationExtensive(blockList, index)
	default:
		panic(fmt.Sprintf("attempted to defragment with unknown algorithm: %s", c.algorithm.String()))
	}
}

func (c *MetadataDefragContext) defragFastSuballocHandler(blockList BlockList, blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData *moveAllocationData) bool {
	return c.allocInOtherBlock(0, blockIndex, moveData, blockList)
}

func (c *MetadataDefragContext) computeDefragmentationFast(blockList BlockList) bool {
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

func (c *MetadataDefragContext) computeDefragmentationBalanced(blockList BlockList, index int, update bool) bool {
	// Go over every allocation and try to fit it in previous blocks at lowest offsets,
	// if not possible: realloc within single block to minimize offset (exclude offset == 0),
	// but only if there are noticeable gaps between them

	state := &c.balancedStates[index]
	if update && state.AverageAllocSize == math.MaxInt {
		state.UpdateStatistics(blockList)
	}

	startMoveCount := len(c.moves)
	minimalFreeRegion := state.AverageFreeSize / 2

	for i := blockList.BlockCount() - 1; i > c.immovableBlockCount; i-- {
		mtdata := blockList.MetadataForBlock(i)

		var prevFreeRegionSize int

		c.walkSuballocations(blockList, i, mtdata,
			func(blockList BlockList, blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData *moveAllocationData) bool {
				// Check all previous blocks for free space
				prevMoveCount := len(c.moves)
				if c.allocInOtherBlock(0, i, moveData, blockList) {
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

					if c.allocIfLowerOffset(offset, blockList, i, mtdata, handle, moveData) {
						return true
					}
				}

				prevFreeRegionSize = nextFreeRegionSize
				return false
			})
	}

	// No moves performed, update statistics to current state
	if startMoveCount == len(c.moves) && !update {
		state.AverageAllocSize = math.MaxInt
		return c.computeDefragmentationBalanced(blockList, index, false)
	}

	return false
}

func (c *MetadataDefragContext) defragFullSuballocHandler(blockList BlockList, blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData *moveAllocationData) bool {
	// Check all previous blocks for free space
	prevMoveCount := len(c.moves)
	if c.allocInOtherBlock(0, blockIndex, moveData, blockList) {
		return true
	}

	// If no room found then realloc within block for lower offset
	offset := c.mustFindOffset(mtdata, handle)
	if prevMoveCount == len(c.moves) &&
		offset != 0 &&
		mtdata.SumFreeSize() >= moveData.Move.Size {

		if c.allocIfLowerOffset(offset, blockList, blockIndex, mtdata, handle, moveData) {
			return true
		}
	}

	return false
}

func (c *MetadataDefragContext) computeDefragmentationFull(blockList BlockList) bool {
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

func (c *MetadataDefragContext) computeDefragemntationExtensive(blockList BlockList, index int) bool {
	// FIrst free single block, then populate it to the brim, then free another block, and so on

	//Fallback to previous algorithm since without granularity conflicts it can achieve max packing
	if blockList.BufferImageGranularity() == 1 {
		return c.computeDefragmentationFull(blockList)
	}

	state := &c.extensiveStates[index]

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
			func(blockList BlockList, blockIndex int, mtdata metadata.BlockMetadata, handle metadata.BlockAllocationHandle, moveData *moveAllocationData) bool {
				if c.allocInOtherBlock(0, last, moveData, blockList) {
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
			return c.computeDefragemntationExtensive(blockList, index)
		}
	case defragmentOperationMoveTextures:
		texturePresent, bufferPresent, otherPresent, success := c.moveDataToFreeBlocks(metadata.SuballocationImageOptimal, blockList, state.FirstFreeBlock)
		if !success {
			break
		}

		if texturePresent {
			state.Operation = defragmentOperationFindFreeBlockTexture
			return c.computeDefragemntationExtensive(blockList, index)
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
			return c.computeDefragemntationExtensive(blockList, index)
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
				return c.computeDefragemntationExtensive(blockList, index)
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
