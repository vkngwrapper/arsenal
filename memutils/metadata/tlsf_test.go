package metadata_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
	"go.uber.org/mock/gomock"
)

func TestTLSFBasicAlloc(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(1000)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

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

	success, req, err := tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

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

	err = tlsf.Free(alloc1)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

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
}

func TestTLSFSameSize(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(10000)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 10000,
		UnusedRangeSizeMax: 10000,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc4 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc4)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 4,
			AllocationBytes: 400,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 9600,
		UnusedRangeSizeMax: 9600,
	}, stats)

	err = tlsf.Free(alloc1)
	require.NoError(t, err)

	err = tlsf.Free(alloc3)
	require.NoError(t, err)

	err = tlsf.Validate()
	require.NoError(t, err)

	err = tlsf.Free(alloc2)
	require.NoError(t, err)

	err = tlsf.Free(alloc4)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 10000,
		UnusedRangeSizeMax: 10000,
	}, stats)
}

func TestTLSFTripleSized(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(10000)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 10000,
		UnusedRangeSizeMax: 10000,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(10, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(1000, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 3,
			AllocationBytes: 1110,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  10,
		AllocationSizeMax:  1000,
		UnusedRangeSizeMin: 8890,
		UnusedRangeSizeMax: 8890,
	}, stats)

	err = tlsf.Validate()
	require.NoError(t, err)

	err = tlsf.Free(alloc2)
	require.NoError(t, err)

	err = tlsf.Free(alloc1)
	require.NoError(t, err)

	err = tlsf.Free(alloc3)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 10000,
		UnusedRangeSizeMax: 10000,
	}, stats)
}

func TestTLSFFreeSpaceHuntDiffTiers(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(10000)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 10000,
		UnusedRangeSizeMax: 10000,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(1000, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(8800, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc4 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc4)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 4,
			AllocationBytes: 10000,
		},
		UnusedRangeCount:   0,
		AllocationSizeMin:  100,
		AllocationSizeMax:  8800,
		UnusedRangeSizeMin: math.MaxInt,
		UnusedRangeSizeMax: 0,
	}, stats)

	err = tlsf.Free(alloc1)
	require.NoError(t, err)

	err = tlsf.Free(alloc3)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(110, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc5 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc5)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 3,
			AllocationBytes: 9010,
		},
		UnusedRangeCount:   2,
		AllocationSizeMin:  100,
		AllocationSizeMax:  8800,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 890,
	}, stats)
}

func TestTLSFFreeSpaceHuntMinOffsetNullBlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(1500)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1500,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 1500,
		UnusedRangeSizeMax: 1500,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(1000, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1500,
			AllocationCount: 2,
			AllocationBytes: 1100,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  100,
		AllocationSizeMax:  1000,
		UnusedRangeSizeMin: 400,
		UnusedRangeSizeMax: 400,
	}, stats)

	err = tlsf.Free(alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(150, 1, false, 1, metadata.AllocationStrategyMinOffset, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1500,
			AllocationCount: 2,
			AllocationBytes: 1150,
		},
		UnusedRangeCount:   2,
		AllocationSizeMin:  150,
		AllocationSizeMax:  1000,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 250,
	}, stats)
}

func TestTLSFFreeSpaceHuntMinOffsetFreeBlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(1500)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1500,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 1500,
		UnusedRangeSizeMax: 1500,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(1000, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(200, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1500,
			AllocationCount: 3,
			AllocationBytes: 1300,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  100,
		AllocationSizeMax:  1000,
		UnusedRangeSizeMin: 200,
		UnusedRangeSizeMax: 200,
	}, stats)

	err = tlsf.Free(alloc1)
	require.NoError(t, err)

	err = tlsf.Free(alloc3)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinOffset, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc4 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc4)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      1500,
			AllocationCount: 2,
			AllocationBytes: 200,
		},
		UnusedRangeCount:   2,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 400,
		UnusedRangeSizeMax: 900,
	}, stats)
}

func TestTLSFFreeSpaceHuntMinTimeSameSizeBlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(200)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      200,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 200,
		UnusedRangeSizeMax: 200,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      200,
			AllocationCount: 2,
			AllocationBytes: 200,
		},
		UnusedRangeCount:   0,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: math.MaxInt,
		UnusedRangeSizeMax: 0,
	}, stats)

	err = tlsf.Free(alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinTime, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      200,
			AllocationCount: 2,
			AllocationBytes: 200,
		},
		UnusedRangeCount:   0,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: math.MaxInt,
		UnusedRangeSizeMax: 0,
	}, stats)
}

func TestTLSFFreeSpaceHuntMinTimeNullBlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(10000)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 10000,
		UnusedRangeSizeMax: 10000,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 2,
			AllocationBytes: 200,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 9800,
		UnusedRangeSizeMax: 9800,
	}, stats)

	err = tlsf.Free(alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinTime, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 2,
			AllocationBytes: 200,
		},
		UnusedRangeCount:   2,
		AllocationSizeMin:  100,
		AllocationSizeMax:  100,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 9700,
	}, stats)
}

func TestTLSFFreeMinTimeLargerBucket(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(10000)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 10000,
		UnusedRangeSizeMax: 10000,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(1000, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(8800, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc4 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc4)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 4,
			AllocationBytes: 10000,
		},
		UnusedRangeCount:   0,
		AllocationSizeMin:  100,
		AllocationSizeMax:  8800,
		UnusedRangeSizeMin: math.MaxInt,
		UnusedRangeSizeMax: 0,
	}, stats)

	err = tlsf.Free(alloc1)
	require.NoError(t, err)

	err = tlsf.Free(alloc3)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(100, 1, false, 1, metadata.AllocationStrategyMinTime, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc5 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc5)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)
	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      10000,
			AllocationCount: 3,
			AllocationBytes: 9000,
		},
		UnusedRangeCount:   2,
		AllocationSizeMin:  100,
		AllocationSizeMax:  8800,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 900,
	}, stats)
}

