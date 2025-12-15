package vam

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"sync"
	"unsafe"

	"log/slog"

	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/pkg/errors"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/defrag"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
	"github.com/vkngwrapper/arsenal/vam/internal/utils"
	"github.com/vkngwrapper/arsenal/vam/internal/vulkan"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/core1_1"
	"github.com/vkngwrapper/core/v2/core1_2"
	"github.com/vkngwrapper/extensions/v2/ext_memory_priority"
	"github.com/vkngwrapper/extensions/v2/khr_external_memory"
)

var blockPool = sync.Pool{
	New: func() any {
		return &deviceMemoryBlock{}
	},
}

type memoryBlockList struct {
	allocOptions    common.Options
	extensionData   *vulkan.ExtensionData
	parentAllocator *Allocator
	parentPool      *Pool
	deviceMemory    *vulkan.DeviceMemoryProperties
	logger          *slog.Logger

	memoryTypeIndex        int
	preferredBlockSize     int
	minBlockCount          int
	maxBlockCount          int
	bufferImageGranularity int

	explicitBlockSize      bool
	algorithm              PoolCreateFlags
	priority               float32
	minAllocationAlignment uint

	memoryAllocateNext unsafe.Pointer
	mutex              utils.OptionalRWMutex
	blocks             []*deviceMemoryBlock
	nextBlockId        int
	incrementalSort    bool
}

func (l *memoryBlockList) MemoryTypeIndex() int                { return l.memoryTypeIndex }
func (l *memoryBlockList) PreferredBlockSize() int             { return l.preferredBlockSize }
func (l *memoryBlockList) BufferImageGranularity() int         { return l.bufferImageGranularity }
func (l *memoryBlockList) Algorithm() PoolCreateFlags          { return l.algorithm }
func (l *memoryBlockList) HasExplicitBlockSize() bool          { return l.explicitBlockSize }
func (l *memoryBlockList) Priority() float32                   { return l.priority }
func (l *memoryBlockList) AllocateNextPointer() unsafe.Pointer { return l.memoryAllocateNext }
func (l *memoryBlockList) BlockCount() int                     { return len(l.blocks) }

func (l *memoryBlockList) Init(
	useMutex bool,
	allocator *Allocator,
	pool *Pool,
	memoryTypeIndex int,
	preferredBlockSize int,
	minBlockCount, maxBlockCount int,
	bufferImageGranularity int,
	explicitBlockSize bool,
	algorithm PoolCreateFlags,
	priority float32,
	minAllocationAlignment uint,
	allocOptions common.Options,
) {
	l.parentAllocator = allocator
	l.parentPool = pool
	l.logger = allocator.logger
	l.allocOptions = allocOptions
	l.extensionData = allocator.extensionData
	l.deviceMemory = allocator.deviceMemory
	l.memoryTypeIndex = memoryTypeIndex
	l.preferredBlockSize = preferredBlockSize
	l.minBlockCount = minBlockCount
	l.maxBlockCount = maxBlockCount
	l.bufferImageGranularity = bufferImageGranularity
	l.explicitBlockSize = explicitBlockSize
	l.algorithm = algorithm
	l.priority = priority
	l.minAllocationAlignment = minAllocationAlignment
	l.incrementalSort = true
	l.mutex = utils.OptionalRWMutex{
		UseMutex: useMutex,
		Mutex:    sync.RWMutex{},
	}
}

func (l *memoryBlockList) Destroy() error {
	for _, block := range l.blocks {
		err := block.Destroy()
		if err != nil {
			return err
		}
		blockPool.Put(block)
	}
	l.blocks = nil
	return nil
}

func (l *memoryBlockList) CreateMinBlocks() (common.VkResult, error) {
	for i := 0; i < l.minBlockCount; i++ {
		_, res, err := l.CreateBlock(l.preferredBlockSize)
		if err != nil {
			return res, err
		}
	}

	return core1_0.VKSuccess, nil
}

func (l *memoryBlockList) AddStatistics(stats *memutils.Statistics) {
	l.mutex.RLock()
	defer l.mutex.RUnlock()

	for blockIndex := 0; blockIndex < len(l.blocks); blockIndex++ {
		block := l.blocks[blockIndex]
		if block == nil {
			panic(fmt.Sprintf("failed to take statistics of nil block at index %d", blockIndex))
		}
		block.metadata.AddStatistics(stats)
	}
}

