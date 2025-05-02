package vam

import (
	"github.com/vkngwrapper/core/v2/core1_0"
)

// MemoryUsage is an enum passed to the Usage field of AllocationCreateInfo to
// indicate how memory types are to be selected for the allocation in question.
type MemoryUsage uint32

const (
	// MemoryUsageUnknown indicates no intended memory usage was specified. When choosing a memory type,
	// the Allocator uses only the Required and Preferred flags specified in AllocationCreateInfo to
	// find an appropriate memory index and nothing else. This may be desirable when using Allocator.AllocateMemory
	// and Allocator.AllocateMemorySlice, since the allocator does not have BufferUsage or ImageUsage flags
	// to make decisions with. Bear in mind that it is possible, in this case, for the allocator to
	// select a memory type that is not compatible with your AllocationCreateFlags if you do not
	// specify appropriate RequiredFlags/PreferredFlags. For instance, if AllocationCreateFlags contains
	// AllocationCreateMapped, but you do not specify HostVisible in your RequiredFlags, then
	// the allocation may fail when it attempts to map your new memory.
	MemoryUsageUnknown MemoryUsage = iota
	// MemoryUsageGPULazilyAllocated indicates lazily-allocated GPU memory. Exists mostly
	// on mobile platforms. Using it on desktop PC or other GPUs with no such memory type present
	// will fail the allocation.
	//
	// Usage: Memory for transient attachment images (color attachments, depth attachments, etc.) created
	// with core1_0.ImageUsageTransientAttachment
	//
	// Allocations with this usage are always created as dedicated - it implies AllocationCreateDedicatedMemory
	MemoryUsageGPULazilyAllocated
	// MemoryUsageAuto selects the best memory type automatically. This value is recommended for most
	// common use cases.
	//
	// When using this value, if you want to map the allocation, you must pass one of the flags
	// AllocationCreateHostAccessSequentialWrite or AllocationCreateHostAccessRandom in AllocationCreateInfo.Flags
	//
	// It can be used only with functions indicate a Buffer or Image and not with generic memory allocation
	// functions.
	MemoryUsageAuto
	// MemoryUsageAutoPreferDevice selects the best memory type automatically with preference for GPU (device)
	// memory. When using this value, if you want to map the allocation, you must pass one of the flags
	// AllocationCreateHostAccessSequentialWrite or AllocationCreateHostAccessRandom in AllocationCreateInfo.Flags
	//
	// It can be used only with functions indicate a Buffer or Image and not with generic memory allocation
	// functions.
	MemoryUsageAutoPreferDevice
	// MemoryUsageAutoPreferHost selects the best memory type automatically with preference for CPU (host)
	// memory. When using this value, if you want to map the allocation, you must pass one of the flags
	// AllocationCreateHostAccessSequentialWrite or AllocationCreateHostAccessRandom in AllocationCreateInfo.Flags
	//
	// It can be used only with functions indicate a Buffer or Image and not with generic memory allocation
	// functions.
	MemoryUsageAutoPreferHost
)

var memoryUsageMapping = map[MemoryUsage]string{
	MemoryUsageUnknown:            "MemoryUsageUnknown",
	MemoryUsageGPULazilyAllocated: "MemoryUsageGPULazilyAllocated",
	MemoryUsageAuto:               "MemoryUsageAuto",
	MemoryUsageAutoPreferDevice:   "MemoryUsageAutoPreferDevice",
	MemoryUsageAutoPreferHost:     "MemoryUsageAutoPreferHost",
}

func (u MemoryUsage) String() string {
	str, ok := memoryUsageMapping[u]
	if !ok {
		return "unknown"
	}
	return str
}

// AllocationCreateInfo is an options struct that is used to define the specifics of a new allocation created
// by Allocator.AllocateMemory, Allocator.AllocateMemorySlice, Allocator.AllocateMemoryForBuffer,
// Allocator.AllocateMemoryForImage, etc.
type AllocationCreateInfo struct {
	// Flags is a memutils.AllocationCreateFlags value that describes the intended behavior of the
	// created Allocation
	Flags AllocationCreateFlags
	// Usage indicates how the new allocation will be used, allowing vam to decide what memory
	// type to use
	Usage MemoryUsage

	// RequiredFlags indicates what flags must be on the memory type. If no type with these flags can be found with
	// enough free memory, the allocation will fail
	RequiredFlags core1_0.MemoryPropertyFlags
	// PreferredFlags indicates a set of flags that should be on the memory type. Each specified flag are considered
	// equally important: that is, if two flags are specified and no memory type contains both, an arbitrary memory
	// type with one of the two will be chosen, if it exists.
	PreferredFlags core1_0.MemoryPropertyFlags

	// MemoryTypeBits is a bitmask of memory types that may be chosen for the requested allocation. If this is left
	// 0, all memory types are permitted.
	MemoryTypeBits uint32
	// Pool is the custom memory pool to allocate from. This is usually nil, in which case the memory will be
	// allocated directly from the Allocator.
	Pool *Pool

	// UserData is an arbitrary value that will be applied to the Allocation. Allocation.UserData() will return
	// this value after the allocation is complete. It's often helpful to place a resource object that ties the
	// Allocation to a Buffer or Image here.
	UserData interface{}
	// Priority is the ext_memory_priority priority value applied to the allocated memory. This only has
	// an effect for dedicated allocations, it is ignored for block allocations.
	Priority float32
}
