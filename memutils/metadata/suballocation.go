package metadata

import "math"

type BlockAllocationHandle uint64

const (
	NoAllocation BlockAllocationHandle = math.MaxUint64
)

type Suballocation struct {
	Offset   int
	Size     int
	UserData any
	Type     uint32
}
