package vam

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/core1_1"
	"github.com/vkngwrapper/core/v2/core1_2"
	"github.com/vkngwrapper/core/v2/mocks"
	"github.com/vkngwrapper/extensions/v2/ext_memory_priority"
	"go.uber.org/mock/gomock"
)

func TestAllocateAndMapNonCoherent(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCached,
				HeapIndex:     0,
			},
			{
				PropertyFlags: 0,
				HeapIndex:     1,
			},
		},
		MemoryHeaps: []core1_0.MemoryHeap{
			{
				Size:  1000000,
				Flags: core1_0.MemoryHeapDeviceLocal,
			},
			{
				Size:  1000000,
				Flags: 0,
			},
		},
		DeviceExtensions: []string{ext_memory_priority.ExtensionName},
		DeviceProperties: core1_0.PhysicalDeviceProperties{
			DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
			Limits: &core1_0.PhysicalDeviceLimits{
				BufferImageGranularity:   1,
				NonCoherentAtomSize:      1,
				MaxMemoryAllocationCount: 1,
			},
		},
		AllocatorOptions: CreateOptions{},
	})

	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
	memory := mocks.EasyMockDeviceMemory(ctrl)
	device.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
		MemoryTypeIndex: 0,
		AllocationSize:  15628,
		NextOptions: common.NextOptions{
			Next: ext_memory_priority.MemoryPriorityAllocateInfo{
				Priority: 0.5,
				NextOptions: common.NextOptions{
					Next: core1_1.MemoryAllocateFlagsInfo{
						Flags: core1_2.MemoryAllocateDeviceAddress,
					},
				},
			},
		},
	}).Return(memory, core1_0.VKSuccess, nil)

	data := make([]byte, 15628)
	dataPtr := unsafe.Pointer(&data[0])

	memory.EXPECT().Map(0, -1, core1_0.MemoryMapFlags(0)).Return(dataPtr, core1_0.VKSuccess, nil)
	device.EXPECT().FlushMappedMemoryRanges([]core1_0.MappedMemoryRange{
		{
			Memory: memory,
			Offset: 0,
			Size:   500,
		},
	}).Return(core1_0.VKSuccess, nil)
	device.EXPECT().InvalidateMappedMemoryRanges([]core1_0.MappedMemoryRange{
		{
			Memory: memory,
			Offset: 500,
			Size:   500,
		},
	}).Return(core1_0.VKSuccess, nil)
	memory.EXPECT().Unmap()

	var allocation Allocation
	_, err := allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           1000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage: MemoryUsageAutoPreferDevice,
		Flags: AllocationCreateHostAccessRandom,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	_, _, err = allocation.Map()
	require.NoError(t, err)

	_, err = allocation.Flush(0, 500)
	require.NoError(t, err)

	_, err = allocation.Invalidate(500, 500)
	require.NoError(t, err)

	err = allocation.Unmap()
	require.NoError(t, err)

	err = allocation.Free()
	require.NoError(t, err)
}

func TestAllocateAndMapDedicatedNonCoherent(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCached,
				HeapIndex:     0,
			},
			{
				PropertyFlags: 0,
				HeapIndex:     1,
			},
		},
		MemoryHeaps: []core1_0.MemoryHeap{
			{
				Size:  1000000,
				Flags: core1_0.MemoryHeapDeviceLocal,
			},
			{
				Size:  1000000,
				Flags: 0,
			},
		},
		DeviceExtensions: []string{ext_memory_priority.ExtensionName},
		DeviceProperties: core1_0.PhysicalDeviceProperties{
			DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
			Limits: &core1_0.PhysicalDeviceLimits{
				BufferImageGranularity:   1,
				NonCoherentAtomSize:      1,
				MaxMemoryAllocationCount: 1,
			},
		},
		AllocatorOptions: CreateOptions{},
	})

	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
	memory := mocks.EasyMockDeviceMemory(ctrl)
	device.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
		MemoryTypeIndex: 0,
		AllocationSize:  1000,
		NextOptions: common.NextOptions{
			Next: ext_memory_priority.MemoryPriorityAllocateInfo{
				Priority: 0,
				NextOptions: common.NextOptions{
					Next: core1_1.MemoryAllocateFlagsInfo{
						Flags: core1_2.MemoryAllocateDeviceAddress,
					},
				},
			},
		},
	}).Return(memory, core1_0.VKSuccess, nil)
	memory.EXPECT().Free(nil)

	data := make([]byte, 1000)
	dataPtr := unsafe.Pointer(&data[0])

	memory.EXPECT().Map(0, -1, core1_0.MemoryMapFlags(0)).Return(dataPtr, core1_0.VKSuccess, nil)
	device.EXPECT().FlushMappedMemoryRanges([]core1_0.MappedMemoryRange{
		{
			Memory: memory,
			Offset: 0,
			Size:   500,
		},
	}).Return(core1_0.VKSuccess, nil)
	device.EXPECT().InvalidateMappedMemoryRanges([]core1_0.MappedMemoryRange{
		{
			Memory: memory,
			Offset: 500,
			Size:   500,
		},
	}).Return(core1_0.VKSuccess, nil)
	memory.EXPECT().Unmap()

	var allocation Allocation
	_, err := allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           1000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage: MemoryUsageAutoPreferDevice,
		Flags: AllocationCreateHostAccessRandom | AllocationCreateDedicatedMemory,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	_, _, err = allocation.Map()
	require.NoError(t, err)

	_, err = allocation.Flush(0, 500)
	require.NoError(t, err)

	_, err = allocation.Invalidate(500, 500)
	require.NoError(t, err)

	err = allocation.Unmap()
	require.NoError(t, err)

	err = allocation.Free()
	require.NoError(t, err)
}

