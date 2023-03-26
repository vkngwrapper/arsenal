package vulkan

import (
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/core1_1"
	"github.com/vkngwrapper/core/v2/core1_2"
	"github.com/vkngwrapper/extensions/v2/khr_bind_memory2"
	khr_bind_memory2_shim "github.com/vkngwrapper/extensions/v2/khr_bind_memory2/shim"
	"github.com/vkngwrapper/extensions/v2/khr_buffer_device_address"
	khr_buffer_device_address_shim "github.com/vkngwrapper/extensions/v2/khr_buffer_device_address/shim"
	"github.com/vkngwrapper/extensions/v2/khr_dedicated_allocation"
	"github.com/vkngwrapper/extensions/v2/khr_external_memory"
	"github.com/vkngwrapper/extensions/v2/khr_get_memory_requirements2"
	khr_get_memory_requirements2_shim "github.com/vkngwrapper/extensions/v2/khr_get_memory_requirements2/shim"
)

type ExtensionData struct {
	DedicatedAllocations  bool
	ExternalMemory        bool
	GetMemoryRequirements khr_get_memory_requirements2_shim.Shim
	BindMemory2           khr_bind_memory2_shim.Shim
	BufferDeviceAddress   khr_buffer_device_address_shim.Shim
	// TODO
	//useMemoryBudget bool
	//useAMDDeviceCoherentMemory bool
	//useMemoryPriority bool
	// TODO External memory
}

func NewExtensionData(device core1_0.Device) *ExtensionData {
	data := &ExtensionData{}

	// Apply device capabilities- add core or extension capabilities to the allocator
	device11 := core1_1.PromoteDevice(device)
	if device11 != nil {
		// Core 1.1 active - that means we can use khr_get_memory_requirements2, khr_bind_memory2,
		// khr_dedicated_allocation, and khr_external_memory
		data.DedicatedAllocations = true
		data.ExternalMemory = true
		data.BindMemory2 = device11
		data.GetMemoryRequirements = device11
	}

	device12 := core1_2.PromoteDevice(device)
	if device12 != nil {
		// Core 1.2 active - that means we can use khr_buffer_device_address
		data.BufferDeviceAddress = device12
	}

	// khr_bind_memory2 if core 1.1 is not active
	if data.BindMemory2 == nil && device.IsDeviceExtensionActive(khr_bind_memory2.ExtensionName) {
		extension := khr_bind_memory2.CreateExtensionFromDevice(device)
		data.BindMemory2 = khr_bind_memory2_shim.NewShim(device, extension)
	}

	// khr_get_memory_requirements2 if core 1.1 is not active
	if data.GetMemoryRequirements == nil && device.IsDeviceExtensionActive(khr_get_memory_requirements2.ExtensionName) {
		extension := khr_get_memory_requirements2.CreateExtensionFromDevice(device)
		data.GetMemoryRequirements = khr_get_memory_requirements2_shim.NewShim(extension, device)
	}

	// khr_dedicated_allocation if khr_get_memory_requirements is active but core 1.1 is not
	if data.GetMemoryRequirements != nil && !data.DedicatedAllocations &&
		device.IsDeviceExtensionActive(khr_dedicated_allocation.ExtensionName) {
		data.DedicatedAllocations = true
	}

	// khr_external_memory if core 1.1 is not active
	if !data.ExternalMemory && device.IsDeviceExtensionActive(khr_external_memory.ExtensionName) {
		data.ExternalMemory = true
	}

	// khr_buffer_device_address if core 1.2 is not active
	if data.BufferDeviceAddress == nil && device.IsDeviceExtensionActive(khr_buffer_device_address.ExtensionName) {
		extension := khr_buffer_device_address.CreateExtensionFromDevice(device)
		data.BufferDeviceAddress = khr_buffer_device_address_shim.NewShim(extension, device)
	}

	return data
}
