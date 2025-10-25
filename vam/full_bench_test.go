package vam

import (
	"log"
	"os"
	"runtime"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/defrag"
	"github.com/vkngwrapper/core/v2"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/extensions/v2/ext_debug_utils"
	"github.com/vkngwrapper/extensions/v2/khr_portability_enumeration"
	"github.com/vkngwrapper/extensions/v2/khr_portability_subset"
	"golang.org/x/exp/slog"
)

func logDebug(msgType ext_debug_utils.DebugUtilsMessageTypeFlags, severity ext_debug_utils.DebugUtilsMessageSeverityFlags, data *ext_debug_utils.DebugUtilsMessengerCallbackData) bool {
	log.Printf("[%s %s] - %s", severity, msgType, data.Message)
	return false
}

func createApplication(t require.TestingT, name string) (core1_0.Instance, ext_debug_utils.DebugUtilsMessenger, core1_0.PhysicalDevice, core1_0.Device) {
	// Because benchmarks that use createApplication and destroyApplication will repeatedly create and
	// destroy instance & device, it runs into https://github.com/golang/go/issues/59724 on windows,
	// so we need to lock the OS as a workaround
	runtime.LockOSThread()

	loader, err := core.CreateSystemLoader()
	if err != nil {
		log.Fatalln(err)
	}

	instanceExtensions, _, err := loader.AvailableExtensions()
	require.NoError(t, err)

	instanceExtensionNames := []string{ext_debug_utils.ExtensionName}
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
		NextOptions: common.NextOptions{Next: ext_debug_utils.DebugUtilsMessengerCreateInfo{
			MessageSeverity: ext_debug_utils.SeverityError | ext_debug_utils.SeverityWarning,
			MessageType:     ext_debug_utils.TypeGeneral | ext_debug_utils.TypeValidation | ext_debug_utils.TypePerformance,
			UserCallback:    logDebug,
		}},
	})
	require.NoError(t, err)

	debugLoader := ext_debug_utils.CreateExtensionFromInstance(instance)
	debugMessenger, _, err := debugLoader.CreateDebugUtilsMessenger(instance, nil, ext_debug_utils.DebugUtilsMessengerCreateInfo{
		MessageSeverity: ext_debug_utils.SeverityError | ext_debug_utils.SeverityWarning,
		MessageType:     ext_debug_utils.TypeGeneral | ext_debug_utils.TypeValidation | ext_debug_utils.TypePerformance,
		UserCallback:    logDebug,
	})

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

	return instance, debugMessenger, physDevice, device
}

func destroyApplication(t require.TestingT, instance core1_0.Instance, debugMessenger ext_debug_utils.DebugUtilsMessenger, device core1_0.Device) {
	_, err := device.WaitIdle()
	require.NoError(t, err)

	device.Destroy(nil)
	debugMessenger.Destroy(nil)
	instance.Destroy(nil)

	runtime.UnlockOSThread()
}

func checkCorruption(t require.TestingT, allocator *Allocator) {
	res, err := allocator.CheckCorruption(0xffffffff)
	if memutils.DebugMargin > 0 {
		require.NoError(t, err)
	} else {
		require.Equal(t, core1_0.VKErrorFeatureNotPresent, res)
	}
}

func BenchmarkCreateAllocation(b *testing.B) {
	instance, debugMessenger, physDevice, device := createApplication(b, "BenchmarkCreateAllocation")
	defer destroyApplication(b, instance, debugMessenger, device)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allocator, err := New(logger, instance, physDevice, device, CreateOptions{})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, allocator.Destroy())
	}()
	var alloc Allocation

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		memReqs := core1_0.MemoryRequirements{
			Size:           100,
			Alignment:      1,
			MemoryTypeBits: 0xffffffff,
		}

		_, err = allocator.AllocateMemory(&memReqs, AllocationCreateInfo{}, &alloc)
		require.NoError(b, err)

		require.NoError(b, alloc.Free())
	}
	b.StopTimer()
	checkCorruption(b, allocator)
}

func BenchmarkCreateAllocationSlice(b *testing.B) {
	instance, debugMessenger, physDevice, device := createApplication(b, "BenchmarkCreateAllocationSlice")
	defer destroyApplication(b, instance, debugMessenger, device)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allocator, err := New(logger, instance, physDevice, device, CreateOptions{})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, allocator.Destroy())
	}()

	slice := make([]Allocation, 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		memReqs := core1_0.MemoryRequirements{
			Size:           100000,
			Alignment:      1,
			MemoryTypeBits: 0xffffffff,
		}

		_, err = allocator.AllocateMemorySlice(&memReqs, AllocationCreateInfo{}, slice)
		require.NoError(b, err)

		require.NoError(b, allocator.FreeAllocationSlice(slice))
	}
	b.StopTimer()
	checkCorruption(b, allocator)
}