func (l *memoryBlockList) AddDetailedStatistics(stats *memutils.DetailedStatistics) {
	l.mutex.RLock()
	defer l.mutex.RUnlock()

	for blockIndex := 0; blockIndex < len(l.blocks); blockIndex++ {
		block := l.blocks[blockIndex]
		if block == nil {
			panic(fmt.Sprintf("failed to take statistics of nil block at index %d", blockIndex))
		}
		block.metadata.AddDetailedStatistics(stats)
	}
}

func (l *memoryBlockList) IsEmpty() bool {
	l.mutex.RLock()
	defer l.mutex.RUnlock()

	return len(l.blocks) == 0
}

func (l *memoryBlockList) HasNoAllocations() bool {
	l.mutex.RLock()
	defer l.mutex.RUnlock()

	for blockIndex := 0; blockIndex < len(l.blocks); blockIndex++ {
		if !l.blocks[blockIndex].metadata.IsEmpty() {
			return false
		}
	}

	return true
}

func (l *memoryBlockList) CreateBlock(blockSize int) (int, common.VkResult, error) {
	if l.priority < 0 || l.priority > 1 {
		panic(fmt.Sprintf("block list had an invalid priority value %f somehow: priority values should be between 0 and 1, inclusive", l.priority))
	}

	// First build MemoryAllocateInfo with all the relevant extensions
	var allocInfo core1_0.MemoryAllocateInfo
	allocInfo.Next = l.allocOptions
	allocInfo.MemoryTypeIndex = l.memoryTypeIndex
	allocInfo.AllocationSize = blockSize

	if l.extensionData.BufferDeviceAddress != nil {
		var allocFlagsInfo core1_1.MemoryAllocateFlagsInfo
		allocFlagsInfo.Flags = core1_2.MemoryAllocateDeviceAddress
		allocFlagsInfo.Next = allocInfo.Next
		allocInfo.Next = allocFlagsInfo
	}

	if l.extensionData.UseMemoryPriority {
		priorityInfo := ext_memory_priority.MemoryPriorityAllocateInfo{
			Priority: l.priority,
		}
		priorityInfo.Next = allocInfo.Next
		allocInfo.Next = priorityInfo
	}

	if l.extensionData.ExternalMemory {
		externalMemoryType := l.deviceMemory.ExternalMemoryTypes(l.memoryTypeIndex)
		if externalMemoryType != 0 {
			var exportMemoryAllocInfo khr_external_memory.ExportMemoryAllocateInfo
			exportMemoryAllocInfo.HandleTypes = externalMemoryType
			exportMemoryAllocInfo.Next = allocInfo.Next
			allocInfo.Next = exportMemoryAllocInfo
		}
	}

	// Allocate
	memory, res, err := l.deviceMemory.AllocateVulkanMemory(allocInfo)
	if err != nil {
		return -1, res, err
	}

	// Build allocation
	block := blockPool.Get().(*deviceMemoryBlock)

	block.Init(l.logger, l.parentPool, l.deviceMemory, l.memoryTypeIndex, memory, allocInfo.AllocationSize, l.nextBlockId, l.algorithm, l.bufferImageGranularity)
	l.nextBlockId++

	l.blocks = append(l.blocks, block)
	return len(l.blocks) - 1, res, nil
}

func (l *memoryBlockList) Remove(block *deviceMemoryBlock) {
	for blockIndex := 0; blockIndex < len(l.blocks); blockIndex++ {
		if l.blocks[blockIndex] == block {
			l.blocks = append(l.blocks[0:blockIndex], l.blocks[blockIndex+1:]...)
			return
		}
	}

	panic("attempted to remove a block from a block list that did not belong to it")
}

func (l *memoryBlockList) IsCorruptionDetectionEnabled() bool {
	requiredMemFlags := core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent
	return memutils.DebugMargin > 0 &&
		(l.algorithm == 0 || l.algorithm == PoolCreateLinearAlgorithm) &&
		l.deviceMemory.MemoryTypeProperties(l.memoryTypeIndex).PropertyFlags&requiredMemFlags == requiredMemFlags
}

