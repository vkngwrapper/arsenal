package allocator

import (
	"github.com/vkngwrapper/arsenal/memory"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/core1_1"
	"github.com/vkngwrapper/core/v2/core1_2"
	"sync"
)

type memoryBlockList struct {
	parentAllocator *VulkanAllocator
	parentPool      *VulkanPool
	allocOptions    common.Options
	extensionData   *ExtensionData

	memoryTypeIndex        int
	preferredBlockSize     int
	minBlockCount          int
	maxBlockCount          int
	bufferImageGranularity int

	explicitBlockSize      bool
	algorithm              memory.PoolCreateFlags
	priority               float32
	minAllocationAlignment int

	mutex           sync.RWMutex
	blocks          []*deviceMemoryBlock
	nextBlockId     int
	incrementalSort bool
}

func newMemoryBlockList(
	allocator *VulkanAllocator,
	parentPool *VulkanPool,
	memoryTypeIndex int,
	preferredBlockSize int,
	minBlockCount, maxBlockCount int,
	bufferImageGranularity int,
	explicitBlockSize bool,
	algorithm memory.PoolCreateFlags,
	priority float32,
	minAllocationAlignment int,
	extensionData *ExtensionData,
	allocOptions common.Options,
) *memoryBlockList {
	return &memoryBlockList{
		parentAllocator:        allocator,
		parentPool:             parentPool,
		allocOptions:           allocOptions,
		extensionData:          extensionData,
		memoryTypeIndex:        memoryTypeIndex,
		preferredBlockSize:     preferredBlockSize,
		minBlockCount:          minBlockCount,
		maxBlockCount:          maxBlockCount,
		bufferImageGranularity: bufferImageGranularity,

		explicitBlockSize:      explicitBlockSize,
		algorithm:              algorithm,
		priority:               priority,
		minAllocationAlignment: minAllocationAlignment,

		incrementalSort: true,
	}
}

func (l *memoryBlockList) CreateBlock(blockSize int) (int, common.VkResult, error) {
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

	// TODO: Memory priority

	if l.extensionData.ExternalMemory {
		externalMemoryType := l.parentAllocator.externalMemoryHandleTypes[l.memoryTypeIndex]
		if externalMemoryType != 0 {
			var exportMemoryAllocInfo core1_1.ExportMemoryAllocateInfo
			exportMemoryAllocInfo.HandleTypes = externalMemoryType
			exportMemoryAllocInfo.Next = allocInfo.Next
			allocInfo.Next = exportMemoryAllocInfo
		}
	}

	// Allocate
	memory, res, err := l.parentAllocator.allocateVulkanMemory(allocInfo)
	if err != nil {
		return -1, res, err
	}

	// Build allocation
	block := &deviceMemoryBlock{}
	err = block.Init(l.parentAllocator, l.parentPool, l.memoryTypeIndex, memory, allocInfo.AllocationSize, l.nextBlockId, l.algorithm, l.bufferImageGranularity)
	if err != nil {
		return -1, core1_0.VKErrorUnknown, err
	}
	l.nextBlockId++

	l.blocks = append(l.blocks, block)
	return len(l.blocks) - 1, res, nil
}
