package metadata

// AllocationRequestType is an enum that indicates the type of allocation that is being made.
// It is returned in AllocationRequest from CreateAllocationRequest
type AllocationRequestType uint32

const (
	// AllocationRequestTLSF indicates that the allocation request was sourced from metadata.TLSFBlockMetadata
	AllocationRequestTLSF AllocationRequestType = iota
	// AllocationRequestUpperAddress indicates that the allocation request was sourced from metadata.LinearBlockMetadata
	// and that it is an allocation for the upper side of a double stack
	AllocationRequestUpperAddress
	// AllocationRequestEndOf1st indicates that the allocation request was sourced from metadata.LinearBlockMetadata
	// and that it is an allocation to be added to the end of the first memory vector
	AllocationRequestEndOf1st
	// AllocationRequestEndOf2nd indicates that the allocation request was sourced from metadata.LinearBlockMetadata
	// and that it is an allocation to be added to the end of the second memory vector
	AllocationRequestEndOf2nd
)

var allocationRequestMapping = map[AllocationRequestType]string{
	AllocationRequestTLSF:         "TLSF",
	AllocationRequestUpperAddress: "UpperAddress",
	AllocationRequestEndOf1st:     "EndOf1st",
	AllocationRequestEndOf2nd:     "EndOf2nd",
}

func (t AllocationRequestType) String() string {
	return allocationRequestMapping[t]
}

// AllocationRequest is a type returned from BlockMetadata.CreateAllocationRequest which indicates where and how
// the metadata intends to allocate new memory. This allocation can be applied to the actual memory system consuming
// memutils, and then committed to the metadata with BlockMetadata.Alloc
type AllocationRequest struct {
	// BlockAllocationHandle is a numeric handle used to identify individual allocations within the metadata
	BlockAllocationHandle BlockAllocationHandle
	// Size the total size of the allocation, maybe larger than what was originally requested
	Size int
	// Item is a Suballocation object indicating basic information about the allocation
	Item Suballocation
	// Type identifies the sort of allocation this request represents (and can be used
	// to identify the BlockMetadata implementation used to generate this request).
	Type AllocationRequestType

	// AllocType is the value passed into CreateAllocationRequest by the consumer to generate
	// this request
	AllocType uint32
	// AlgorithmData is arbitrary data used by the BlockMetadata implementation for internal
	// purposes
	AlgorithmData uint64
}
