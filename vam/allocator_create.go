package vam

import (
	"github.com/cockroachdb/errors"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/vam/internal/vulkan"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/driver"
	"github.com/vkngwrapper/extensions/v2/khr_external_memory_capabilities"
	"golang.org/x/exp/slog"
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
	// AllocatorCreateExternallySynchronized ensures that this allocator and all objects created from it
	// will not be synchronized internally. The consumer must guarantee they are used from only one
	// thread at a time or are synchronized by some other mechanism, but performance may improve because
	// internal mutexes are not used.
	AllocatorCreateExternallySynchronized CreateFlags = 1 << iota
)

func init() {
	AllocatorCreateExternallySynchronized.Register("AllocatorCreateExternallySynchronized")
}

const (
	// defaultLargeHeapBlockSize is the value that is used as the PreferredLargeHeapBlockSize when none
	// is provided via CreateOptions. It is equal to 256Mb.
	defaultLargeHeapBlockSize int = 256 * 1024 * 1024
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
	ExternalMemoryHandleTypes []khr_external_memory_capabilities.ExternalMemoryHandleTypeFlags
}

// New creates a new Allocator
//
// instance - The instance that owns the provided Device
//
// physicalDevice - The PhysicalDevice that owns the provided Device
//
// device - The Device that memory will be allocated into
//
// options - Optional parameters: it is valid to leave all the fields blank
func New(logger *slog.Logger, instance core1_0.Instance, physicalDevice core1_0.PhysicalDevice, device core1_0.Device, options CreateOptions) (*Allocator, error) {
	useMutex := options.Flags&AllocatorCreateExternallySynchronized == 0

	allocator := &Allocator{
		useMutex:       useMutex,
		logger:         logger,
		instance:       instance,
		physicalDevice: physicalDevice,
		device:         device,
		extensionData:  vulkan.NewExtensionData(device),

		createFlags:                      options.Flags,
		gpuDefragmentationMemoryTypeBits: math.MaxUint32,
	}

	if options.PreferredLargeHeapBlockSize == 0 {
		allocator.preferredLargeHeapBlockSize = defaultLargeHeapBlockSize
	} else {
		allocator.preferredLargeHeapBlockSize = options.PreferredLargeHeapBlockSize
	}

	heapTypeCount := len(options.HeapSizeLimits)
	externalMemoryTypes := make([]khr_external_memory_capabilities.ExternalMemoryHandleTypeFlags, heapTypeCount)
	// khr_external_memory present by any means
	if allocator.extensionData.ExternalMemory {
		externalMemoryTypes = options.ExternalMemoryHandleTypes
	} else if heapTypeCount > 0 {
		return nil, errors.New("memory.CreateOptions.ExternalMemoryHandleTypes was provided, but neither the core 1.1 or the extension khr_external_memory are active")
	}

	var err error
	allocator.deviceMemory, err = vulkan.NewDeviceMemoryProperties(
		useMutex,
		options.VulkanCallbacks,
		&memoryCallbacks{
			Callbacks: options.MemoryCallbackOptions,
			Allocator: allocator,
		},
		device,
		physicalDevice,
		options.HeapSizeLimits,
		externalMemoryTypes,
	)
	if err != nil {
		return nil, err
	}

	allocator.globalMemoryTypeBits = allocator.deviceMemory.CalculateGlobalMemoryTypeBits()

	// Initialize memory block lists
	typeCount := allocator.deviceMemory.MemoryTypeCount()
	for typeIndex := 0; typeIndex < typeCount; typeIndex++ {
		if allocator.globalMemoryTypeBits&(1<<typeIndex) != 0 {
			preferredBlockSize, err := allocator.calculatePreferredBlockSize(typeIndex)
			if err != nil {
				return nil, err
			}
			allocator.memoryBlockLists[typeIndex] = &memoryBlockList{}

			allocator.memoryBlockLists[typeIndex].Init(
				useMutex,
				logger,
				typeIndex,
				preferredBlockSize,
				0,
				math.MaxInt,
				allocator.deviceMemory.CalculateBufferImageGranularity(),
				false,
				0,
				0.5,
				allocator.deviceMemory.MemoryTypeMinimumAlignment(typeIndex),
				allocator.extensionData,
				allocator.deviceMemory,
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

func (a *Allocator) calculatePreferredBlockSize(memTypeIndex int) (int, error) {
	heapIndex := a.deviceMemory.MemoryTypeIndexToHeapIndex(memTypeIndex)

	heapSize := a.deviceMemory.MemoryHeapProperties(heapIndex).Size
	rawSize := a.preferredLargeHeapBlockSize
	if heapSize <= smallHeapMaxSize {
		rawSize = heapSize / 8
	}

	return memutils.AlignUp(rawSize, 32), nil
}