func TestAllocateAndMap(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent,
				HeapIndex:     0,
			},
			{
				PropertyFlags: 0,
				HeapIndex:     1,
			},
		},
		MemoryHeaps: []core1_0.MemoryHeap{
			{
				Size:  1000000,
				Flags: core1_0.MemoryHeapDeviceLocal,
			},
			{
				Size:  1000000,
				Flags: 0,
			},
		},
		DeviceExtensions: []string{ext_memory_priority.ExtensionName},
		DeviceProperties: core1_0.PhysicalDeviceProperties{
			DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
			Limits: &core1_0.PhysicalDeviceLimits{
				BufferImageGranularity:   1,
				NonCoherentAtomSize:      1,
				MaxMemoryAllocationCount: 1,
			},
		},
		AllocatorOptions: CreateOptions{},
	})

	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
	memory := mocks.EasyMockDeviceMemory(ctrl)
	device.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
		MemoryTypeIndex: 0,
		AllocationSize:  15628,
		NextOptions: common.NextOptions{
			Next: ext_memory_priority.MemoryPriorityAllocateInfo{
				Priority: 0.5,
				NextOptions: common.NextOptions{
					Next: core1_1.MemoryAllocateFlagsInfo{
						Flags: core1_2.MemoryAllocateDeviceAddress,
					},
				},
			},
		},
	}).Return(memory, core1_0.VKSuccess, nil)

	data := make([]byte, 15628)
	dataPtr := unsafe.Pointer(&data[0])

	mapCount := 1
	unmapCount := 1
	if memutils.DebugMargin > 0 {
		// We map immediately after allocation to write corruption tag values
		// and immediately after free to verify corruption tag values are still there
		mapCount = 3

		// Due to repeated map/unmap operations in this test we should not unmap the last map
		// before free
		unmapCount = 2
	}

	memory.EXPECT().Map(0, -1, core1_0.MemoryMapFlags(0)).Return(dataPtr, core1_0.VKSuccess, nil).Times(mapCount)
	memory.EXPECT().Unmap().Times(unmapCount)

	var allocation Allocation
	_, err := allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           1000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage: MemoryUsageAutoPreferDevice,
		Flags: AllocationCreateHostAccessRandom,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	_, _, err = allocation.Map()
	require.NoError(t, err)
	err = allocation.Unmap()
	require.NoError(t, err)

	result, err := allocator.CheckCorruption(0xffffffff)

	if memutils.DebugMargin > 0 {
		require.NoError(t, err)
	} else {
		require.Error(t, err)
		require.Equal(t, core1_0.VKErrorFeatureNotPresent, result)
	}

	err = allocation.Free()
	require.NoError(t, err)
}

