package vulkan

import (
	"fmt"
	"github.com/cockroachdb/errors"
	"github.com/vkngwrapper/arsenal/memory"
	"github.com/vkngwrapper/arsenal/memory/internal/utils"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/driver"
	"github.com/vkngwrapper/extensions/v2/khr_external_memory_capabilities"
	"sync/atomic"
)

type Budget struct {
	Statistics memory.Statistics
	Usage      int
	Budget     int
}

type DeviceMemoryProperties struct {
	blockCount      [common.MaxMemoryHeaps]uint32
	allocationCount [common.MaxMemoryHeaps]uint32
	blockBytes      [common.MaxMemoryHeaps]uint64
	allocationBytes [common.MaxMemoryHeaps]uint64

	// TODO: Memory budget

	useMutex            bool
	allocationCallbacks *driver.AllocationCallbacks
	memoryCallbacks     *MemoryCallbacks
	memoryCount         uint32
	heapLimits          []int

	device                    core1_0.Device
	physicalDevice            core1_0.PhysicalDevice
	deviceProperties          *core1_0.PhysicalDeviceProperties
	memoryProperties          *core1_0.PhysicalDeviceMemoryProperties
	externalMemoryHandleTypes []khr_external_memory_capabilities.ExternalMemoryHandleTypeFlags
}

func NewDeviceMemoryProperties(
	useMutex bool,
	allocationCallbacks *driver.AllocationCallbacks,
	memoryCallbacks *MemoryCallbacks,
	device core1_0.Device,
	physicalDevice core1_0.PhysicalDevice,
	heapSizeLimits []int,
	externalMemoryHandleTypes []khr_external_memory_capabilities.ExternalMemoryHandleTypeFlags,
) (*DeviceMemoryProperties, error) {
	deviceProperties := &DeviceMemoryProperties{
		useMutex:            useMutex,
		allocationCallbacks: allocationCallbacks,
		memoryCallbacks:     memoryCallbacks,

		device:         device,
		physicalDevice: physicalDevice,
	}

	var err error
	deviceProperties.deviceProperties, err = physicalDevice.Properties()
	if err != nil {
		return nil, err
	}

	deviceProperties.memoryProperties = physicalDevice.MemoryProperties()

	err = utils.CheckPow2(deviceProperties.deviceProperties.Limits.BufferImageGranularity, "device bufferImageGranularity")
	if err != nil {
		return nil, err
	}
	err = utils.CheckPow2(deviceProperties.deviceProperties.Limits.NonCoherentAtomSize, "device nonCoherentAtomSize")
	if err != nil {
		return nil, err
	}

	// Initialize memory heap data
	heapCount := deviceProperties.MemoryHeapCount()
	heapLimitCount := len(heapSizeLimits)
	heapTypeCount := len(externalMemoryHandleTypes)

	if heapLimitCount > 0 && heapLimitCount != heapCount {
		return nil, errors.New("memory.CreateOptions.HeapSizeLimits was provided, but the length does not equal the number of PhysicalDevice heap types")
	}

	if heapTypeCount > 0 && heapTypeCount != heapCount {
		return nil, errors.New("memory.CreateOptions.ExternalMemoryHandleTypes was provided, but the length does not equal the number of PhysicalDevice heap types")
	}

	deviceProperties.heapLimits = heapSizeLimits
	deviceProperties.externalMemoryHandleTypes = externalMemoryHandleTypes

	return deviceProperties, nil
}

func (m *DeviceMemoryProperties) MemoryTypeCount() int {
	return len(m.memoryProperties.MemoryTypes)
}

func (m *DeviceMemoryProperties) MemoryHeapCount() int {
	return len(m.memoryProperties.MemoryHeaps)
}

func (m *DeviceMemoryProperties) MemoryTypeIndexToHeapIndex(memTypeIndex int) int {
	return m.memoryProperties.MemoryTypes[memTypeIndex].HeapIndex
}

func (m *DeviceMemoryProperties) MemoryTypeMinimumAlignment(memTypeIndex int) uint {
	memTypeFlags := m.memoryProperties.MemoryTypes[memTypeIndex].PropertyFlags

	if (memTypeFlags&core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent) == core1_0.MemoryPropertyHostVisible {
		// Memory is non-coherent
		alignment := uint(m.deviceProperties.Limits.NonCoherentAtomSize)
		if alignment < 1 {
			return 1
		}
		return alignment
	}

	return 1
}

func (m *DeviceMemoryProperties) DeviceProperties() *core1_0.PhysicalDeviceProperties {
	return m.deviceProperties
}

func (m *DeviceMemoryProperties) MemoryTypeProperties(memoryTypeIndex int) core1_0.MemoryType {
	return m.memoryProperties.MemoryTypes[memoryTypeIndex]
}

func (m *DeviceMemoryProperties) MemoryHeapProperties(heapIndex int) core1_0.MemoryHeap {
	return m.memoryProperties.MemoryHeaps[heapIndex]
}

func (m *DeviceMemoryProperties) IsMemoryTypeHostNonCoherent(memoryTypeIndex int) bool {
	flags := m.memoryProperties.MemoryTypes[memoryTypeIndex].PropertyFlags

	return flags&(core1_0.MemoryPropertyHostVisible|core1_0.MemoryPropertyHostCoherent) == core1_0.MemoryPropertyHostVisible
}

func (m *DeviceMemoryProperties) AddBlockAllocation(heapIndex int, allocationSize int) {
	atomic.AddUint64(&m.blockBytes[heapIndex], uint64(allocationSize))
	atomic.AddUint32(&m.blockCount[heapIndex], 1)
}

