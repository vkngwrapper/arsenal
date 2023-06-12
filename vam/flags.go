package vam

import "github.com/vkngwrapper/core/v2/common"

// AllocationCreateFlags exposes several options for allocation behavior that can be applied.
type AllocationCreateFlags int32

var allocationCreateFLagsMapping = common.NewFlagStringMapping[AllocationCreateFlags]()

func (f AllocationCreateFlags) Register(str string) {
	allocationCreateFLagsMapping.Register(f, str)
}
func (f AllocationCreateFlags) String() string {
	return allocationCreateFLagsMapping.FlagsToString(f)
}

const (
	// AllocationCreateDedicatedMemory instructs the allocator to give this allocate its own memory block
	AllocationCreateDedicatedMemory AllocationCreateFlags = 1 << iota
	// AllocationCreateNeverAllocate instructs the allocator to only try to allocate from existing
	// DeviceMemory blocks and never create new blocks
	//
	// If a new allocation cannot be placed in any of the existing blocks, allocation fails with
	// core1_0.VKErrorOutOfDeviceMemory
	AllocationCreateNeverAllocate
	// AllocationCreateMapped instructs the allocator to use memory that will be persistently mapped
	// and retrieve a pointer to it
	//
	// The pointer will be available via Allocation.mappedData()
	//
	// It is valid to use this flag for an allocation made from a memory type that is not HOST_VISIBLE.
	// This flag is then ignored and the memory is not mapped. This is useful if you need an allocation
	// that is efficient to use on GPU (DEVICE_LOCAL) and want to map it if possible on platforms that
	// support it (i.e. integrated memory).
	AllocationCreateMapped
	// AllocationCreateUpperAddress will instruct the allocator to create the allocation from the upper
	// stack in a double stack pool. This flag is only allowed for custom pools created with the
	// PoolCreateLinearAlgorithm flag
	AllocationCreateUpperAddress
	// AllocationCreateDontBind instructs the allocator to create both buffer/image and allocation, but
	// don't bind them together. This is useful when you want to make the binding yourself to do more advanced
	// binding such as using some extension. This flag is only useful in functions that bind by default:
	// Allocator.CreateBuffer, Allocator.CreateImage, etc. Otherwise, it is ignored.
	//
	// If you want to make sure the buffer/image are not tied to the new allocation through
	// MemoryDedicatedAllocateInfo structure in case the allocation ends up in its own memory block,
	// also use AllocationCreateCanAlias
	AllocationCreateDontBind
	// AllocationCreateWithinBudget instructs the allocator to only create the allocation if additional
	// device memory required for it won't exceed memory budget. Otherwise, return  core1_0.VKErrorOutOfDeviceMemory
	AllocationCreateWithinBudget
	// AllocationCreateCanAlias indicates whether the allocated memory will have aliasing resources.
	//
	// Using this flag prevents supplying MemoryDedicatedAllocateInfo when AllocationCreateDedicatedMemory
	// is specified. Otherwise, created dedicated memory will not be suitable for aliasing resources.
	AllocationCreateCanAlias
	// AllocationCreateHostAccessSequentialWrite requests the possibility to map the allocation
	//
	// If you use MemoryUsageAuto* you must use this flag to be able to map the allocation. If
	// you use other values of MemoryUsage, this flag is ignored and mapping is always possible
	// in memory types that are HostVisible. This includes allocations created in custom memory pools.
	//
	// This declares that mapped memory will only be written sequentially, never read or accessed randomly,
	// so a memory type can be selected that is uncached and write-combined. Violating this restriction
	// may or may not work correctly, but will likely be very slow
	AllocationCreateHostAccessSequentialWrite
	// AllocationCreateHostAccessRandom requests the possibility to map the allocation
	//
	// If you use MemoryUsageAuto* you must use this flag to be able to map the allocation. If
	// you use other values of MemoryUsage, this flag is ignored and mapping is always possible in memory
	// types that are HostVisible. This includes allocations created in custom memory pools.
	//
	// This declares that mapped memory can be read, written, and accessed in random order, so a HostCached
	// memory type is required.
	AllocationCreateHostAccessRandom
	// AllocationCreateHostAccessAllowTransferInstead combines with AllocationCreateHostAccessSequentialWrite
	// or AllocationCreateHostAccessRandom to indicate that despite request for host access, a non-HostVisible
	// memory type can be selected if it may improve performance.
	//
	// By using this flag, you declare that you will check if the allocation ended up in a HostVisible memory
	// type and if not, you will create some "staging" buffer and issue an explicit transfer to write/read your
	// data. To prepare for this possibility, don't forget to add appropriate flags like core1_0.BufferUsageTransferDst
	// and core1_0.BufferUsageTransferSrc to the parameters of the created Buffer or Image.
	AllocationCreateHostAccessAllowTransferInstead
	// AllocationCreateStrategyMinMemory selects the allocation strategy that chooses the smallest-possible
	// free range for the allocation to minimize memory usage and fragmentation, possibly at the expense of
	// allocation time
	AllocationCreateStrategyMinMemory
	// AllocationCreateStrategyMinTime selects the allocation strategy that chooses the first suitable free
	// range for the allocation- not necessarily in terms of the smallest offset, but the one that is easiest
	// and fastest to find to minimize allocation time, possibly at the expense of allocation quality.
	AllocationCreateStrategyMinTime
	// AllocationCreateStrategyMinOffset selects the allocation strategy that chooses the lowest offset in
	// available space. This is not the most efficient strategy, but achieves highly packed data. Used internally
	// by defragmentation, not recommended in typical usage.
	AllocationCreateStrategyMinOffset

	AllocationCreateStrategyMask = AllocationCreateStrategyMinMemory |
		AllocationCreateStrategyMinTime |
		AllocationCreateStrategyMinOffset
)

func init() {
	AllocationCreateDedicatedMemory.Register("AllocationCreateDedicatedMemory")
	AllocationCreateNeverAllocate.Register("AllocationCreateNeverAllocate")
	AllocationCreateMapped.Register("AllocationCreateMapped")
	AllocationCreateUpperAddress.Register("AllocationCreateUpperAddress")
	AllocationCreateDontBind.Register("AllocationCreateDontBind")
	AllocationCreateWithinBudget.Register("AllocationCreateWithinBudget")
	AllocationCreateCanAlias.Register("AllocationCreateCanAlias")
	AllocationCreateHostAccessSequentialWrite.Register("AllocationCreateHostAccessSequentialWrite")
	AllocationCreateHostAccessRandom.Register("AllocationCreateHostAccessRandom")
	AllocationCreateHostAccessAllowTransferInstead.Register("AllocationCreateHostAccessAllowTransferInstead")
	AllocationCreateStrategyMinMemory.Register("AllocationCreateStrategyMinMemory")
	AllocationCreateStrategyMinTime.Register("AllocationCreateStrategyMinTime")
	AllocationCreateStrategyMinOffset.Register("AllocationCreateStrategyMinOffset")
}
