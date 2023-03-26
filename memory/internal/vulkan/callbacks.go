package vulkan

import (
	"github.com/vkngwrapper/arsenal/memory"
	"github.com/vkngwrapper/core/v2/core1_0"
)

type MemoryCallbacks struct {
	Callbacks *memory.MemoryCallbackOptions
	Allocator memory.Allocator
}

func (c *MemoryCallbacks) Allocate(
	memoryType int,
	memory core1_0.DeviceMemory,
	size int,
) {
	if c.Callbacks.Allocate != nil {
		c.Callbacks.Allocate(c.Allocator, memoryType, memory, size, c.Callbacks.UserData)
	}
}

func (c *MemoryCallbacks) Free(
	memoryType int,
	memory core1_0.DeviceMemory,
	size int,
) {
	if c.Callbacks.Free != nil {
		c.Callbacks.Free(c.Allocator, memoryType, memory, size, c.Callbacks.UserData)
	}
}
