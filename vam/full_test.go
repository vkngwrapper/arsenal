package vam

import (
	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/core/v2"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/extensions/v2/khr_portability_enumeration"
	"github.com/vkngwrapper/extensions/v2/khr_portability_subset"
	"golang.org/x/exp/slog"
	"log"
	"os"
	"testing"
)

func createApplication(t require.TestingT, name string) (core1_0.Instance, core1_0.PhysicalDevice, core1_0.Device) {
	loader, err := core.CreateSystemLoader()
	if err != nil {
		log.Fatalln(err)
	}

	instanceExtensions, _, err := loader.AvailableExtensions()
	require.NoError(t, err)

	var instanceExtensionNames []string
	var flags core1_0.InstanceCreateFlags
	_, ok := instanceExtensions[khr_portability_enumeration.ExtensionName]
	if ok {
		instanceExtensionNames = append(instanceExtensionNames, khr_portability_enumeration.ExtensionName)
		flags |= khr_portability_enumeration.InstanceCreateEnumeratePortability
	}

	instance, _, err := loader.CreateInstance(nil, core1_0.InstanceCreateInfo{
		ApplicationName:       name,
		ApplicationVersion:    common.CreateVersion(1, 0, 0),
		EngineName:            "go test",
		EngineVersion:         common.CreateVersion(1, 0, 0),
		APIVersion:            common.Vulkan1_0,
		EnabledExtensionNames: instanceExtensionNames,
		Flags:                 flags,
	})
	require.NoError(t, err)

	gpus, _, err := instance.EnumeratePhysicalDevices()
	require.NoError(t, err)

	physDevice := gpus[0]

	graphicsFamily := -1
	queueProps := physDevice.QueueFamilyProperties()
	for queueIndex, queueFamily := range queueProps {
		if queueFamily.QueueFlags&core1_0.QueueGraphics != 0 {
			graphicsFamily = queueIndex
			break
		}
	}
	require.GreaterOrEqual(t, graphicsFamily, 0)

	var deviceExtensionNames []string
	deviceExtensions, _, err := physDevice.EnumerateDeviceExtensionProperties()
	require.NoError(t, err)

	_, ok = deviceExtensions[khr_portability_subset.ExtensionName]
	if ok {
		deviceExtensionNames = append(deviceExtensionNames, khr_portability_subset.ExtensionName)
	}

	device, _, err := physDevice.CreateDevice(nil, core1_0.DeviceCreateInfo{
		QueueCreateInfos: []core1_0.DeviceQueueCreateInfo{
			{
				QueueFamilyIndex: graphicsFamily,
				QueuePriorities:  []float32{0.0},
			},
		},
		EnabledExtensionNames: deviceExtensionNames,
	})
	require.NoError(t, err)

	return instance, physDevice, device
}

func destroyApplication(t require.TestingT, instance core1_0.Instance, device core1_0.Device) {
	_, err := device.WaitIdle()
	require.NoError(t, err)

	device.Destroy(nil)
	instance.Destroy(nil)
}

func BenchmarkCreateAllocation(b *testing.B) {
	instance, physDevice, device := createApplication(b, "BenchmarkCreateAllocator")
	defer destroyApplication(b, instance, device)

	logger := slog.New(slog.NewTextHandler(os.Stdout))

	allocator, err := New(logger, instance, physDevice, device, CreateOptions{})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, allocator.Destroy())
	}()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		memReqs := core1_0.MemoryRequirements{
			Size:           100,
			Alignment:      1,
			MemoryTypeBits: 0xffffffff,
		}
		var alloc Allocation
		_, err = allocator.AllocateMemory(&memReqs, AllocationCreateInfo{}, &alloc)
		require.NoError(b, err)

		require.NoError(b, alloc.Free())
	}
}
