package vam

import (
	"io"
	"log/slog"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/core/v3/common"
	"github.com/vkngwrapper/core/v3/core1_0"
	"github.com/vkngwrapper/core/v3/core1_1"
	"github.com/vkngwrapper/core/v3/core1_2"
	"github.com/vkngwrapper/core/v3/mocks"
	"github.com/vkngwrapper/core/v3/mocks/mocks1_2"
	"github.com/vkngwrapper/extensions/v3/ext_memory_priority"
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
	PreNewMock         func(driver *mocks1_2.MockCoreDeviceDriver, physDevice core1_0.PhysicalDevice)
}

func readyAllocator(t *testing.T, ctrl *gomock.Controller, setup AllocatorSetup) (*mocks1_2.MockCoreDeviceDriver, core1_0.PhysicalDevice, *Allocator) {
	mockInstance := mocks1_2.NewMockCoreInstanceDriver(ctrl)
	mockCore := mocks1_2.NewMockCoreDeviceDriver(ctrl)
	mockCore.EXPECT().InstanceDriver().Return(mockInstance).AnyTimes()

	instance := mocks.NewDummyInstance(common.Vulkan1_2, setup.InstanceExtensions)
	physicalDevice := mocks.NewDummyPhysicalDevice(instance, setup.DeviceVersion)
	device := mocks.NewDummyDevice(setup.DeviceVersion, setup.DeviceExtensions)

	mockInstance.EXPECT().Instance().Return(instance).AnyTimes()
	mockCore.EXPECT().Device().Return(device).AnyTimes()

	mockInstance.EXPECT().GetPhysicalDeviceProperties(physicalDevice).Return(&setup.DeviceProperties, nil).AnyTimes()
	mockInstance.EXPECT().GetPhysicalDeviceMemoryProperties(physicalDevice).Return(&core1_0.PhysicalDeviceMemoryProperties{
		MemoryTypes: setup.MemoryTypes,
		MemoryHeaps: setup.MemoryHeaps,
	}).AnyTimes()

	if setup.PreNewMock != nil {
		setup.PreNewMock(mockCore, physicalDevice)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	allocator, err := New(logger, mockCore, physicalDevice, setup.AllocatorOptions)
	require.NoError(t, err)

	return mockCore, physicalDevice, allocator
}

func TestAllocateMemory(t *testing.T) {
	ctrl := gomock.NewController(t)

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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
	memory := mocks.NewDummyDeviceMemory(driver.Device(), 15628)
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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
	driver.EXPECT().FreeMemory(memory, nil)

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

	err = allocator.Destroy()
	require.NoError(t, err)
}

func TestAllocateMemoryDestroyWithoutFree(t *testing.T) {
	ctrl := gomock.NewController(t)

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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
	memory := mocks.NewDummyDeviceMemory(driver.Device(), 15628)
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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

	err = allocator.Destroy()
	require.Error(t, err)
}

func TestAllocateMemorySlice(t *testing.T) {
	ctrl := gomock.NewController(t)

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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
	memory := mocks.NewDummyDeviceMemory(driver.Device(), 15628)
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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
	driver.EXPECT().FreeMemory(memory, nil)

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

	err = allocator.Destroy()
	require.NoError(t, err)
}

func TestAllocateDedicatedMemory(t *testing.T) {
	ctrl := gomock.NewController(t)

	allocCalled := 0
	freeCalled := 0

	var allocator *Allocator
	var memory core1_0.DeviceMemory

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

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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

	memory = mocks.NewDummyDeviceMemory(driver.Device(), 1000)
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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
	driver.EXPECT().FreeMemory(memory, gomock.Any())

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

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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

	memory := mocks.NewDummyDeviceMemory(driver.Device(), 80000)
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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
	driver.EXPECT().FreeMemory(memory, nil)

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

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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

	memory := mocks.NewDummyDeviceMemory(driver.Device(), 15628)
	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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

	mockBuffer := mocks.NewDummyBuffer(driver.Device())
	driver.EXPECT().CreateBuffer(gomock.Any(), core1_0.BufferCreateInfo{
		Size:  1000,
		Usage: core1_0.BufferUsageStorageBuffer,
	}).Return(mockBuffer, core1_0.VKSuccess, nil)
	driver.EXPECT().BindBufferMemory(mockBuffer, memory, 0).Return(core1_0.VKSuccess, nil)
	driver.EXPECT().DestroyBuffer(mockBuffer, nil)

	driver.EXPECT().GetBufferMemoryRequirements2(core1_1.BufferMemoryRequirementsInfo2{
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

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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

	memory := mocks.NewDummyDeviceMemory(driver.Device(), 15628)
	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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

	mockBuffer := mocks.NewDummyBuffer(driver.Device())

	driver.EXPECT().GetBufferMemoryRequirements2(core1_1.BufferMemoryRequirementsInfo2{
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

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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

	memory := mocks.NewDummyDeviceMemory(driver.Device(), 15628)
	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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

	mockBuffer := mocks.NewDummyBuffer(driver.Device())
	driver.EXPECT().CreateBuffer(gomock.Any(), core1_0.BufferCreateInfo{
		Size:  1000,
		Usage: core1_0.BufferUsageStorageBuffer,
	}).Return(mockBuffer, core1_0.VKSuccess, nil)
	driver.EXPECT().BindBufferMemory(mockBuffer, memory, 0).Return(core1_0.VKSuccess, nil)
	driver.EXPECT().DestroyBuffer(mockBuffer, nil)

	driver.EXPECT().GetBufferMemoryRequirements2(core1_1.BufferMemoryRequirementsInfo2{
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

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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

	memory := mocks.NewDummyDeviceMemory(driver.Device(), 15628)
	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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

	mockImage := mocks.NewDummyImage(driver.Device())
	driver.EXPECT().CreateImage(gomock.Any(), core1_0.ImageCreateInfo{
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
	driver.EXPECT().BindImageMemory(mockImage, memory, 0).Return(core1_0.VKSuccess, nil)
	driver.EXPECT().DestroyImage(mockImage, nil)

	driver.EXPECT().GetImageMemoryRequirements2(core1_1.ImageMemoryRequirementsInfo2{
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

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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

	memory := mocks.NewDummyDeviceMemory(driver.Device(), 15628)
	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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

	mockImage := mocks.NewDummyImage(driver.Device())

	driver.EXPECT().GetImageMemoryRequirements2(core1_1.ImageMemoryRequirementsInfo2{
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

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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
	memory := mocks.NewDummyDeviceMemory(driver.Device(), 15628)
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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
	driver.EXPECT().FreeMemory(memory, nil)

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

	var allocation2 Allocation
	_, err = allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           1000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage:    MemoryUsageAuto,
		Flags:    0,
		Priority: 1,
		Pool:     pool,
	}, &allocation2)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation2.Size())

	err = allocation.Free()
	require.NoError(t, err)

	err = allocation2.Free()
	require.NoError(t, err)

	err = pool.Destroy()
	require.NoError(t, err)

	err = allocator.Destroy()
	require.NoError(t, err)
}

func TestCalculateStatistics(t *testing.T) {
	ctrl := gomock.NewController(t)

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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
				MaxMemoryAllocationCount: 2,
			},
		},
		AllocatorOptions: CreateOptions{},
	})

	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
	memory := mocks.NewDummyDeviceMemory(driver.Device(), 15628)
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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

	memory2 := mocks.NewDummyDeviceMemory(driver.Device(), 1000)
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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
	}).Return(memory2, core1_0.VKSuccess, nil)

	var allocation2 Allocation
	_, err = allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           1000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage:    MemoryUsageAuto,
		Flags:    AllocationCreateDedicatedMemory,
		Priority: 1,
	}, &allocation2)
	require.NoError(t, err)

	var stats AllocatorStatistics
	allocator.CalculateStatistics(&stats)

	freeRangeCount := 1
	minFreeRangeSize := 14628
	maxFreeRangeSize := 14628

	if memutils.DebugMargin > 0 {
		freeRangeCount += 1
		minFreeRangeSize = memutils.DebugMargin
		maxFreeRangeSize -= memutils.DebugMargin
	}

	empty := memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      0,
			AllocationCount: 0,
			BlockBytes:      0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   0,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: math.MaxInt,
		UnusedRangeSizeMax: 0,
	}

	require.Equal(t, AllocatorStatistics{
		MemoryTypes: [common.MaxMemoryTypes]memutils.DetailedStatistics{
			memutils.DetailedStatistics{
				Statistics: memutils.Statistics{
					BlockCount:      2,
					AllocationCount: 2,
					BlockBytes:      16628,
					AllocationBytes: 2000,
				},
				UnusedRangeCount:   freeRangeCount,
				AllocationSizeMin:  1000,
				AllocationSizeMax:  1000,
				UnusedRangeSizeMin: minFreeRangeSize,
				UnusedRangeSizeMax: maxFreeRangeSize,
			}, empty, empty, empty, empty, empty, empty, empty,
			empty, empty, empty, empty, empty, empty, empty, empty,
			empty, empty, empty, empty, empty, empty, empty, empty,
			empty, empty, empty, empty, empty, empty, empty, empty,
		},
		MemoryHeaps: [common.MaxMemoryHeaps]memutils.DetailedStatistics{
			memutils.DetailedStatistics{
				Statistics: memutils.Statistics{
					BlockCount:      2,
					AllocationCount: 2,
					BlockBytes:      16628,
					AllocationBytes: 2000,
				},
				UnusedRangeCount:   freeRangeCount,
				AllocationSizeMin:  1000,
				AllocationSizeMax:  1000,
				UnusedRangeSizeMin: minFreeRangeSize,
				UnusedRangeSizeMax: maxFreeRangeSize,
			}, empty, empty, empty, empty, empty, empty, empty,
			empty, empty, empty, empty, empty, empty, empty, empty,
		},
		Total: memutils.DetailedStatistics{
			Statistics: memutils.Statistics{
				BlockCount:      2,
				AllocationCount: 2,
				BlockBytes:      16628,
				AllocationBytes: 2000,
			},
			UnusedRangeCount:   freeRangeCount,
			AllocationSizeMin:  1000,
			AllocationSizeMax:  1000,
			UnusedRangeSizeMin: minFreeRangeSize,
			UnusedRangeSizeMax: maxFreeRangeSize,
		},
	}, stats)
}

func TestCreateAliasingImage(t *testing.T) {
	ctrl := gomock.NewController(t)

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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

	memory := mocks.NewDummyDeviceMemory(driver.Device(), 15628)
	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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

	mockBuffer := mocks.NewDummyBuffer(driver.Device())
	driver.EXPECT().CreateBuffer(gomock.Any(), core1_0.BufferCreateInfo{
		Size:  1000,
		Usage: core1_0.BufferUsageStorageBuffer,
	}).Return(mockBuffer, core1_0.VKSuccess, nil)
	driver.EXPECT().BindBufferMemory(mockBuffer, memory, 0).Return(core1_0.VKSuccess, nil)
	driver.EXPECT().DestroyBuffer(mockBuffer, nil)

	mockImage := mocks.NewDummyImage(driver.Device())
	driver.EXPECT().CreateImage(gomock.Any(), core1_0.ImageCreateInfo{
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
	driver.EXPECT().BindImageMemory(mockImage, memory, 0).Return(core1_0.VKSuccess, nil)
	driver.EXPECT().DestroyImage(mockImage, nil)

	driver.EXPECT().GetBufferMemoryRequirements2(core1_1.BufferMemoryRequirementsInfo2{
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

	image, _, err := allocation.CreateAliasingImageWithOffset(0, core1_0.ImageCreateInfo{
		Usage:  core1_0.ImageUsageStorage,
		Format: core1_0.FormatA1R5G5B5UnsignedNormalizedPacked,
		Extent: core1_0.Extent3D{
			Width:  100,
			Height: 100,
			Depth:  1,
		},
		MipLevels:   1,
		ArrayLayers: 1,
	})
	require.NoError(t, err)

	driver.DestroyImage(image, nil)
	err = allocation.DestroyBuffer(buffer)
	require.NoError(t, err)
}

func TestCreateAliasingBuffer(t *testing.T) {
	ctrl := gomock.NewController(t)

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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

	memory := mocks.NewDummyDeviceMemory(driver.Device(), 15628)
	// Expect a block to be allocated, the default preferred size for a 1MB heap is
	// 128KB (125024) but it will size down by half 3 times because the allocation is so small
	// resulting in 15628
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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

	mockImage := mocks.NewDummyImage(driver.Device())
	driver.EXPECT().CreateImage(gomock.Any(), core1_0.ImageCreateInfo{
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
	driver.EXPECT().BindImageMemory(mockImage, memory, 0).Return(core1_0.VKSuccess, nil)
	driver.EXPECT().DestroyImage(mockImage, nil)

	mockBuffer := mocks.NewDummyBuffer(driver.Device())
	driver.EXPECT().CreateBuffer(gomock.Any(), core1_0.BufferCreateInfo{
		Usage: core1_0.BufferUsageStorageBuffer,
		Size:  500,
	}).Return(mockBuffer, core1_0.VKSuccess, nil)
	driver.EXPECT().BindBufferMemory(mockBuffer, memory, 100).Return(core1_0.VKSuccess, nil)
	driver.EXPECT().DestroyBuffer(mockBuffer, nil)

	driver.EXPECT().GetImageMemoryRequirements2(core1_1.ImageMemoryRequirementsInfo2{
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

	buffer, _, err := allocation.CreateAliasingBufferWithOffset(100, core1_0.BufferCreateInfo{
		Size:  500,
		Usage: core1_0.BufferUsageStorageBuffer,
	})
	require.NoError(t, err)

	driver.DestroyBuffer(buffer, nil)
	err = allocation.DestroyImage(image)
	require.NoError(t, err)
}

func TestAllocateMultipleBlocks(t *testing.T) {
	ctrl := gomock.NewController(t)

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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
				Size:  64000,
				Flags: core1_0.MemoryHeapDeviceLocal,
			},
			{
				Size:  64000,
				Flags: 0,
			},
		},
		DeviceExtensions: []string{ext_memory_priority.ExtensionName},
		DeviceProperties: core1_0.PhysicalDeviceProperties{
			DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
			Limits: &core1_0.PhysicalDeviceLimits{
				BufferImageGranularity:   1,
				NonCoherentAtomSize:      1,
				MaxMemoryAllocationCount: 3,
			},
		},
		AllocatorOptions: CreateOptions{},
	})

	// Expect a block to be allocated, the default preferred size for a 32KB heap is
	// 8KB but it will size down by half 3 times because the allocation is so small
	// resulting in 2000
	memory := mocks.NewDummyDeviceMemory(driver.Device(), 2000)
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
		MemoryTypeIndex: 0,
		AllocationSize:  2000,
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
	driver.EXPECT().FreeMemory(memory, nil)

	// The second block will be double sized, so 4KB
	memory2 := mocks.NewDummyDeviceMemory(driver.Device(), 4000)
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
		MemoryTypeIndex: 0,
		AllocationSize:  4000,
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
	}).Return(memory2, core1_0.VKSuccess, nil)
	driver.EXPECT().FreeMemory(memory2, nil)

	// The third block will be double sized again, so 8KB
	memory3 := mocks.NewDummyDeviceMemory(driver.Device(), 8000)
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
		MemoryTypeIndex: 0,
		AllocationSize:  8000,
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
	}).Return(memory3, core1_0.VKSuccess, nil)
	driver.EXPECT().FreeMemory(memory3, nil)

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

	err = allocator.Destroy()
	require.NoError(t, err)
}

func TestAllocateMemoryMinTime(t *testing.T) {
	ctrl := gomock.NewController(t)

	driver, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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
	memory := mocks.NewDummyDeviceMemory(driver.Device(), 15628)
	driver.EXPECT().AllocateMemory(gomock.Any(), core1_0.MemoryAllocateInfo{
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
	driver.EXPECT().FreeMemory(memory, nil)

	var allocation Allocation
	_, err := allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           1000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage:    MemoryUsageAuto,
		Flags:    AllocationCreateStrategyMinTime,
		Priority: 1,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	err = allocation.Free()
	require.NoError(t, err)

	err = allocator.Destroy()
	require.NoError(t, err)
}
