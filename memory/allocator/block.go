package allocator

import (
	"fmt"
	"github.com/cockroachdb/errors"
	"github.com/vkngwrapper/arsenal/memory"
	"github.com/vkngwrapper/arsenal/memory/allocation"
	"github.com/vkngwrapper/arsenal/memory/internal/utils"
	"github.com/vkngwrapper/arsenal/memory/metadata"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"log"
	"os"
	"sync"
	"unsafe"
)

type BlockAllocation struct {
	block       *deviceMemoryBlock
	allocHandle allocation.BlockAllocationHandle
}

type deviceMemoryBlock struct {
	metadata metadata.BlockMetadata

	parentPool      memory.Pool
	memoryTypeIndex int
	id              int
	memory          core1_0.DeviceMemory

	// the DeviceMemory can only be accessed by one thread at a time- metadata is protected elsewhere
	mapAndBindMutex   sync.Mutex
	hysteresis        mappingHysteresis
	mapReferenceCount int
	mappedData        unsafe.Pointer
}

func (b *deviceMemoryBlock) Init(
	allocator Allocator,
	parentPool memory.Pool,
	newMemoryTypeIndex int,
	newMemory core1_0.DeviceMemory,
	newSize int,
	id int,
	algorithm memory.PoolCreateFlags,
	bufferImageGranularity int,
) error {
	if b.memory != nil {
		return errors.New("attempting to initialize a device memory block that is already in use")
	}

	b.parentPool = parentPool
	b.memoryTypeIndex = newMemoryTypeIndex
	b.id = id
	b.memory = newMemory

	switch algorithm {
	case 0:
		b.metadata = metadata.NewTLSFBlockMetadata(allocator.AllocationCallbacks(), bufferImageGranularity, false)
		break
	case memory.PoolCreateLinearAlgorithm:
		b.metadata = metadata.NewLinearBlockMetadata(allocator.AllocationCallbacks(), bufferImageGranularity, false)
		break
	default:
		return errors.New("unknown pool algorithm")
	}

	b.metadata.Init(newSize)
	return nil
}

func (b *deviceMemoryBlock) Destroy(allocator Allocator) error {
	if !b.metadata.IsEmpty() {
		// Log all remaining allocations
		b.metadata.DebugLogAllAllocations(log.New(os.Stdout, "[UNRELEASED MEMORY] ", log.LstdFlags))

		return errors.New("some allocations were not freed before the destruction of this memory block!")
	}

	if b.memory == nil {
		return errors.New("attempting to destroy a memory block, but it did not have a backing vulkan memory handle")
	}

	allocator.FreeVulkanMemory(b.memoryTypeIndex, b.metadata.Size(), b.memory)

	b.memory = nil
	b.metadata = nil
	return nil
}

func (b *deviceMemoryBlock) ParentPool() memory.Pool {
	return b.parentPool
}

func (b *deviceMemoryBlock) DeviceMemory() core1_0.DeviceMemory {
	return b.memory
}

func (b *deviceMemoryBlock) MemoryTypeIndex() int {
	return b.memoryTypeIndex
}

func (b *deviceMemoryBlock) ID() int { return b.id }

func (b *deviceMemoryBlock) MappedData() unsafe.Pointer { return b.mappedData }

func (b *deviceMemoryBlock) MapRefCount() int { return b.mapReferenceCount }

func (b *deviceMemoryBlock) PostAlloc(allocator Allocator) {
	if allocator.UseMutex() {
		b.mapAndBindMutex.Lock()
		defer b.mapAndBindMutex.Unlock()
	}

	b.hysteresis.PostAlloc()
}

func (b *deviceMemoryBlock) PostFree(allocator Allocator) error {
	if allocator.UseMutex() {
		b.mapAndBindMutex.Lock()
		defer b.mapAndBindMutex.Unlock()
	}

	if b.hysteresis.PostFree() {
		if b.hysteresis.ExtraMapping() {
			return errors.New("hysteresis indicated it had swapped, but it is in an unswapped state")
		}

		if b.mapReferenceCount == 0 {
			b.mappedData = nil
			b.memory.Unmap()
		}
	}

	return nil
}

func (b *deviceMemoryBlock) Validate() error {
	if b.memory == nil {
		return errors.New("no valid memory for this memory block")
	}
	if b.metadata.Size() < 1 {
		return errors.New("this memory block's metadata has an invalid size")
	}

	return b.metadata.Validate()
}

