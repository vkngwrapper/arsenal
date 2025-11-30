package vulkan

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/mocks"
	"github.com/vkngwrapper/extensions/v2/amd_device_coherent_memory"
	"github.com/vkngwrapper/extensions/v2/ext_memory_budget"
	"github.com/vkngwrapper/extensions/v2/ext_memory_priority"
	"github.com/vkngwrapper/extensions/v2/khr_bind_memory2"
	"github.com/vkngwrapper/extensions/v2/khr_buffer_device_address"
	"github.com/vkngwrapper/extensions/v2/khr_dedicated_allocation"
	"github.com/vkngwrapper/extensions/v2/khr_external_memory"
	"github.com/vkngwrapper/extensions/v2/khr_get_memory_requirements2"
	"github.com/vkngwrapper/extensions/v2/khr_get_physical_device_properties2"
	"github.com/vkngwrapper/extensions/v2/khr_maintenance4"
	"go.uber.org/mock/gomock"
)

func TestExtensionsNew_NoExtensions(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	instance, physicalDevice, device := mocks.MockRig1_0(ctrl, common.Vulkan1_0, []string{}, []string{})

	extension := NewExtensionData(device, physicalDevice, instance)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:       false,
		ExternalMemory:             false,
		UseMemoryBudget:            false,
		UseAMDDeviceCoherentMemory: false,
		UseMemoryPriority:          false,
	}, extension)
}

func TestExtensionsNew_Core1_1(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	instance, physicalDevice, device := mocks.MockRig1_1(ctrl, common.Vulkan1_1, []string{}, []string{})

	extension := NewExtensionData(device, physicalDevice, instance)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:         true,
		ExternalMemory:               true,
		UseMemoryBudget:              false,
		UseAMDDeviceCoherentMemory:   false,
		UseMemoryPriority:            false,
		BindMemory2:                  device,
		GetMemoryRequirements:        device,
		GetPhysicalDeviceProperties2: physicalDevice.InstanceScopedPhysicalDevice1_1(),
	}, extension)
}

func TestExtensionsNew_Core1_2(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	instance, physicalDevice, device := mocks.MockRig1_2(ctrl, common.Vulkan1_2, []string{}, []string{})

	extension := NewExtensionData(device, physicalDevice, instance)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:         true,
		ExternalMemory:               true,
		UseMemoryBudget:              false,
		UseAMDDeviceCoherentMemory:   false,
		UseMemoryPriority:            false,
		BindMemory2:                  device,
		GetMemoryRequirements:        device,
		BufferDeviceAddress:          device,
		GetPhysicalDeviceProperties2: physicalDevice.InstanceScopedPhysicalDevice1_1(),
	}, extension)
}

func TestExtensionsNew_SpareExtensions(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	instance, physicalDevice, device := mocks.MockRig1_0(ctrl, common.Vulkan1_0, []string{},
		[]string{
			ext_memory_priority.ExtensionName,
			amd_device_coherent_memory.ExtensionName,
			khr_maintenance4.ExtensionName,
		})

	extension := NewExtensionData(device, physicalDevice, instance)

	require.NotNil(t, extension.Maintenance4)
	extension.Maintenance4 = nil

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:       false,
		ExternalMemory:             false,
		UseMemoryBudget:            false,
		UseAMDDeviceCoherentMemory: true,
		UseMemoryPriority:          true,
	}, extension)
}

func TestExtensionsNew_DedicatedAllocations(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	instance, physicalDevice, device := mocks.MockRig1_0(ctrl, common.Vulkan1_0, []string{},
		[]string{
			khr_get_memory_requirements2.ExtensionName,
			khr_dedicated_allocation.ExtensionName,
		})

	extension := NewExtensionData(device, physicalDevice, instance)

	require.NotNil(t, extension.GetMemoryRequirements)
	extension.GetMemoryRequirements = nil

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:       true,
		ExternalMemory:             false,
		UseMemoryBudget:            false,
		UseAMDDeviceCoherentMemory: false,
		UseMemoryPriority:          false,
	}, extension)
}

