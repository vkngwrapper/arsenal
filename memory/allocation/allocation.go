package allocation

import (
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
)

type Allocation interface {
	InitBlockAllocation()
	InitDedicatedAllocation()

	AllocationType() AllocationType
	Alignment() uint
	Size() int
	UserData() any
	Name() string
	SuballocationType() SuballocationType
	BlockAllocationHandle() BlockAllocationHandle

	PrintParameters(json *jwriter.ObjectState)
	BufferUsage() *core1_0.BufferUsageFlags
	ImageUsage() *core1_0.ImageUsageFlags
}

type AllocationType byte

const (
	AllocationTypeNone AllocationType = iota
	AllocationTypeBlock
	AllocationTypeDedicated
)

type AllocationFlags uint32

const (
	AllocationPersistentMap AllocationFlags = 1 << iota
	AllocationMappingAllowed
)

var allocationFlagsMapping = common.NewFlagStringMapping[AllocationFlags]()

func init() {
	allocationFlagsMapping.Register(AllocationPersistentMap, "AllocationPersistentMap")
	allocationFlagsMapping.Register(AllocationMappingAllowed, "AllocationMappingAllowed")
}

type VulkanAllocation struct {
}
