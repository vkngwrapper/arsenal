package defrag

import (
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
	"github.com/vkngwrapper/core/v2/common"
)

type BlockList[T any] interface {
	MetadataForBlock(index int) metadata.BlockMetadata
	BlockCount() int
	AddStatistics(stats *memutils.Statistics)
	MoveDataForUserData(userData any) MoveAllocationData[T]
	BufferImageGranularity() int

	Lock()
	Unlock()

	CreateAlloc() *T
	CommitDefragAllocationRequest(allocRequest metadata.AllocationRequest, blockIndex int, alignment uint, flags memutils.AllocationCreateFlags, userData any, suballocType metadata.SuballocationType, outAlloc *T) (common.VkResult, error)
	SwapBlocks(leftIndex, rightIndex int)
}
