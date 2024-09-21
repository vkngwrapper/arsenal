package metadata

// GranularityCheck represents an important concept in some memory systems.  In certain memory systems
// (video memory being an important one), certain types of allocations cannot be too close to each other.
// Memory is separated into "slots" of particular size, and slots must effectively be assigned an allocation
// type. As a result, it is incumbent upon a memory management system to track the type assigned to each
// "slot" and not allow conflicting suballocations to be created.
//
// GranularityCheck is responsible for this. For systems that do not have the concept of memory granularity,
// it is sufficient to create a dummy implementation that always returns success and does not store any
// information.  Otherwise, the granularity rules of the system will need to be represented in the implementation
// in order to allow memutils to make good allocation decisions.
type GranularityCheck interface {
	// AllocRegions stores a region of memory and assigns it a memory-system-specific allocation type
	AllocRegions(allocType uint32, offset, size int)
	// FreeRegions removes a region of memory
	FreeRegions(offset, size int)
	// Clear removes all regions of memory
	Clear()
	// CheckConflictAndAlignUp is provided information about a potential offset and attempts to find a non-
	// conflicting place for that offset. If the offset is being placed in a location where it will conflict
	// with an existing allocation, the allocation will attempt to be nudged forward into a new slot.  An
	// updated allocation offset will be returned, along with a boolean indicating whether a non-conflicting
	// location could be found.
	//
	// allocOffset - Offset within the block of the requested allocation
	// allocSize - Size of the requested allocation
	// regionOffset - Offset within the block of the free region being allocated to
	// regionSize - Size of the free region being allocated to
	// allocType - The memory-system-specific allocation type of the requested allocation
	CheckConflictAndAlignUp(allocOffset, allocSize, regionOffset, regionSize int, allocType uint32) (int, bool)
	// RoundUpAllocRequest will return an updated alignment and size of a requested allocation, if necessary.
	// Some allocation types in some memory systems can't share or can't easily share slots with other allocations,
	// and so it is better for these allocations to expand to fill their slots in order to prevent the creation
	// of free regions that cannot be used by anyone.
	RoundUpAllocRequest(allocType uint32, allocSize int, allocAlignment uint) (int, uint)
	// AllocationsConflict will return a boolean indicating whether two memory-system-specific allocation
	// types can share the same slot.
	AllocationsConflict(firstAllocType uint32, secondAllocType uint32) bool

	// StartValidation will begin an internal consistency check on the granularity data. These checks may
	// be expensive, depending on the implementation. When an implementation is functioning correctly, it
	// should not be possible for this process to return an error, but this may assist in diagnosing issues
	// with the implementation.
	//
	// StartValidation will return a ctx object that will be passed to Validate and FinishValidation.
	StartValidation() any
	// Validate will validate a single allocation. An error will be returned if there are issues with the
	// provided region.
	//
	// ctx - The object that was returned from StartValidation
	// offset - The offset within the block of a specific allocation
	// size - The size of the allocation
	Validate(ctx any, offset, size int) error
	// FinishValidation will complete internal consistency checks and return an error if there was an issue.
	// Validate must have been called for each allocation before calling this method.
	//
	// ctx - The object that was returned from StartValidation.
	FinishValidation(ctx any) error
}