func BenchmarkMapAlloc(b *testing.B) {
	instance, debugMessenger, physDevice, device := createApplication(b, "BenchmarkMapAlloc")
	defer destroyApplication(b, instance, debugMessenger, device)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allocator, err := New(logger, instance, physDevice, device, CreateOptions{})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, allocator.Destroy())
	}()

	var alloc Allocation
	memReqs := core1_0.MemoryRequirements{
		Size:           100000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}

	_, err = allocator.AllocateMemory(&memReqs, AllocationCreateInfo{
		Usage: MemoryUsageAuto,
		Flags: AllocationCreateHostAccessRandom,
	}, &alloc)
	require.NoError(b, err)

	defer func() {
		require.NoError(b, alloc.Free())
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ptr, _, err := alloc.Map()
		require.NoError(b, err)

		slice := ([]byte)(unsafe.Slice((*byte)(ptr), alloc.Size()))

		for i := 0; i < len(slice); i++ {
			slice[i] = 1
		}

		err = alloc.Unmap()
		require.NoError(b, err)
	}
	b.StopTimer()
	checkCorruption(b, allocator)
}

func BenchmarkPoolAlloc(b *testing.B) {
	instance, debugMessenger, physDevice, device := createApplication(b, "BenchmarkPoolAlloc")
	defer destroyApplication(b, instance, debugMessenger, device)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allocator, err := New(logger, instance, physDevice, device, CreateOptions{})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, allocator.Destroy())
	}()

	index, _, err := allocator.FindMemoryTypeIndex(0xffffffff, AllocationCreateInfo{
		RequiredFlags: core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent,
	})
	require.NoError(b, err)

	pool, _, err := allocator.CreatePool(PoolCreateInfo{
		MemoryTypeIndex: index,
		Flags:           PoolCreateLinearAlgorithm,
		BlockSize:       1000000,
	})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, pool.Destroy())
	}()

	var alloc Allocation

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		memReqs := core1_0.MemoryRequirements{
			Size:           100,
			Alignment:      1,
			MemoryTypeBits: 0xffffffff,
		}

		_, err = allocator.AllocateMemory(&memReqs, AllocationCreateInfo{
			Pool: pool,
		}, &alloc)
		require.NoError(b, err)

		require.NoError(b, alloc.Free())
	}
	b.StopTimer()
	res, err := pool.CheckCorruption()
	if memutils.DebugMargin > 0 {
		require.NoError(b, err)
	} else {
		require.Equal(b, core1_0.VKErrorFeatureNotPresent, res)
	}
	checkCorruption(b, allocator)
}

func BenchmarkAllocator_BuildStatsString(b *testing.B) {
	instance, debugMessenger, physDevice, device := createApplication(b, "BenchmarkPoolAlloc")
	defer destroyApplication(b, instance, debugMessenger, device)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allocator, err := New(logger, instance, physDevice, device, CreateOptions{})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, allocator.Destroy())
	}()

	index, _, err := allocator.FindMemoryTypeIndex(0xffffffff, AllocationCreateInfo{
		RequiredFlags: core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent,
	})
	require.NoError(b, err)

	pool, _, err := allocator.CreatePool(PoolCreateInfo{
		MemoryTypeIndex: index,
		Flags:           PoolCreateLinearAlgorithm,
		BlockSize:       1000000,
	})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, pool.Destroy())
	}()

	var alloc Allocation

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		memReqs := core1_0.MemoryRequirements{
			Size:           100,
			Alignment:      1,
			MemoryTypeBits: 0xffffffff,
		}

		_, err = allocator.AllocateMemory(&memReqs, AllocationCreateInfo{
			Pool: pool,
		}, &alloc)
		require.NoError(b, err)

		str := allocator.BuildStatsString(true)
		require.NotEmpty(b, str)

		require.NoError(b, alloc.Free())
	}
	b.StopTimer()
	res, err := pool.CheckCorruption()
	if memutils.DebugMargin > 0 {
		require.NoError(b, err)
	} else {
		require.Equal(b, core1_0.VKErrorFeatureNotPresent, res)
	}
	checkCorruption(b, allocator)
}

