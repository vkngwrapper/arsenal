package vam

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/core1_1"
	"github.com/vkngwrapper/core/v2/core1_2"
	"github.com/vkngwrapper/core/v2/mocks"
	"github.com/vkngwrapper/extensions/v2/ext_memory_priority"
	"go.uber.org/mock/gomock"
)

type AllocatorSetup struct {
	DeviceVersion      common.APIVersion
	InstanceExtensions []string
	DeviceExtensions   []string
	MemoryTypes        []core1_0.MemoryType
	MemoryHeaps        []core1_0.MemoryHeap
	DeviceProperties   core1_0.PhysicalDeviceProperties
	AllocatorOptions   CreateOptions
	PreNewMock         func(instance *mocks.Instance1_2, instancePhysDevice *mocks.InstanceScopedPhysicalDevice1_2, physDevice *mocks.PhysicalDevice1_2, device *mocks.Device1_2)
}

func readyAllocator(t *testing.T, ctrl *gomock.Controller, setup AllocatorSetup) (*mocks.Instance1_2, *mocks.InstanceScopedPhysicalDevice1_2, *mocks.PhysicalDevice1_2, *mocks.Device1_2, *Allocator) {
	instance, instanceScopedPhysDevice, physDevice, device := mocks.MockRig1_2(ctrl, setup.DeviceVersion, setup.InstanceExtensions, setup.DeviceExtensions)

	physDevice.EXPECT().Properties().AnyTimes().Return(&setup.DeviceProperties, nil)
	physDevice.EXPECT().MemoryProperties().AnyTimes().Return(&core1_0.PhysicalDeviceMemoryProperties{
		MemoryTypes: setup.MemoryTypes,
		MemoryHeaps: setup.MemoryHeaps,
	})

	if setup.PreNewMock != nil {
		setup.PreNewMock(instance, instanceScopedPhysDevice, physDevice, device)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	allocator, err := New(logger, instance, physDevice, device, setup.AllocatorOptions)
	require.NoError(t, err)

	return instance, instanceScopedPhysDevice, physDevice, device, allocator
}

func TestAllocateMemory(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal,
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

	var allocation Allocation
	_, err := allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           1000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage:    MemoryUsageAuto,
		Flags:    0,
		Priority: 1,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	err = allocation.Free()
	require.NoError(t, err)
}

func TestAllocateMemorySlice(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal,
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

	allocations := make([]Allocation, 10)
	_, err := allocator.AllocateMemorySlice(&core1_0.MemoryRequirements{
		Size:           1000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage:    MemoryUsageAuto,
		Flags:    0,
		Priority: 1,
	}, allocations)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocations[0].Size())

	err = allocator.FreeAllocationSlice(allocations)
	require.NoError(t, err)
}

func TestAllocateDedicatedMemory(t *testing.T) {
	ctrl := gomock.NewController(t)

	allocCalled := 0
	freeCalled := 0

	var allocator *Allocator
	var memory *mocks.MockDeviceMemory

	callbacks := MemoryCallbackOptions{
		Allocate: func(actualAlloc *Allocator, memoryType int, actualMemory core1_0.DeviceMemory, size int, userData interface{}) {
			require.Equal(t, allocator, actualAlloc)
			require.Equal(t, 0, memoryType)
			require.Equal(t, memory, actualMemory)
			require.Equal(t, 1000, size)
			require.Equal(t, "wow", userData)
			allocCalled++
		},
		Free: func(actualAlloc *Allocator, memoryType int, actualMemory core1_0.DeviceMemory, size int, userData interface{}) {
			require.Equal(t, allocator, actualAlloc)
			require.Equal(t, 0, memoryType)
			require.Equal(t, memory, actualMemory)
			require.Equal(t, 1000, size)
			require.Equal(t, "wow", userData)
			freeCalled++
		},
		UserData: "wow",
	}

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal,
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
		AllocatorOptions: CreateOptions{
			MemoryCallbackOptions: &callbacks,
		},
	})

	require.Equal(t, 0, allocCalled)
	require.Equal(t, 0, freeCalled)

	memory = mocks.EasyMockDeviceMemory(ctrl)
	device.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
		MemoryTypeIndex: 0,
		AllocationSize:  1000,
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
	memory.EXPECT().Free(gomock.Any())

	var allocation Allocation
	_, err := allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           1000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage:    MemoryUsageAuto,
		Flags:    AllocationCreateDedicatedMemory,
		Priority: 1,
	}, &allocation)
	require.NoError(t, err)

	require.Equal(t, 1, allocCalled)
	require.Equal(t, 0, freeCalled)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	err = allocation.Free()
	require.NoError(t, err)

	require.Equal(t, 1, allocCalled)
	require.Equal(t, 1, freeCalled)
}

