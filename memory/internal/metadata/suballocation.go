package metadata

import "math"

type BlockAllocationHandle uint64

const (
	NoAllocation BlockAllocationHandle = math.MaxUint64
)

type SuballocationType uint32

const (
	SuballocationFree SuballocationType = iota
	SuballocationUnknown
	SuballocationBuffer
	SuballocationImageUnknown
	SuballocationImageLinear
	SuballocationImageOptimal
)

var suballocationTypeMapping = map[SuballocationType]string{
	SuballocationFree:         "SuballocationFree",
	SuballocationUnknown:      "SuballocationUnknown",
	SuballocationBuffer:       "SuballocationBuffer",
	SuballocationImageUnknown: "SuballocationImageUnknown",
	SuballocationImageLinear:  "SuballocationImageLinear",
	SuballocationImageOptimal: "SuballocationImageOptimal",
}

func (s SuballocationType) String() string {
	str, ok := suballocationTypeMapping[s]
	if !ok {
		return "unknown SuballocationType"
	}

	return str
}

type Suballocation struct {
	Offset   int
	Size     int
	UserData any
	Type     SuballocationType
}