func BenchmarkPoolAllocSlice(b *testing.B) {
	instance, debugMessenger, physDevice, device := createApplication(b, "BenchmarkPoolAllocSlice")
	defer destroyApplication(b, instance, debugMessenger, device)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allocator, err := New(logger, instance, physDevice, device, CreateOptions{})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, allocator.Destroy())
	}()

	pool, _, err := allocator.CreatePool(PoolCreateInfo{
		Flags:     PoolCreateLinearAlgorithm,
		BlockSize: 1000000,
	})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, pool.Destroy())
	}()

	slice := make([]Allocation, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		memReqs := core1_0.MemoryRequirements{
			Size:           100,
			Alignment:      1,
			MemoryTypeBits: 0xffffffff,
		}

		_, err = allocator.AllocateMemorySlice(&memReqs, AllocationCreateInfo{
			Pool: pool,
		}, slice)
		require.NoError(b, err)

		require.NoError(b, allocator.FreeAllocationSlice(slice))
	}
	b.StopTimer()
	checkCorruption(b, allocator)
}

func BenchmarkAllocDedicated(b *testing.B) {
	instance, debugMessenger, physDevice, device := createApplication(b, "BenchmarkAllocDedicated")
	defer destroyApplication(b, instance, debugMessenger, device)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allocator, err := New(logger, instance, physDevice, device, CreateOptions{})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, allocator.Destroy())
	}()
	var alloc Allocation

	for i := 0; i < b.N; i++ {
		memReqs := core1_0.MemoryRequirements{
			Size:           100,
			Alignment:      1,
			MemoryTypeBits: 0xffffffff,
		}

		_, err = allocator.AllocateMemory(&memReqs, AllocationCreateInfo{
			Flags: AllocationCreateDedicatedMemory,
		}, &alloc)
		require.NoError(b, err)

		require.NoError(b, alloc.Free())
	}
	b.StopTimer()
	checkCorruption(b, allocator)
}

func BenchmarkAllocDefragFast(b *testing.B) {
	instance, debugMessenger, physDevice, device := createApplication(b, "BenchmarkAllocDefragFast")
	defer destroyApplication(b, instance, debugMessenger, device)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allocator, err := New(logger, instance, physDevice, device, CreateOptions{})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, allocator.Destroy())
	}()

	memReqs := core1_0.MemoryRequirements{
		Size:           10000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}

	b.ResetTimer()
	b.StopTimer()

	for i := 0; i < b.N; i++ {
		allocs := make([]Allocation, 10000)
		_, err = allocator.AllocateMemorySlice(&memReqs, AllocationCreateInfo{}, allocs)
		require.NoError(b, err)

		for allocIndex := 0; allocIndex < 5000; allocIndex++ {
			err = allocs[allocIndex*2].Free()
			require.NoError(b, err)
		}

		b.StartTimer()

		var defragContext DefragmentationContext
		_, err = allocator.BeginDefragmentation(DefragmentationInfo{
			Flags:                 DefragmentationFlagAlgorithmFast,
			MaxAllocationsPerPass: 50,
			MaxBytesPerPass:       100000000,
		}, &defragContext)
		require.NoError(b, err)

		var finished bool

		for !finished {
			_ = defragContext.BeginDefragPass()
			finished, err = defragContext.EndDefragPass()
			if err != nil {
				break
			}
		}
		require.NoError(b, err)

		var stats defrag.DefragmentationStats
		defragContext.Finish(&stats)
		require.GreaterOrEqual(b, stats.AllocationsMoved, 1600)

		b.StopTimer()
		for allocIndex := 0; allocIndex < 5000; allocIndex++ {
			err = allocs[allocIndex*2+1].Free()
			require.NoError(b, err)
		}
	}
	checkCorruption(b, allocator)
}

func BenchmarkAllocDefragFull(b *testing.B) {
	instance, debugMessenger, physDevice, device := createApplication(b, "BenchmarkAllocDefragFull")
	defer destroyApplication(b, instance, debugMessenger, device)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allocator, err := New(logger, instance, physDevice, device, CreateOptions{})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, allocator.Destroy())
	}()

	memReqs := core1_0.MemoryRequirements{
		Size:           10000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}

	b.ResetTimer()
	b.StopTimer()

	for i := 0; i < b.N; i++ {
		allocs := make([]Allocation, 10000)
		_, err = allocator.AllocateMemorySlice(&memReqs, AllocationCreateInfo{}, allocs)
		require.NoError(b, err)

		for allocIndex := 0; allocIndex < 5000; allocIndex++ {
			err = allocs[allocIndex*2].Free()
			require.NoError(b, err)
		}

		b.StartTimer()

		var defragContext DefragmentationContext
		_, err = allocator.BeginDefragmentation(DefragmentationInfo{
			Flags:                 DefragmentationFlagAlgorithmFull,
			MaxAllocationsPerPass: 50,
			MaxBytesPerPass:       100000000,
		}, &defragContext)
		require.NoError(b, err)

		var finished bool

		for !finished {
			_ = defragContext.BeginDefragPass()
			finished, err = defragContext.EndDefragPass()
			require.NoError(b, err)
		}

		var stats defrag.DefragmentationStats
		defragContext.Finish(&stats)
		require.GreaterOrEqual(b, stats.AllocationsMoved, 2500)

		b.StopTimer()
		for allocIndex := 0; allocIndex < 5000; allocIndex++ {
			err = allocs[allocIndex*2+1].Free()
			require.NoError(b, err)
		}
	}
	checkCorruption(b, allocator)
}

