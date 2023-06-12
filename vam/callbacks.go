package vam

import "github.com/vkngwrapper/core/v2/core1_0"

// AllocateDeviceMemoryCallback is a callback that fires when new device memory is allocated from Vulkan.
// It does not fire with every new Allocation object, since device memory is frequently reused for block
// allocations.
type AllocateDeviceMemoryCallback func(
	allocator *Allocator,
	memoryType int,
	memory core1_0.DeviceMemory,
	size int,
	userData interface{},
)

// FreeDeviceMemoryCallback is a callback that fires when device memory is freed from Vulkan. It does
// not fire with every call to Allocation.Free(), since device memory is frequently reused for block allocations.
type FreeDeviceMemoryCallback func(
	allocator *Allocator,
	memoryType int,
	memory core1_0.DeviceMemory,
	size int,
	userData interface{},
)

// MemoryCallbackOptions allows optional Allocate and Free callbacks to be fired from an Allocator.
// The UserData field indicates the userData parameter that will be passed to Allocate and Free
// callbacks.
type MemoryCallbackOptions struct {
	Allocate AllocateDeviceMemoryCallback
	Free     FreeDeviceMemoryCallback
	UserData interface{}
}

type memoryCallbacks struct {
	Callbacks *MemoryCallbackOptions
	Allocator *Allocator
}

func (c *memoryCallbacks) Allocate(
	memoryType int,
	memory core1_0.DeviceMemory,
	size int,
) {
	if c.Callbacks != nil && c.Callbacks.Allocate != nil {
		c.Callbacks.Allocate(c.Allocator, memoryType, memory, size, c.Callbacks.UserData)
	}
}

func (c *memoryCallbacks) Free(
	memoryType int,
	memory core1_0.DeviceMemory,
	size int,
) {
	if c.Callbacks != nil && c.Callbacks.Free != nil {
		c.Callbacks.Free(c.Allocator, memoryType, memory, size, c.Callbacks.UserData)
	}
}
