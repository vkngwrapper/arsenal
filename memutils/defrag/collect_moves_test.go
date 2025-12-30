package defrag_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/arsenal/memutils/defrag"
	mock_defrag "github.com/vkngwrapper/arsenal/memutils/defrag/mocks"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
	mock_metadata "github.com/vkngwrapper/arsenal/memutils/metadata/mocks"
	"go.uber.org/mock/gomock"
)

type Alloc struct {
	Id   metadata.BlockAllocationHandle
	Size int
}

type TargetedMove struct {
	// Expected values of the incoming move
	SourceAlloc metadata.BlockAllocationHandle
	SourceBlock int
	MaxOffset   int
	Size        int

	// The offset that the move will be assigned
	ActualOffset int

	// This will be filled in when setting up the test
	ActualDestHandle metadata.BlockAllocationHandle
}

type Block struct {
	Size int
	// Starting state of the block- free regions should be assigned the ID metadata.NoAllocation
	Allocs []Alloc
	// Incoming moves in the order that they're expected to be allocated
	TargetedMoves []TargetedMove
}

var testCases = map[string]struct {
	MaxPassBytes       int
	MaxPassAllocations int
	Algorithm          defrag.Algorithm
	MemoryState        []Block
}{
	"FullAlgoSimpleMoveWithinBlock": {
		MaxPassBytes:       100,
		MaxPassAllocations: 1,
		Algorithm:          defrag.AlgorithmFull,
		MemoryState: []Block{
			{
				Size: 250,
				Allocs: []Alloc{
					{metadata.NoAllocation, 100},
					{1, 100},
				},
				TargetedMoves: []TargetedMove{
					{
						SourceAlloc:  1,
						SourceBlock:  0,
						MaxOffset:    100,
						ActualOffset: 0,
						Size:         100,
					},
				},
			},
		},
	},
	"FullAlgoNoWorkToDo": {
		MaxPassBytes:       100,
		MaxPassAllocations: 1,
		Algorithm:          defrag.AlgorithmFull,
		MemoryState: []Block{
			{
				Size: 250,
				Allocs: []Alloc{
					{1, 100},
				},
			},
		},
	},
	"FastAlgoSimpleMoveBetweenBlocks": {
		MaxPassBytes:       100,
		MaxPassAllocations: 1,
		Algorithm:          defrag.AlgorithmFast,
		MemoryState: []Block{
			{
				Size: 250,
				Allocs: []Alloc{
					{metadata.NoAllocation, 100},
					{1, 100},
				},
				TargetedMoves: []TargetedMove{
					{
						SourceAlloc:  2,
						SourceBlock:  1,
						MaxOffset:    math.MaxInt,
						ActualOffset: 0,
						Size:         99,
					},
				},
			},
			{
				Size: 250,
				Allocs: []Alloc{
					{2, 99},
				},
			},
		},
	},
	"FastAlgoNoWorkToDo": {
		MaxPassBytes:       100,
		MaxPassAllocations: 1,
		Algorithm:          defrag.AlgorithmFast,
		MemoryState: []Block{
			{
				Size: 250,
				Allocs: []Alloc{
					{metadata.NoAllocation, 100},
					{1, 100},
				},
			},
		},
	},
	"EnforceMaxBytes": {
		MaxPassBytes:       100,
		MaxPassAllocations: 1,
		Algorithm:          defrag.AlgorithmFull,
		MemoryState: []Block{
			{
				Size: 500,
				Allocs: []Alloc{
					{metadata.NoAllocation, 200},
					{1, 200},
				},
			},
		},
	},
	"EnforceMaxAllocations": {
		MaxPassBytes:       100,
		MaxPassAllocations: 1,
		Algorithm:          defrag.AlgorithmFull,
		MemoryState: []Block{
			{
				Size: 500,
				Allocs: []Alloc{
					{metadata.NoAllocation, 200},
					{1, 100},
					{2, 100},
				},
				TargetedMoves: []TargetedMove{
					{
						SourceAlloc:  1,
						SourceBlock:  0,
						MaxOffset:    200,
						ActualOffset: 0,
						Size:         100,
					},
				},
			},
		},
	},
	"FullAlgoMultiAllocation": {
		MaxPassBytes:       200,
		MaxPassAllocations: 2,
		Algorithm:          defrag.AlgorithmFull,
		MemoryState: []Block{
			{
				Size: 500,
				Allocs: []Alloc{
					{metadata.NoAllocation, 200},
					{1, 100},
					{2, 99},
				},
				TargetedMoves: []TargetedMove{
					{
						SourceAlloc:  1,
						SourceBlock:  0,
						MaxOffset:    200,
						ActualOffset: 0,
						Size:         100,
					},
					{
						SourceAlloc:  2,
						SourceBlock:  0,
						MaxOffset:    300,
						ActualOffset: 100,
						Size:         99,
					},
				},
			},
		},
	},
	"FastAlgoMultiAllocation": {
		MaxPassBytes:       200,
		MaxPassAllocations: 2,
		Algorithm:          defrag.AlgorithmFast,
		MemoryState: []Block{
			{
				Size: 300,
				Allocs: []Alloc{
					{metadata.NoAllocation, 200},
					{1, 100},
				},
				TargetedMoves: []TargetedMove{
					{
						SourceAlloc:  2,
						SourceBlock:  1,
						MaxOffset:    math.MaxInt,
						ActualOffset: 0,
						Size:         50,
					},
					{
						SourceAlloc:  3,
						SourceBlock:  1,
						MaxOffset:    math.MaxInt,
						ActualOffset: 50,
						Size:         49,
					},
				},
			},
			{
				Size: 100,
				Allocs: []Alloc{
					{2, 50},
					{3, 49},
				},
			},
		},
	},
	"FullAlgoCrossBlock": {
		MaxPassBytes:       100,
		MaxPassAllocations: 1,
		Algorithm:          defrag.AlgorithmFull,
		MemoryState: []Block{
			{
				Size: 200,
				Allocs: []Alloc{
					{metadata.NoAllocation, 100},
					{1, 100},
				},
				TargetedMoves: []TargetedMove{
					{
						SourceAlloc:  2,
						SourceBlock:  1,
						MaxOffset:    math.MaxInt,
						ActualOffset: 0,
						Size:         99,
					},
				},
			},
			{
				Size: 100,
				Allocs: []Alloc{
					{2, 99},
				},
			},
		},
	},
	"FullAlgoWithinLateBlock": {
		MaxPassBytes:       100,
		MaxPassAllocations: 1,
		Algorithm:          defrag.AlgorithmFull,
		MemoryState: []Block{
			{
				Size: 150,
				Allocs: []Alloc{
					{metadata.NoAllocation, 50},
					{1, 100},
				},
			},
			{
				Size: 200,
				Allocs: []Alloc{
					{metadata.NoAllocation, 100},
					{2, 99},
				},
				TargetedMoves: []TargetedMove{
					{
						SourceAlloc:  2,
						SourceBlock:  1,
						MaxOffset:    100,
						ActualOffset: 0,
						Size:         99,
					},
				},
			},
		},
	},
}

