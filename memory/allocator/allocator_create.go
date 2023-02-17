package allocator

import (
	"github.com/cockroachdb/errors"
	"github.com/vkngwrapper/arsenal/memory/internal/utils"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/core1_1"
	"github.com/vkngwrapper/core/v2/driver"
	"math"
)

// CreateFlags indicate specific allocator behaviors to activate or deactivate
type CreateFlags int32

var allocatorCreateFlagsMapping = common.NewFlagStringMapping[CreateFlags]()

func (f CreateFlags) Register(str string) {
	allocatorCreateFlagsMapping.Register(f, str)
}
func (f CreateFlags) String() string {
	return allocatorCreateFlagsMapping.FlagsToString(f)
}

const (
	// CreateExternallySynchronized ensures that this allocator and all objects created from it
	// will not be synchronized internally. The consumer must guarantee they are used from only one
	// thread at a time or are synchronized by some other mechanism, but performance may improve because
	// internal mutexes are not used.
	CreateExternallySynchronized CreateFlags = 1 << iota
)

func init() {
	CreateExternallySynchronized.Register("memory.CreateExternallySynchronized")
}

const (
	// DefaultLargeHeapBlockSize is the value that is used as the PreferredLargeHeapBlockSize when none
	// is provided via CreateOptions. It is equal to 256Mb.
	DefaultLargeHeapBlockSize int = 256 * 1024 * 1024
)

// CreateOptions contains optional settings when creating an allocator
type CreateOptions struct {
	// Flags indicates specific allocator behaviors to activate or deactivate
	Flags CreateFlags
	// PreferredLargeHeapBlockSize is the block size to use when allocating from heaps larger
	// than a gigabyte
	PreferredLargeHeapBlockSize int

	// VulkanCallbacks is an optional set of callbacks that will be executed from Vulkan on memory
	// created from this allocator. Allocations & frees performed by this allocator do not map 1:1
	// with allocations & frees performed by Vulkan, so these will not always be called
	VulkanCallbacks *driver.AllocationCallbacks

	// MemoryCallbackOptions is an optional set of callbacks that will be executed when Vulkan memory
	// is allocated from this allocator. It can be helpful in cases when the consumer requires allocator-
	// level info about allocated memory
	MemoryCallbackOptions *MemoryCallbackOptions

	// HeapSizeLimits can be left empty. If it is provided, though, it must be a slice
	// with a number of entries corresponding to the number of heaps in the PhysicalDevice
	// used to create this Allocator. Each entry must be either the maximum number of bytes
	// that should be allocated from the corresponding device memory heap, or -1 indicating
	// no limit.
	//
	// Heap memory limits will be enforced at runtime (the allocator will go so far as to
	// return an out of memory error when attempting to allocate beyond the limit).
	HeapSizeLimits []int

	// ExternalMemoryHandleTypes can be left empty. If it is provided though, it must be a slice
	// with a number of entries corresponding to the number of memory types in the PhysicalDevice
	// used to create this Allocator. Each entry must be either 0, indicating not to use external
	// memory, or a memory handle type, indicating which type of memory handles to use for
	// the memory type
	ExternalMemoryHandleTypes []core1_1.ExternalMemoryHandleTypeFlags
}

