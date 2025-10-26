package metadata_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
	mock_metadata "github.com/vkngwrapper/arsenal/memutils/metadata/mocks"
	"go.uber.org/mock/gomock"
)

func TestLinearAlloc(t *testing.T) {
	linear := metadata.NewLinearBlockMetadata(1, metadata.FakeGranularityCheck{})
	linear.Init(1000)

	var stats memutils.DetailedStatistics
	stats.Clear()
	linear.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 1000,
		UnusedRangeSizeMax: 1000,
	}, stats)

	success, request, err := linear.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinTime, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc1)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 1,
			AllocationBytes: 100,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 900,
		UnusedRangeSizeMax: 900,
	}, stats)

	success, request, err = linear.CreateAllocationRequest(50, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc2)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 2,
			AllocationBytes: 150,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  50,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 850,
		UnusedRangeSizeMax: 850,
	}, stats)

	success, request, err = linear.CreateAllocationRequest(25, 1, true, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc3)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 3,
			AllocationBytes: 175,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  25,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 825,
		UnusedRangeSizeMax: 825,
	}, stats)

	err = linear.Validate()
	require.NoError(t, err)

	err = linear.Free(alloc1)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 2,
			AllocationBytes: 75,
		},
		UnusedRangeCount:   2,
		AllocationSizeMin:  25,
		AllocationSizeMax:  50,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 825,
	}, stats)

	err = linear.Free(alloc2)
	require.NoError(t, err)
	err = linear.Free(alloc3)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 1000,
		UnusedRangeSizeMax: 1000,
	}, stats)

	err = linear.Validate()
	require.NoError(t, err)
}

func TestRingBuffer(t *testing.T) {
	linear := metadata.NewLinearBlockMetadata(1, metadata.FakeGranularityCheck{})
	linear.Init(1000)

	var stats memutils.DetailedStatistics
	stats.Clear()
	linear.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 1000,
		UnusedRangeSizeMax: 1000,
	}, stats)

	success, request, err := linear.CreateAllocationRequest(500, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc1)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 1,
			AllocationBytes: 500,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  500,
		AllocationSizeMax:  500,
		UnusedRangeSizeMin: 500,
		UnusedRangeSizeMax: 500,
	}, stats)

	success, request, err = linear.CreateAllocationRequest(500, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc2)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 2,
			AllocationBytes: 1000,
		},
		UnusedRangeCount:   0,
		AllocationSizeMin:  500,
		AllocationSizeMax:  500,
		UnusedRangeSizeMin: math.MaxInt,
		UnusedRangeSizeMax: 0,
	}, stats)

	err = linear.Validate()
	require.NoError(t, err)

	err = linear.Free(alloc1)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(500, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc3)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 2,
			AllocationBytes: 1000,
		},
		UnusedRangeCount:   0,
		AllocationSizeMin:  500,
		AllocationSizeMax:  500,
		UnusedRangeSizeMin: math.MaxInt,
		UnusedRangeSizeMax: 0,
	}, stats)

	err = linear.Validate()
	require.NoError(t, err)

	linear.Clear()

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 1000,
		UnusedRangeSizeMax: 1000,
	}, stats)

	err = linear.Validate()
	require.NoError(t, err)
}

func TestLinearFree(t *testing.T) {
	linear := metadata.NewLinearBlockMetadata(1, metadata.FakeGranularityCheck{})
	linear.Init(1000)

	var stats memutils.DetailedStatistics
	stats.Clear()
	linear.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 1000,
		UnusedRangeSizeMax: 1000,
	}, stats)

	success, request, err := linear.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinTime, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc1)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinTime, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc2)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinTime, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc3)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinTime, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc4 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc4)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(100, 1, true, 1, metadata.AllocationStrategyMinTime, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc5 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc5)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(100, 1, true, 1, metadata.AllocationStrategyMinTime, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc6 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc6)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(100, 1, true, 1, metadata.AllocationStrategyMinTime, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc7 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc7)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 7,
			AllocationBytes: 700,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 300,
		UnusedRangeSizeMax: 300,
	}, stats)

	err = linear.Validate()
	require.NoError(t, err)

	err = linear.Free(alloc4)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 6,
			AllocationBytes: 600,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 400,
		UnusedRangeSizeMax: 400,
	}, stats)

	err = linear.Free(alloc2)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 5,
			AllocationBytes: 500,
		},
		UnusedRangeCount:   2,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 400,
	}, stats)

	err = linear.Free(alloc6)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 4,
			AllocationBytes: 400,
		},
		UnusedRangeCount:   3,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 400,
	}, stats)

	err = linear.Free(alloc1)
	require.NoError(t, err)
	err = linear.Free(alloc3)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 2,
			AllocationBytes: 200,
		},
		UnusedRangeCount:   2,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 700,
	}, stats)

	err = linear.Free(alloc7)
	require.NoError(t, err)
	err = linear.Free(alloc5)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 1000,
		UnusedRangeSizeMax: 1000,
	}, stats)

	err = linear.Validate()
	require.NoError(t, err)
}