func (l *memoryBlockList) Allocate(size int, alignment uint, createInfo *AllocationCreateInfo, suballocType suballocationType, allocations []Allocation) (res common.VkResult, err error) {
	if l.minAllocationAlignment > alignment {
		alignment = l.minAllocationAlignment
	}

	if l.IsCorruptionDetectionEnabled() {
		size = memutils.AlignUp(size, 4)
		alignment = uint(memutils.AlignUp(int(alignment), 4))
	}

	allocIndex := 0

	defer func() {
		if err != nil {
			for allocIndex > 0 {
				allocIndex--

				freeErr := l.Free(&allocations[allocIndex])
				if freeErr != nil {
					panic(fmt.Sprintf("unexpected error when freeing an allocation that was created as part of a failed allocation: %+v", err))
				}
			}
		}
	}()

	l.mutex.Lock()
	defer l.mutex.Unlock()

	for allocIndex = 0; allocIndex < len(allocations); allocIndex++ {
		res, err = l.allocPage(size, alignment, createInfo, suballocType, &allocations[allocIndex])
		if err != nil {
			return res, err
		}
	}

	return res, err
}

func (l *memoryBlockList) allocPage(size int, alignment uint, createInfo *AllocationCreateInfo, suballocationType suballocationType, outAlloc *Allocation) (common.VkResult, error) {
	isUpperAddress := createInfo.Flags&AllocationCreateUpperAddress != 0

	var res common.VkResult
	var err error

	heapIndex := l.deviceMemory.MemoryTypeIndexToHeapIndex(l.memoryTypeIndex)

	budget := vulkan.Budget{}
	l.deviceMemory.HeapBudget(heapIndex, &budget)
	freeMemory := budget.Budget - budget.Usage

	if freeMemory < 0 {
		freeMemory = 0
	}

	canFallbackToDedicated := !l.HasExplicitBlockSize() &&
		createInfo.Flags&AllocationCreateNeverAllocate == 0
	canCreateNewBlock := createInfo.Flags&AllocationCreateNeverAllocate == 0 &&
		len(l.blocks) < l.maxBlockCount &&
		(freeMemory >= size || !canFallbackToDedicated)
	strategy := createInfo.Flags & AllocationCreateStrategyMask

	// Upper address can only be used with linear allocator and within a single memory blcok
	if isUpperAddress && (l.algorithm != PoolCreateLinearAlgorithm || l.maxBlockCount > 1) {
		return core1_0.VKErrorFeatureNotPresent, core1_0.VKErrorFeatureNotPresent.ToError()
	}

	// Early reject: requested allocation size is larger than maximum block size for this block list
	if size+memutils.DebugMargin > l.preferredBlockSize {
		return core1_0.VKErrorOutOfDeviceMemory, core1_0.VKErrorOutOfDeviceMemory.ToError()
	}

	// 1. Search existing allocations & try to do an allocation
	if l.algorithm == PoolCreateLinearAlgorithm {
		// Only use the last block in linear
		if len(l.blocks) > 0 {
			currentBlock := l.blocks[len(l.blocks)-1]
			if currentBlock == nil {
				panic("a nil block was found in this block list")
			}

			res, err = l.allocFromBlock(currentBlock, size, alignment, createInfo.Flags, createInfo.UserData, suballocationType, strategy, outAlloc)
			if err != nil {
				return res, err
			} else if res == core1_0.VKSuccess {
				l.logger.LogAttrs(context.Background(), slog.LevelDebug, "    Returned from last block", slog.Int("block.id", currentBlock.id))
				l.incrementallySortBlocks()
				return res, nil
			}
		}
	} else if strategy != AllocationCreateStrategyMinTime {
		// Iterate forward through the blocks to find the smallest/best block where this will fit

		if l.deviceMemory.MemoryTypeProperties(l.memoryTypeIndex).PropertyFlags&core1_0.MemoryPropertyHostVisible != 0 {
			// Host-visible

			isMappingAllowed := createInfo.Flags&(AllocationCreateHostAccessSequentialWrite|AllocationCreateHostAccessRandom) != 0

			/*
				For non-mappable allocations, check blocks that are not mapped first. For mappable allocations,
				check blocks that are already mapped first. This way, if there are a lot of blocks, we'll separate
				mappable and non-mappable allocations, hopefully limiting the number of mapped blocks
			*/
			for mappingIndex := 0; mappingIndex < 2; mappingIndex++ {
				// Prefer blocks with the smallest amount of free space by iterating forward
				for blockIndex := 0; blockIndex < len(l.blocks); blockIndex++ {
					currentBlock := l.blocks[blockIndex]
					if currentBlock == nil {
						panic(fmt.Sprintf("a memory block at index %d is unexpectedly nil", blockIndex))
					}

					isBlockMapped := currentBlock.memory.MappedData() != nil
					if (mappingIndex == 0) == (isMappingAllowed == isBlockMapped) {
						res, err = l.allocFromBlock(currentBlock, size, alignment, createInfo.Flags, createInfo.UserData, suballocationType, strategy, outAlloc)
						if err != nil {
							return res, err
						} else if res == core1_0.VKSuccess {
							l.logger.LogAttrs(context.Background(), slog.LevelDebug, "    Returned from existing block", slog.Int("block.id", currentBlock.id))
							l.incrementallySortBlocks()
							return res, nil
						}
					}
				}
			}
		} else {
			// Not host-visible

			for blockIndex := 0; blockIndex < len(l.blocks); blockIndex++ {
				// Prefer blocks with the smallest amount of free space by iterating forward
				currentBlock := l.blocks[blockIndex]
				if currentBlock == nil {
					panic(fmt.Sprintf("a memory block at index %d is unexpectedly nil", blockIndex))
				}

				res, err = l.allocFromBlock(currentBlock, size, alignment, createInfo.Flags, createInfo.UserData, suballocationType, strategy, outAlloc)
				if err != nil {
					return res, err
				} else if res == core1_0.VKSuccess {
					l.logger.LogAttrs(context.Background(), slog.LevelDebug, "   Returned from existing block", slog.Int("block.id", currentBlock.id))
					l.incrementallySortBlocks()
					return res, nil
				}
			}
		}
	} else {
		for blockIndex := len(l.blocks) - 1; blockIndex >= 0; blockIndex-- {
			// Prefer blocks with the largest amount of free space by iterating backward
			currentBlock := l.blocks[blockIndex]
			if currentBlock == nil {
				panic(fmt.Sprintf("a memory block at index %d is unexpectedly nil", blockIndex))
			}

			res, err = l.allocFromBlock(currentBlock, size, alignment, createInfo.Flags, createInfo.UserData, suballocationType, strategy, outAlloc)
			if err != nil {
				return res, err
			} else if res == core1_0.VKSuccess {
				l.logger.LogAttrs(context.Background(), slog.LevelDebug, "    Returned from existing block", slog.Int("block.id", currentBlock.id))
				l.incrementallySortBlocks()
				return res, nil
			}
		}
	}

	// 2. Try to create a new block
	if canCreateNewBlock {
		newBlockSize := l.preferredBlockSize
		newBlockSizeShift := 0
		const MaxNewBlockSizeShift = 3

		if !l.explicitBlockSize {
			maxExistingBlockSize := l.calcMaxBlockSize()

			for i := 0; i < MaxNewBlockSizeShift; i++ {
				smallerNewBlockSize := newBlockSize / 2
				if smallerNewBlockSize > maxExistingBlockSize && smallerNewBlockSize >= size*2 {
					newBlockSize = smallerNewBlockSize
					newBlockSizeShift++
				} else {
					break
				}
			}
		}

		newBlockIndex := 0
		var retStatus common.VkResult
		if newBlockSize <= freeMemory || !canFallbackToDedicated {
			newBlockIndex, retStatus, err = l.CreateBlock(newBlockSize)
		} else {
			retStatus = core1_0.VKErrorOutOfDeviceMemory
			err = retStatus.ToError()
		}

		if !l.explicitBlockSize {
			for err != nil && newBlockSizeShift < MaxNewBlockSizeShift {
				smallerNewBlockSize := newBlockSize / 2
				if smallerNewBlockSize >= size {
					newBlockSize = smallerNewBlockSize
					newBlockSizeShift++
					if newBlockSize <= freeMemory || !canFallbackToDedicated {
						newBlockIndex, retStatus, err = l.CreateBlock(newBlockSize)
					}
				} else {
					break
				}
			}
		}

		if err != nil {
			return retStatus, err
		}

		block := l.blocks[newBlockIndex]
		if block.metadata.Size() < size {
			panic(fmt.Sprintf("created a new block at index %d to hold an allocation of size %d but the created block was somehow only size %d", newBlockIndex, size, block.metadata.Size()))
		}

		res, err = l.allocFromBlock(block, size, alignment, createInfo.Flags, createInfo.UserData, suballocationType, strategy, outAlloc)
		if err != nil {
			return res, err
		} else if res == core1_0.VKSuccess {
			l.incrementallySortBlocks()
			return res, nil
		}
	}

	return core1_0.VKErrorOutOfDeviceMemory, core1_0.VKErrorOutOfDeviceMemory.ToError()
}