func TestAllocatePersistentMap(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent,
				HeapIndex:     0,
			},
			{
				PropertyFlags: 0,
				HeapIndex:     1,
			},
		},
		MemoryHeaps: []core1_0.MemoryHeap{
			{
				Size:  1000000,
				Flags: core1_0.MemoryHeapDeviceLocal,
			},
			{
				Size:  1000000,
				Flags: 0,
			},
		},
		DeviceExtensions: []string{ext_memory_priority.ExtensionName},
		DeviceProperties: core1_0.PhysicalDeviceProperties{
			DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
			Limits: &core1_0.PhysicalDeviceLimits{
				BufferImageGranularity:   1,
				NonCoherentAtomSize:      1,
				MaxMemoryAllocationCount: 1,
			},
		},
		AllocatorOptions: CreateOptions{},
	})

	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
	memory := mocks.EasyMockDeviceMemory(ctrl)
	device.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
		MemoryTypeIndex: 0,
		AllocationSize:  15628,
		NextOptions: common.NextOptions{
			Next: ext_memory_priority.MemoryPriorityAllocateInfo{
				Priority: 0.5,
				NextOptions: common.NextOptions{
					Next: core1_1.MemoryAllocateFlagsInfo{
						Flags: core1_2.MemoryAllocateDeviceAddress,
					},
				},
			},
		},
	}).Return(memory, core1_0.VKSuccess, nil)

	data := make([]byte, 15628)
	dataPtr := unsafe.Pointer(&data[0])

	memory.EXPECT().Map(0, -1, core1_0.MemoryMapFlags(0)).Return(dataPtr, core1_0.VKSuccess, nil)

	if memutils.DebugMargin == 0 {
		// The extra map/unmap calls for writing the debug margins
		// and reading them will force this into persistent map
		// and prevent unmap
		memory.EXPECT().Unmap()
	}

	var allocation Allocation
	_, err := allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           1000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage: MemoryUsageAutoPreferDevice,
		Flags: AllocationCreateHostAccessRandom | AllocationCreateMapped,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	_, _, err = allocation.Map()
	require.NoError(t, err)
	err = allocation.Unmap()
	require.NoError(t, err)

	result, err := allocator.CheckCorruption(0xffffffff)

	if memutils.DebugMargin > 0 {
		require.NoError(t, err)
	} else {
		require.Error(t, err)
		require.Equal(t, core1_0.VKErrorFeatureNotPresent, result)
	}

	err = allocation.Free()
	require.NoError(t, err)
}

func TestMapDedicatedMemory(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent,
				HeapIndex:     0,
			},
			{
				PropertyFlags: 0,
				HeapIndex:     1,
			},
		},
		MemoryHeaps: []core1_0.MemoryHeap{
			{
				Size:  1000000,
				Flags: core1_0.MemoryHeapDeviceLocal,
			},
			{
				Size:  1000000,
				Flags: 0,
			},
		},
		DeviceExtensions: []string{ext_memory_priority.ExtensionName},
		DeviceProperties: core1_0.PhysicalDeviceProperties{
			DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
			Limits: &core1_0.PhysicalDeviceLimits{
				BufferImageGranularity:   1,
				NonCoherentAtomSize:      1,
				MaxMemoryAllocationCount: 1,
			},
		},
		AllocatorOptions: CreateOptions{},
	})

	memory := mocks.EasyMockDeviceMemory(ctrl)
	device.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
		MemoryTypeIndex: 0,
		AllocationSize:  80000,
		NextOptions: common.NextOptions{
			Next: ext_memory_priority.MemoryPriorityAllocateInfo{
				Priority: 1,
				NextOptions: common.NextOptions{
					Next: core1_1.MemoryAllocateFlagsInfo{
						Flags: core1_2.MemoryAllocateDeviceAddress,
					},
				},
			},
		},
	}).Return(memory, core1_0.VKSuccess, nil)

	data := make([]byte, 80000)
	dataPtr := unsafe.Pointer(&data[0])

	memory.EXPECT().Map(0, -1, core1_0.MemoryMapFlags(0)).Return(dataPtr, core1_0.VKSuccess, nil)
	memory.EXPECT().Unmap()
	memory.EXPECT().Free(gomock.Any())

	var allocation Allocation
	_, err := allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           80000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage:    MemoryUsageAutoPreferDevice,
		Flags:    AllocationCreateHostAccessRandom,
		Priority: 1,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 80000, allocation.Size())

	_, _, err = allocation.Map()
	require.NoError(t, err)

	err = allocation.Unmap()
	require.NoError(t, err)

	err = allocation.Free()
	require.NoError(t, err)
}

func TestMapDedicatedMemoryPersistentMap(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent,
				HeapIndex:     0,
			},
			{
				PropertyFlags: 0,
				HeapIndex:     1,
			},
		},
		MemoryHeaps: []core1_0.MemoryHeap{
			{
				Size:  1000000,
				Flags: core1_0.MemoryHeapDeviceLocal,
			},
			{
				Size:  1000000,
				Flags: 0,
			},
		},
		DeviceExtensions: []string{ext_memory_priority.ExtensionName},
		DeviceProperties: core1_0.PhysicalDeviceProperties{
			DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
			Limits: &core1_0.PhysicalDeviceLimits{
				BufferImageGranularity:   1,
				NonCoherentAtomSize:      1,
				MaxMemoryAllocationCount: 1,
			},
		},
		AllocatorOptions: CreateOptions{},
	})

	memory := mocks.EasyMockDeviceMemory(ctrl)
	device.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
		MemoryTypeIndex: 0,
		AllocationSize:  80000,
		NextOptions: common.NextOptions{
			Next: ext_memory_priority.MemoryPriorityAllocateInfo{
				Priority: 1,
				NextOptions: common.NextOptions{
					Next: core1_1.MemoryAllocateFlagsInfo{
						Flags: core1_2.MemoryAllocateDeviceAddress,
					},
				},
			},
		},
	}).Return(memory, core1_0.VKSuccess, nil)

	data := make([]byte, 80000)
	dataPtr := unsafe.Pointer(&data[0])

	memory.EXPECT().Map(0, -1, core1_0.MemoryMapFlags(0)).Return(dataPtr, core1_0.VKSuccess, nil)
	memory.EXPECT().Free(gomock.Any())

	var allocation Allocation
	_, err := allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           80000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage:    MemoryUsageAutoPreferDevice,
		Flags:    AllocationCreateHostAccessRandom | AllocationCreateMapped,
		Priority: 1,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 80000, allocation.Size())

	_, _, err = allocation.Map()
	require.NoError(t, err)

	err = allocation.Unmap()
	require.NoError(t, err)

	err = allocation.Free()
	require.NoError(t, err)
}

