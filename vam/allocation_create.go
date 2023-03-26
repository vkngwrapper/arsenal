package vam

import (
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/core/v2/core1_0"
)

type MemoryUsage uint32

const (
	// MemoryUsageUnknown indicates no intended memory usage was specified
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
	// MemoryUsageAuto selects the best memory type automatically. This flag is recommended for most
	// common use cases.
	//
	// When using this flag, if you want to map the allocation, you must pass one of the flags
	// AllocationCreateHostAccessSequentialWrite or AllocationCreateHostAccessRandom in AllocationCreateInfo.Flags
	//
	// It can be used only with functions indicate a Buffer or Image and not with generic memory allocation
	// functions.
	MemoryUsageAuto
	// MemoryUsageAutoPreferDevice selects the best memory type automatically with preference for GPU (device)
	// memory. When using this flag, if you want to map the allocation, you must pass one of the flags
	// AllocationCreateHostAccessSequentialWrite or AllocationCreateHostAccessRandom in AllocationCreateInfo.Flags
	//
	// It can be used only with functions indicate a Buffer or Image and not with generic memory allocation
	// functions.
	MemoryUsageAutoPreferDevice
	// MemoryUsageAutoPreferHost selects the best memory type automatically with preference for CPU (host)
	// memory. When using this flag, if you want to map the allocation, you must pass one of the flags
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

type AllocationCreateInfo struct {
	Flags memutils.AllocationCreateFlags
	Usage MemoryUsage

	RequiredFlags  core1_0.MemoryPropertyFlags
	PreferredFlags core1_0.MemoryPropertyFlags

	MemoryTypeBits uint32
	Pool           Pool

	UserData interface{}
	Priority float32
}
