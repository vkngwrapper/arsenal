package metadata

import (
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/core/v2/driver"
	"unsafe"
)

// BlockMetadata represents a single large allocation of memory within some system. It manages
// suballocations within the block, allowing allocations to be requested and freed, as well as
// enumerated and queried.
type BlockMetadata interface {
	// Init must be called before the BlockMetadata is used. It gives the implementation an opportunity
	// to ensure that metadata structures are prepared for allocations, as well as allows the consumer
	// to inform the implementation of the size in bytes of the block of memory it will be managing,
	// via the size parameter.
	Init(size int)
	// Size retrieves the size in bytes that the block was initialized with
	Size() int
	// SupportsRandomAccess returns a boolean indicating whether the implementation allows allocations
	// to be made in arbitrary sections of the managed block, or whether the implementation demands
	// that allocation offsets be deterministic. As an example, the two-level segregated fit implementation
	// allows random access, while the linear (stack and ring buffer) implementation does not.
	// This method must return true for the block to be used with the memutils.defrag package.
	SupportsRandomAccess() bool

	// Validate performs internal consistency checks on the metadata. These checks may be expensive, depending
	// on the implementation. When the implementation is functioning correctly, it should not be possible
	// for this method to return an error, but this may assist in diagnosing issues with the implementation.
	Validate() error
	// AllocationCount returns the number of suballocations currently live in the implementation. This number
	// should generally be the number of successful allocations minus the number of successful frees.
	AllocationCount() int
	// FreeRegionsCount returns the number of unique regions of free memory in the block. The specific meaning
	// of this value is implementation-specific, but adjacent regions of free memory should usually be counted
	// as a single region (or, in fact, merged into a single region).
	FreeRegionsCount() int
	// SumFreeSize returns the number of free bytes of memory in the block.
	SumFreeSize() int
	// MayHaveFreeBlock should return a heuristic indicating whether the block could possibly support a new
	// allocation of the provided type and size. allocType is a value that has meaning within the memory
	// system consuming BlockMetadata. The implementation may or may not care, and could potentially pass
	// the value back to some callback or interface provided by the consumer. The size parameter is the size
	// in bytes of the hypothetical allocation.
	//
	// This method is used by memutils.defrag to very rapidly determine whether it can ignore blocks when
	// trying to reposition allocations. As a result, the most important requirement for the implementation
	// is that this method be fast and not produce false negatives. False positives are ok, but ideal performance
	// requires that this method balance runtime with the likelihood of false positives.
	//
	// It is completely acceptable for consumers to use this method for the same purpose as memutils.defrag
	// (determine whether a block can be ignored while attempting to rapidly make allocations of a particular
	// size).
	MayHaveFreeBlock(allocType uint32, size int) bool

	// IsEmpty will return true if this block has no live suballocations
	IsEmpty() bool

	// VisitAllRegions will call the provided callback once for each allocation and free region in
	// the block.  Depending on implementation, this can be extremely slow and should generally not
	// be done except for diagnostic purposes.
	VisitAllRegions(handleBlock func(handle BlockAllocationHandle, offset int, size int, userData any, free bool) error) error
	// AllocationListBegin will retrieve the handle very first allocation in the block, if any. If none exist, the
	// BlockAllocationHandle value NoAllocation will be returned.
	//
	// The implementation must return an error if SupportsRandomAccess() returns false.
	AllocationListBegin() (BlockAllocationHandle, error)
	// FindNextAllocation accepts a BlockAllocationHandle that maps to a live allocation within the block
	// and returns the handle for the next live allocation within the block, if any. If none exist, the
	// BlockAllocationHandle value NoAllocation will be returned.
	//
	// The implementation must return an error if SupportsRandomAccess() returns false. It must also
	// return an error if the provided allocHandle does not map to a live allocation within this block.
	FindNextAllocation(allocHandle BlockAllocationHandle) (BlockAllocationHandle, error)

	// AllocationOffset accepts a BlockAllocationHandle that maps to a live region of memory
	// (allocated or free) within the block and returns the offset in bytes within the block for that
	// region of memory.
	//
	// The implementation must return an error if the provided handle does not map to a live region of
	// memory within this block.
	AllocationOffset(allocHandle BlockAllocationHandle) (int, error)
	// AllocationUserData accepts a BlockAllocationHandle that maps to a live allocation within the block
	// and returns the userdata value provided by the consumer for that allocation.
	//
	// The implementation must return an error if the provided handle does not map to a live allocation
	// within this block.
	AllocationUserData(allocHandle BlockAllocationHandle) (any, error)
	// SetAllocationUserData accepts a BlockAllocationHandle that maps to a live allocation within the
	// block and a userData value. The allocation's userData is changed to the provided userData.
	//
	// The implementation must return an error if the provided handle does not map to a live allocation
	// within this block.
	SetAllocationUserData(allocHandle BlockAllocationHandle, userData any) error

	// AddDetailedStatistics sums this block's allocation statistics into the statistics currently present
	// in the provided memutils.DetailedStatistics object.
	AddDetailedStatistics(stats *memutils.DetailedStatistics)
	// AddStatistics sums this block's allocation statistics into the statistics currently present in the
	// provided memutils.Statistics object.
	AddStatistics(stats *memutils.Statistics)

	// Clear instantly frees all allocations and
	Clear()
	// BlockJsonData populates a json object with information about this block
	BlockJsonData(json jwriter.ObjectState)

	// CheckCorruption accepts a pointer to the underlying memory that this block manages. It will return
	// nil if anti-corruption memory markers are present for every suballocation in the block. This method
	// is fairly expensive and so should only be run as part of some sort of diagnostic regime.
	//
	// Bear in mind that anti-corruption memory markers are only written when memutils is built with
	// the build flag `debug_mem_utils`. This method will not return an error when that flag is not present,
	// but it is expensive regardless of build flags and so should only be run when mem_utils.DebugMargin
	// is not 0.
	//
	// Additionally, it is the responsibility of consumers to write the debug markers themselves after
	// allocation, by calling memutils.WriteMagicValue with the same pointer sent to CheckCorruption.
	// If the consumer has failed to write the anti-corruption markers, then this method will return an
	// error.
	CheckCorruption(blockData unsafe.Pointer) error

	// CreateAllocationRequest retrieves an AllocationRequest object indicating where and how the implementation
	// would prefer to allocate the requested memory. That object can be passed to Alloc to commit the
	// allocation.
	//
	// allocSize - the size in bytes of the requested allocation
	// allocAlignment - the minimum alignment of the requested allocation. The implementation may increase
	// the alignment above this value, but may not reduce it below this value
	// upperAddress - In implementations that split the memory block into two tranches (such as
	// LinearBlockMetadata and its double stack mode), this parameter indicates that the allocation should
	// be made in the upper tranch if true. When there is only a single tranch of memory in the implementation,
	// the implementation should return an error when this argument is true.
	// allocType - Memory-system-dependent allocation type value. The consumer may care about this.
	// Implementations usually have a consumer-provided "granularity handler" which may care about this.
	// strategy - Whether to prioritize memory usage, memory offset, or allocation speed when choosing
	// a place for the requested allocation.
	// maxOffset - This parameter should usually be math.MaxInt. The requested allocation must fail
	// if the allocation cannot be placed at an offset before the provided maxOffset. This is primarily
	// used by memutils.defrag to make relocating an allocation within a block more performant.
	CreateAllocationRequest(
		allocSize int, allocAlignment uint,
		upperAddress bool,
		allocType uint32,
		strategy AllocationStrategy,
		maxOffset int,
	) (bool, AllocationRequest, error)
	// Alloc commits an AllocationRequest object, creating the suballocation within the block based
	// on the data described in the AllocationRequest. The implementation must return an error if the
	// allocation is no longer valid- i.e. the requested free region no longer exists, is not free,
	// offset has changed, is no longer large enough to support the request, etc.
	Alloc(request AllocationRequest, allocType uint32, userData any) error

	// Free frees a suballocation within the block, causing it to become a free region once again.
	//
	// The implementation must return an error if the provided handle does not map to a live allocation
	// within this block.
	Free(allocHandle BlockAllocationHandle) error
}