func TestAutoAllocateDedicatedMemory(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal,
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
	memory.EXPECT().Free(gomock.Any())

	var allocation Allocation
	_, err := allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           80000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage:    MemoryUsageAuto,
		Flags:    0,
		Priority: 1,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 80000, allocation.Size())

	err = allocation.Free()
	require.NoError(t, err)
}

func TestCreateBuffer(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal,
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
	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
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

	mockBuffer := mocks.EasyMockBuffer(ctrl)
	device.EXPECT().CreateBuffer(gomock.Any(), core1_0.BufferCreateInfo{
		Size:  1000,
		Usage: core1_0.BufferUsageStorageBuffer,
	}).Return(mockBuffer, core1_0.VKSuccess, nil)
	mockBuffer.EXPECT().BindBufferMemory(memory, 0).Return(core1_0.VKSuccess, nil)
	mockBuffer.EXPECT().Destroy(gomock.Any())

	device.EXPECT().BufferMemoryRequirements2(core1_1.BufferMemoryRequirementsInfo2{
		Buffer: mockBuffer,
	}, gomock.Any()).DoAndReturn(func(info core1_1.BufferMemoryRequirementsInfo2, out *core1_1.MemoryRequirements2) error {
		out.MemoryRequirements = core1_0.MemoryRequirements{
			Size:           1000,
			Alignment:      1,
			MemoryTypeBits: 0xffffffff,
		}
		return nil
	})

	var allocation Allocation
	buffer, _, err := allocator.CreateBuffer(core1_0.BufferCreateInfo{
		Size:  1000,
		Usage: core1_0.BufferUsageStorageBuffer,
	}, AllocationCreateInfo{
		Usage: MemoryUsageAuto,
	}, &allocation)
	require.NoError(t, err)
	require.Equal(t, mockBuffer, buffer)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	err = allocation.DestroyBuffer(buffer)
	require.NoError(t, err)
}

