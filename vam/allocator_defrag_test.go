package vam

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/defrag"
	"github.com/vkngwrapper/core/v3/common"
	"github.com/vkngwrapper/core/v3/core1_0"
	"github.com/vkngwrapper/core/v3/core1_1"
	"github.com/vkngwrapper/core/v3/core1_2"
	"github.com/vkngwrapper/core/v3/mocks"
	"github.com/vkngwrapper/extensions/v3/ext_memory_priority"
	"go.uber.org/mock/gomock"
)

func TestAllocateDefrag(t *testing.T) {
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

	for i := 0; i < 10; i += 2 {
		err = allocations[i].Free()
		require.NoError(t, err)
	}

	var defragContext DefragmentationContext
	allocator.BeginDefragmentation(DefragmentationInfo{
		Flags:                 DefragmentationFlagAlgorithmFull,
		MaxBytesPerPass:       20000,
		MaxAllocationsPerPass: 5,
	}, &defragContext)

	// Defragmentation starts at the end of the block and works backwards so:
	// Before pass: EFEFEFEFEF
	// During pass: FFFFFFEFEF
	// After pass: FFFFFEEEEE
	//
	// That's three moves!
	moves := defragContext.BeginDefragPass()
	require.Len(t, moves, 3)

	done, err := defragContext.EndDefragPass()
	require.NoError(t, err)
	require.False(t, done)

	moves = defragContext.BeginDefragPass()
	require.Len(t, moves, 0)
	done, err = defragContext.EndDefragPass()
	require.NoError(t, err)
	require.True(t, done)

	var stats defrag.DefragmentationStats
	defragContext.Finish(&stats)

	require.Equal(t, defrag.DefragmentationStats{
		BytesMoved:       3000,
		BytesFreed:       3000,
		AllocationsMoved: 3,
		AllocationsFreed: 3,
	}, stats)

	offset := allocations[1].FindOffset()
	require.Equal(t, offset, 1000+memutils.DebugMargin)

	offset = allocations[3].FindOffset()
	require.Equal(t, offset, 3000+memutils.DebugMargin*3)

	// This was moved last and so ended up in the 4 position
	offset = allocations[5].FindOffset()
	require.Equal(t, offset, 4000+memutils.DebugMargin*4)

	// Moved second and ended up in the 2 position
	offset = allocations[7].FindOffset()
	require.Equal(t, offset, 2000+memutils.DebugMargin*2)

	// Moved first and ended up in the 0 position
	offset = allocations[9].FindOffset()
	require.Equal(t, offset, 0)
}