func TestRandomAccessFails(t *testing.T) {
	linear := metadata.NewLinearBlockMetadata(1, metadata.FakeGranularityCheck{})
	linear.Init(1000)
	require.False(t, linear.SupportsRandomAccess())
	_, err := linear.AllocationListBegin()
	require.Error(t, err)
}

func TestUserDataGetSet(t *testing.T) {
	linear := metadata.NewLinearBlockMetadata(1, metadata.FakeGranularityCheck{})
	linear.Init(1000)

	success, request, err := linear.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinTime, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc1)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(100, 1, true, 1, metadata.AllocationStrategyMinTime, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc2)
	require.NoError(t, err)

	userData, err := linear.AllocationUserData(alloc1)
	require.NoError(t, err)
	allocUserData, ok := userData.(*metadata.BlockAllocationHandle)
	require.True(t, ok)
	require.NotNil(t, allocUserData)
	require.Equal(t, alloc1, *allocUserData)

	userData, err = linear.AllocationUserData(alloc2)
	require.NoError(t, err)
	allocUserData, ok = userData.(*metadata.BlockAllocationHandle)
	require.True(t, ok)
	require.NotNil(t, allocUserData)
	require.Equal(t, alloc2, *allocUserData)

	var value int = 99
	err = linear.SetAllocationUserData(alloc1, &value)
	require.NoError(t, err)

	userData, err = linear.AllocationUserData(alloc1)
	require.NoError(t, err)
	allocValue, ok := userData.(*int)
	require.True(t, ok)
	require.NotNil(t, allocValue)
	require.Equal(t, 99, *allocValue)
}

func TestGranularityLogic(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := mock_metadata.NewMockGranularityCheck(ctrl)
	granularity.EXPECT().AllocationsConflict(uint32(1), uint32(2)).AnyTimes().Return(true)
	granularity.EXPECT().AllocationsConflict(uint32(1), uint32(1)).AnyTimes().Return(false)

	linear := metadata.NewLinearBlockMetadata(8, granularity)
	linear.Init(1000)

	// Two conflicting alloc types next to each other
	success, request, err := linear.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc1)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(100, 1, false, 2, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := request.BlockAllocationHandle
	err = linear.Alloc(request, 2, &alloc2)
	require.NoError(t, err)

	var stats memutils.DetailedStatistics
	stats.Clear()
	linear.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 2,
			AllocationBytes: 200,
		},
		UnusedRangeCount:   2,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 4,
		UnusedRangeSizeMax: 796,
	}, stats)
	linear.Clear()

	// Two non-conflicting alloc types next to each other
	success, request, err = linear.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 = request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc1)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 = request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc2)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 2,
			AllocationBytes: 200,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 800,
		UnusedRangeSizeMax: 800,
	}, stats)

	linear.Clear()

	// Two conflicting alloc types next to each other in the upper region
	success, request, err = linear.CreateAllocationRequest(100, 1, true, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 = request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc1)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(100, 1, true, 2, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 = request.BlockAllocationHandle
	err = linear.Alloc(request, 2, &alloc2)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 2,
			AllocationBytes: 200,
		},
		UnusedRangeCount:   2,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 8,
		UnusedRangeSizeMax: 792,
	}, stats)
	linear.Clear()

	// Two conflicting alloc types in a ring buffer
	success, request, err = linear.CreateAllocationRequest(500, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 = request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc1)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(500, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 = request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc2)
	require.NoError(t, err)

	err = linear.Free(alloc1)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := request.BlockAllocationHandle
	err = linear.Alloc(request, 1, &alloc3)
	require.NoError(t, err)

	success, request, err = linear.CreateAllocationRequest(100, 1, false, 2, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc4 := request.BlockAllocationHandle
	err = linear.Alloc(request, 2, &alloc4)
	require.NoError(t, err)

	stats.Clear()
	linear.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1000,
			AllocationCount: 3,
			AllocationBytes: 700,
		},
		UnusedRangeCount:   2,
		AllocationSizeMin:  100,
		AllocationSizeMax:  500,
		UnusedRangeSizeMin: 4,
		UnusedRangeSizeMax: 296,
	}, stats)

	err = linear.Free(alloc2)
	require.NoError(t, err)
}
