package defrag_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/defrag"
	mock_defrag "github.com/vkngwrapper/arsenal/memutils/defrag/mocks"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
	mock_metadata "github.com/vkngwrapper/arsenal/memutils/metadata/mocks"
	"go.uber.org/mock/gomock"
)

func TestSimpleCompletePass(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockList := mock_defrag.NewMockBlockList[metadata.BlockAllocationHandle](ctrl)
	mockList.EXPECT().BlockCount().AnyTimes().Return(2)
	mockList.EXPECT().AddStatistics(gomock.Any()).DoAndReturn(func(stats *memutils.Statistics) {
		stats.AllocationBytes = 200
		stats.AllocationCount = 2
	})
	mockList.EXPECT().AddStatistics(gomock.Any()).DoAndReturn(func(stats *memutils.Statistics) {
		stats.AllocationBytes = 100
		stats.AllocationCount = 1
	})

	block1 := mock_metadata.NewMockBlockMetadata(ctrl)
	block2 := mock_metadata.NewMockBlockMetadata(ctrl)

	alloc1 := metadata.BlockAllocationHandle(1)
	alloc2 := metadata.BlockAllocationHandle(2)

	context := defrag.DefragContextWithMoves[metadata.BlockAllocationHandle](
		[]defrag.DefragmentationMove[metadata.BlockAllocationHandle]{
			{
				Size:             100,
				SrcBlockMetadata: block1,
				SrcAllocation:    &alloc1,
				DstBlockMetadata: block2,
				DstTmpAllocation: &alloc2,
			},
		},
	)
	context.BlockList = mockList
	context.Handler = func(move defrag.DefragmentationMove[metadata.BlockAllocationHandle]) error {
		return nil
	}

	var pass defrag.PassContext
	pass.Stats = defrag.DefragmentationStats{
		AllocationsMoved: 1,
		BytesMoved:       100,
	}

	err := context.BlockListCompletePass(&pass)
	require.NoError(t, err)
	require.Equal(t, defrag.DefragmentationStats{
		AllocationsMoved: 1,
		BytesMoved:       100,
		BytesFreed:       100,
		AllocationsFreed: 1,
	}, pass.Stats)
}

func TestDestroyAllocCompletePass(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockList := mock_defrag.NewMockBlockList[metadata.BlockAllocationHandle](ctrl)
	mockList.EXPECT().BlockCount().AnyTimes().Return(3)
	mockList.EXPECT().AddStatistics(gomock.Any()).DoAndReturn(func(stats *memutils.Statistics) {
		stats.AllocationBytes = 200
		stats.AllocationCount = 2
	})
	mockList.EXPECT().AddStatistics(gomock.Any()).DoAndReturn(func(stats *memutils.Statistics) {
		stats.AllocationBytes = 0
		stats.AllocationCount = 0
	})

	block1 := mock_metadata.NewMockBlockMetadata(ctrl)
	block3 := mock_metadata.NewMockBlockMetadata(ctrl)

	alloc1 := metadata.BlockAllocationHandle(1)
	alloc2 := metadata.BlockAllocationHandle(2)

	context := defrag.DefragContextWithMoves[metadata.BlockAllocationHandle](
		[]defrag.DefragmentationMove[metadata.BlockAllocationHandle]{
			{
				Size:             100,
				SrcBlockMetadata: block3,
				SrcAllocation:    &alloc2,
				DstBlockMetadata: block1,
				DstTmpAllocation: &alloc1,
			},
		},
	)
	context.Moves()[0].MoveOperation = defrag.DefragmentationMoveDestroy
	context.BlockList = mockList
	context.Handler = func(move defrag.DefragmentationMove[metadata.BlockAllocationHandle]) error {
		return nil
	}

	var pass defrag.PassContext
	pass.Stats = defrag.DefragmentationStats{
		AllocationsMoved: 1,
		BytesMoved:       100,
	}

	err := context.BlockListCompletePass(&pass)
	require.NoError(t, err)
	require.Equal(t, defrag.DefragmentationStats{
		AllocationsMoved: 0,
		BytesMoved:       0,
		BytesFreed:       200,
		AllocationsFreed: 2,
	}, pass.Stats)
}

func TestIgnoreAllocCompletePass(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockList := mock_defrag.NewMockBlockList[metadata.BlockAllocationHandle](ctrl)
	mockList.EXPECT().BlockCount().AnyTimes().Return(3)
	mockList.EXPECT().AddStatistics(gomock.Any()).DoAndReturn(func(stats *memutils.Statistics) {
		stats.AllocationBytes = 200
		stats.AllocationCount = 2
	})
	mockList.EXPECT().AddStatistics(gomock.Any()).DoAndReturn(func(stats *memutils.Statistics) {
		stats.AllocationBytes = 100
		stats.AllocationCount = 1
	})

	block1 := mock_metadata.NewMockBlockMetadata(ctrl)
	block2 := mock_metadata.NewMockBlockMetadata(ctrl)
	block3 := mock_metadata.NewMockBlockMetadata(ctrl)

	alloc1 := metadata.BlockAllocationHandle(1)
	alloc2 := metadata.BlockAllocationHandle(2)

	mockList.EXPECT().Lock()
	mockList.EXPECT().Unlock()
	mockList.EXPECT().MetadataForBlock(0).Return(block1)
	mockList.EXPECT().MetadataForBlock(1).Return(block2)
	mockList.EXPECT().MetadataForBlock(2).Return(block3)
	mockList.EXPECT().SwapBlocks(2, 0)

	context := defrag.DefragContextWithMoves[metadata.BlockAllocationHandle](
		[]defrag.DefragmentationMove[metadata.BlockAllocationHandle]{
			{
				Size:             100,
				SrcBlockMetadata: block3,
				SrcAllocation:    &alloc2,
				DstBlockMetadata: block1,
				DstTmpAllocation: &alloc1,
			},
		},
	)
	context.Moves()[0].MoveOperation = defrag.DefragmentationMoveIgnore
	context.BlockList = mockList
	context.Handler = func(move defrag.DefragmentationMove[metadata.BlockAllocationHandle]) error {
		return nil
	}

	var pass defrag.PassContext
	pass.Stats = defrag.DefragmentationStats{
		AllocationsMoved: 1,
		BytesMoved:       100,
	}

	err := context.BlockListCompletePass(&pass)
	require.NoError(t, err)
	require.Equal(t, defrag.DefragmentationStats{
		AllocationsMoved: 0,
		BytesMoved:       0,
		BytesFreed:       100,
		AllocationsFreed: 1,
	}, pass.Stats)
}