func TestExtensionsNew_NoDedicatedAllocations(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	instance, physicalDevice, device := mocks.MockRig1_0(ctrl, common.Vulkan1_0, []string{},
		[]string{
			khr_dedicated_allocation.ExtensionName,
		})

	extension := NewExtensionData(device, physicalDevice, instance)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:       false,
		ExternalMemory:             false,
		UseMemoryBudget:            false,
		UseAMDDeviceCoherentMemory: false,
		UseMemoryPriority:          false,
	}, extension)
}

func TestExtensionsNew_Core11_MemoryBudget(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	instance, physicalDevice, device := mocks.MockRig1_1(ctrl, common.Vulkan1_1, []string{},
		[]string{
			ext_memory_budget.ExtensionName,
		})

	extension := NewExtensionData(device, physicalDevice, instance)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:         true,
		ExternalMemory:               true,
		UseMemoryBudget:              true,
		UseAMDDeviceCoherentMemory:   false,
		UseMemoryPriority:            false,
		BindMemory2:                  device,
		GetMemoryRequirements:        device,
		GetPhysicalDeviceProperties2: physicalDevice.InstanceScopedPhysicalDevice1_1(),
	}, extension)
}

func TestExtensionsNew_Extensions_MemoryBudget(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	instance, physicalDevice, device := mocks.MockRig1_0(ctrl, common.Vulkan1_0,
		[]string{
			khr_get_physical_device_properties2.ExtensionName,
		},
		[]string{
			ext_memory_budget.ExtensionName,
		})

	extension := NewExtensionData(device, physicalDevice, instance)

	require.NotNil(t, extension.GetPhysicalDeviceProperties2)
	extension.GetPhysicalDeviceProperties2 = nil

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:       false,
		ExternalMemory:             false,
		UseMemoryBudget:            true,
		UseAMDDeviceCoherentMemory: false,
		UseMemoryPriority:          false,
	}, extension)
}

func TestExtensionsNew_NoMemoryBudget(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	instance, physicalDevice, device := mocks.MockRig1_0(ctrl, common.Vulkan1_0,
		[]string{},
		[]string{
			ext_memory_budget.ExtensionName,
		})

	extension := NewExtensionData(device, physicalDevice, instance)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:       false,
		ExternalMemory:             false,
		UseMemoryBudget:            false,
		UseAMDDeviceCoherentMemory: false,
		UseMemoryPriority:          false,
	}, extension)
}

func TestExtensionsNew_BufferDeviceAddress(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	instance, physicalDevice, device := mocks.MockRig1_1(ctrl, common.Vulkan1_1, []string{}, []string{
		khr_buffer_device_address.ExtensionName,
	})

	extension := NewExtensionData(device, physicalDevice, instance)

	require.NotNil(t, extension.BufferDeviceAddress)
	extension.BufferDeviceAddress = nil

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:         true,
		ExternalMemory:               true,
		UseMemoryBudget:              false,
		UseAMDDeviceCoherentMemory:   false,
		UseMemoryPriority:            false,
		BindMemory2:                  device,
		GetMemoryRequirements:        device,
		GetPhysicalDeviceProperties2: physicalDevice.InstanceScopedPhysicalDevice1_1(),
	}, extension)
}

func TestExtensionsNew_Extensions_Core11(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	instance, physicalDevice, device := mocks.MockRig1_0(ctrl, common.Vulkan1_0,
		[]string{},
		[]string{
			khr_bind_memory2.ExtensionName,
			khr_external_memory.ExtensionName,
		})

	extension := NewExtensionData(device, physicalDevice, instance)
	require.NotNil(t, extension.BindMemory2)
	extension.BindMemory2 = nil

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:       false,
		ExternalMemory:             true,
		UseMemoryBudget:            false,
		UseAMDDeviceCoherentMemory: false,
		UseMemoryPriority:          false,
	}, extension)
}