func (b *deviceMemoryBlock) CheckCorruption(allocator Allocator) (res common.VkResult, err error) {
	data, res, err := b.Map(allocator, 1)
	if err != nil {
		return res, err
	}
	defer func() {
		unmapErr := b.Unmap(allocator, 1)
		if err == nil && unmapErr != nil {
			err = unmapErr
			res = core1_0.VKErrorUnknown
		}
	}()

	return b.metadata.CheckCorruption(data)
}

func (b *deviceMemoryBlock) Map(allocator Allocator, references int) (unsafe.Pointer, common.VkResult, error) {
	if references == 0 {
		return nil, core1_0.VKSuccess, nil
	}

	if allocator.UseMutex() {
		b.mapAndBindMutex.Lock()
		defer b.mapAndBindMutex.Unlock()
	}

	oldTotalMapCount := b.mapReferenceCount
	if b.hysteresis.ExtraMapping() {
		oldTotalMapCount++
	}
	b.hysteresis.PostMap()

	if oldTotalMapCount != 0 {
		b.mapReferenceCount += references
		if b.mappedData == nil {
			return nil, core1_0.VKErrorUnknown, errors.New("the block is showing existing memory mapping references, but no mapped memory")
		}

		return b.mappedData, core1_0.VKSuccess, nil
	}

	mappedData, result, err := b.memory.Map(0, -1, 0)
	if err != nil {
		return nil, result, err
	}

	b.mappedData = mappedData
	b.mapReferenceCount = references
	return mappedData, result, nil
}

func (b *deviceMemoryBlock) Unmap(allocator Allocator, references int) error {
	if references == 0 {
		return nil
	}

	if allocator.UseMutex() {
		b.mapAndBindMutex.Lock()
		defer b.mapAndBindMutex.Unlock()
	}

	if b.mapReferenceCount < references {
		return errors.New("device memory block has more references being unmapped than are currently maped")
	}

	b.mapReferenceCount -= references
	totalMapCount := b.mapReferenceCount
	if b.hysteresis.ExtraMapping() {
		totalMapCount++
	}

	if totalMapCount == 0 {
		b.memory.Unmap()
	}
	b.hysteresis.PostUnmap()
	return nil
}

func (b *deviceMemoryBlock) WriteMagicBlockAfterAllocation(allocator Allocator, allocOffset int, allocSize int) (res common.VkResult, err error) {
	if utils.DebugMargin == 0 {
		return core1_0.VKErrorUnknown, errors.New("attempting to write a debug margin block outside debug mode")
	} else if utils.DebugMargin%4 != 0 {
		panic(fmt.Sprintf("invalid debug margin: debug margin %d must be a multiple of 4", utils.DebugMargin))
	}

	data, res, err := b.Map(allocator, 1)
	if err != nil {
		return res, err
	}
	defer func() {
		unmapErr := b.Unmap(allocator, 1)
		if err == nil && unmapErr != nil {
			err = unmapErr
			res = core1_0.VKErrorUnknown
		}
	}()

	utils.WriteMagicValue(data, allocOffset+allocSize)

	return res, nil
}

func (b *deviceMemoryBlock) ValidateMagicValueAfterAllocation(allocator Allocator, allocOffset int, allocSize int) (common.VkResult, error) {
	if utils.DebugMargin == 0 {
		return core1_0.VKErrorUnknown, errors.New("attempting to validate a debug margin block outside debug mode")
	} else if utils.DebugMargin%4 != 0 {
		panic(fmt.Sprintf("invalid debug margin: debug margin %d must be a multiple of 4", utils.DebugMargin))
	}

	data, res, err := b.Map(allocator, 1)
	if err != nil {
		return res, err
	}
	defer func() {
		unmapErr := b.Unmap(allocator, 1)
		if err == nil && unmapErr != nil {
			err = unmapErr
			res = core1_0.VKErrorUnknown
		}
	}()

	if !utils.ValidateMagicValue(data, allocOffset+allocSize) {
		return core1_0.VKErrorUnknown, errors.New("memory corruption detected after freed allocation")
	}

	return res, nil
}

func (b *deviceMemoryBlock) BindBufferMemory(allocator Allocator, alloc allocation.Allocation, allocationLocalOffset int, buffer core1_0.Buffer, bindOptions common.Options) (common.VkResult, error) {
	if alloc.AllocationType() != allocation.AllocationTypeBlock {
		return core1_0.VKErrorUnknown, errors.New("attempted to bind a non-block allocation to a buffer via a block")
	} else if alloc.
}