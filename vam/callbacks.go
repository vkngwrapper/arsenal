package vam

import "github.com/vkngwrapper/core/v2/core1_0"

type AllocateDeviceMemoryCallback func(
	allocator Allocator,
	memoryType int,
	memory core1_0.DeviceMemory,
	size int,
	userData interface{},
)

type FreeDeviceMemoryCallback func(
	allocator Allocator,
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
