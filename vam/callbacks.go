package vam

import "github.com/vkngwrapper/core/v2/core1_0"

type AllocateDeviceMemoryCallback func(
	allocator *Allocator,
	memoryType int,
	memory core1_0.DeviceMemory,
	size int,
	userData interface{},
)

type FreeDeviceMemoryCallback func(
	allocator *Allocator,
	memoryType int,
	memory core1_0.DeviceMemory,
	size int,
	userData interface{},
)

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