// BlockMetadataBase is a simple struct that provides a few shared utilities for BlockMetadata
// implementations in the memutils module.
type BlockMetadataBase struct {
	size                  int
	allocationCallbacks   *driver.AllocationCallbacks
	allocationGranularity int
	granularityHandler    GranularityCheck
}

// NewBlockMetadata creates a new BlockMetadataBase from a granularity value and handler. These
// are memory-system-specific and should have been provided by the consumer. See GranularityCheck
// for more information. If your memory system does not have granularity requirements,
// then allocationGranularity should be 1.
func NewBlockMetadata(allocationGranularity int, granularityHandler GranularityCheck) BlockMetadataBase {
	return BlockMetadataBase{
		size:                  0,
		allocationGranularity: allocationGranularity,
		granularityHandler:    granularityHandler,
	}
}

// Init prepares this structure for allocations and sizes the block in bytes based on the parameter size.
func (m *BlockMetadataBase) Init(size int) {
	m.size = size
}

// Size returns the size of the block in bytes
func (m *BlockMetadataBase) Size() int { return m.size }

// BlockJsonData populates a json object with information about this block
func (m *BlockMetadataBase) BlockJsonData(json jwriter.ObjectState, unusedBytes, allocationCount, unusedRangeCount int) {
	json.Name("TotalBytes").Int(m.Size())
	json.Name("UnusedBytes").Int(unusedBytes)
	json.Name("Allocations").Int(allocationCount)
	json.Name("UnusedRanges").Int(unusedRangeCount)
}
