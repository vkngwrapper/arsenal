package vulkan

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/core/v3/common"
	"github.com/vkngwrapper/core/v3/core1_1"
	mock_loader "github.com/vkngwrapper/core/v3/loader/mocks"
	"github.com/vkngwrapper/core/v3/mocks"
	"github.com/vkngwrapper/core/v3/mocks/mocks1_0"
	"github.com/vkngwrapper/core/v3/mocks/mocks1_1"
	"github.com/vkngwrapper/core/v3/mocks/mocks1_2"
	"github.com/vkngwrapper/extensions/v3/amd_device_coherent_memory"
	"github.com/vkngwrapper/extensions/v3/ext_memory_budget"
	"github.com/vkngwrapper/extensions/v3/ext_memory_priority"
	"github.com/vkngwrapper/extensions/v3/khr_bind_memory2"
	mock_bind_memory2 "github.com/vkngwrapper/extensions/v3/khr_bind_memory2/mocks"
	"github.com/vkngwrapper/extensions/v3/khr_buffer_device_address"
	mock_buffer_device_address "github.com/vkngwrapper/extensions/v3/khr_buffer_device_address/mocks"
	"github.com/vkngwrapper/extensions/v3/khr_dedicated_allocation"
	"github.com/vkngwrapper/extensions/v3/khr_external_memory"
	"github.com/vkngwrapper/extensions/v3/khr_get_memory_requirements2"
	mock_get_memory_requirements2 "github.com/vkngwrapper/extensions/v3/khr_get_memory_requirements2/mocks"
	"github.com/vkngwrapper/extensions/v3/khr_get_physical_device_properties2"
	mock_get_physical_device_properties2 "github.com/vkngwrapper/extensions/v3/khr_get_physical_device_properties2/mocks"
	"github.com/vkngwrapper/extensions/v3/khr_maintenance4"
	mock_maintenance4 "github.com/vkngwrapper/extensions/v3/khr_maintenance4/mocks"
	mock_library "github.com/vkngwrapper/extensions/v3/library/mocks"
	"go.uber.org/mock/gomock"
)

func TestExtensionsNew_NoExtensions(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreLoader := mock_loader.NewMockLoader(ctrl)
	instance := mocks.NewDummyInstance(common.Vulkan1_0, []string{})
	device := mocks.NewDummyDevice(common.Vulkan1_0, []string{})
	driver := mocks1_0.InternalCoreDriver(instance, device, coreLoader)

	library := mock_library.NewMockLibrary(ctrl)

	extension := NewExtensionData(driver, library)

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

	coreLoader := mock_loader.NewMockLoader(ctrl)
	instance := mocks.NewDummyInstance(common.Vulkan1_1, []string{})
	device := mocks.NewDummyDevice(common.Vulkan1_1, []string{})
	driver := mocks1_1.InternalCoreDriver(instance, device, coreLoader)

	library := mock_library.NewMockLibrary(ctrl)

	extension := NewExtensionData(driver, library)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:         true,
		ExternalMemory:               true,
		UseMemoryBudget:              false,
		UseAMDDeviceCoherentMemory:   false,
		UseMemoryPriority:            false,
		BindMemory2:                  driver,
		GetMemoryRequirements:        driver,
		GetPhysicalDeviceProperties2: driver.InstanceDriver().(core1_1.CoreInstanceDriver),
	}, extension)
}

func TestExtensionsNew_Core1_2(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreLoader := mock_loader.NewMockLoader(ctrl)
	instance := mocks.NewDummyInstance(common.Vulkan1_2, []string{})
	device := mocks.NewDummyDevice(common.Vulkan1_2, []string{})
	driver := mocks1_2.InternalCoreDriver(instance, device, coreLoader)

	library := mock_library.NewMockLibrary(ctrl)

	extension := NewExtensionData(driver, library)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:         true,
		ExternalMemory:               true,
		UseMemoryBudget:              false,
		UseAMDDeviceCoherentMemory:   false,
		UseMemoryPriority:            false,
		BindMemory2:                  driver,
		GetMemoryRequirements:        driver,
		BufferDeviceAddress:          driver,
		GetPhysicalDeviceProperties2: driver.InstanceDriver().(core1_1.CoreInstanceDriver),
	}, extension)
}

func TestExtensionsNew_SpareExtensions(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreLoader := mock_loader.NewMockLoader(ctrl)
	instance := mocks.NewDummyInstance(common.Vulkan1_0, []string{})
	device := mocks.NewDummyDevice(common.Vulkan1_0,
		[]string{
			ext_memory_priority.ExtensionName,
			amd_device_coherent_memory.ExtensionName,
			khr_maintenance4.ExtensionName,
		})
	driver := mocks1_0.InternalCoreDriver(instance, device, coreLoader)

	library := mock_library.NewMockLibrary(ctrl)
	khrMaint4 := mock_maintenance4.NewMockExtensionDriver(ctrl)
	library.EXPECT().KhrMaintenance4(driver).Return(khrMaint4)
	extension := NewExtensionData(driver, library)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:       false,
		ExternalMemory:             false,
		UseMemoryBudget:            false,
		UseAMDDeviceCoherentMemory: true,
		UseMemoryPriority:          true,
		Maintenance4:               khrMaint4,
	}, extension)
}

