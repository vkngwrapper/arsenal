package allocation

import "math"

type AllocationRequestType uint32

const (
	AllocationRequestNormal AllocationRequestType = iota
	AllocationRequestTLSF
	AllocationRequestUpperAddress
	AllocationRequestEndOf1st
	AllocationRequestEndOf2nd
)

var allocationRequestMapping = map[AllocationRequestType]string{
	AllocationRequestNormal:       "Normal",
	AllocationRequestTLSF:         "TLSF",
	AllocationRequestUpperAddress: "UpperAddress",
	AllocationRequestEndOf1st:     "EndOf1st",
	AllocationRequestEndOf2nd:     "EndOf2nd",
}

func (t AllocationRequestType) String() string {
	return allocationRequestMapping[t]
}

type BlockAllocationHandle uint64

const (
	NoAllocation BlockAllocationHandle = math.MaxUint64
)

type AllocationRequest struct {
	BlockAllocationHandle BlockAllocationHandle
	Size                  int
	Item                  *Suballocation
	CustomData            any
	AlgorithmData         uint64
	Type                  AllocationRequestType
}