func (l *memoryBlockList) Free(alloc *Allocation) error {
	heapIndex := l.deviceMemory.MemoryTypeIndexToHeapIndex(l.memoryTypeIndex)
	blockToDelete, err := l.freeWithLock(alloc, heapIndex)
	if err != nil {
		return err
	}

	if blockToDelete != nil {
		l.logger.LogAttrs(context.Background(), slog.LevelDebug, "    Deleted empty block", slog.Int("block.id", blockToDelete.id))
		err = blockToDelete.Destroy()
		if err != nil {
			panic(fmt.Sprintf("unexpected failure when destroying a memory block in response to freeing an allocation: %+v", err))
		}
		blockPool.Put(blockToDelete)
	}

	l.deviceMemory.RemoveAllocation(heapIndex, alloc.size)
	return nil
}

func (l *memoryBlockList) freeWithLock(alloc *Allocation, heapIndex int) (blockToDelete *deviceMemoryBlock, err error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	block := alloc.blockData.block

	heapBudget := vulkan.Budget{}
	l.deviceMemory.HeapBudget(heapIndex, &heapBudget)
	budgetExceeded := heapBudget.Usage >= heapBudget.Budget

	if l.IsCorruptionDetectionEnabled() {
		_, err = block.ValidateMagicValueAfterAllocation(alloc.FindOffset(), alloc.Size())
		if err != nil {
			panic(fmt.Sprintf("unexpected error while validating magic values: %+v", err))
		}
	}

	if alloc.isPersistentMap() {
		// Unmap might fail if the user has screwed up Map/Unmap pairs, we want to return error in that case
		err := block.memory.Unmap(1)
		if err != nil {
			return nil, err
		}
	}

	hasEmptyBlockBeforeFree := l.hasEmptyBlock()
	err = block.metadata.Free(alloc.blockData.handle)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when freeing allocation with handle %+v in metadata: %+v", alloc.blockData.handle, err))
	}
	block.memory.RecordSuballocSubfree()
	memutils.DebugValidate(block)

	l.logger.LogAttrs(context.Background(), slog.LevelDebug, "    Freed from block", slog.Int("MemoryTypeIndex", l.memoryTypeIndex))

	canDeleteBlock := len(l.blocks) > l.minBlockCount

	// The block is empty & we can delete it
	if block.metadata.IsEmpty() && (hasEmptyBlockBeforeFree || budgetExceeded) && canDeleteBlock {
		blockToDelete = block
		l.Remove(block)
	} else if !block.metadata.IsEmpty() && hasEmptyBlockBeforeFree && canDeleteBlock {
		// There is an empty block somewhere we don't need
		lastBlock := l.blocks[len(l.blocks)-1]
		if lastBlock.metadata.IsEmpty() {
			blockToDelete = lastBlock
			l.blocks = l.blocks[:len(l.blocks)-1]
		}
	}

	l.incrementallySortBlocks()

	return blockToDelete, nil
}