func TestTLSFFreeSpaceHuntSameTier(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(130)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      130,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 130,
		UnusedRangeSizeMax: 130,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(20, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(50, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(30, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      130,
			AllocationCount: 3,
			AllocationBytes: 100,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  20,
		AllocationSizeMax:  50,
		UnusedRangeSizeMin: 30,
		UnusedRangeSizeMax: 30,
	}, stats)

	err = tlsf.Free(alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(30, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc4 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc4)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      130,
			AllocationCount: 3,
			AllocationBytes: 110,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  30,
		AllocationSizeMax:  50,
		UnusedRangeSizeMin: 20,
		UnusedRangeSizeMax: 20,
	}, stats)

	err = tlsf.Validate()
	require.NoError(t, err)

	err = tlsf.Free(alloc2)
	require.NoError(t, err)

	err = tlsf.Free(alloc3)
	require.NoError(t, err)

	err = tlsf.Free(alloc4)
	require.NoError(t, err)
}

func TestTLSFClear(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(100)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      100,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 100,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(20, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      100,
			AllocationCount: 1,
			AllocationBytes: 20,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  20,
		AllocationSizeMax:  20,
		UnusedRangeSizeMin: 80,
		UnusedRangeSizeMax: 80,
	}, stats)

	tlsf.Clear()
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      100,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 100,
	}, stats)
}

func TestTLSFMayHaveFreeBlockFalse(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(100)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      100,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 100,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(40, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	require.False(t, tlsf.MayHaveFreeBlock(1, 64))
}

func TestTLSFMayHaveFreeBlockTrue(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(100)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      100,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 100,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(40, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(40, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(20, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	err = tlsf.Free(alloc2)
	require.NoError(t, err)

	require.True(t, tlsf.MayHaveFreeBlock(1, 32))
}

func TestTLSFAllocProperties(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(100)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      100,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 100,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(40, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(40, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(20, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	offset1, err := tlsf.AllocationOffset(alloc1)
	require.NoError(t, err)
	require.Equal(t, 0, offset1)

	offset2, err := tlsf.AllocationOffset(alloc2)
	require.NoError(t, err)
	require.Equal(t, 40, offset2)

	offset3, err := tlsf.AllocationOffset(alloc3)
	require.NoError(t, err)
	require.Equal(t, 80, offset3)

	err = tlsf.SetAllocationUserData(alloc1, 99)
	require.NoError(t, err)
	userData, err := tlsf.AllocationUserData(alloc1)
	require.NoError(t, err)
	require.Equal(t, 99, userData)
}

func TestTLSFMinOffsetAllocFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(100)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      100,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 100,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(40, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(40, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(20, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      100,
			AllocationCount: 3,
			AllocationBytes: 100,
		},
		UnusedRangeCount:   0,
		AllocationSizeMin:  20,
		AllocationSizeMax:  40,
		UnusedRangeSizeMin: math.MaxInt,
		UnusedRangeSizeMax: 0,
	}, stats)

	err = tlsf.Free(alloc2)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(10, 1, false, 1, metadata.AllocationStrategyMinOffset, 30)
	require.NoError(t, err)
	require.False(t, success)
}

func TestTLSFMinOffsetAllocFailNullBlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(100)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      100,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 100,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(40, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(40, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(20, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      100,
			AllocationCount: 3,
			AllocationBytes: 100,
		},
		UnusedRangeCount:   0,
		AllocationSizeMin:  20,
		AllocationSizeMax:  40,
		UnusedRangeSizeMin: math.MaxInt,
		UnusedRangeSizeMax: 0,
	}, stats)

	err = tlsf.Free(alloc3)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(10, 1, false, 1, metadata.AllocationStrategyMinOffset, 60)
	require.NoError(t, err)
	require.False(t, success)
}

func TestTLSFIterateAllocs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	granularity := metadata.FakeGranularityCheck{}

	tlsf := metadata.NewTLSFBlockMetadata(1, granularity)
	tlsf.Init(100)

	var stats memutils.DetailedStatistics
	stats.Clear()
	tlsf.AddDetailedStatistics(&stats)

	require.Equal(t, memutils.DetailedStatistics{
		Statistics: memutils.Statistics{
			BlockCount:      1,
			BlockBytes:      100,
			AllocationCount: 0,
			AllocationBytes: 0,
		},
		UnusedRangeCount:   1,
		AllocationSizeMin:  math.MaxInt,
		AllocationSizeMax:  0,
		UnusedRangeSizeMin: 100,
		UnusedRangeSizeMax: 100,
	}, stats)

	success, req, err := tlsf.CreateAllocationRequest(40, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc1 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc1)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(40, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc2 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc2)
	require.NoError(t, err)

	success, req, err = tlsf.CreateAllocationRequest(20, 1, false, 1, metadata.AllocationStrategyMinMemory, math.MaxInt)
	require.NoError(t, err)
	require.True(t, success)

	alloc3 := req.BlockAllocationHandle
	err = tlsf.Alloc(req, 1, &alloc3)
	require.NoError(t, err)

	iter, err := tlsf.AllocationListBegin()
	require.NoError(t, err)
	require.Equal(t, alloc3, iter)

	iter, err = tlsf.FindNextAllocation(iter)
	require.NoError(t, err)
	require.Equal(t, alloc2, iter)

	iter, err = tlsf.FindNextAllocation(iter)
	require.NoError(t, err)
	require.Equal(t, alloc1, iter)

	iter, err = tlsf.FindNextAllocation(iter)
	require.NoError(t, err)
	require.Equal(t, metadata.NoAllocation, iter)
}
