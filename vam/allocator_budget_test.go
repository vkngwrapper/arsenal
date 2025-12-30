package vam

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/core/v3/common"
	"github.com/vkngwrapper/core/v3/core1_0"
	"github.com/vkngwrapper/core/v3/core1_1"
	"github.com/vkngwrapper/core/v3/core1_2"
	"github.com/vkngwrapper/core/v3/mocks"
	"github.com/vkngwrapper/core/v3/mocks/mocks1_2"
	"github.com/vkngwrapper/extensions/v3/ext_memory_budget"
	"github.com/vkngwrapper/extensions/v3/ext_memory_priority"
	"go.uber.org/mock/gomock"
)

func TestAllocateMemoryWithBudgetForceDedicated(t *testing.T) {
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
		PreNewMock: func(driver *mocks1_2.MockCoreDeviceDriver, physDevice core1_0.PhysicalDevice) {
			driver.InstanceDriver().(*mocks1_2.MockCoreInstanceDriver).EXPECT().GetPhysicalDeviceMemoryProperties2(physDevice, gomock.Any()).DoAndReturn(func(physDevice core1_0.PhysicalDevice, out *core1_1.PhysicalDeviceMemoryProperties2) error {
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
	memory := mocks.NewDummyDeviceMemory(driver.Device(), 1000)
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
	driver.EXPECT().FreeMemory(memory, nil)

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
		PreNewMock: func(driver *mocks1_2.MockCoreDeviceDriver, physDevice core1_0.PhysicalDevice) {
			driver.InstanceDriver().(*mocks1_2.MockCoreInstanceDriver).EXPECT().GetPhysicalDeviceMemoryProperties2(physDevice, gomock.Any()).DoAndReturn(func(physDevice core1_0.PhysicalDevice, out *core1_1.PhysicalDeviceMemoryProperties2) error {
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
	memory := mocks.NewDummyDeviceMemory(driver.Device(), 1000)
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
	driver.EXPECT().FreeMemory(memory, nil)

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

	_, _, allocator := readyAllocator(t, ctrl, AllocatorSetup{
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
		PreNewMock: func(driver *mocks1_2.MockCoreDeviceDriver, physDevice core1_0.PhysicalDevice) {
			driver.InstanceDriver().(*mocks1_2.MockCoreInstanceDriver).EXPECT().GetPhysicalDeviceMemoryProperties2(physDevice, gomock.Any()).DoAndReturn(func(physDevice core1_0.PhysicalDevice, out *core1_1.PhysicalDeviceMemoryProperties2) error {
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
		PreNewMock: func(driver *mocks1_2.MockCoreDeviceDriver, physDevice core1_0.PhysicalDevice) {
			driver.InstanceDriver().(*mocks1_2.MockCoreInstanceDriver).EXPECT().GetPhysicalDeviceMemoryProperties2(physDevice, gomock.Any()).DoAndReturn(func(physDevice core1_0.PhysicalDevice, out *core1_1.PhysicalDeviceMemoryProperties2) error {
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
		Flags:    AllocationCreateWithinBudget,
		Priority: 1,
	}, &allocation)
	require.NoError(t, err)

	require.GreaterOrEqual(t, 1000, allocation.Size())

	err = allocation.Free()
	require.NoError(t, err)
}