func TestFlushMemorySlice(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCached,
				HeapIndex:     0,
			},
			{
				PropertyFlags: 0,
				HeapIndex:     1,
			},
		},
		MemoryHeaps: []core1_0.MemoryHeap{
			{
				Size:  1000000,
				Flags: core1_0.MemoryHeapDeviceLocal,
			},
			{
				Size:  1000000,
				Flags: 0,
			},
		},
		DeviceExtensions: []string{ext_memory_priority.ExtensionName},
		DeviceProperties: core1_0.PhysicalDeviceProperties{
			DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
			Limits: &core1_0.PhysicalDeviceLimits{
				BufferImageGranularity:   1,
				NonCoherentAtomSize:      1,
				MaxMemoryAllocationCount: 1,
			},
		},
		AllocatorOptions: CreateOptions{},
	})

	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
	memory := mocks.EasyMockDeviceMemory(ctrl)
	device.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
		MemoryTypeIndex: 0,
		AllocationSize:  15628,
		NextOptions: common.NextOptions{
			Next: ext_memory_priority.MemoryPriorityAllocateInfo{
				Priority: 0.5,
				NextOptions: common.NextOptions{
					Next: core1_1.MemoryAllocateFlagsInfo{
						Flags: core1_2.MemoryAllocateDeviceAddress,
					},
				},
			},
		},
	}).Return(memory, core1_0.VKSuccess, nil)

	data := make([]byte, 15628)
	dataPtr := unsafe.Pointer(&data[0])
	memory.EXPECT().Map(0, -1, core1_0.MemoryMapFlags(0)).Return(dataPtr, core1_0.VKSuccess, nil)

	device.EXPECT().FlushMappedMemoryRanges([]core1_0.MappedMemoryRange{
		{
			Memory: memory,
			Offset: 0,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 1000 + memutils.DebugMargin,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 2000 + memutils.DebugMargin*2,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 3000 + memutils.DebugMargin*3,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 4000 + memutils.DebugMargin*4,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 5000 + memutils.DebugMargin*5,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 6000 + memutils.DebugMargin*6,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 7000 + memutils.DebugMargin*7,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 8000 + memutils.DebugMargin*8,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 9000 + memutils.DebugMargin*9,
			Size:   500,
		},
	})

	device.EXPECT().InvalidateMappedMemoryRanges([]core1_0.MappedMemoryRange{
		{
			Memory: memory,
			Offset: 500,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 1500 + memutils.DebugMargin,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 2500 + memutils.DebugMargin*2,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 3500 + memutils.DebugMargin*3,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 4500 + memutils.DebugMargin*4,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 5500 + memutils.DebugMargin*5,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 6500 + memutils.DebugMargin*6,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 7500 + memutils.DebugMargin*7,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 8500 + memutils.DebugMargin*8,
			Size:   500,
		},
		{
			Memory: memory,
			Offset: 9500 + memutils.DebugMargin*9,
			Size:   500,
		},
	})

	allocations := make([]Allocation, 10)
	_, err := allocator.AllocateMemorySlice(&core1_0.MemoryRequirements{
		Size:           1000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage:    MemoryUsageAutoPreferDevice,
		Flags:    AllocationCreateHostAccessRandom,
		Priority: 1,
	}, allocations)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocations[0].Size())

	for i := 0; i < 10; i++ {
		_, _, err = allocations[i].Map()
		require.NoError(t, err)
	}

	_, err = allocator.FlushAllocationSlice(allocations, []int{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}, []int{
		500, 500, 500, 500, 500, 500, 500, 500, 500, 500,
	})
	require.NoError(t, err)

	_, err = allocator.InvalidateAllocationSlice(allocations, []int{
		500, 500, 500, 500, 500, 500, 500, 500, 500, 500,
	}, []int{
		500, 500, 500, 500, 500, 500, 500, 500, 500, 500,
	})
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		err = allocations[i].Unmap()
		require.NoError(t, err)
	}

	err = allocator.FreeAllocationSlice(allocations)
	require.NoError(t, err)
}
