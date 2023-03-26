package vulkan

import (
	"github.com/cockroachdb/errors"
	"github.com/vkngwrapper/arsenal/memory/internal/utils"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/core1_1"
	"github.com/vkngwrapper/core/v2/driver"
	"unsafe"
)

type SynchronizedMemory struct {
	// Mapping data
	mapReferences int
	mapData       unsafe.Pointer

	// Hysteresis data- if we're calling map/unmap a lot more than suballoc/subfree then
	// maintain a persistent mapping to save time
	delayCounter  uint32
	statusCounter int32
	extraMapping  bool

	mapMutex      utils.OptionalMutex
	memory        core1_0.DeviceMemory
	extensionData *ExtensionData

	allocationCallbacks *driver.AllocationCallbacks
}

func allocateSynchronizedMemory(device core1_0.Device, useMutex bool, callbacks *driver.AllocationCallbacks, allocateInfo core1_0.MemoryAllocateInfo) (*SynchronizedMemory, common.VkResult, error) {
	memory, res, err := device.AllocateMemory(callbacks, allocateInfo)
	if err != nil {
		return nil, res, err
	}

	return &SynchronizedMemory{
		memory: memory,
		mapMutex: utils.OptionalMutex{
			UseMutex: useMutex,
		},
		allocationCallbacks: callbacks,
	}, res, nil
}

func (m *SynchronizedMemory) VulkanDeviceMemory() core1_0.DeviceMemory {
	return m.memory
}

func (m *SynchronizedMemory) BindVulkanBuffer(offset int, buffer core1_0.Buffer, next common.Options) (common.VkResult, error) {
	if next != nil && m.extensionData.BindMemory2 == nil {
		// We included a next pointer for BindBUfferMemory2 but it isn't active
		return core1_0.VKErrorExtensionNotPresent, core1_0.VKErrorExtensionNotPresent.ToError()
	}

	m.mapMutex.Lock()
	defer m.mapMutex.Unlock()

	if next != nil {
		return m.extensionData.BindMemory2.BindBufferMemory2([]core1_1.BindBufferMemoryInfo{
			{
				Buffer:       buffer,
				Memory:       m.memory,
				MemoryOffset: offset,
				NextOptions:  common.NextOptions{Next: next},
			},
		})
	}

	return buffer.BindBufferMemory(m.memory, offset)
}

func (m *SynchronizedMemory) BindVulkanImage(offset int, image core1_0.Image, next common.Options) (common.VkResult, error) {
	if next != nil && m.extensionData.BindMemory2 == nil {
		// We included a next pointer for BindBUfferMemory2 but it isn't active
		return core1_0.VKErrorExtensionNotPresent, core1_0.VKErrorExtensionNotPresent.ToError()
	}

	m.mapMutex.Lock()
	defer m.mapMutex.Unlock()

	if next != nil {
		return m.extensionData.BindMemory2.BindImageMemory2([]core1_1.BindImageMemoryInfo{
			{
				Image:        image,
				MemoryOffset: uint64(offset),
				Memory:       m.memory,
				NextOptions:  common.NextOptions{Next: next},
			},
		})
	}

	return image.BindImageMemory(m.memory, offset)
}
func (m *SynchronizedMemory) References() int {
	refs := m.mapReferences
	if m.extraMapping {
		refs++
	}
	return refs
}

func (m *SynchronizedMemory) MappedData() unsafe.Pointer {
	return m.mapData
}

const MapDelay uint32 = 7

func (m *SynchronizedMemory) postMapUnmap() bool {
	m.delayCounter++
	m.statusCounter++

	if m.delayCounter >= MapDelay {
		m.delayCounter = 0
		if m.statusCounter >= 1 {
			m.statusCounter = 0
			m.extraMapping = true
			return true
		}
	}

	return false
}

func (m *SynchronizedMemory) RecordSuballocSubfree() bool {
	m.mapMutex.Lock()
	defer m.mapMutex.Unlock()

	m.delayCounter++
	m.statusCounter--

	if m.delayCounter >= MapDelay {
		m.delayCounter = 0
		if m.statusCounter <= -2 {
			m.statusCounter = 0
			m.extraMapping = false
			return true
		}
	}

	return false
}

func (m *SynchronizedMemory) Map(references int, offset int, size int, flags core1_0.MemoryMapFlags) (unsafe.Pointer, common.VkResult, error) {
	if references == 0 {
		return nil, core1_0.VKSuccess, nil
	}

	m.mapMutex.Lock()
	defer m.mapMutex.Unlock()

	oldRefCount := m.References()
	_ = m.postMapUnmap()

	if oldRefCount > 0 {
		m.mapReferences += references
		if m.mapData == nil {
			return nil, core1_0.VKErrorUnknown, errors.New("the block is showing existing memory mapping references, but no mapped memory")
		}

		return m.mapData, core1_0.VKSuccess, nil
	}

	mappedData, result, err := m.memory.Map(offset, size, flags)
	if err != nil {
		return nil, result, err
	}

	m.mapData = mappedData
	m.mapReferences = references
	return mappedData, result, nil
}

func (m *SynchronizedMemory) Unmap(references int) error {
	if m.mapReferences == 0 {
		return nil
	}

	m.mapMutex.Lock()
	defer m.mapMutex.Unlock()

	if m.mapReferences < references {
		return errors.New("device memory block has more references being unmapped than are currently mapped")
	}

	m.mapReferences -= references
	if m.mapReferences < 0 {
		m.mapReferences = 0
	}

	if m.References() <= 0 {
		m.memory.Unmap()
		m.mapData = nil
	}

	m.postMapUnmap()
	return nil
}

func (m *SynchronizedMemory) FreeMemory() {
	m.mapMutex.Lock()
	defer m.mapMutex.Unlock()

	m.memory.Free(m.allocationCallbacks)
}