func BenchmarkAllocDefragBig(b *testing.B) {
	instance, messenger, physDevice, device := createApplication(b, "BenchmarkAllocDefragBig")
	defer destroyApplication(b, instance, messenger, device)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allocator, err := New(logger, instance, physDevice, device, CreateOptions{})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, allocator.Destroy())
	}()

	memReqs := core1_0.MemoryRequirements{
		Size:           10000,
		Alignment:      1,
		MemoryTypeBits: 0xffffffff,
	}

	b.ResetTimer()
	b.StopTimer()

	for i := 0; i < b.N; i++ {
		allocs := make([]Allocation, 50000)
		_, err = allocator.AllocateMemorySlice(&memReqs, AllocationCreateInfo{}, allocs)
		require.NoError(b, err)

		for allocIndex := 0; allocIndex < 25000; allocIndex++ {
			err = allocs[allocIndex*2].Free()
			require.NoError(b, err)
		}

		b.StartTimer()

		var defragContext DefragmentationContext
		_, err = allocator.BeginDefragmentation(DefragmentationInfo{
			Flags:                 DefragmentationFlagAlgorithmFull,
			MaxAllocationsPerPass: 50,
			MaxBytesPerPass:       100000000,
		}, &defragContext)
		require.NoError(b, err)

		var finished bool

		for !finished {
			_ = defragContext.BeginDefragPass()
			finished, err = defragContext.EndDefragPass()
			require.NoError(b, err)
		}

		var stats defrag.DefragmentationStats
		defragContext.Finish(&stats)
		require.Equal(b, stats.AllocationsMoved, 12500)

		b.StopTimer()
		for allocIndex := 0; allocIndex < 25000; allocIndex++ {
			err = allocs[allocIndex*2+1].Free()
			require.NoError(b, err)
		}
	}
	checkCorruption(b, allocator)
}

func BenchmarkBuffer(b *testing.B) {
	instance, debugMessenger, physDevice, device := createApplication(b, "BenchmarkBuffer")
	defer destroyApplication(b, instance, debugMessenger, device)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allocator, err := New(logger, instance, physDevice, device, CreateOptions{})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, allocator.Destroy())
	}()
	var alloc Allocation

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buffer, _, err := allocator.CreateBuffer(core1_0.BufferCreateInfo{
			Size:  10000,
			Usage: core1_0.BufferUsageUniformBuffer,
		}, AllocationCreateInfo{
			Usage: MemoryUsageAuto,
		}, &alloc)
		require.NoError(b, err)

		err = alloc.DestroyBuffer(buffer)
		require.NoError(b, err)
	}
	checkCorruption(b, allocator)
}

func BenchmarkImage(b *testing.B) {
	instance, debugMessenger, physDevice, device := createApplication(b, "BenchmarkBuffer")
	defer destroyApplication(b, instance, debugMessenger, device)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allocator, err := New(logger, instance, physDevice, device, CreateOptions{})
	require.NoError(b, err)
	defer func() {
		require.NoError(b, allocator.Destroy())
	}()
	var alloc Allocation

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		image, _, err := allocator.CreateImage(core1_0.ImageCreateInfo{
			ImageType: core1_0.ImageType2D,
			Format:    core1_0.FormatA8B8G8R8UnsignedIntPacked,
			Extent: core1_0.Extent3D{
				Width:  512,
				Height: 512,
				Depth:  1,
			},
			ArrayLayers: 1,
			MipLevels:   1,
			Usage:       core1_0.ImageUsageSampled,
		}, AllocationCreateInfo{
			Usage: MemoryUsageAuto,
		}, &alloc)
		require.NoError(b, err)

		err = alloc.DestroyImage(image)
		require.NoError(b, err)
	}
	checkCorruption(b, allocator)
}
