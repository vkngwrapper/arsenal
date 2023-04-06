package defrag

import (
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
	"github.com/vkngwrapper/core/v2/common"
)

type BlockList interface {
	MetadataForBlock(index int) metadata.BlockMetadata
	BlockCount() int
	AddStatistics(stats *memutils.Statistics)
	MoveDataForUserData(userData any) moveAllocationData
	MemoryTypeIndexForUserData(userData any) int
	BufferImageGranularity() int

	Lock()
	Unlock()

	CommitAllocationRequest(allocRequest *metadata.AllocationRequest, blockIndex int, alignment uint, flags memutils.AllocationCreateFlags, userData any, suballocType metadata.SuballocationType, outAlloc *any) (common.VkResult, error)
	SwapBlocks(leftIndex, rightIndex int)
}
