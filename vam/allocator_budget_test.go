package vam

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/core1_1"
	"github.com/vkngwrapper/core/v2/core1_2"
	"github.com/vkngwrapper/core/v2/mocks"
	"github.com/vkngwrapper/extensions/v2/ext_memory_budget"
	"github.com/vkngwrapper/extensions/v2/ext_memory_priority"
	"go.uber.org/mock/gomock"
)

func TestAllocateMemoryWithBudgetForceDedicated(t *testing.T) {
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
		DeviceExtensions: []string{ext_memory_priority.ExtensionName, ext_memory_budget.ExtensionName},
		DeviceProperties: core1_0.PhysicalDeviceProperties{
			DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
			Limits: &core1_0.PhysicalDeviceLimits{
				BufferImageGranularity:   1,
				NonCoherentAtomSize:      1,
				MaxMemoryAllocationCount: 1,
			},
		},
		AllocatorOptions: CreateOptions{},
		PreNewMock: func(instance *mocks.Instance1_2, instancePhysDevice *mocks.InstanceScopedPhysicalDevice1_2, physDevice *mocks.PhysicalDevice1_2, device *mocks.Device1_2) {
			instancePhysDevice.EXPECT().MemoryProperties2(gomock.Any()).DoAndReturn(func(out *core1_1.PhysicalDeviceMemoryProperties2) error {
				budget := out.Next.(*ext_memory_budget.PhysicalDeviceMemoryBudgetProperties)
				budget.HeapBudget[0] = 1500
				budget.HeapUsage[0] = 100

				budget.HeapBudget[1] = 1500
				budget.HeapUsage[1] = 100

				return nil
			})
		},
	})

	// The block will be tiny because we're trying to fit into the remaining memory budget so we'll do a dedicated
	// allocation
	memory := mocks.EasyMockDeviceMemory(ctrl)
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
		Flags:    AllocationCreateWithinBudget,
		Priority: 1,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	err = allocation.Free()
	require.NoError(t, err)
}

func TestAllocateMemoryWithBudgetRequestDedicated(t *testing.T) {
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
		DeviceExtensions: []string{ext_memory_priority.ExtensionName, ext_memory_budget.ExtensionName},
		DeviceProperties: core1_0.PhysicalDeviceProperties{
			DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
			Limits: &core1_0.PhysicalDeviceLimits{
				BufferImageGranularity:   1,
				NonCoherentAtomSize:      1,
				MaxMemoryAllocationCount: 1,
			},
		},
		AllocatorOptions: CreateOptions{},
		PreNewMock: func(instance *mocks.Instance1_2, instancePhysDevice *mocks.InstanceScopedPhysicalDevice1_2, physDevice *mocks.PhysicalDevice1_2, device *mocks.Device1_2) {
			instancePhysDevice.EXPECT().MemoryProperties2(gomock.Any()).DoAndReturn(func(out *core1_1.PhysicalDeviceMemoryProperties2) error {
				budget := out.Next.(*ext_memory_budget.PhysicalDeviceMemoryBudgetProperties)
				budget.HeapBudget[0] = 1500
				budget.HeapUsage[0] = 100

				budget.HeapBudget[1] = 1500
				budget.HeapUsage[1] = 100

				return nil
			})
		},
	})

	// The block will be tiny because we're trying to fit into the remaining memory budget so we'll do a dedicated
	// allocation
	memory := mocks.EasyMockDeviceMemory(ctrl)
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
		Flags:    AllocationCreateDedicatedMemory | AllocationCreateWithinBudget,
		Priority: 1,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	err = allocation.Free()
	require.NoError(t, err)
}

func TestAllocateMemoryWithBudgetCantFit(t *testing.T) {
	ctrl := gomock.NewController(t)

	_, _, _, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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
		DeviceExtensions: []string{ext_memory_priority.ExtensionName, ext_memory_budget.ExtensionName},
		DeviceProperties: core1_0.PhysicalDeviceProperties{
			DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
			Limits: &core1_0.PhysicalDeviceLimits{
				BufferImageGranularity:   1,
				NonCoherentAtomSize:      1,
				MaxMemoryAllocationCount: 1,
			},
		},
		AllocatorOptions: CreateOptions{},
		PreNewMock: func(instance *mocks.Instance1_2, instancePhysDevice *mocks.InstanceScopedPhysicalDevice1_2, physDevice *mocks.PhysicalDevice1_2, device *mocks.Device1_2) {
			instancePhysDevice.EXPECT().MemoryProperties2(gomock.Any()).DoAndReturn(func(out *core1_1.PhysicalDeviceMemoryProperties2) error {
				budget := out.Next.(*ext_memory_budget.PhysicalDeviceMemoryBudgetProperties)
				budget.HeapBudget[0] = 1500
				budget.HeapUsage[0] = 700

				budget.HeapBudget[1] = 1500
				budget.HeapUsage[1] = 700

				return nil
			})
		},
	})

	var allocation Allocation
	res, err := allocator.AllocateMemory(&core1_0.MemoryRequirements{
		Size:           1000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}, AllocationCreateInfo{
		Usage:    MemoryUsageAuto,
		Flags:    AllocationCreateWithinBudget,
		Priority: 1,
	}, &allocation)
	require.Error(t, err)
	require.Equal(t, core1_0.VKErrorOutOfDeviceMemory, res)
}

func TestAllocateMemoryWithBudget(t *testing.T) {
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
		DeviceExtensions: []string{ext_memory_priority.ExtensionName, ext_memory_budget.ExtensionName},
		DeviceProperties: core1_0.PhysicalDeviceProperties{
			DriverType: core1_0.PhysicalDeviceTypeDiscreteGPU,
			Limits: &core1_0.PhysicalDeviceLimits{
				BufferImageGranularity:   1,
				NonCoherentAtomSize:      1,
				MaxMemoryAllocationCount: 1,
			},
		},
		AllocatorOptions: CreateOptions{},
		PreNewMock: func(instance *mocks.Instance1_2, instancePhysDevice *mocks.InstanceScopedPhysicalDevice1_2, physDevice *mocks.PhysicalDevice1_2, device *mocks.Device1_2) {
			instancePhysDevice.EXPECT().MemoryProperties2(gomock.Any()).DoAndReturn(func(out *core1_1.PhysicalDeviceMemoryProperties2) error {
				budget := out.Next.(*ext_memory_budget.PhysicalDeviceMemoryBudgetProperties)
				budget.HeapBudget[0] = 16000
				budget.HeapUsage[0] = 300

				budget.HeapBudget[1] = 16000
				budget.HeapUsage[1] = 300

				return nil
			})
		},
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
		Flags:    AllocationCreateWithinBudget,
		Priority: 1,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	err = allocation.Free()
	require.NoError(t, err)
}
