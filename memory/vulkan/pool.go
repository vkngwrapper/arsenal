package vulkan

import (
	"github.com/cockroachdb/errors"
	"github.com/vkngwrapper/arsenal/memory"
)

type VulkanPool struct {
	blockList            memoryBlockList
	dedicatedAllocations dedicatedAllocationList

	id   int
	name string
	prev *VulkanPool
	next *VulkanPool
}

func NewPool(allocator *VulkanAllocator, createInfo memory.PoolCreateInfo, preferredBlockSize int) *VulkanPool {
	pool := &VulkanPool{}
	blockSize := preferredBlockSize
	if createInfo.BlockSize != 0 {
		blockSize = createInfo.BlockSize
	}
	bufferImageGranularity := 1
	if createInfo.Flags&memory.PoolCreateIgnoreBufferImageGranularity == 0 {
		bufferImageGranularity = allocator.calculateBufferImageGranularity()
	}

	alignment := allocator.deviceMemory.MemoryTypeMinimumAlignment(createInfo.MemoryTypeIndex)
	if createInfo.MinAllocationAlignment > alignment {
		alignment = createInfo.MinAllocationAlignment
	}

	pool.blockList.Init(
		allocator.useMutex,
		allocator.logger,
		createInfo.MemoryTypeIndex,
		blockSize,
		createInfo.MinBlockCount,
		createInfo.MaxBlockCount,
		bufferImageGranularity,
		createInfo.BlockSize != 0,
		createInfo.Flags&memory.PoolCreateAlgorithmMask,
		createInfo.Priority,
		alignment,
		allocator.extensionData,
		allocator.deviceMemory,
		createInfo.MemoryAllocateNext,
	)

	return pool
}

func (p *VulkanPool) SetName(name string) {
	p.name = name
}

func (p *VulkanPool) SetID(id int) error {
	if p.id != 0 {
		return errors.New("attempted to set id on a vulkan pool that already has one")
	}
	p.id = id
	return nil
}

func (p *VulkanPool) ID() int {
	return p.id
}

func (p *VulkanPool) Name() string {
	return p.name
}
