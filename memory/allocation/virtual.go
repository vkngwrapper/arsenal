package allocation

import "github.com/vkngwrapper/core/v2/common"

type VirtualAllocationCreateFlags uint32

var virtualAllocationCreateFlagsMapping = common.NewFlagStringMapping[VirtualAllocationCreateFlags]()

func (f VirtualAllocationCreateFlags) Register(str string) {
	virtualAllocationCreateFlagsMapping.Register(f, str)
}

func (f VirtualAllocationCreateFlags) String() string {
	return virtualAllocationCreateFlagsMapping.FlagsToString(f)
}

const (
	VirtualAllocationCreateUpperAddress      VirtualAllocationCreateFlags = VirtualAllocationCreateFlags(AllocationCreateUpperAddress)
	VirtualAllocationCreateStrategyMinMemory VirtualAllocationCreateFlags = VirtualAllocationCreateFlags(AllocationCreateStrategyMinMemory)
	VirtualAllocationCreateStrategyMinTime   VirtualAllocationCreateFlags = VirtualAllocationCreateFlags(AllocationCreateStrategyMinTime)
	VirtualAllocationCreateStrategyMinOffset VirtualAllocationCreateFlags = VirtualAllocationCreateFlags(AllocationCreateStrategyMinOffset)

	VirtualAllocationCreateStrategyMask VirtualAllocationCreateFlags = VirtualAllocationCreateFlags(AllocationCreateStrategyMask)
)

func init() {
	VirtualAllocationCreateUpperAddress.Register("VirtualAllocationCreateUpperAddress")
	VirtualAllocationCreateStrategyMinMemory.Register("VirtualAllocationCreateStrategyMinMemory")
	VirtualAllocationCreateStrategyMinTime.Register("VirtualAllocationCreateStrategyMinTime")
	VirtualAllocationCreateStrategyMinOffset.Register("VirtualAllocationCreateStrategyMinOffset")
}

type VirtualAllocationCreateInfo struct {
	Size      int
	Alignment uint

	Flags VirtualAllocationCreateFlags

	UserData any
}

type VirtualAllocationInfo struct {
	Offset int
	Size   int

	UserData any
}