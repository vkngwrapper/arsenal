package vulkan

import (
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/core1_1"
	"github.com/vkngwrapper/core/v2/core1_2"
	"github.com/vkngwrapper/extensions/v2/amd_device_coherent_memory"
	"github.com/vkngwrapper/extensions/v2/ext_memory_budget"
	"github.com/vkngwrapper/extensions/v2/ext_memory_priority"
	"github.com/vkngwrapper/extensions/v2/khr_bind_memory2"
	khr_bind_memory2_shim "github.com/vkngwrapper/extensions/v2/khr_bind_memory2/shim"
	"github.com/vkngwrapper/extensions/v2/khr_buffer_device_address"
	khr_buffer_device_address_shim "github.com/vkngwrapper/extensions/v2/khr_buffer_device_address/shim"
	"github.com/vkngwrapper/extensions/v2/khr_dedicated_allocation"
	"github.com/vkngwrapper/extensions/v2/khr_external_memory"
	"github.com/vkngwrapper/extensions/v2/khr_get_memory_requirements2"
	khr_get_memory_requirements2_shim "github.com/vkngwrapper/extensions/v2/khr_get_memory_requirements2/shim"
	"github.com/vkngwrapper/extensions/v2/khr_get_physical_device_properties2"
	khr_get_physical_device_properties2_shim "github.com/vkngwrapper/extensions/v2/khr_get_physical_device_properties2/shim"
	"github.com/vkngwrapper/extensions/v2/khr_maintenance4"
)

type ExtensionData struct {
	DedicatedAllocations         bool
	ExternalMemory               bool
	GetMemoryRequirements        khr_get_memory_requirements2_shim.Shim
	BindMemory2                  khr_bind_memory2_shim.Shim
	BufferDeviceAddress          khr_buffer_device_address_shim.Shim
	GetPhysicalDeviceProperties2 khr_get_physical_device_properties2_shim.Shim
	UseMemoryBudget              bool
	UseAMDDeviceCoherentMemory   bool
	UseMemoryPriority            bool
	Maintenance4                 khr_maintenance4.Extension
}

func NewExtensionData(device core1_0.Device, physicalDevice core1_0.PhysicalDevice, instance core1_0.Instance) *ExtensionData {
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

	physicalDevice11 := core1_1.PromoteInstanceScopedPhysicalDevice(physicalDevice)
	if physicalDevice11 != nil {
		// Core 1.1 active on the instance side - that means we can use khr_get_physical_device_properties2
		data.GetPhysicalDeviceProperties2 = physicalDevice11
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

	// khr_get_physical_device_properties2 if core 1.1 is not active
	if data.GetPhysicalDeviceProperties2 == nil && instance.IsInstanceExtensionActive(khr_get_physical_device_properties2.ExtensionName) {
		extension := khr_get_physical_device_properties2.CreateExtensionFromInstance(instance)
		data.GetPhysicalDeviceProperties2 = khr_get_physical_device_properties2_shim.NewShim(extension, physicalDevice)
	}

	// ext_memory_budget
	if data.GetPhysicalDeviceProperties2 != nil && device.IsDeviceExtensionActive(ext_memory_budget.ExtensionName) {
		data.UseMemoryBudget = true
	}

	// ext_memory_priority
	if device.IsDeviceExtensionActive(ext_memory_priority.ExtensionName) {
		data.UseMemoryPriority = true
	}

	// amd_device_coherent_memory
	if device.IsDeviceExtensionActive(amd_device_coherent_memory.ExtensionName) {
		data.UseAMDDeviceCoherentMemory = true
	}

	// khr_maintenance4
	if device.IsDeviceExtensionActive(khr_maintenance4.ExtensionName) {
		data.Maintenance4 = khr_maintenance4.CreateExtensionFromDevice(device)
	}

	return data
}
