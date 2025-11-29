package vulkan

import (
	"unsafe"

	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
)

type SynchronizedMemory interface {
	VulkanDeviceMemory() core1_0.DeviceMemory
	BindVulkanBuffer(offset int, buffer core1_0.Buffer, next common.Options) (common.VkResult, error)
	BindVulkanImage(offset int, image core1_0.Image, next common.Options) (common.VkResult, error)
	References() int
	MappedData() unsafe.Pointer
	RecordSuballocSubfree() bool
	Map(references int, offset int, size int, flags core1_0.MemoryMapFlags) (unsafe.Pointer, common.VkResult, error)
	Unmap(references int) error
	FreeMemory()
}
