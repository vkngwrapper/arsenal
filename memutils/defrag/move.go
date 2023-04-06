package defrag

import (
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
)

type DefragmentationMoveOperation uint32

const (
	DefragmentationMoveCopy DefragmentationMoveOperation = iota
	DefragmentationMoveIgnore
	DefragmentationMoveDestroy
)

type DefragmentOperationHandler func(srcAllocation any, dstAllocation any) DefragmentationMoveOperation

type DefragmentationMove struct {
	Size             int
	SrcBlockMetadata metadata.BlockMetadata
	SrcAllocation    any
	DstBlockMetadata metadata.BlockMetadata
	DstTmpAllocation any
}

type defragmentOperation uint32

const (
	defragmentOperationFindFreeBlockTexture defragmentOperation = iota
	defragmentOperationFindFreeBlockBuffer
	defragmentOperationFindFreeBlockAll
	defragmentOperationMoveBuffers
	defragmentOperationMoveTextures
	defragmentOperationMoveAll
	defragmentOperationCleanup
	defragmentOperationDone
)

var defragOperationMapping = map[defragmentOperation]string{
	defragmentOperationFindFreeBlockTexture: "defragmentOperationFindFreeBlockTexture",
	defragmentOperationFindFreeBlockBuffer:  "defragmentOperationFindFreeBlockBuffer",
	defragmentOperationFindFreeBlockAll:     "defragmentOperationFindFreeBlockAll",
	defragmentOperationMoveBuffers:          "defragmentOperationMoveBuffers",
	defragmentOperationMoveTextures:         "defragmentOperationMoveTextures",
	defragmentOperationMoveAll:              "defragmentOperationMoveAll",
	defragmentOperationCleanup:              "defragmentOperationCleanup",
	defragmentOperationDone:                 "defragmentOperationDone",
}

func (m defragmentOperation) String() string {
	return defragOperationMapping[m]
}

type stateBalanced struct {
	AverageFreeSize  int
	AverageAllocSize int
}

func (s *stateBalanced) UpdateStatistics(blockList BlockList) {
	s.AverageFreeSize = 0
	s.AverageAllocSize = 0

	var allocCount, freeCount int
	for i := 0; i < blockList.BlockCount(); i++ {
		metadata := blockList.MetadataForBlock(i)

		allocCount += metadata.AllocationCount()
		freeCount += metadata.FreeRegionsCount()
		s.AverageFreeSize += metadata.SumFreeSize()
		s.AverageAllocSize += metadata.Size()
	}

	s.AverageAllocSize = (s.AverageAllocSize - s.AverageFreeSize) / allocCount
	s.AverageFreeSize /= freeCount
}

type stateExtensive struct {
	Operation      defragmentOperation
	FirstFreeBlock int
}

type moveAllocationData struct {
	Alignment         uint
	SuballocationType metadata.SuballocationType
	Flags             memutils.AllocationCreateFlags
	Move              DefragmentationMove
}