// New creates a new VulkanAllocator
//
// instance - The instance that owns the provided Device
//
// physicalDevice - The PhysicalDevice that owns the provided Device
//
// device - The Device that memory will be allocated into
//
// options - Optional parameters: it is valid to leave all the fields blank
func New(instance core1_0.Instance, physicalDevice core1_0.PhysicalDevice, device core1_0.Device, options CreateOptions) (*VulkanAllocator, error) {
	allocator := &VulkanAllocator{
		instance:            instance,
		physicalDevice:      physicalDevice,
		device:              device,
		allocationCallbacks: options.VulkanCallbacks,
		memoryCallbacks:     options.MemoryCallbackOptions,
		extensionData:       NewExtensionData(device),

		createFlags:                      options.Flags,
		gpuDefragmentationMemoryTypeBits: math.MaxUint32,
	}

	// Pull device info
	var err error
	allocator.deviceProperties, err = physicalDevice.Properties()
	if err != nil {
		return nil, err
	}
	allocator.memoryProperties = physicalDevice.MemoryProperties()

	if options.PreferredLargeHeapBlockSize == 0 {
		allocator.preferredLargeHeapBlockSize = DefaultLargeHeapBlockSize
	} else {
		allocator.preferredLargeHeapBlockSize = options.PreferredLargeHeapBlockSize
	}

	allocator.globalMemoryTypeBits = allocator.calculateGlobalMemoryTypeBits()

	// Initialize memory heap data
	heapCount := allocator.MemoryHeapCount()
	heapLimitCount := len(options.HeapSizeLimits)
	heapTypeCount := len(options.ExternalMemoryHandleTypes)

	if heapLimitCount > 0 && heapLimitCount != heapCount {
		return nil, errors.New("memory.CreateOptions.HeapSizeLimits was provided, but the length does not equal the number of PhysicalDevice heap types")
	}

	if heapTypeCount > 0 && heapTypeCount != heapCount {
		return nil, errors.New("memory.CreateOptions.ExternalMemoryHandleTypes was provided, but the length does not equal the number of PhysicalDevice heap types")
	}

	allocator.heapLimits = options.HeapSizeLimits
	allocator.externalMemoryHandleTypes = make([]core1_1.ExternalMemoryHandleTypeFlags, heapTypeCount)

	// khr_external_memory present by any means
	if allocator.extensionData.ExternalMemory {
		allocator.externalMemoryHandleTypes = options.ExternalMemoryHandleTypes
	} else if heapTypeCount > 0 {
		return nil, errors.New("memory.CreateOptions.ExternalMemoryHandleTypes was provided, but neither the core 1.1 or the extension khr_external_memory are active")
	}

	// Initialize memory block lists
	typeCount := allocator.MemoryTypeCount()
	for typeIndex := 0; typeIndex < typeCount; typeIndex++ {
		if allocator.globalMemoryTypeBits&(1<<typeIndex) != 0 {
			preferredBlockSize, err := allocator.calculatePreferredBlockSize(typeIndex)
			if err != nil {
				return nil, err
			}

			allocator.memoryBlockLists[typeIndex] = newMemoryBlockList(
				allocator,
				nil,
				typeIndex,
				preferredBlockSize,
				0,
				math.MaxInt,
				allocator.calculateBufferImageGranularity(),
				false,
				0,
				0.5,
				allocator.memoryTypeMinimumAlignment(typeIndex),
				allocator.extensionData,
				nil,
			)
		}
	}

	// TODO Memory budget setup

	return allocator, nil
}

const (
	smallHeapMaxSize int = 1024 * 1024 * 1024 // 1 GB
)

func (a *VulkanAllocator) calculatePreferredBlockSize(memTypeIndex int) (int, error) {
	heapIndex, err := a.MemoryTypeIndexToHeapIndex(memTypeIndex)
	if err != nil {
		return -1, err
	}

	heapSize := a.memoryProperties.MemoryHeaps[heapIndex].Size
	rawSize := a.preferredLargeHeapBlockSize
	if heapSize <= smallHeapMaxSize {
		rawSize = heapSize / 8
	}

	return utils.AlignUp(rawSize, 32)
}

func (a *VulkanAllocator) calculateGlobalMemoryTypeBits() uint32 {
	var typeBits uint32

	memTypeCount := a.MemoryTypeCount()
	for memoryTypeIndex := 0; memoryTypeIndex < memTypeCount; memoryTypeIndex++ {
		// TODO: AMD coherent memory exclude
		typeBits |= 1 << memoryTypeIndex
	}

	return typeBits
}

func (a *VulkanAllocator) calculateBufferImageGranularity() int {
	granularity := a.deviceProperties.Limits.BufferImageGranularity

	if granularity < 1 {
		return 1
	}
	return granularity
}
