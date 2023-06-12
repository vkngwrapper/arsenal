package defrag

import (
	"github.com/vkngwrapper/arsenal/memutils/metadata"
)

// DefragmentationMoveOperation is an enum that identifies what type of move operation to perform
// on a DefragmentationMove
type DefragmentationMoveOperation uint32

const (
	DefragmentationMoveCopy DefragmentationMoveOperation = iota
	DefragmentationMoveIgnore
	DefragmentationMoveDestroy
)

// DefragmentOperationHandler is a callback type that is used to allow consumers to be notified that
// it is time to follow through on the operation identified by DefragmentationMove.MoveOperation
type DefragmentOperationHandler[T any] func(move DefragmentationMove[T]) error

// DefragmentationMove represents a single relocation operation being proposed by the defragmentation code.
// Within is a Size indicating the total number of bytes to relocate, a source and destination allocation,
// and a MoveOperation that initially defaults to DefragmentationMoveCopy but can be changed by the consumer
// if other operations are more appropriate).
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

// MoveAllocationData represents a single allocation that needs to be relocated. It contains
// useful information about the moving allocation, as well as a DefragmentationMove object
// that is generally populated with source allocation information, but not destination allocation
// yet. This object will be used to allocate the DefragmentationMove.DstTmpAllocation object
type MoveAllocationData[T any] struct {
	Alignment         uint
	SuballocationType uint32
	Flags             uint32
	Move              DefragmentationMove[T]
}