func (l *memoryBlockList) hasEmptyBlock() bool {
	for blockIndex := 0; blockIndex < len(l.blocks); blockIndex++ {
		block := l.blocks[blockIndex]
		if block.metadata.IsEmpty() {
			return true
		}
	}

	return false
}

func (l *memoryBlockList) incrementallySortBlocks() {
	if !l.incrementalSort || l.algorithm == PoolCreateLinearAlgorithm {
		return
	}

	for blockIndex := 1; blockIndex < len(l.blocks); blockIndex++ {
		if l.blocks[blockIndex-1].metadata.SumFreeSize() > l.blocks[blockIndex].metadata.SumFreeSize() {
			l.blocks[blockIndex-1], l.blocks[blockIndex] = l.blocks[blockIndex], l.blocks[blockIndex-1]
			return
		}
	}
}

func (l *memoryBlockList) SortByFreeSize() {
	sort.Slice(l.blocks, func(i, j int) bool {
		return l.blocks[i].metadata.SumFreeSize() < l.blocks[j].metadata.SumFreeSize()
	})
}

func (l *memoryBlockList) calcMaxBlockSize() int {
	result := 0
	for blockIndex := len(l.blocks) - 1; blockIndex >= 0; blockIndex-- {
		blockSize := l.blocks[blockIndex].metadata.Size()
		if blockSize <= result {
			continue
		}

		result = blockSize
		if result >= l.preferredBlockSize {
			return result
		}
	}

	return result
}

