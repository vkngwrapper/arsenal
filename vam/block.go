package vam

import (
	stderr "errors"
	"fmt"
	"github.com/cockroachdb/errors"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
	"github.com/vkngwrapper/arsenal/vam/internal/vulkan"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"golang.org/x/exp/slog"
)

type deviceMemoryBlock struct {
	id              int
	memory          *vulkan.SynchronizedMemory
	memoryTypeIndex int
	logger          *slog.Logger

	metadata     metadata.BlockMetadata
	deviceMemory *vulkan.DeviceMemoryProperties
}

func (b *deviceMemoryBlock) Init(
	logger *slog.Logger,
	deviceMemory *vulkan.DeviceMemoryProperties,
	newMemoryTypeIndex int,
	newMemory *vulkan.SynchronizedMemory,
	newSize int,
	id int,
	algorithm PoolCreateFlags,
	bufferImageGranularity int,
) {
	if b.memory != nil {
		panic("attempting to initialize a device memory block that is already in use")
	}

	b.memoryTypeIndex = newMemoryTypeIndex
	b.id = id
	b.memory = newMemory
	b.deviceMemory = deviceMemory
	b.logger = logger

	switch algorithm {
	case 0:
		b.metadata = metadata.NewTLSFBlockMetadata(bufferImageGranularity, false)
		break
	case PoolCreateLinearAlgorithm:
		b.metadata = metadata.NewLinearBlockMetadata(bufferImageGranularity, false)
		break
	default:
		panic(fmt.Sprintf("unknown pool algorithm: %s", algorithm.String()))
	}

	b.metadata.Init(newSize)
}

func (b *deviceMemoryBlock) Destroy() error {
	if !b.metadata.IsEmpty() {
		// Log all remaining allocations
		b.metadata.DebugLogAllAllocations(b.logger, b.logUnreleasedMemory)

		return errors.New("some allocations were not freed before the destruction of this memory block!")
	}

	if b.memory == nil {
		panic("attempting to destroy a memory block, but it did not have a backing vulkan memory handle")
	}

	b.deviceMemory.FreeVulkanMemory(b.memoryTypeIndex, b.metadata.Size(), b.memory)
	b.metadata.Destroy()

	b.memory = nil
	b.metadata = nil
	return nil
}

func (b *deviceMemoryBlock) logUnreleasedMemory(logger *slog.Logger, offset, size int, userData any) {
	allocation := userData.(*Allocation)
	userData = allocation.UserData()
	name := allocation.Name()
	if name == "" {
		name = "empty"
	}

	logger.Error("[UNRELEASED MEMORY] unfreed allocation",
		slog.Int("offset", offset),
		slog.Int("size", size),
		slog.Any("userData", userData),
		slog.String("name", name),
	)
}

func (b *deviceMemoryBlock) Validate() error {
	if b.memory == nil {
		return errors.New("no valid memory for this memory block")
	}
	if b.metadata.Size() < 1 {
		return errors.New("this memory block's metadata has an invalid size")
	}

	var err error

	b.metadata.VisitAllBlocks(func(handle metadata.BlockAllocationHandle, offset, size int, userData any, free bool) {
		allocation, isAllocation := userData.(*Allocation)
		if free && isAllocation {
			err = stderr.Join(err, errors.Newf("an allocation at offset %d is marked as free but contains an allocation object", offset))
		} else if !free && (!isAllocation || allocation == nil) {
			err = stderr.Join(err, errors.Newf("an allocation at offset %d is marked as allocated but has no allocation object", offset))
		}
	})

	if err != nil {
		return err
	}

	return b.metadata.Validate()
}

func (b *deviceMemoryBlock) CheckCorruption() (res common.VkResult, err error) {
	data, res, err := b.memory.Map(1, 0, -1, 0)
	if err != nil {
		return res, err
	}
	defer func() {
		unmapErr := b.memory.Unmap(1)
		if err == nil && unmapErr != nil {
			err = unmapErr
			res = core1_0.VKErrorUnknown
		}
	}()

	return b.metadata.CheckCorruption(data)
}

func (b *deviceMemoryBlock) WriteMagicBlockAfterAllocation(allocOffset int, allocSize int) (res common.VkResult, err error) {
	if memutils.DebugMargin == 0 {
		return core1_0.VKErrorUnknown, errors.New("attempting to write a debug margin block outside debug mode")
	} else if memutils.DebugMargin%4 != 0 {
		panic(fmt.Sprintf("invalid debug margin: debug margin %d must be a multiple of 4", memutils.DebugMargin))
	}

	data, res, err := b.memory.Map(1, 0, -1, 0)
	if err != nil {
		return res, err
	}
	defer func() {
		unmapErr := b.memory.Unmap(1)
		if err == nil && unmapErr != nil {
			err = unmapErr
			res = core1_0.VKErrorUnknown
		}
	}()

	memutils.WriteMagicValue(data, allocOffset+allocSize)

	return res, nil
}

func (b *deviceMemoryBlock) ValidateMagicValueAfterAllocation(allocOffset int, allocSize int) (common.VkResult, error) {
	if memutils.DebugMargin == 0 {
		panic("attempting to validate a debug margin block outside debug mode")
	} else if memutils.DebugMargin%4 != 0 {
		panic(fmt.Sprintf("invalid debug margin: debug margin %d must be a multiple of 4", memutils.DebugMargin))
	}

	data, res, err := b.memory.Map(1, 0, -1, 0)
	if err != nil {
		return res, err
	}
	defer func() {
		err = b.memory.Unmap(1)
		if err != nil {
			res = core1_0.VKErrorUnknown
		}
	}()

	if !memutils.ValidateMagicValue(data, allocOffset+allocSize) {
		panic("MEMORY CORRUPTION DETECTED AFTER FREED ALLOCATION")
	}

	return res, nil
}
