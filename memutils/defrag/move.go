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

type DefragmentOperationHandler[T any] func(move DefragmentationMove[T]) error

type DefragmentationMove[T any] struct {
	MoveOperation DefragmentationMoveOperation

	Size             int
	SrcBlockMetadata metadata.BlockMetadata
	SrcAllocation    *T
	DstBlockMetadata metadata.BlockMetadata
	DstTmpAllocation *T
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

type stateExtensive struct {
	Operation      defragmentOperation
	FirstFreeBlock int
}

type MoveAllocationData[T any] struct {
	Alignment         uint
	SuballocationType metadata.SuballocationType
	Flags             memutils.AllocationCreateFlags
	Move              DefragmentationMove[T]
}