func (l *memoryBlockList) allocFromBlock(block *deviceMemoryBlock, size int, alignment uint, allocFlags AllocationCreateFlags, userData any, suballocType suballocationType, flags AllocationCreateFlags, outAlloc *Allocation) (common.VkResult, error) {
	if !block.metadata.MayHaveFreeBlock(uint32(suballocType), size) {
		return core1_0.VKErrorOutOfDeviceMemory, nil
	}

	isUpperAddress := allocFlags&AllocationCreateUpperAddress != 0

	var strategy metadata.AllocationStrategy
	if flags&AllocationCreateStrategyMinOffset != 0 {
		strategy |= metadata.AllocationStrategyMinOffset
	}
	if flags&AllocationCreateStrategyMinMemory != 0 {
		strategy |= metadata.AllocationStrategyMinMemory
	}
	if flags&AllocationCreateStrategyMinTime != 0 {
		strategy |= metadata.AllocationStrategyMinTime
	}

	success, currRequest, err := block.metadata.CreateAllocationRequest(size, alignment, isUpperAddress, uint32(suballocType), strategy, math.MaxInt)
	if err != nil {
		return core1_0.VKErrorUnknown, err
	} else if !success {
		return core1_0.VKErrorOutOfDeviceMemory, nil
	}

	return l.commitAllocationRequest(currRequest, block, alignment, allocFlags, userData, suballocType, outAlloc)
}

func (l *memoryBlockList) commitAllocationRequest(allocRequest metadata.AllocationRequest, block *deviceMemoryBlock, alignment uint, allocFlags AllocationCreateFlags, userData any, suballocType suballocationType, outAlloc *Allocation) (common.VkResult, error) {
	mapped := allocFlags&AllocationCreateMapped != 0
	isMappingAllowed := allocFlags&(AllocationCreateHostAccessSequentialWrite|AllocationCreateHostAccessRandom) != 0

	block.memory.RecordSuballocSubfree()

	// Allocate from block
	if mapped {
		_, res, err := block.memory.Map(1, 0, -1, 0)
		if err != nil {
			return res, err
		}
	}

	outAlloc.init(l.parentAllocator, isMappingAllowed)
	err := block.metadata.Alloc(allocRequest, uint32(suballocType), outAlloc)
	if err != nil {
		return core1_0.VKErrorUnknown, err
	}

	outAlloc.initBlockAllocation(block, allocRequest.BlockAllocationHandle, alignment, allocRequest.Size, l.memoryTypeIndex, suballocType, mapped)

	outAlloc.SetUserData(userData)
	heapIndex := l.deviceMemory.MemoryTypeIndexToHeapIndex(l.memoryTypeIndex)
	l.deviceMemory.AddAllocation(heapIndex, allocRequest.Size)

	outAlloc.fillAllocation(createdFillPattern)

	if l.IsCorruptionDetectionEnabled() {
		_, err = block.WriteMagicBlockAfterAllocation(outAlloc.FindOffset(), allocRequest.Size)
		if err != nil {
			panic(fmt.Sprintf("failed to write magic values with unexpected error: %+v", err))
		}
	}

	return core1_0.VKSuccess, nil
}

