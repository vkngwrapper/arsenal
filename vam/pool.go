package vam

import (
	"github.com/cockroachdb/errors"
	"github.com/vkngwrapper/core/v2/common"
)

type PoolCreateInfo struct {
	MemoryTypeIndex int
	Flags           PoolCreateFlags

	BlockSize     int
	MinBlockCount int
	MaxBlockCount int

	Priority               float32
	MinAllocationAlignment uint
	MemoryAllocateNext     common.Options
}

type Pool struct {
	blockList            memoryBlockList
	dedicatedAllocations dedicatedAllocationList

	id   int
	name string
	prev *Pool
	next *Pool
}

func NewPool(allocator *Allocator, createInfo PoolCreateInfo, preferredBlockSize int) *Pool {
	pool := &Pool{}
	blockSize := preferredBlockSize
	if createInfo.BlockSize != 0 {
		blockSize = createInfo.BlockSize
	}
	bufferImageGranularity := 1
	if createInfo.Flags&PoolCreateIgnoreBufferImageGranularity == 0 {
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
		createInfo.Flags&PoolCreateAlgorithmMask,
		createInfo.Priority,
		alignment,
		allocator.extensionData,
		allocator.deviceMemory,
		createInfo.MemoryAllocateNext,
	)

	return pool
}

func (p *Pool) SetName(name string) {
	p.name = name
}

func (p *Pool) SetID(id int) error {
	if p.id != 0 {
		return errors.New("attempted to set id on a vulkan pool that already has one")
	}
	p.id = id
	return nil
}

func (p *Pool) ID() int {
	return p.id
}

func (p *Pool) Name() string {
	return p.name
}

func (p *Pool) unregisterDedicatedAllocation(alloc *Allocation) {
	p.dedicatedAllocations.Unregister(alloc)
}