func (m *DeviceMemoryProperties) AddBlockAllocationWithBudget(heapIndex, allocationSize, maxAllocatable int) (common.VkResult, error) {
	for {
		currentVal := atomic.LoadUint64(&m.blockBytes[heapIndex])
		targetVal := currentVal + uint64(allocationSize)

		if targetVal > uint64(maxAllocatable) {
			return core1_0.VKErrorOutOfDeviceMemory, core1_0.VKErrorOutOfDeviceMemory.ToError()
		}

		if atomic.CompareAndSwapUint64(&m.blockBytes[heapIndex], currentVal, targetVal) {
			break
		}
	}

	atomic.AddUint32(&m.blockCount[heapIndex], 1)
	return core1_0.VKSuccess, nil
}

func (m *DeviceMemoryProperties) RemoveBlockAllocation(heapIndex, allocationSize int) {
	if m.blockBytes[heapIndex] < uint64(allocationSize) {
		panic(fmt.Sprintf("block bytes budget for heapIndex %d went negative", heapIndex))
	}
	atomic.AddUint64(&m.blockBytes[heapIndex], uint64(-allocationSize))
	if m.blockCount[heapIndex] == 0 {
		panic(fmt.Sprintf("block count budget for heapIndex %d went negative", heapIndex))
	}

	atomic.AddUint32(&m.blockCount[heapIndex], -1)
}

func (m *DeviceMemoryProperties) AllocateVulkanMemory(
	allocateInfo core1_0.MemoryAllocateInfo,
) (mem *SynchronizedMemory, res common.VkResult, err error) {
	newDeviceCount := atomic.AddUint32(&m.memoryCount, 1)
	defer func() {
		// If we failed out, roll back the device increment
		if err != nil {
			atomic.AddUint32(&m.memoryCount, -1)
		}
	}()

	if int(newDeviceCount) > m.deviceProperties.Limits.MaxMemoryAllocationCount {
		return nil, core1_0.VKErrorTooManyObjects, core1_0.VKErrorTooManyObjects.ToError()
	}

	heapIndex := m.MemoryTypeIndexToHeapIndex(allocateInfo.MemoryTypeIndex)
	heapLimit := m.heapLimits[heapIndex]
	if heapLimit == 0 {
		m.AddBlockAllocation(heapIndex, allocateInfo.AllocationSize)
	} else {
		maxSize := heapLimit
		heapSize := m.memoryProperties.MemoryHeaps[heapIndex].Size
		if heapLimit < heapSize {
			maxSize = heapSize
		}
		res, err = m.AddBlockAllocationWithBudget(heapIndex, allocateInfo.AllocationSize, maxSize)
		if err != nil {
			return nil, res, err
		}
	}
	defer func() {
		// If we failed out, roll back the block allocation
		if err != nil {
			m.RemoveBlockAllocation(heapIndex, allocateInfo.AllocationSize)
		}
	}()

	vulkanMem, res, err := m.device.AllocateMemory(m.allocationCallbacks, allocateInfo)
	if err != nil {
		return mem, res, err
	}

	// TODO: Memory budget

	mem, res, err = allocateSynchronizedMemory(
		m.device,
		m.useMutex,
		m.allocationCallbacks,
		allocateInfo,
	)
	if err != nil {
		return nil, res, err
	}

	if m.memoryCallbacks != nil {
		m.memoryCallbacks.Allocate(
			allocateInfo.MemoryTypeIndex,
			vulkanMem,
			allocateInfo.AllocationSize,
		)
	}

	return mem, res, nil
}

func (m *DeviceMemoryProperties) FreeVulkanMemory(memoryType int, size int, memory *SynchronizedMemory) {
	if m.memoryCallbacks != nil {
		m.memoryCallbacks.Free(
			memoryType,
			memory.VulkanDeviceMemory(),
			size,
		)
	}

	memory.FreeMemory()

	heapIndex := m.MemoryTypeIndexToHeapIndex(memoryType)
	m.RemoveBlockAllocation(heapIndex, size)
	atomic.AddUint32(&m.memoryCount, -1)
}

func (m *DeviceMemoryProperties) ExternalMemoryTypes(memoryTypeIndex int) khr_external_memory_capabilities.ExternalMemoryHandleTypeFlags {
	return m.externalMemoryHandleTypes[memoryTypeIndex]
}

func (m *DeviceMemoryProperties) HeapBudgets(firstHeap int, budgets []Budget) {
	// TODO: Memory budget extension

	for i := 0; i < len(budgets); i++ {
		heapIndex := firstHeap + i

		budgets[i].Statistics.BlockCount = int(m.blockCount[heapIndex])
		budgets[i].Statistics.AllocationCount = int(m.allocationCount[heapIndex])
		budgets[i].Statistics.BlockBytes = int(m.blockBytes[heapIndex])
		budgets[i].Statistics.AllocationBytes = int(m.allocationBytes[heapIndex])

		budgets[i].Usage = budgets[heapIndex].Statistics.BlockBytes
		budgets[i].Budget = m.memoryProperties.MemoryHeaps[heapIndex].Size * 8 / 10
	}
}

func (m *DeviceMemoryProperties) FlushOrInvalidateAllocations(memRanges []core1_0.MappedMemoryRange, operation memory.CacheOperation) (common.VkResult, error) {
	if len(memRanges) == 0 {
		return core1_0.VKSuccess, nil
	}

	switch operation {
	case memory.CacheOperationFlush:
		return m.device.FlushMappedMemoryRanges(memRanges)
	case memory.CacheOperationInvalidate:
		return m.device.InvalidateMappedMemoryRanges(memRanges)
	}

	return core1_0.VKErrorUnknown, errors.Newf("attempted to carry out invalid cache operation %s", operation.String())
}
