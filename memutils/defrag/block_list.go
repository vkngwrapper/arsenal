package defrag

import (
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
)

//go:generate mockgen -source block_list.go -destination ./mocks/block_list.go

// BlockList is an interface that is used to indicate a single block-bearing memory pool from a consuming application.
// The defragmentation code uses this to communicate back to a memory pool that holds the allocations being
// relocated in the code calling into the defragmentation process.
//
// Semantically, it is expected that a BlockList represent several pages of memory, identified by index, and
// each page has several allocations of type T that point into various offsets in the memory page.
//
// The memory page is represented within memutils by metadata.BlockMetadata, which tracks where the offsets
// of free and allocated memory are.
//
// If you are attempting to use memutils to implement a new type of memory manager with defragmentation, you
// will need a memory pool object to implement this interface.
type BlockList[T any] interface {
	// MetadataForBlock retrieves the metadata.BlockMetadata corresponding to the memory page identified by
	// the provided index
	MetadataForBlock(index int) metadata.BlockMetadata
	// BlockCount retrieves the number of memory pages in the BlockList
	BlockCount() int
	// AddStatistics accumulates basic statistics about how many blocks, bytes, etc. are in the BlockList
	// and adds them to a memutils.Statistics object
	AddStatistics(stats *memutils.Statistics)
	// MoveDataForUserData retrieves a MoveAllocationData with data prepopulated (except for Move.DstTmpAllocation)
	// based on a userData object retrieved from metadata.BlockMetadata
	MoveDataForUserData(userData any) MoveAllocationData[T]
	// BufferImageGranularity retrieves the BufferImageGranularity that is used to assign offsets for this
	// BlockList object
	BufferImageGranularity() int

	// Lock locks the BlockList so that the internal slice of blocks can be modified
	Lock()
	// Unlock releases the lock taken by Lock
	Unlock()

	// CreateAlloc generates a new allocation of the type used internally to the BlockList, to be used
	// as DstTmpAllocation in a MoveAllocationData object
	CreateAlloc() *T
	// CommitDefragAllocationRequest allocates real memory in the BlockList as part of a relocation oepration
	//
	// allocRequest - a new metadata.AllocationRequest indicating the properties of the allocation to apply, generated
	// internally by metadata.BlockMetadata
	//
	// blockIndex - The index of the memory page to allocate to
	//
	// alignment - The alignment of the new memory offset being chosen
	//
	// flags - The intended behavior of the new allocation
	//
	// userData - A sentinel value used internally by the defragmentation system to be assigned as userData to the allocation
	//
	// subAllocType - The type used for the new allocation
	//
	// outAlloc - An allocation object of the type used internally to the BlockList, to which the allocation will be assigned
	CommitDefragAllocationRequest(allocRequest metadata.AllocationRequest, blockIndex int, alignment uint, flags uint32, userData any, suballocType uint32, outAlloc *T) error
	// SwapBlocks rearranges memory pages within the BlockList's internal slice. The defragmentation code will handle calling Lock / Unlock
	// so it is not necessary for this method to be thread safe.
	SwapBlocks(leftIndex, rightIndex int)
}
