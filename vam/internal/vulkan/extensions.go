package vulkan

import (
	"github.com/vkngwrapper/core/v3/core1_0"
	"github.com/vkngwrapper/core/v3/core1_1"
	"github.com/vkngwrapper/core/v3/core1_2"
	"github.com/vkngwrapper/extensions/v3/amd_device_coherent_memory"
	"github.com/vkngwrapper/extensions/v3/ext_memory_budget"
	"github.com/vkngwrapper/extensions/v3/ext_memory_priority"
	"github.com/vkngwrapper/extensions/v3/khr_bind_memory2"
	"github.com/vkngwrapper/extensions/v3/khr_buffer_device_address"
	"github.com/vkngwrapper/extensions/v3/khr_dedicated_allocation"
	"github.com/vkngwrapper/extensions/v3/khr_external_memory"
	"github.com/vkngwrapper/extensions/v3/khr_get_memory_requirements2"
	"github.com/vkngwrapper/extensions/v3/khr_get_physical_device_properties2"
	"github.com/vkngwrapper/extensions/v3/khr_maintenance4"
	"github.com/vkngwrapper/extensions/v3/library"
)

type ExtensionData struct {
	DedicatedAllocations         bool
	ExternalMemory               bool
	GetMemoryRequirements        khr_get_memory_requirements2.ExtensionDriver
	BindMemory2                  khr_bind_memory2.ExtensionDriver
	BufferDeviceAddress          khr_buffer_device_address.ExtensionDriver
	GetPhysicalDeviceProperties2 khr_get_physical_device_properties2.ExtensionDriver
	UseMemoryBudget              bool
	UseAMDDeviceCoherentMemory   bool
	UseMemoryPriority            bool
	Maintenance4                 khr_maintenance4.ExtensionDriver
}

func NewExtensionData(driver core1_0.CoreDeviceDriver, extensionLibrary library.Library) *ExtensionData {
	data := &ExtensionData{}

	// Apply device capabilities- add core or extension capabilities to the allocator
	driver11, ok := driver.(core1_1.DeviceDriver)
	if ok {
		// Core 1.1 active - that means we can use khr_get_memory_requirements2, khr_bind_memory2,
		// khr_dedicated_allocation, and khr_external_memory
		data.DedicatedAllocations = true
		data.ExternalMemory = true
		data.BindMemory2 = driver11
		data.GetMemoryRequirements = driver11
	}

	driver12, ok := driver.(core1_2.DeviceDriver)
	if ok {
		// Core 1.2 active - that means we can use khr_buffer_device_address
		data.BufferDeviceAddress = driver12
	}

	instanceDriver11, ok := driver.InstanceDriver().(core1_1.CoreInstanceDriver)
	if ok {
		// Core 1.1 active on the instance side - that means we can use khr_get_physical_device_properties2
		data.GetPhysicalDeviceProperties2 = instanceDriver11
	}

	device := driver.Device()
	instance := driver.InstanceDriver().Instance()

	// khr_bind_memory2 if core 1.1 is not active
	if data.BindMemory2 == nil && device.IsDeviceExtensionActive(khr_bind_memory2.ExtensionName) {
		data.BindMemory2 = extensionLibrary.KhrBindMemory2(driver)
	}

	// khr_get_memory_requirements2 if core 1.1 is not active
	if data.GetMemoryRequirements == nil && device.IsDeviceExtensionActive(khr_get_memory_requirements2.ExtensionName) {
		data.GetMemoryRequirements = extensionLibrary.KhrGetMemoryRequirements2(driver)
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
		data.BufferDeviceAddress = extensionLibrary.KhrBufferDeviceAddress(driver)
	}

	// khr_get_physical_device_properties2 if core 1.1 is not active
	if data.GetPhysicalDeviceProperties2 == nil && instance.IsInstanceExtensionActive(khr_get_physical_device_properties2.ExtensionName) {
		data.GetPhysicalDeviceProperties2 = extensionLibrary.KhrGetPhysicalDeviceProperties2(driver.InstanceDriver())
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
		data.Maintenance4 = extensionLibrary.KhrMaintenance4(driver)
	}

	return data
}