func TestAllocateForBuffer(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal,
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
	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
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

	mockBuffer := mocks.EasyMockBuffer(ctrl)

	device.EXPECT().BufferMemoryRequirements2(core1_1.BufferMemoryRequirementsInfo2{
		Buffer: mockBuffer,
	}, gomock.Any()).DoAndReturn(func(info core1_1.BufferMemoryRequirementsInfo2, out *core1_1.MemoryRequirements2) error {
		out.MemoryRequirements = core1_0.MemoryRequirements{
			Size:           1000,
			Alignment:      1,
			MemoryTypeBits: 0xffffffff,
		}
		return nil
	})

	var allocation Allocation
	_, err := allocator.AllocateMemoryForBuffer(mockBuffer, AllocationCreateInfo{
		Usage: MemoryUsageAuto,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	err = allocation.Free()
	require.NoError(t, err)
}

func TestCreateBufferWithAlignment(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal,
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
	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
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

	mockBuffer := mocks.EasyMockBuffer(ctrl)
	device.EXPECT().CreateBuffer(gomock.Any(), core1_0.BufferCreateInfo{
		Size:  1000,
		Usage: core1_0.BufferUsageStorageBuffer,
	}).Return(mockBuffer, core1_0.VKSuccess, nil)
	mockBuffer.EXPECT().BindBufferMemory(memory, 0).Return(core1_0.VKSuccess, nil)
	mockBuffer.EXPECT().Destroy(gomock.Any())

	device.EXPECT().BufferMemoryRequirements2(core1_1.BufferMemoryRequirementsInfo2{
		Buffer: mockBuffer,
	}, gomock.Any()).DoAndReturn(func(info core1_1.BufferMemoryRequirementsInfo2, out *core1_1.MemoryRequirements2) error {
		out.MemoryRequirements = core1_0.MemoryRequirements{
			Size:           1000,
			Alignment:      1,
			MemoryTypeBits: 0xffffffff,
		}
		return nil
	})

	var allocation Allocation
	buffer, _, err := allocator.CreateBufferWithAlignment(core1_0.BufferCreateInfo{
		Size:  1000,
		Usage: core1_0.BufferUsageStorageBuffer,
	}, AllocationCreateInfo{
		Usage: MemoryUsageAuto,
	}, 4, &allocation)
	require.NoError(t, err)
	require.Equal(t, mockBuffer, buffer)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	err = allocation.DestroyBuffer(buffer)
	require.NoError(t, err)
}

func TestCreateImage(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal,
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
	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
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

	mockImage := mocks.EasyMockImage(ctrl)
	device.EXPECT().CreateImage(gomock.Any(), core1_0.ImageCreateInfo{
		Usage:  core1_0.ImageUsageStorage,
		Format: core1_0.FormatA1R5G5B5UnsignedNormalizedPacked,
		Extent: core1_0.Extent3D{
			Width:  100,
			Height: 100,
			Depth:  1,
		},
		MipLevels:   1,
		ArrayLayers: 1,
	}).Return(mockImage, core1_0.VKSuccess, nil)
	mockImage.EXPECT().BindImageMemory(memory, 0).Return(core1_0.VKSuccess, nil)
	mockImage.EXPECT().Destroy(gomock.Any())

	device.EXPECT().ImageMemoryRequirements2(core1_1.ImageMemoryRequirementsInfo2{
		Image: mockImage,
	}, gomock.Any()).DoAndReturn(func(info core1_1.ImageMemoryRequirementsInfo2, out *core1_1.MemoryRequirements2) error {
		out.MemoryRequirements = core1_0.MemoryRequirements{
			Size:           1000,
			Alignment:      1,
			MemoryTypeBits: 0xffffffff,
		}
		return nil
	})

	var allocation Allocation
	image, _, err := allocator.CreateImage(core1_0.ImageCreateInfo{
		Usage:  core1_0.ImageUsageStorage,
		Format: core1_0.FormatA1R5G5B5UnsignedNormalizedPacked,
		Extent: core1_0.Extent3D{
			Width:  100,
			Height: 100,
			Depth:  1,
		},
		MipLevels:   1,
		ArrayLayers: 1,
	}, AllocationCreateInfo{
		Usage: MemoryUsageAuto,
	}, &allocation)
	require.NoError(t, err)
	require.Equal(t, mockImage, image)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	err = allocation.DestroyImage(image)
	require.NoError(t, err)
}

func TestAllocateForImage(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal,
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
	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
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

	mockImage := mocks.EasyMockImage(ctrl)

	device.EXPECT().ImageMemoryRequirements2(core1_1.ImageMemoryRequirementsInfo2{
		Image: mockImage,
	}, gomock.Any()).DoAndReturn(func(info core1_1.ImageMemoryRequirementsInfo2, out *core1_1.MemoryRequirements2) error {
		out.MemoryRequirements = core1_0.MemoryRequirements{
			Size:           1000,
			Alignment:      1,
			MemoryTypeBits: 0xffffffff,
		}
		return nil
	})

	var allocation Allocation
	_, err := allocator.AllocateMemoryForImage(mockImage, AllocationCreateInfo{
		Usage: MemoryUsageAuto,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	err = allocation.Free()
	require.NoError(t, err)
}

func TestAllocateWithPool(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, device, allocator := readyAllocator(t, ctrl, AllocatorSetup{
		DeviceVersion: common.Vulkan1_2,
		MemoryTypes: []core1_0.MemoryType{
			{
				PropertyFlags: core1_0.MemoryPropertyDeviceLocal,
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
	memory.EXPECT().Free(gomock.Any())

	pool, _, err := allocator.CreatePool(PoolCreateInfo{
		MemoryTypeIndex: 0,
		Flags:           PoolCreateLinearAlgorithm,
		Priority:        0.5,
	})
	require.NoError(t, err)

	var allocation Allocation
	_, err = allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           1000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage:    MemoryUsageAuto,
		Flags:    0,
		Priority: 1,
		Pool:     pool,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	err = allocation.Free()
	require.NoError(t, err)

	err = pool.Destroy()
	require.NoError(t, err)
}
