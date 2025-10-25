package metadata

import "math"

// BlockAllocationHandle is a value used to represent individual live or free memory segments within
// a BlockMetadata.  The underlying meaning of the handle is determined by the actual BlockMetadata
// implementation, and should not be reasoned about by consumers.
type BlockAllocationHandle uint64

const (
	// NoAllocation is a special value used as a "nil" type value for BlockAllocationHandle
	NoAllocation BlockAllocationHandle = math.MaxUint64
)

type Suballocation struct {
	Offset   int
	Size     int
	UserData any
	Type     uint32
}