func (l *memoryBlockList) PrintDetailedMap(writer *jwriter.Writer) {
	l.mutex.RLock()
	defer l.mutex.RUnlock()

	objState := writer.Object()
	defer objState.End()

	for i := 0; i < len(l.blocks); i++ {
		block := l.blocks[i]

		blockObj := objState.Name(strconv.Itoa(block.id)).Object()

		blockObj.Name("MapReferences").Int(block.memory.References())
		block.metadata.BlockJsonData(blockObj)

		l.printDetailedMapAllocations(block.metadata, blockObj)

		blockObj.End()
	}
}

func (l *memoryBlockList) printDetailedMapAllocations(md metadata.BlockMetadata, json jwriter.ObjectState) {
	arrayState := json.Name("Suballocations").Array()
	defer arrayState.End()

	// Second pass
	_ = md.VisitAllRegions(
		func(handle metadata.BlockAllocationHandle, offset int, size int, userData any, free bool) error {
			if free {
				obj := arrayState.Object()
				defer obj.End()

				obj.Name("Offset").Int(offset)
				obj.Name("Type").String(SuballocationFree.String())
				obj.Name("Size").Int(size)
			} else {
				obj := arrayState.Object()
				defer obj.End()

				obj.Name("Offset").Int(offset)

				var alloc *Allocation
				var isAllocation bool
				if userData != nil {
					alloc, isAllocation = userData.(*Allocation)
				}

				if isAllocation && alloc != nil {
					alloc.printParameters(&obj)
				} else if userData != nil {
					obj.Name("CustomData").String(fmt.Sprintf("%+v", userData))
				}
			}

			return nil
		})

}

func (l *memoryBlockList) CheckCorruption() (common.VkResult, error) {
	if !l.IsCorruptionDetectionEnabled() {
		return core1_0.VKErrorFeatureNotPresent, core1_0.VKErrorFeatureNotPresent.ToError()
	}

	l.mutex.RLock()
	defer l.mutex.RUnlock()

	for blockIndex := 0; blockIndex < len(l.blocks); blockIndex++ {
		block := l.blocks[blockIndex]
		if block == nil {
			return core1_0.VKErrorUnknown, errors.Errorf("unexpected nil block at memory type %d, block %d", l.memoryTypeIndex, blockIndex)
		}

		res, err := block.CheckCorruption()
		if err != nil {
			return res, err
		}
	}

	return core1_0.VKSuccess, nil
}

func (l *memoryBlockList) MetadataForBlock(blockIndex int) metadata.BlockMetadata {
	return l.blocks[blockIndex].metadata
}

func (l *memoryBlockList) Lock() {
	l.mutex.Lock()
}

func (l *memoryBlockList) Unlock() {
	l.mutex.Unlock()
}

func (l *memoryBlockList) CommitDefragAllocationRequest(allocRequest metadata.AllocationRequest, blockIndex int, alignment uint, flags uint32, userData any, suballocType uint32, outAlloc *Allocation) (common.VkResult, error) {
	return l.commitAllocationRequest(
		allocRequest,
		l.blocks[blockIndex],
		alignment,
		AllocationCreateFlags(flags),
		userData,
		suballocationType(suballocType),
		outAlloc,
	)
}

func (l *memoryBlockList) CreateAlloc() *Allocation {
	return &Allocation{}
}

func (l *memoryBlockList) MoveDataForUserData(userData any) defrag.MoveAllocationData[Allocation] {
	alloc, ok := userData.(*Allocation)
	if !ok || alloc == nil {
		panic(fmt.Sprintf("attempted to create a MoveAllocationData for a non-Allocation userData: %+v", userData))
	}

	var flags AllocationCreateFlags

	if alloc.isPersistentMap() {
		flags |= AllocationCreateMapped
	}
	if alloc.IsMappingAllowed() {
		flags |= AllocationCreateHostAccessSequentialWrite | AllocationCreateHostAccessRandom
	}

	return defrag.MoveAllocationData[Allocation]{
		Alignment:         alloc.alignment,
		SuballocationType: uint32(alloc.suballocationType),
		Flags:             uint32(flags),
		Move: defrag.DefragmentationMove[Allocation]{
			Size:             alloc.size,
			SrcAllocation:    alloc,
			SrcBlockMetadata: alloc.blockData.block.metadata,
		},
	}
}

func (l *memoryBlockList) SwapBlocks(left, right int) {
	l.blocks[left], l.blocks[right] = l.blocks[right], l.blocks[left]
}