func TestExtensionsNew_DedicatedAllocations(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreLoader := mock_loader.NewMockLoader(ctrl)
	instance := mocks.NewDummyInstance(common.Vulkan1_0, []string{})
	device := mocks.NewDummyDevice(common.Vulkan1_0,
		[]string{
			khr_get_memory_requirements2.ExtensionName,
			khr_dedicated_allocation.ExtensionName,
		})
	driver := mocks1_0.InternalCoreDriver(instance, device, coreLoader)
	memReqs := mock_get_memory_requirements2.NewMockExtensionDriver(ctrl)
	library := mock_library.NewMockLibrary(ctrl)
	library.EXPECT().KhrGetMemoryRequirements2(driver).Return(memReqs)

	extension := NewExtensionData(driver, library)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:       true,
		ExternalMemory:             false,
		UseMemoryBudget:            false,
		UseAMDDeviceCoherentMemory: false,
		UseMemoryPriority:          false,
		GetMemoryRequirements:      memReqs,
	}, extension)
}

func TestExtensionsNew_NoDedicatedAllocations(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreLoader := mock_loader.NewMockLoader(ctrl)
	instance := mocks.NewDummyInstance(common.Vulkan1_0, []string{})
	device := mocks.NewDummyDevice(common.Vulkan1_0,
		[]string{
			khr_dedicated_allocation.ExtensionName,
		})
	driver := mocks1_0.InternalCoreDriver(instance, device, coreLoader)

	library := mock_library.NewMockLibrary(ctrl)

	extension := NewExtensionData(driver, library)

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

	coreLoader := mock_loader.NewMockLoader(ctrl)
	instance := mocks.NewDummyInstance(common.Vulkan1_1, []string{})
	device := mocks.NewDummyDevice(common.Vulkan1_1,
		[]string{
			ext_memory_budget.ExtensionName,
		})
	driver := mocks1_1.InternalCoreDriver(instance, device, coreLoader)

	library := mock_library.NewMockLibrary(ctrl)

	extension := NewExtensionData(driver, library)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:         true,
		ExternalMemory:               true,
		UseMemoryBudget:              true,
		UseAMDDeviceCoherentMemory:   false,
		UseMemoryPriority:            false,
		BindMemory2:                  driver.(core1_1.DeviceDriver),
		GetMemoryRequirements:        driver.(core1_1.DeviceDriver),
		GetPhysicalDeviceProperties2: driver.InstanceDriver().(core1_1.CoreInstanceDriver),
	}, extension)
}

func TestExtensionsNew_Extensions_MemoryBudget(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreLoader := mock_loader.NewMockLoader(ctrl)
	instance := mocks.NewDummyInstance(common.Vulkan1_0, []string{
		khr_get_physical_device_properties2.ExtensionName,
	})
	device := mocks.NewDummyDevice(common.Vulkan1_0,
		[]string{
			ext_memory_budget.ExtensionName,
		})
	driver := mocks1_0.InternalCoreDriver(instance, device, coreLoader)
	properties2 := mock_get_physical_device_properties2.NewMockExtensionDriver(ctrl)
	library := mock_library.NewMockLibrary(ctrl)
	library.EXPECT().KhrGetPhysicalDeviceProperties2(driver.InstanceDriver()).Return(properties2)

	extension := NewExtensionData(driver, library)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:         false,
		ExternalMemory:               false,
		UseMemoryBudget:              true,
		UseAMDDeviceCoherentMemory:   false,
		UseMemoryPriority:            false,
		GetPhysicalDeviceProperties2: properties2,
	}, extension)
}

func TestExtensionsNew_NoMemoryBudget(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreLoader := mock_loader.NewMockLoader(ctrl)
	instance := mocks.NewDummyInstance(common.Vulkan1_0, []string{})
	device := mocks.NewDummyDevice(common.Vulkan1_0,
		[]string{
			ext_memory_budget.ExtensionName,
		})
	driver := mocks1_0.InternalCoreDriver(instance, device, coreLoader)
	library := mock_library.NewMockLibrary(ctrl)

	extension := NewExtensionData(driver, library)

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

	coreLoader := mock_loader.NewMockLoader(ctrl)
	instance := mocks.NewDummyInstance(common.Vulkan1_1, []string{})
	device := mocks.NewDummyDevice(common.Vulkan1_1,
		[]string{
			khr_buffer_device_address.ExtensionName,
		})
	driver := mocks1_1.InternalCoreDriver(instance, device, coreLoader)
	library := mock_library.NewMockLibrary(ctrl)
	bufferDeviceAddress := mock_buffer_device_address.NewMockExtensionDriver(ctrl)
	library.EXPECT().KhrBufferDeviceAddress(driver).Return(bufferDeviceAddress)

	extension := NewExtensionData(driver, library)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:         true,
		ExternalMemory:               true,
		UseMemoryBudget:              false,
		UseAMDDeviceCoherentMemory:   false,
		UseMemoryPriority:            false,
		BindMemory2:                  driver,
		GetMemoryRequirements:        driver,
		GetPhysicalDeviceProperties2: driver.InstanceDriver().(core1_1.CoreInstanceDriver),
		BufferDeviceAddress:          bufferDeviceAddress,
	}, extension)
}

func TestExtensionsNew_Extensions_Core11(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	coreLoader := mock_loader.NewMockLoader(ctrl)
	instance := mocks.NewDummyInstance(common.Vulkan1_0, []string{})
	device := mocks.NewDummyDevice(common.Vulkan1_0,
		[]string{
			khr_bind_memory2.ExtensionName,
			khr_external_memory.ExtensionName,
		})
	driver := mocks1_0.InternalCoreDriver(instance, device, coreLoader)
	bindMemory := mock_bind_memory2.NewMockExtensionDriver(ctrl)
	library := mock_library.NewMockLibrary(ctrl)
	library.EXPECT().KhrBindMemory2(driver).Return(bindMemory)

	extension := NewExtensionData(driver, library)

	require.Equal(t, &ExtensionData{
		DedicatedAllocations:       false,
		ExternalMemory:             true,
		UseMemoryBudget:            false,
		UseAMDDeviceCoherentMemory: false,
		UseMemoryPriority:          false,
		BindMemory2:                bindMemory,
	}, extension)
}