// Rig up a single block metadata with all its starting allocations & expected incoming allocations
func mockBlockMetadata(ctrl *gomock.Controller, list *mock_defrag.MockBlockList[metadata.BlockAllocationHandle], index int, blockData Block) *mock_metadata.MockBlockMetadata {
	blockMetadata := mock_metadata.NewMockBlockMetadata(ctrl)

	lastAlloc := metadata.NoAllocation
	offset := 0
	largestFree := 0
	for _, alloc := range blockData.Allocs {
		// This is a free region so just track the size
		if alloc.Id == metadata.NoAllocation {
			if alloc.Size > largestFree {
				largestFree = alloc.Size
			}

			offset += alloc.Size
			continue
		}

		// AllocationListBegin and FindNextAllocation are used by defrag to enumerate all allocations in the block
		if lastAlloc == metadata.NoAllocation {
			blockMetadata.EXPECT().AllocationListBegin().AnyTimes().Return(alloc.Id, nil)
		} else {
			blockMetadata.EXPECT().FindNextAllocation(lastAlloc).AnyTimes().Return(alloc.Id, nil)
		}

		// Basic data about the allocation
		blockMetadata.EXPECT().AllocationUserData(alloc.Id).AnyTimes().Return(alloc.Id, nil)
		blockMetadata.EXPECT().AllocationOffset(alloc.Id).AnyTimes().Return(offset, nil)

		// Get a move without a destination filled in before shopping around for better spots
		list.EXPECT().MoveDataForUserData(alloc.Id).AnyTimes().Return(defrag.MoveAllocationData[metadata.BlockAllocationHandle]{
			Alignment:         0,
			SuballocationType: 0,
			Flags:             0,
			Move: defrag.DefragmentationMove[metadata.BlockAllocationHandle]{
				Size:             alloc.Size,
				SrcAllocation:    &alloc.Id,
				SrcBlockMetadata: blockMetadata,
			},
		})

		offset += alloc.Size
		lastAlloc = alloc.Id
	}

	// Mock out the tail of the defrag enumeration chain we set up above
	if lastAlloc == metadata.NoAllocation {
		blockMetadata.EXPECT().AllocationListBegin().Return(metadata.NoAllocation, nil)
	} else {
		blockMetadata.EXPECT().FindNextAllocation(lastAlloc).AnyTimes().Return(metadata.NoAllocation, nil)
	}

	if blockData.Size-offset > largestFree {
		largestFree = blockData.Size - offset
	}

	// Set up MayHaveFreeBlock to return true for areas smaller than the largest free region
	blockMetadata.EXPECT().MayHaveFreeBlock(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(allocType uint32, size int) bool {
		return size <= largestFree
	})

	// Set up all incoming moves
	for moveIndex, move := range blockData.TargetedMoves {
		// The handle and allocation request that will be generated for this incoming allocation
		handle := metadata.BlockAllocationHandle(999 - int(move.SourceAlloc))
		request := metadata.AllocationRequest{
			BlockAllocationHandle: handle,
			Size:                  move.Size,
			Item: metadata.Suballocation{
				Offset:   move.ActualOffset,
				Size:     move.Size,
				UserData: move.SourceAlloc,
				Type:     0,
			},
			Type:          metadata.AllocationRequestTLSF,
			AllocType:     0,
			AlgorithmData: 0,
		}
		blockData.TargetedMoves[moveIndex].ActualDestHandle = handle
		blockMetadata.EXPECT().CreateAllocationRequest(move.Size, uint(0), false, uint32(0), gomock.Any(), move.MaxOffset).Return(
			true, request, nil,
		)
		list.EXPECT().CreateAlloc().Return(&handle)
		list.EXPECT().CommitDefragAllocationRequest(request, index, uint(0), uint32(0), gomock.Any(), uint32(0), gomock.Any()).DoAndReturn(
			func(allocRequest metadata.AllocationRequest, blockIndex int, alignment uint, flags uint32, userData any, suballocType uint32, outAlloc *metadata.BlockAllocationHandle) error {
				*outAlloc = handle
				return nil
			},
		)
		blockMetadata.EXPECT().AllocationOffset(handle).AnyTimes().Return(move.ActualOffset, nil)
	}

	// This fallback CreateAllocationRequest mock will be used for anything that doesn't match the targeted moves above
	// or if we do the same move twice or something- it just says we don't have enough room to fit whatever requested allocation
	blockMetadata.EXPECT().CreateAllocationRequest(gomock.Any(), uint(0), false, uint32(0), gomock.Any(), gomock.Any()).AnyTimes().Return(false, metadata.AllocationRequest{}, nil)

	return blockMetadata
}

func TestDefragmentation(t *testing.T) {
	for testName, testCase := range testCases {
		t.Run(testName, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			blockList := mock_defrag.NewMockBlockList[metadata.BlockAllocationHandle](ctrl)

			blockList.EXPECT().BlockCount().AnyTimes().Return(len(testCase.MemoryState))
			blockList.EXPECT().Lock().AnyTimes()
			blockList.EXPECT().Unlock().AnyTimes()

			for index, mem := range testCase.MemoryState {
				metadata := mockBlockMetadata(ctrl, blockList, index, mem)
				blockList.EXPECT().MetadataForBlock(index).AnyTimes().Return(metadata)
			}

			context := &defrag.PassContext{
				MaxPassBytes:       testCase.MaxPassBytes,
				MaxPassAllocations: testCase.MaxPassAllocations,
			}

			defragContext := defrag.MetadataDefragContext[metadata.BlockAllocationHandle]{
				Algorithm: testCase.Algorithm,
				BlockList: blockList,
			}

			defragContext.BlockListCollectMoves(context)

			var expectedMoves []defrag.DefragmentationMove[metadata.BlockAllocationHandle]

			for blockIndex, block := range testCase.MemoryState {
				for _, expectedMove := range block.TargetedMoves {
					destBlock := blockList.MetadataForBlock(blockIndex)
					srcBlock := blockList.MetadataForBlock(expectedMove.SourceBlock)

					expectedMoves = append(expectedMoves, defrag.DefragmentationMove[metadata.BlockAllocationHandle]{
						MoveOperation:    defrag.DefragmentationMoveCopy,
						Size:             expectedMove.Size,
						SrcBlockMetadata: srcBlock,
						DstBlockMetadata: destBlock,
						SrcAllocation:    &expectedMove.SourceAlloc,
						DstTmpAllocation: &expectedMove.ActualDestHandle,
					})
				}
			}

			actualMoves := defragContext.Moves()
			require.ElementsMatch(t, expectedMoves, actualMoves)
		})
	}
}
