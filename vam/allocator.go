package vam

import (
	"context"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"unsafe"

	"log/slog"

	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/pkg/errors"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/vam/internal/utils"
	"github.com/vkngwrapper/arsenal/vam/internal/vulkan"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/core1_1"
	"github.com/vkngwrapper/core/v2/driver"
	"github.com/vkngwrapper/extensions/v2/amd_device_coherent_memory"
	"github.com/vkngwrapper/extensions/v2/ext_memory_priority"
	"github.com/vkngwrapper/extensions/v2/khr_buffer_device_address"
	"github.com/vkngwrapper/extensions/v2/khr_dedicated_allocation"
	"github.com/vkngwrapper/extensions/v2/khr_external_memory"
	"github.com/vkngwrapper/extensions/v2/khr_maintenance4"
)

const (
	createdFillPattern   uint8 = 0xDC
	destroyedFillPattern uint8 = 0xEF
)

// Allocator is vam's root object and is used to create Pool and Allocation objects
type Allocator struct {
	useMutex            bool
	logger              *slog.Logger
	instance            core1_0.Instance
	physicalDevice      core1_0.PhysicalDevice
	device              core1_0.Device
	allocationCallbacks *driver.AllocationCallbacks

	createFlags   CreateFlags
	extensionData *vulkan.ExtensionData

	preferredLargeHeapBlockSize      int
	gpuDefragmentationMemoryTypeBits uint32
	globalMemoryTypeBits             uint32
	nextPoolId                       int
	poolsMutex                       utils.OptionalRWMutex
	pools                            *Pool

	deviceMemory         *vulkan.DeviceMemoryProperties
	memoryBlockLists     [common.MaxMemoryTypes]*memoryBlockList
	dedicatedAllocations [common.MaxMemoryTypes]*dedicatedAllocationList
}

// AllocatorStatistics is used to get the current state of memory within an Allocator, including
// breakdowns by memory type and heap of currently-allocated memory
type AllocatorStatistics struct {
	MemoryTypes [common.MaxMemoryTypes]memutils.DetailedStatistics
	MemoryHeaps [common.MaxMemoryHeaps]memutils.DetailedStatistics
	Total       memutils.DetailedStatistics
}

func (a *Allocator) calcAllocationParams(
	o *AllocationCreateInfo,
	requiresDedicatedAllocation bool,
) (common.VkResult, error) {
	hostAccessFlags := o.Flags & (AllocationCreateHostAccessSequentialWrite | AllocationCreateHostAccessRandom)
	if hostAccessFlags == (AllocationCreateHostAccessSequentialWrite | AllocationCreateHostAccessRandom) {
		return core1_0.VKErrorUnknown, errors.New("AllocationCreateHostAccessSequentialWrite and AllocationCreateHostAccessRandom cannot both be specified")
	}

	if hostAccessFlags == 0 && (o.Flags&AllocationCreateHostAccessAllowTransferInstead) != 0 {
		return core1_0.VKErrorUnknown, errors.New("if AllocationCreateHostAccessAllowTransferInstead is specified, " +
			"either AllocationCreateHostAccessSequentialWrite or AllocationCreateHostAccessRandom must be specified as well")
	}

	if o.Usage == MemoryUsageAuto || o.Usage == MemoryUsageAutoPreferDevice || o.Usage == MemoryUsageAutoPreferHost {
		if hostAccessFlags == 0 && o.Flags&AllocationCreateMapped != 0 {
			return core1_0.VKErrorUnknown, errors.New("when using MemoryUsageAuto* with AllocationCreateMapped, either " +
				"AllocationCreateHostAccessSequentialWrite or AllocationCreateHostAccessRandom must be specified as well")
		}
	}

	// GPU lazily allocated requires dedicated allocations
	if requiresDedicatedAllocation || o.Usage == MemoryUsageGPULazilyAllocated {
		o.Flags |= AllocationCreateDedicatedMemory
	}

	if o.Pool != nil {
		if o.Pool.blockList.HasExplicitBlockSize() && o.Flags&AllocationCreateDedicatedMemory != 0 {
			return core1_0.VKErrorUnknown, errors.New("specified memutils.AllocationCreateDedicatedMemory with a pool that does not support it")
		}

		o.Priority = o.Pool.blockList.priority
	}

	if o.Flags&AllocationCreateDedicatedMemory != 0 && o.Flags&AllocationCreateNeverAllocate != 0 {
		return core1_0.VKErrorUnknown, errors.New("AllocationCreateDedicatedMemory and AllocationCreateNeverAllocate cannot be specified together")
	}

	if o.Usage != MemoryUsageAuto && o.Usage != MemoryUsageAutoPreferDevice && o.Usage != MemoryUsageAutoPreferHost {
		if hostAccessFlags == 0 {
			o.Flags |= AllocationCreateHostAccessRandom
		}
	}

	return core1_0.VKSuccess, nil
}

func (a *Allocator) findMemoryPreferences(
	o *AllocationCreateInfo,
	bufferOrImageUsage *uint32,
) (requiredFlags, preferredFlags, notPreferredFlags core1_0.MemoryPropertyFlags) {
	isIntegratedGPU := a.deviceMemory.IsIntegratedGPU()
	requiredFlags = o.RequiredFlags
	preferredFlags = o.PreferredFlags
	notPreferredFlags = 0

	switch o.Usage {
	case MemoryUsageGPULazilyAllocated:
		requiredFlags |= core1_0.MemoryPropertyLazilyAllocated
	case MemoryUsageAuto, MemoryUsageAutoPreferDevice, MemoryUsageAutoPreferHost:
		{
			transferUsages := uint32(core1_0.BufferUsageTransferDst) | uint32(core1_0.BufferUsageTransferSrc) |
				uint32(core1_0.ImageUsageTransferSrc) | uint32(core1_0.ImageUsageTransferDst)
			nonTransferUsages := ^transferUsages

			var deviceAccess bool

			if bufferOrImageUsage != nil {
				deviceAccess = (*bufferOrImageUsage & nonTransferUsages) != 0
			}

			hostAccessSequentialWrite := o.Flags&AllocationCreateHostAccessSequentialWrite != 0
			hostAccessRandom := o.Flags&AllocationCreateHostAccessRandom != 0
			hostAccessAllowTransferInstead := o.Flags&AllocationCreateHostAccessAllowTransferInstead != 0
			preferDevice := o.Usage == MemoryUsageAutoPreferDevice
			preferHost := o.Usage == MemoryUsageAutoPreferHost

			if hostAccessRandom && !isIntegratedGPU && deviceAccess && hostAccessAllowTransferInstead && !preferHost {
				// CPU-accessible memory that should live on the device
				// Prefer DEVICE_LOCAL, HOST_VISIBLE would be nice but not as important so omitted
				preferredFlags |= core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostCached
			} else if hostAccessRandom {
				// Other CPU-accessible memory
				requiredFlags |= core1_0.MemoryPropertyHostVisible
				preferredFlags |= core1_0.MemoryPropertyHostCached
			} else if hostAccessSequentialWrite {
				// Uncached write-combined memory
				notPreferredFlags |= core1_0.MemoryPropertyHostCached

				if !isIntegratedGPU && deviceAccess && hostAccessAllowTransferInstead && !preferHost {
					// Sequential write against memory that lives on the device, transfers are allowed so it
					// doesn't have to be host visible
					preferredFlags |= core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostVisible
				} else {
					// CPU must have write access
					requiredFlags |= core1_0.MemoryPropertyHostVisible

					if deviceAccess && preferHost {
						// User requested CPU memory so make it CPU memory
						notPreferredFlags |= core1_0.MemoryPropertyDeviceLocal
					} else if deviceAccess || preferDevice {
						// GPU needs access OR it doesn't need access but the user asked for it anyway
						preferredFlags |= core1_0.MemoryPropertyDeviceLocal
					} else {
						// No direct GPU access needed, user didn't request GPU
						notPreferredFlags |= core1_0.MemoryPropertyDeviceLocal
					}
				}
			} else {
				// No CPU access required, but if user asked for CPU give them CPU
				if preferHost {
					notPreferredFlags |= core1_0.MemoryPropertyDeviceLocal
				} else {
					preferredFlags |= core1_0.MemoryPropertyDeviceLocal
				}
			}

			break
		}
	}

	if (o.RequiredFlags|o.PreferredFlags)&(amd_device_coherent_memory.MemoryPropertyDeviceCoherentAMD|
		amd_device_coherent_memory.MemoryPropertyDeviceUncachedAMD) == 0 {

		notPreferredFlags |= amd_device_coherent_memory.MemoryPropertyDeviceUncachedAMD
	}
	return requiredFlags, preferredFlags, notPreferredFlags
}

// FindMemoryTypeIndex gets the memory type that should be used for a requested allocation, as provided
// by an AllocationCreateInfo. This can be used to decide what memoryTypeIndex to use when creating a
// Pool, or in any situation where you want a good memory type to be intelligently selected without actually
// allocating memory.
//
// Because this method does not accept a core1_0.BufferCreateInfo or core1_0.ImageCreateInfo, it can't be
// all that intelligent about how it chooses the memory type. You can help it by providing RequiredFlags
// and PreferredFlags in the AllocationCreateInfo object.
//
// memoryTypeBits - You can use this bitmask to prevent this method from returning memory types you
// don't want.
//
// o - Options indicating the sort of allocation being made
func (a *Allocator) FindMemoryTypeIndex(
	memoryTypeBits uint32,
	o AllocationCreateInfo,
) (int, common.VkResult, error) {
	a.logger.Debug("Allocator::FindMemoryTypeIndex")

	return a.findMemoryTypeIndex(memoryTypeBits, &o, nil)
}

// FindMemoryTypeIndexForBufferInfo gets the memory type that should be used for a requested allocation,
// as provided by the combination of an AllocationCreateInfo and a core1_0.BufferCreateInfo. This can be
// used to decide what memory type index to use when creating a Pool, or in any situation where you want
// a good memory type to be intelligently selected without actually allocating memory. The memory type
// index returned will be for the memory type that is best for a buffer created with the provided core1_0.BufferCreateInfo.
//
// bufferInfo - The core1_0.BufferCreateInfo that will be used to create the eventual buffer
//
// o - Options indicating the sort of allocation being made
func (a *Allocator) FindMemoryTypeIndexForBufferInfo(
	bufferInfo core1_0.BufferCreateInfo,
	o AllocationCreateInfo,
) (int, common.VkResult, error) {
	a.logger.Debug("Allocator::FindMemoryTypeIndexForBufferInfo")

	if a.extensionData.Maintenance4 != nil {
		// khr_maintenance4 (promoted to 1.3) lets us query memory requirements with BufferCreateInfo directly
		var memoryReqs core1_1.MemoryRequirements2
		err := a.extensionData.Maintenance4.DeviceBufferMemoryRequirements(
			a.device,
			khr_maintenance4.DeviceBufferMemoryRequirements{
				CreateInfo: bufferInfo,
			}, &memoryReqs)
		if err != nil {
			return -1, core1_0.VKErrorUnknown, err
		}

		usage := uint32(bufferInfo.Usage)
		return a.findMemoryTypeIndex(memoryReqs.MemoryRequirements.MemoryTypeBits, &o, &usage)
	}

	// Without that extension, we need to create a buffer directly, check memory requirements, and then
	// destroy it :\
	memReqs, res, err := a.getMemoryRequirementsFromBufferInfo(bufferInfo)
	if err != nil {
		return -1, res, err
	}

	usage := uint32(bufferInfo.Usage)
	return a.findMemoryTypeIndex(memReqs.MemoryTypeBits, &o, &usage)
}

// FindMemoryTypeIndexForImageInfo gets the memory type that should be used for a requested allocation,
// as provided by the combination of an AllocationCreateInfo and a core1_0.ImageCreateInfo. This can be
// used to decide what memory type index to use when creating a Pool, or in any situation where you want
// a good memory type to be intelligently selected without actually allocating memory. The memory type
// index returned will be for the memory type that is best for an image created with the provided
// core1_0.ImageCreateInfo
//
// imageInfo - The core1_0.ImageCreateInfo that will be used to create the eventual image
//
// o - Options indicating the sort of allocation being made
func (a *Allocator) FindMemoryTypeIndexForImageInfo(
	imageInfo core1_0.ImageCreateInfo,
	o AllocationCreateInfo,
) (int, common.VkResult, error) {
	a.logger.Debug("Allocator::FindMemoryTYpeIndexForImageInfo")

	if a.extensionData.Maintenance4 != nil {
		// khr_maintenance4 (promoted to 1.3) lets us query memory requirements with ImageCreateInfo directly
		var memoryReqs core1_1.MemoryRequirements2
		err := a.extensionData.Maintenance4.DeviceImageMemoryRequirements(
			a.device,
			khr_maintenance4.DeviceImageMemoryRequirements{
				CreateInfo: imageInfo,
			}, &memoryReqs)
		if err != nil {
			return -1, core1_0.VKErrorUnknown, err
		}

		usage := uint32(imageInfo.Usage)
		return a.findMemoryTypeIndex(memoryReqs.MemoryRequirements.MemoryTypeBits, &o, &usage)
	}

	// Without that extension, we need to create an image directly, check memory requirements, and then
	// destroy it :\
	memReqs, res, err := a.getMemoryRequirementsForImageInfo(imageInfo)
	if err != nil {
		return -1, res, err
	}

	usage := uint32(imageInfo.Usage)
	return a.findMemoryTypeIndex(memReqs.MemoryTypeBits, &o, &usage)
}

func (a *Allocator) getMemoryRequirementsForImageInfo(
	imageInfo core1_0.ImageCreateInfo,
) (*core1_0.MemoryRequirements, common.VkResult, error) {
	image, res, err := a.device.CreateImage(a.allocationCallbacks, imageInfo)
	if err != nil {
		return nil, res, err
	}
	defer image.Destroy(a.allocationCallbacks)

	return image.MemoryRequirements(), core1_0.VKSuccess, nil
}

func (a *Allocator) getMemoryRequirementsFromBufferInfo(
	bufferInfo core1_0.BufferCreateInfo,
) (*core1_0.MemoryRequirements, common.VkResult, error) {
	buffer, res, err := a.device.CreateBuffer(a.allocationCallbacks, bufferInfo)
	if err != nil {
		return nil, res, err
	}
	defer buffer.Destroy(a.allocationCallbacks)

	return buffer.MemoryRequirements(), core1_0.VKSuccess, nil
}

func (a *Allocator) findMemoryTypeIndex(
	memoryTypeBits uint32,
	o *AllocationCreateInfo,
	bufferOrImageUsage *uint32,
) (int, common.VkResult, error) {
	memoryTypeBits &= a.globalMemoryTypeBits
	if o.MemoryTypeBits != 0 {
		memoryTypeBits &= o.MemoryTypeBits
	}

	requiredFlags, preferredFlags, notPreferredFlags := a.findMemoryPreferences(o, bufferOrImageUsage)
	bestMemoryTypeIndex := -1
	minCost := math.MaxInt

	for memTypeIndex := 0; memTypeIndex < a.deviceMemory.MemoryTypeCount(); memTypeIndex++ {
		memTypeBit := uint32(1 << memTypeIndex)

		if memTypeBit&memoryTypeBits == 0 {
			// This memory type is banned by the bitmask
			continue
		}

		flags := a.deviceMemory.MemoryTypeProperties(memTypeIndex).PropertyFlags
		if requiredFlags&flags != requiredFlags {
			// This memory type is missing required flags
			continue
		}

		missingPreferredFlags := preferredFlags & ^flags
		presentNotPreferredFlags := notPreferredFlags & flags
		cost := bits.OnesCount32(uint32(missingPreferredFlags)) + bits.OnesCount32(uint32(presentNotPreferredFlags))
		if cost == 0 {
			return memTypeIndex, core1_0.VKSuccess, nil
		} else if cost < minCost {
			bestMemoryTypeIndex = memTypeIndex
			minCost = cost
		}
	}

	if bestMemoryTypeIndex < 0 {
		return -1, core1_0.VKErrorFeatureNotPresent, core1_0.VKErrorFeatureNotPresent.ToError()
	}

	return bestMemoryTypeIndex, core1_0.VKSuccess, nil
}

func (a *Allocator) calculateMemoryTypeParameters(
	options *AllocationCreateInfo,
	memoryTypeIndex int,
	size int,
	allocationCount int,
) (common.VkResult, error) {
	// If memory type is not HOST_VISIBLE, disable MAPPED
	if options.Flags&AllocationCreateMapped != 0 &&
		a.deviceMemory.MemoryTypeProperties(memoryTypeIndex).PropertyFlags&core1_0.MemoryPropertyHostVisible == 0 {
		options.Flags &= ^AllocationCreateMapped
	}

	// Check budget if appropriate
	if options.Flags&AllocationCreateDedicatedMemory != 0 &&
		options.Flags&AllocationCreateWithinBudget != 0 {
		heapIndex := a.deviceMemory.MemoryTypeIndexToHeapIndex(memoryTypeIndex)

		budget := vulkan.Budget{}
		a.deviceMemory.HeapBudget(heapIndex, &budget)
		if budget.Usage+size*allocationCount > budget.Budget {
			return core1_0.VKErrorOutOfDeviceMemory, core1_0.VKErrorOutOfDeviceMemory.ToError()
		}
	}

	return core1_0.VKSuccess, nil
}

func (a *Allocator) allocateDedicatedMemoryPage(
	pool *Pool,
	size int,
	suballocationType suballocationType,
	memoryTypeIndex int,
	allocInfo core1_0.MemoryAllocateInfo,
	doMap, isMappingAllowed bool,
	userData any,
	alloc *Allocation,
) (res common.VkResult, err error) {
	mem, res, err := a.deviceMemory.AllocateVulkanMemory(allocInfo)
	if err != nil {
		a.logger.Debug("    Allocator::allocateDedicatedMemoryPage FAILED")
		return res, err
	}
	defer func() {
		if err != nil {
			a.logger.Debug("    Allocator::allocateDedicatedMemoryPage FAILED")
			a.deviceMemory.FreeVulkanMemory(memoryTypeIndex, size, mem)
		}
	}()

	if doMap {
		// Set up our persistent map
		_, res, err = mem.Map(1, 0, common.WholeSize, 0)
		if err != nil {
			return res, err
		}
	}

	alloc.init(a, isMappingAllowed)
	alloc.initDedicatedAllocation(pool, memoryTypeIndex, mem, suballocationType, size)

	userDataStr, ok := userData.(string)
	if ok {
		alloc.SetName(userDataStr)
	} else {
		alloc.SetUserData(userData)
	}
	a.deviceMemory.AddAllocation(a.deviceMemory.MemoryTypeIndexToHeapIndex(memoryTypeIndex), size)

	if InitializeAllocs {
		alloc.fillAllocation(createdFillPattern)
	}

	return core1_0.VKSuccess, nil
}

func (a *Allocator) allocateDedicatedMemory(
	pool *Pool,
	size int,
	suballocationType suballocationType,
	dedicatedAllocations *dedicatedAllocationList,
	memoryTypeIndex int,
	doMap, isMappingAllowed, canAliasMemory bool,
	userData any,
	priority float32,
	dedicatedBuffer core1_0.Buffer,
	dedicatedImage core1_0.Image,
	dedicatedBufferOrImageUsage *uint32,
	allocations []Allocation,
	options common.Options,
) (common.VkResult, error) {
	if len(allocations) == 0 {
		panic("called Allocator::allocateDedicatedMemory with empty allocation list")
	}
	if dedicatedBuffer != nil && dedicatedImage != nil {
		panic("called Allocator::allocateDedicatedMemory with both dedicatedBuffer and dedicatedImage - only one is permitted")
	}

	allocInfo := core1_0.MemoryAllocateInfo{
		MemoryTypeIndex: memoryTypeIndex,
		AllocationSize:  size,
		NextOptions:     common.NextOptions{Next: options},
	}

	if a.extensionData.DedicatedAllocations && !canAliasMemory {
		dedicatedAllocInfo := khr_dedicated_allocation.MemoryDedicatedAllocateInfo{}
		if dedicatedBuffer != nil {
			dedicatedAllocInfo.Buffer = dedicatedBuffer
			dedicatedAllocInfo.Next = allocInfo.Next
			allocInfo.Next = dedicatedAllocInfo
		} else if dedicatedImage != nil {
			dedicatedAllocInfo.Image = dedicatedImage
			dedicatedAllocInfo.Next = allocInfo.Next
			allocInfo.Next = dedicatedAllocInfo
		}
	}

	if a.extensionData.BufferDeviceAddress != nil {
		allocFlagsInfo := core1_1.MemoryAllocateFlagsInfo{}
		canContainBufferWithDeviceAddress := true
		if dedicatedBuffer != nil {
			canContainBufferWithDeviceAddress = dedicatedBufferOrImageUsage == nil ||
				(core1_0.BufferUsageFlags(*dedicatedBufferOrImageUsage)&khr_buffer_device_address.BufferUsageShaderDeviceAddress != 0)
		} else if dedicatedImage != nil {
			canContainBufferWithDeviceAddress = false
		}

		if canContainBufferWithDeviceAddress {
			allocFlagsInfo.Flags = khr_buffer_device_address.MemoryAllocateDeviceAddress
			allocFlagsInfo.Next = allocInfo.Next
			allocInfo.Next = allocFlagsInfo
		}
	}

	if a.extensionData.UseMemoryPriority {
		if priority < 0 || priority > 1 {
			return core1_0.VKErrorUnknown, errors.Errorf("attempted to allocate memory with invalid priority %f: priority must be between 0 and 1, inclusive", priority)
		}

		priorityInfo := ext_memory_priority.MemoryPriorityAllocateInfo{
			Priority: priority,
		}
		priorityInfo.Next = allocInfo.Next
		allocInfo.Next = priorityInfo
	}

	if a.extensionData.ExternalMemory {
		exportMemoryAllocInfo := khr_external_memory.ExportMemoryAllocateInfo{
			HandleTypes: a.deviceMemory.ExternalMemoryTypes(memoryTypeIndex),
		}

		if exportMemoryAllocInfo.HandleTypes != 0 {
			exportMemoryAllocInfo.Next = allocInfo.Next
			allocInfo.Next = exportMemoryAllocInfo
		}
	}

	var res common.VkResult
	var err error
	var allocIndex int
	for allocIndex = 0; allocIndex < len(allocations); allocIndex++ {
		res, err = a.allocateDedicatedMemoryPage(
			pool,
			size,
			suballocationType,
			memoryTypeIndex,
			allocInfo,
			doMap,
			isMappingAllowed,
			userData,
			&allocations[allocIndex],
		)
		if err != nil {
			break
		}
	}

	if err == nil {
		for registerIndex := 0; registerIndex < len(allocations); registerIndex++ {
			dedicatedAllocations.Register(&allocations[registerIndex])
		}

		a.logger.LogAttrs(context.Background(), slog.LevelDebug, "    Allocated DedicatedMemory", slog.Int("Count", len(allocations)), slog.Int("MemoryTypeIndex", memoryTypeIndex))

		return core1_0.VKSuccess, nil
	}

	// Clean up allocations after error
	for allocIndex > 0 {
		allocIndex--

		a.deviceMemory.FreeVulkanMemory(memoryTypeIndex, allocations[allocIndex].Size(), allocations[allocIndex].memory)
		a.deviceMemory.RemoveAllocation(a.deviceMemory.MemoryTypeIndexToHeapIndex(memoryTypeIndex), allocations[allocIndex].Size())
	}

	return res, err
}

func (a *Allocator) allocateMemoryOfType(
	pool *Pool,
	size int,
	alignment uint,
	dedicatedPreferred bool,
	dedicatedBuffer core1_0.Buffer,
	dedicatedImage core1_0.Image,
	dedicatedBufferOrImageUsage *uint32,
	createInfo *AllocationCreateInfo,
	memoryTypeIndex int,
	suballocationType suballocationType,
	dedicatedAllocations *dedicatedAllocationList,
	blockAllocations *memoryBlockList,
	allocations []Allocation,
) (common.VkResult, error) {
	if len(allocations) == 0 {
		panic("allocateMemoryOfType called with an empty list of target allocations")
	}
	if createInfo == nil {
		panic("allocateMemoryOfType called with a nil createInfo")
	}

	a.logger.LogAttrs(context.Background(), slog.LevelDebug, "Allocator::allocateMemoryOfType", slog.Int("MemoryTypeIndex", memoryTypeIndex), slog.Int("AllocationCount", len(allocations)), slog.Int("Size", size))

	finalCreateInfo := *createInfo

	res, err := a.calculateMemoryTypeParameters(&finalCreateInfo, memoryTypeIndex, size, len(allocations))
	if err != nil {
		return res, err
	}

	mappingAllowed := finalCreateInfo.Flags&(AllocationCreateHostAccessSequentialWrite|AllocationCreateHostAccessRandom) != 0

	if finalCreateInfo.Flags&AllocationCreateDedicatedMemory != 0 {
		return a.allocateDedicatedMemory(
			pool,
			size,
			suballocationType,
			dedicatedAllocations,
			memoryTypeIndex,
			finalCreateInfo.Flags&AllocationCreateMapped != 0,
			mappingAllowed,
			finalCreateInfo.Flags&AllocationCreateCanAlias != 0,
			finalCreateInfo.UserData,
			finalCreateInfo.Priority,
			dedicatedBuffer,
			dedicatedImage,
			dedicatedBufferOrImageUsage,
			allocations,
			blockAllocations.allocOptions,
		)
	}

	canAllocateDedicated := finalCreateInfo.Flags&AllocationCreateNeverAllocate == 0 && (pool == nil || !blockAllocations.HasExplicitBlockSize())

	if canAllocateDedicated {
		// Allocate dedicated memory if requested size is more than half of preferred block size
		if size > blockAllocations.PreferredBlockSize()/2 {
			dedicatedPreferred = true
		}

		// We don't want to create all allocations as dedicated when we're near maximum size, so don't prefer
		// allocations when we're nearing the maximum number of allocations
		if a.deviceMemory.DeviceProperties().Limits.MaxMemoryAllocationCount < math.MaxUint32/4 &&
			a.deviceMemory.AllocationCount() > uint32(a.deviceMemory.DeviceProperties().Limits.MaxMemoryAllocationCount*3/4) {
			dedicatedPreferred = false
		}

		if dedicatedPreferred {
			res, err = a.allocateDedicatedMemory(
				pool,
				size,
				suballocationType,
				dedicatedAllocations,
				memoryTypeIndex,
				finalCreateInfo.Flags&AllocationCreateMapped != 0,
				mappingAllowed,
				finalCreateInfo.Flags&AllocationCreateCanAlias != 0,
				finalCreateInfo.UserData,
				finalCreateInfo.Priority,
				dedicatedBuffer,
				dedicatedImage,
				dedicatedBufferOrImageUsage,
				allocations,
				blockAllocations.allocOptions,
			)
			if err == nil {
				a.logger.Debug("  Allocated as DedicatedMemory")
				return res, err
			}
		}
	}

	res, err = blockAllocations.Allocate(
		size,
		alignment,
		&finalCreateInfo,
		suballocationType,
		allocations,
	)
	if err == nil {
		return res, err
	}

	// Try dedicated memory
	if canAllocateDedicated && !dedicatedPreferred {
		res, err = a.allocateDedicatedMemory(
			pool,
			size,
			suballocationType,
			dedicatedAllocations,
			memoryTypeIndex,
			finalCreateInfo.Flags&AllocationCreateMapped != 0,
			mappingAllowed,
			finalCreateInfo.Flags&AllocationCreateCanAlias != 0,
			finalCreateInfo.UserData,
			finalCreateInfo.Priority,
			dedicatedBuffer,
			dedicatedImage,
			dedicatedBufferOrImageUsage,
			allocations,
			blockAllocations.allocOptions,
		)
		if err == nil {
			a.logger.Debug("  Allocated as DedicatedMemory")
			return res, err
		}
	}

	a.logger.Debug("  AllocateMemory FAILED")
	return res, err
}

func (a *Allocator) multiAllocateMemory(
	memoryRequirements *core1_0.MemoryRequirements,
	requiresDedicatedAllocation bool,
	prefersDedicatedAllocation bool,
	dedicatedBuffer core1_0.Buffer,
	dedicatedImage core1_0.Image,
	dedicatedBufferOrImageUsage *uint32,
	options *AllocationCreateInfo,
	suballocType suballocationType,
	outAllocations []Allocation,
) (common.VkResult, error) {
	err := memutils.CheckPow2(memoryRequirements.Alignment, "core1_0.MemoryRequirements.Alignment")
	if err != nil {
		return core1_0.VKErrorUnknown, err
	}

	if memoryRequirements.Size < 1 {
		return core1_0.VKErrorUnknown, errors.New("provided memory requirement size was not a positive integer")
	}

	res, err := a.calcAllocationParams(options, requiresDedicatedAllocation)
	if err != nil {
		return res, err
	}

	if options.Pool != nil {
		return a.allocateMemoryOfType(
			options.Pool,
			memoryRequirements.Size,
			uint(memoryRequirements.Alignment),
			prefersDedicatedAllocation,
			dedicatedBuffer,
			dedicatedImage,
			dedicatedBufferOrImageUsage,
			options,
			options.Pool.blockList.memoryTypeIndex,
			suballocType,
			&options.Pool.dedicatedAllocations,
			&options.Pool.blockList,
			outAllocations,
		)
	}

	memoryBits := memoryRequirements.MemoryTypeBits
	memoryTypeIndex, res, err := a.findMemoryTypeIndex(memoryBits, options, dedicatedBufferOrImageUsage)
	if err != nil {
		return res, err
	}

	for err == nil {
		blockList := a.memoryBlockLists[memoryTypeIndex]
		if blockList == nil {
			return core1_0.VKErrorUnknown, errors.Errorf("attempted to allocate from unsupported memory type index %d", memoryTypeIndex)
		}

		res, err = a.allocateMemoryOfType(
			nil,
			memoryRequirements.Size,
			uint(memoryRequirements.Alignment),
			requiresDedicatedAllocation || prefersDedicatedAllocation,
			dedicatedBuffer,
			dedicatedImage,
			dedicatedBufferOrImageUsage,
			options,
			memoryTypeIndex,
			suballocType,
			a.dedicatedAllocations[memoryTypeIndex],
			blockList,
			outAllocations,
		)

		// Allocation succeeded (or irrevocably failed)
		if err == nil || res == core1_0.VKErrorUnknown {
			return res, err
		}

		// Remove memory type index from possibilities
		memoryBits &= ^(1 << memoryTypeIndex)
		// Find a new memorytypeindex
		memoryTypeIndex, _, err = a.findMemoryTypeIndex(memoryBits, options, dedicatedBufferOrImageUsage)
	}

	return core1_0.VKErrorOutOfDeviceMemory, core1_0.VKErrorOutOfDeviceMemory.ToError()
}

// AllocateMemory allocates memory according to provided memory requirements and AllocationCreateInfo
// and assigns it to a provided Allocation object.
//
// Because this method does not accept a core1_0.BufferCreateInfo or core1_0.ImageCreateInfo, it can't be
// all that intelligent about how it chooses the memory type. You can help it by providing RequiredFlags
// and PreferredFlags in the AllocationCreateInfo object.
//
// Return errors will mostly come from Vulkan, but key parameter validation will also return errors:
// if any parameter is nil, if memoryRequirements has a zero or negative size, if memoryRequirements has
// an alignment that is not a power of 2, or if outAlloc already contains an active allocation.
//
// memoryRequirements - A pointer to a populated core1_0.MemoryRequirements object indicating the size and other
// properties the allocation should have
//
// o - Options indicating the sort of allocation being made
//
// outAlloc - A pointer to a valid Allocation that has not yet been allocated to (or has been recently freed). Passing
// an Allocation object to this method will cause it to escape to the heap, but because freed allocations can
// be reused, it is valid to get them from a sync.Pool
func (a *Allocator) AllocateMemory(memoryRequirements *core1_0.MemoryRequirements, o AllocationCreateInfo, outAlloc *Allocation) (common.VkResult, error) {
	a.logger.Debug("Allocator::AllocateMemory")

	if outAlloc == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to allocate into a nil allocation")
	} else if memoryRequirements == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to allocate with nil memory requirements")
	} else if outAlloc.memory != nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to overwrite an unfreed allocation")
	}

	// Attempt to create a one-length slice for the provided alloc pointer
	outAllocSlice := unsafe.Slice(outAlloc, 1)
	return a.multiAllocateMemory(
		memoryRequirements,
		false,
		false,
		nil, nil, nil,
		&o,
		SuballocationUnknown,
		outAllocSlice,
	)
}

// AllocateMemorySlice allocates several tranches of memory according to a provided core1_0.MemoryRequirements and
// AllocationCreateInfo and assigns them to a slice of provided Allocation objects.
//
// Because this method does not accept a core1_0.BufferCreateInfo or core1_0.ImageCreateInfo, it can't be
// all that intelligent about how it chooses the memory type. You can help it by providing RequiredFlags
// and PreferredFlags in the AllocationCreateInfo object.
//
// Return errors will mostly come from Vulkan, but key parameter validation will also return errors:
// if any memoryRequirements is nil, if memoryRequirements has a zero or negative size, if memoryRequirements has
// an alignment that is not a power of 2, or if any element of allocations already contains an active allocation.
//
// memoryRequirements - A pointer to a populated core1_0.MemoryRequirements object indicating the size and other
// properties the allocation should have
//
// o - Options indicating the sort of allocation being made
//
// outAlloc - A slice of Allocation objects that have not yet been allocated to (or have been recently freed). Passing
// a slice to this method will cause it to escape to the heap.
func (a *Allocator) AllocateMemorySlice(memoryRequirements *core1_0.MemoryRequirements, o AllocationCreateInfo, allocations []Allocation) (common.VkResult, error) {
	a.logger.Debug("Allocator::AllocateMemorySlice")

	if memoryRequirements == nil {
		return core1_0.VKErrorUnknown, errors.New("attempt to allocate with nil memory requirements")
	}

	if len(allocations) == 0 {
		return core1_0.VKSuccess, nil
	}

	for allocIndex := range allocations {
		if allocations[allocIndex].memory != nil {
			return core1_0.VKErrorUnknown, errors.Errorf("attempted to overwrite an unfreed allocation at index %d", allocIndex)
		}
	}

	return a.multiAllocateMemory(
		memoryRequirements,
		false,
		false,
		nil,
		nil,
		nil,
		&o,
		SuballocationUnknown,
		allocations,
	)
}

// AllocateMemoryForBuffer allocates memory ideal for the provided core1_0.Buffer and AllocationCreateInfo and
// assigns it to a provided Allocation object. The buffer is not bound to the allocated memory.
//
// Return errors will mostly come from Vulkan, but key parameter validation will also return errors:
// if any parameter is nil or if outAlloc already contains an active allocation.
//
// buffer - A core1_0.Buffer object indicating the buffer that memory will be allocated for
//
// o - Options indicating the sort of allocation being made
//
// outAlloc - A pointer to a valid Allocation that has not yet been allocated to (or has been recently freed). Passing
// an Allocation object to this method will cause it to escape to the heap, but because freed allocations can
// be reused, it is valid to get them from a sync.Pool
func (a *Allocator) AllocateMemoryForBuffer(buffer core1_0.Buffer, o AllocationCreateInfo, outAlloc *Allocation) (common.VkResult, error) {
	a.logger.Debug("Allocator::AllocateMemoryForBuffer")

	if buffer == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to allocate for a nil buffer")
	} else if outAlloc == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to allocate into a nil allocation")
	} else if outAlloc.memory != nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to overwrite an unfreed allocation")
	}

	var memReqs core1_0.MemoryRequirements
	requiresDedicated, prefersDedicated, err := a.getBufferMemoryRequirements(buffer, &memReqs)
	if err != nil {
		return core1_0.VKErrorUnknown, err
	}

	// Attempt to create a one-length slice for the provided alloc pointer
	outAllocSlice := unsafe.Slice(outAlloc, 1)
	return a.multiAllocateMemory(
		&memReqs,
		requiresDedicated,
		prefersDedicated,
		buffer,
		nil,
		nil,
		&o,
		SuballocationBuffer,
		outAllocSlice,
	)
}

func (a *Allocator) getBufferMemoryRequirements(buffer core1_0.Buffer, memoryRequirements *core1_0.MemoryRequirements) (requiresDedicated, prefersDedicated bool, err error) {
	if a.extensionData.DedicatedAllocations && a.extensionData.GetMemoryRequirements != nil {
		dedicatedReqs := khr_dedicated_allocation.MemoryDedicatedRequirements{}
		memReqs := core1_1.MemoryRequirements2{
			NextOutData: common.NextOutData{
				Next: &dedicatedReqs,
			},
		}

		err = a.extensionData.GetMemoryRequirements.BufferMemoryRequirements2(
			core1_1.BufferMemoryRequirementsInfo2{
				Buffer: buffer,
			},
			&memReqs)
		if err != nil {
			return false, false, err
		}

		*memoryRequirements = memReqs.MemoryRequirements
		return dedicatedReqs.RequiresDedicatedAllocation, dedicatedReqs.PrefersDedicatedAllocation, nil
	}

	*memoryRequirements = *buffer.MemoryRequirements()
	return false, false, nil
}

// AllocateMemoryForImage allocates memory ideal for the provided core1_0.Image and AllocationCreateInfo and
// assigns it to a provided Allocation object. The image is not bound to the allocated memory.
//
// Return errors will mostly come from Vulkan, but key parameter validation will also return errors:
// if any parameter is nil or if outAlloc already contains an active allocation.
//
// image - A core1_0.Image object indicating the image that memory will be allocated for
//
// o - Options indicating the sort of allocation being made
//
// outAlloc - A pointer to a valid Allocation that has not yet been allocated to (or has been recently freed). Passing
// an Allocation object to this method will cause it to escape to the heap, but because freed allocations can
// be reused, it is valid to get them from a sync.Pool
func (a *Allocator) AllocateMemoryForImage(image core1_0.Image, o AllocationCreateInfo, outAlloc *Allocation) (common.VkResult, error) {
	a.logger.Debug("Allocator::AllocateMemoryForImage")

	if image == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to allocate for a nil image")
	} else if outAlloc == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to allocate into a nil allocation")
	} else if outAlloc.memory != nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to overwrite an unfreed allocation")
	}

	var memReqs core1_0.MemoryRequirements
	requiresDedicated, prefersDedicated, err := a.getImageMemoryRequirements(image, &memReqs)
	if err != nil {
		return core1_0.VKErrorUnknown, err
	}

	// Attempt to create a one-length slice for the provided alloc pointer
	outAllocSlice := unsafe.Slice(outAlloc, 1)
	return a.multiAllocateMemory(
		&memReqs,
		requiresDedicated,
		prefersDedicated,
		nil,
		image,
		nil,
		&o,
		SuballocationImageUnknown,
		outAllocSlice,
	)
}

func (a *Allocator) getImageMemoryRequirements(image core1_0.Image, memoryRequirements *core1_0.MemoryRequirements) (requiresDedicated, prefersDedicated bool, err error) {
	if a.extensionData.DedicatedAllocations && a.extensionData.GetMemoryRequirements != nil {
		dedicatedReqs := khr_dedicated_allocation.MemoryDedicatedRequirements{}
		memReqs := core1_1.MemoryRequirements2{
			NextOutData: common.NextOutData{
				Next: &dedicatedReqs,
			},
		}

		err = a.extensionData.GetMemoryRequirements.ImageMemoryRequirements2(
			core1_1.ImageMemoryRequirementsInfo2{
				Image: image,
			},
			&memReqs)
		if err != nil {
			return false, false, err
		}

		*memoryRequirements = memReqs.MemoryRequirements
		return dedicatedReqs.RequiresDedicatedAllocation, dedicatedReqs.PrefersDedicatedAllocation, nil
	}

	*memoryRequirements = *image.MemoryRequirements()
	return false, false, nil
}

// FreeAllocationSlice is a convenience method that is roughly equivalent to calling Allocation.Free on an
// entire slice of allocations.
func (a *Allocator) FreeAllocationSlice(allocs []Allocation) error {
	a.logger.Debug("Allocator::FreeAllocationSlice")

	if len(allocs) == 0 {
		return nil
	}

	return a.multiFreeMemory(allocs)
}

func (a *Allocator) multiFreeMemory(allocs []Allocation) error {
	for i := 0; i < len(allocs); i++ {
		if InitializeAllocs {
			allocs[i].fillAllocation(destroyedFillPattern)
		}

		err := a.freeSingleAllocation(&allocs[i])
		if err != nil {
			return err
		}
		allocs[i].memory = nil
	}

	return nil
}

func (a *Allocator) freeSingleAllocation(alloc *Allocation) error {
	switch alloc.allocationType {
	case allocationTypeBlock:
		pool := alloc.ParentPool()
		if pool != nil {
			return pool.blockList.Free(alloc)
		}

		return a.memoryBlockLists[alloc.MemoryTypeIndex()].Free(alloc)
	case allocationTypeDedicated:
		a.freeDedicatedMemory(alloc)
		return nil
	}

	panic(fmt.Sprintf("unexpected allocation type: %s", alloc.allocationType.String()))
}

func (a *Allocator) freeDedicatedMemory(alloc *Allocation) {
	if alloc == nil {
		panic("attempted to free nil allocation")
	} else if alloc.allocationType != allocationTypeDedicated {
		panic("attempted to free dedicated memory for a non-dedicated allocation")
	}

	memoryTypeIndex := alloc.MemoryTypeIndex()
	parentPool := alloc.dedicatedData.parentPool
	if parentPool == nil {
		// Default pool
		a.dedicatedAllocations[memoryTypeIndex].Unregister(alloc)
	} else {
		// Custom pool
		parentPool.dedicatedAllocations.Unregister(alloc)
	}

	a.deviceMemory.FreeVulkanMemory(memoryTypeIndex, alloc.Size(), alloc.memory)

	heapIndex := a.deviceMemory.MemoryTypeIndexToHeapIndex(memoryTypeIndex)
	a.deviceMemory.RemoveAllocation(heapIndex, alloc.Size())

	a.logger.LogAttrs(context.Background(), slog.LevelDebug, "    Freed DedicatedMemory", slog.Int("MemoryTypeIndex", memoryTypeIndex))
}

// FlushAllocationSlice is a convenience method that is roughly equivalent to, but may be slightly more
// performant than, calling Allocation.Flush on a slice of allocations.
func (a *Allocator) FlushAllocationSlice(allocations []Allocation, offsets []int, sizes []int) (common.VkResult, error) {
	a.logger.Debug("Allocator::FlushAllocationSlice")

	if len(allocations) != len(offsets) {
		return core1_0.VKErrorUnknown, errors.Errorf("allocations contains %d elements but offsets contains %d elements- they must be equal", len(allocations), len(offsets))
	}

	if len(allocations) != len(sizes) {
		return core1_0.VKErrorUnknown, errors.Errorf("allocations contains %d elements but sizes contains %d elements- they must be equal", len(allocations), len(sizes))
	}

	if len(allocations) == 0 {
		return core1_0.VKSuccess, nil
	}

	return a.flushOrInvalidateAllocations(allocations, offsets, sizes, vulkan.CacheOperationFlush)
}

// InvalidateAllocationSlice is a convenience method that is roughly equivalent to, but may be slightly more
// performant than, calling Allocation.Invalidate on a slice of allocations
func (a *Allocator) InvalidateAllocationSlice(allocations []Allocation, offsets []int, sizes []int) (common.VkResult, error) {
	a.logger.Debug("Allocator::InvalidateAllocationSlize")

	if len(allocations) != len(offsets) {
		return core1_0.VKErrorUnknown, errors.Errorf("allocations contains %d elements but offsets contains %d elements- they must be equal", len(allocations), len(offsets))
	}

	if len(allocations) != len(sizes) {
		return core1_0.VKErrorUnknown, errors.Errorf("allocations contains %d elements but sizes contains %d elements- they must be equal", len(allocations), len(sizes))
	}

	if len(allocations) == 0 {
		return core1_0.VKSuccess, nil
	}

	return a.flushOrInvalidateAllocations(allocations, offsets, sizes, vulkan.CacheOperationInvalidate)
}

func (a *Allocator) flushOrInvalidateAllocations(allocations []Allocation, offsets []int, sizes []int, operation vulkan.CacheOperation) (common.VkResult, error) {

	ranges := make([]core1_0.MappedMemoryRange, 0, len(allocations))

	for i := 0; i < len(allocations); i++ {
		var mappedRange core1_0.MappedMemoryRange
		success, err := allocations[i].flushOrInvalidateRange(offsets[i], sizes[i], &mappedRange)
		if err != nil {
			return core1_0.VKErrorUnknown, err
		}

		if success {
			ranges = append(ranges, mappedRange)
		}
	}

	if len(ranges) == 0 {
		return core1_0.VKSuccess, nil
	}

	switch operation {
	case vulkan.CacheOperationFlush:
		return a.device.FlushMappedMemoryRanges(ranges)
	case vulkan.CacheOperationInvalidate:
		return a.device.InvalidateMappedMemoryRanges(ranges)
	}

	panic(fmt.Sprintf("unknown cache operation %s", operation.String()))
}

// CheckCorruption verifies the internal consistency and integrity of a set of memory types indicated
// by the memoryTypeBits mask. This will return VKErrorFeatureNotPresent if there are no memory types
// in the mask that have corruption-checking active, or VKSuccess if at least one memory type was
// checked for corruption, and no corruption was found. All other errors indicate some form of corruption.
//
// For corruption detection to be active, the memory type has to support memory mapping, and the binary needs
// to have been built with the debug_mem_utils build tag
func (a *Allocator) CheckCorruption(memoryTypeBits uint32) (common.VkResult, error) {
	a.logger.Debug("Allocator::CheckCorruption")

	res := core1_0.VKErrorFeatureNotPresent

	// Process default pools
	for memoryTypeIndex := 0; memoryTypeIndex < a.deviceMemory.MemoryTypeCount(); memoryTypeIndex++ {
		list := a.memoryBlockLists[memoryTypeIndex]
		if list != nil {
			innerRes, err := list.CheckCorruption()
			switch innerRes {
			case core1_0.VKErrorFeatureNotPresent:
				break
			case core1_0.VKSuccess:
				res = core1_0.VKSuccess
			default:
				if err != nil {
					return res, err
				}
			}
		}
	}

	// Process custom pools
	poolRes, err := a.checkCustomPools(memoryTypeBits)
	if poolRes == core1_0.VKErrorFeatureNotPresent {
		return res, res.ToError()
	}

	return poolRes, err
}

func (a *Allocator) checkCustomPools(memoryTypeBits uint32) (common.VkResult, error) {
	a.poolsMutex.RLock()
	defer a.poolsMutex.RUnlock()

	res := core1_0.VKErrorFeatureNotPresent
	for pool := a.pools; pool != nil; pool = pool.next {
		memBit := uint32(1 << pool.blockList.memoryTypeIndex)
		if memBit&memoryTypeBits == 0 {
			continue
		}

		innerRes, err := pool.blockList.CheckCorruption()
		switch innerRes {
		case core1_0.VKErrorFeatureNotPresent:
			break
		case core1_0.VKSuccess:
			res = core1_0.VKSuccess
		default:
			if err != nil {
				return res, err
			}
		}
	}

	return res, res.ToError()
}

// CreatePool creates a custom memory pool that can be used for unusual allocation cases. See Pool for more
// information.
//
// VKErrorFeatureNotPresent will be returned if the provided MemoryTypeIndex is invalid or not present in
// this Allocator. Other errors are for other obvious forms of parameter validation.
func (a *Allocator) CreatePool(createInfo PoolCreateInfo) (*Pool, common.VkResult, error) {

	a.logger.LogAttrs(context.Background(), slog.LevelDebug, "Allocator::CreatePool",
		slog.Int("MemoryTypeIndex", createInfo.MemoryTypeIndex),
		slog.String("Flags", createInfo.Flags.String()),
	)

	if createInfo.MaxBlockCount == 0 {
		createInfo.MaxBlockCount = math.MaxInt
	}
	if createInfo.MinBlockCount > createInfo.MaxBlockCount {
		return nil, core1_0.VKErrorUnknown, errors.Errorf("provided MinBlockCount %d was greater than provided MaxBlockCount %d", createInfo.MinBlockCount, createInfo.MaxBlockCount)
	}

	memTypeBits := uint32(1 << createInfo.MemoryTypeIndex)
	if createInfo.MemoryTypeIndex >= a.deviceMemory.MemoryTypeCount() || memTypeBits&a.globalMemoryTypeBits == 0 {
		return nil, core1_0.VKErrorFeatureNotPresent, core1_0.VKErrorFeatureNotPresent.ToError()
	}

	if createInfo.MinAllocationAlignment > 0 {
		err := memutils.CheckPow2(createInfo.MinAllocationAlignment, "createInfo.MinAllocationAlignment")
		if err != nil {
			return nil, core1_0.VKErrorUnknown, err
		}
	}

	if createInfo.Priority < 0 || createInfo.Priority > 1 {
		return nil, core1_0.VKErrorUnknown, errors.Errorf("invalid Priority value %f: priority must be between 0 and 1, inclusive", createInfo.Priority)
	}

	preferredBlockSize := a.calculatePreferredBlockSize(createInfo.MemoryTypeIndex)
	pool := &Pool{
		parentAllocator: a,
		logger:          a.logger,
	}
	blockSize := preferredBlockSize
	if createInfo.BlockSize != 0 {
		blockSize = createInfo.BlockSize
	}
	bufferImageGranularity := 1
	if createInfo.Flags&PoolCreateIgnoreBufferImageGranularity == 0 {
		bufferImageGranularity = a.deviceMemory.CalculateBufferImageGranularity()
	}

	alignment := a.deviceMemory.MemoryTypeMinimumAlignment(createInfo.MemoryTypeIndex)
	if createInfo.MinAllocationAlignment > alignment {
		alignment = createInfo.MinAllocationAlignment
	}

	pool.blockList.Init(
		a.useMutex,
		a,
		pool,
		createInfo.MemoryTypeIndex,
		blockSize,
		createInfo.MinBlockCount,
		createInfo.MaxBlockCount,
		bufferImageGranularity,
		createInfo.BlockSize != 0,
		createInfo.Flags&PoolCreateAlgorithmMask,
		createInfo.Priority,
		alignment,
		createInfo.MemoryAllocateNext,
	)

	res, err := pool.blockList.CreateMinBlocks()
	if err != nil {
		destroyErr := pool.Destroy()
		if err != nil {
			a.logger.LogAttrs(context.Background(), slog.LevelError, "error attempting to destroy pool after creation failure", slog.Any("error", destroyErr))
		}
		return nil, res, err
	}

	a.poolsMutex.Lock()
	defer a.poolsMutex.Unlock()

	err = pool.setID(a.nextPoolId)
	if err != nil {
		destroyErr := pool.destroyAfterLock()
		if destroyErr != nil {
			a.logger.LogAttrs(context.Background(), slog.LevelError, "error attempting to destroy pool after failing to set id", slog.Any("error", destroyErr))
		}

		return nil, core1_0.VKErrorUnknown, err
	}
	pool.next = a.pools

	if a.pools != nil {
		a.pools.prev = pool
	}

	a.pools = pool

	return pool, core1_0.VKSuccess, nil
}

// BeginDefragmentation populates a provided DefragmentationContext object and prepares it to run a defragmentation
// process. See the README for more information about defragmentation.
//
// This method will return an error if defragContext is nil. defragContext will escape to the heap if passed
// to this method, but it can be reused multiple times.
func (a *Allocator) BeginDefragmentation(o DefragmentationInfo, defragContext *DefragmentationContext) (common.VkResult, error) {
	a.logger.Debug("Allocator::BeginDefragmentation")

	if defragContext == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to begin defragmentation with a nil context")
	}

	var err error
	if o.Pool != nil {
		// Linear can't defragment
		if o.Pool.blockList.Algorithm()&PoolCreateLinearAlgorithm != 0 {
			return core1_0.VKErrorFeatureNotPresent, core1_0.VKErrorFeatureNotPresent.ToError()
		}
		err = defragContext.initForPool(o.Pool, &o)
	} else {
		err = defragContext.initForAllocator(a, &o)
	}

	if err != nil {
		return core1_0.VKErrorUnknown, err
	}

	return core1_0.VKSuccess, nil
}

// CreateBuffer creates a brand new core1_0.Buffer for the provided core1_0.BufferCreateInfo, allocates memory
// to support it using the core1_0.BufferCreateInfo and AllocationCreateInfo, assigns the allocation to the provided
// Allocation, and binds the Buffer to the Allocation.
//
// This method will return VKErrorExtensionNotPresent if the bufferInfo.Usage attempts to use Shader Device Address
// but the khr_buffer_device_address extension is not available (and core 1.2 is not active). Validation errors
// will return if any parameter is nil or if outAlloc already contains an active allocation. Other VKErrorUnknown
// errors indicate an issue with the bufferInfo object, such as 0 size or a requested size that cannot fit within
// this Allocation. Other errors come from Vulkan.
//
// bufferInfo - A core1_0.BufferCreateInfo describing the Buffer to create
//
// allocInfo - Options indicating the sort of allocation being made
//
// outAlloc - A pointer to a valid Allocation that has not yet been allocated to (or has been recently freed). Passing
// an Allocation object to this method will cause it to escape to the heap, but because freed allocations can
// be reused, it is valid to get them from a sync.Pool
func (a *Allocator) CreateBuffer(bufferInfo core1_0.BufferCreateInfo, allocInfo AllocationCreateInfo, outAlloc *Allocation) (core1_0.Buffer, common.VkResult, error) {
	a.logger.Debug("Allocator::CreateBuffer")

	if outAlloc == nil {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to allocate into a nil Allocation")
	} else if outAlloc.memory != nil {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to overwrite an unfreed allocation")
	}

	return a.createBuffer(&bufferInfo, &allocInfo, 0, outAlloc)
}

// CreateBufferWithAlignment creates a brand new core1_0.Buffer for the provided core1_0.BufferCreateInfo, allocates memory
// to support it using the core1_0.BufferCreateInfo and AllocationCreateInfo, assigns the allocation to the provided
// Allocation, and binds the Buffer to the Allocation.
//
// This method will return VKErrorExtensionNotPresent if the bufferInfo.Usage attempts to use Shader Device Address
// but the khr_buffer_device_address extension is not available (and core 1.2 is not active). Validation errors
// will return if any parameter is nil or if outAlloc already contains an active allocation. Other VKErrorUnknown
// errors indicate an issue with the bufferInfo object, such as 0 size, a requested size that cannot fit within
// this Allocation, or a minimum alignment that is not a power of 2. Other errors come from Vulkan.
//
// bufferInfo - A core1_0.BufferCreateInfo describing the Buffer to create
//
// allocInfo - Options indicating the sort of allocation being made
//
// minAlignment - The allocated memory will use this alignment if it is larger than the alignment
// for the buffer's memory requirements
//
// outAlloc - A pointer to a valid Allocation that has not yet been allocated to (or has been recently freed). Passing
// an Allocation object to this method will cause it to escape to the heap, but because freed allocations can
// be reused, it is valid to get them from a sync.Pool
func (a *Allocator) CreateBufferWithAlignment(bufferInfo core1_0.BufferCreateInfo, allocInfo AllocationCreateInfo, minAlignment int, outAlloc *Allocation) (core1_0.Buffer, common.VkResult, error) {
	a.logger.Debug("Allocator::CreateBufferWithAlignment")

	if outAlloc == nil {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to allocate into a nil Allocation")
	} else if outAlloc.memory != nil {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to overwrite an unfreed allocation")
	}

	err := memutils.CheckPow2(minAlignment, "minAlignment")
	if err != nil {
		return nil, core1_0.VKErrorUnknown, err
	}

	return a.createBuffer(&bufferInfo, &allocInfo, minAlignment, outAlloc)
}

func (a *Allocator) createBuffer(bufferInfo *core1_0.BufferCreateInfo, allocationInfo *AllocationCreateInfo, minAlignment int, outAlloc *Allocation) (buffer core1_0.Buffer, res common.VkResult, err error) {

	if bufferInfo == nil {
		panic("nil bufferInfo")
	}
	if allocationInfo == nil {
		panic("nil allocationInfo")
	}
	if outAlloc == nil {
		panic("nil outAlloc")
	}

	if bufferInfo.Size == 0 {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to allocate a 0-sized buffer")
	} else if bufferInfo.Usage&khr_buffer_device_address.BufferUsageShaderDeviceAddress != 0 && a.extensionData.BufferDeviceAddress == nil {
		return nil, core1_0.VKErrorExtensionNotPresent, errors.New("attempted to use BufferUsageShaderDeviceAddress, but khr_buffer_device_address is not loaded")
	}

	buffer, res, err = a.device.CreateBuffer(
		a.allocationCallbacks,
		*bufferInfo,
	)
	if err != nil {
		return nil, res, err
	}
	defer func() {
		if err != nil {
			buffer.Destroy(a.allocationCallbacks)
		}
	}()

	var memReqs core1_0.MemoryRequirements
	requiresDedicated, prefersDedicated, err := a.getBufferMemoryRequirements(buffer, &memReqs)
	if err != nil {
		return buffer, core1_0.VKErrorUnknown, err
	}

	if minAlignment > memReqs.Alignment {
		memReqs.Alignment = minAlignment
	}

	bufferOrImage := uint32(bufferInfo.Usage)

	// Attempt to create a one-length slice for the provided alloc pointer
	outAllocSlice := unsafe.Slice(outAlloc, 1)
	res, err = a.multiAllocateMemory(
		&memReqs,
		requiresDedicated,
		prefersDedicated,
		buffer,
		nil,
		&bufferOrImage,
		allocationInfo,
		SuballocationBuffer,
		outAllocSlice,
	)
	if err != nil {
		return buffer, res, err
	}
	defer func() {
		if err != nil {
			freeErr := outAlloc.free()
			if freeErr != nil {
				a.logger.LogAttrs(context.Background(), slog.LevelError, "failed to free temporary alloc after error", slog.Any("error", freeErr))
			}
		}
	}()

	if allocationInfo.Flags&AllocationCreateDontBind == 0 {
		res, err = outAlloc.BindBufferMemory(buffer)
	}

	return buffer, res, err
}

// CalculateStatistics populates an AllocatorStatistics struct with the current state of this Allocator.
// This method will return an error if the provided stats object is nil.
func (a *Allocator) CalculateStatistics(stats *AllocatorStatistics) error {
	if stats == nil {
		return errors.New("attemped to calculate statistics and store to nil statistics object")
	}

	stats.Total.Clear()
	for i := 0; i < common.MaxMemoryTypes; i++ {
		stats.MemoryTypes[i].Clear()
		if i < len(a.memoryBlockLists) && a.memoryBlockLists[i] != nil {
			a.memoryBlockLists[i].AddDetailedStatistics(&stats.MemoryTypes[i])
		}
	}
	for i := 0; i < common.MaxMemoryHeaps; i++ {
		stats.MemoryHeaps[i].Clear()
	}

	a.calculatePoolStatistics(stats)

	for i := 0; i < len(a.dedicatedAllocations); i++ {
		if a.dedicatedAllocations[i] != nil {
			a.dedicatedAllocations[i].AddDetailedStatistics(&stats.MemoryTypes[i])
		}
	}

	for i := 0; i < a.deviceMemory.MemoryTypeCount(); i++ {
		memHeapIndex := a.deviceMemory.MemoryTypeIndexToHeapIndex(i)
		stats.MemoryHeaps[memHeapIndex].AddDetailedStatistics(&stats.MemoryTypes[i])
		stats.Total.AddDetailedStatistics(&stats.MemoryTypes[i])
	}

	if stats.Total.AllocationCount > 0 && stats.Total.AllocationSizeMin > stats.Total.AllocationSizeMax {
		panic(fmt.Sprintf("somehow the minimum allocation size %d is greater than the maximum allocation size %d", stats.Total.AllocationSizeMin, stats.Total.AllocationSizeMax))
	}

	if stats.Total.UnusedRangeCount > 0 && stats.Total.UnusedRangeSizeMin > stats.Total.UnusedRangeSizeMax {
		panic(fmt.Sprintf("somehow the minimum unused range size %d is greater than the maximum unused range size %d", stats.Total.UnusedRangeSizeMin, stats.Total.UnusedRangeSizeMax))
	}

	return nil
}

func (a *Allocator) calculatePoolStatistics(stats *AllocatorStatistics) {
	a.poolsMutex.RLock()
	defer a.poolsMutex.RUnlock()

	pool := a.pools
	for pool != nil {
		pool.blockList.AddDetailedStatistics(&stats.MemoryTypes[pool.blockList.memoryTypeIndex])
		pool.dedicatedAllocations.AddDetailedStatistics(&stats.MemoryTypes[pool.blockList.memoryTypeIndex])
		pool = pool.next
	}
}

// BuildStatsString will return a json string representing the full state of this Allocator for debugging
// purposes.
//
// detailed - A boolean indicating how much data to provide. If false, basic statistical information will
// be printed. If true, full information about all allocations will be printed.
func (a *Allocator) BuildStatsString(detailed bool) string {
	writer := jwriter.NewWriter()
	objState := writer.Object()
	a.buildGeneralObj(&objState)

	var stats AllocatorStatistics
	err := a.CalculateStatistics(&stats)
	if err != nil {
		panic(fmt.Sprintf("CalculateStatistics failed: %+v", err))
	}
	stats.Total.PrintJson(objState.Name("Total"))

	memoryInfoObj := objState.Name("MemoryInfo").Object()
	for i := 0; i < a.deviceMemory.MemoryHeapCount(); i++ {
		key := fmt.Sprintf("Heap %d", i)
		a.buildHeapInfo(memoryInfoObj.Name(key), i, &stats)
	}
	memoryInfoObj.End()

	if detailed {
		a.buildDefaultPools(objState.Name("DefaultPools"))
		a.buildCustomPools(objState.Name("CustomPools"))
	}
	objState.End()
	return string(writer.Bytes())
}

func (a *Allocator) buildDefaultPools(writer *jwriter.Writer) {
	objState := writer.Object()
	defer objState.End()

	for i := 0; i < a.deviceMemory.MemoryTypeCount(); i++ {
		blockList := a.memoryBlockLists[i]
		if blockList != nil {
			key := fmt.Sprintf("Type %d", i)
			blockObj := objState.Name(key).Object()
			blockObj.Name("PreferredBlockSize").Int(blockList.PreferredBlockSize())
			blockList.PrintDetailedMap(blockObj.Name("Blocks"))
			a.dedicatedAllocations[i].BuildStatsString(blockObj.Name("DedicatedAllocations"))
			blockObj.End()
		}
	}
}

func (a *Allocator) buildCustomPools(writer *jwriter.Writer) {
	a.poolsMutex.RLock()
	defer a.poolsMutex.RUnlock()

	objState := writer.Object()
	defer objState.End()

	if a.pools == nil {
		return
	}

	for i := 0; i < a.deviceMemory.MemoryTypeCount(); i++ {
		var arrayState *jwriter.ArrayState
		poolIndex := 0

		for pool := a.pools; pool != nil; pool = pool.next {
			blockList := &pool.blockList
			if blockList.MemoryTypeIndex() == i {
				if arrayState == nil {
					key := fmt.Sprintf("Type %d", i)
					arrayStateObj := objState.Name(key).Array()
					arrayState = &arrayStateObj
				}

				poolObjState := arrayState.Object()
				name := strconv.Itoa(poolIndex)
				if pool.Name() != "" {
					name = fmt.Sprintf("%s - %s", name, pool.Name())
				}

				poolObjState.Name("Name").String(name)
				poolObjState.Name("PreferredBlockSize").Int(blockList.PreferredBlockSize())
				blockList.PrintDetailedMap(poolObjState.Name("Blocks"))

				pool.dedicatedAllocations.BuildStatsString(poolObjState.Name("DedicatedAllocations"))
				poolObjState.End()
			}
		}

		if arrayState != nil {
			arrayState.End()
		}
	}
}

func (a *Allocator) buildHeapInfo(writer *jwriter.Writer, heapIndex int, stats *AllocatorStatistics) {
	heapInfo := writer.Object()
	defer heapInfo.End()

	heapProps := a.deviceMemory.MemoryHeapProperties(heapIndex)
	heapInfo.Name("Flags").String(heapProps.Flags.String())
	heapInfo.Name("Size").Int(heapProps.Size)

	var budget vulkan.Budget
	a.deviceMemory.HeapBudget(heapIndex, &budget)
	budget.PrintJson(heapInfo.Name("Budget"))
	stats.MemoryHeaps[heapIndex].PrintJson(heapInfo.Name("Stats"))

	memoryPools := heapInfo.Name("MemoryPools").Object()
	defer memoryPools.End()

	for i := 0; i < a.deviceMemory.MemoryTypeCount(); i++ {
		if a.deviceMemory.MemoryTypeIndexToHeapIndex(i) == heapIndex {
			key := fmt.Sprintf("Type %d", i)
			memoryPoolObj := memoryPools.Name(key).Object()

			typeProps := a.deviceMemory.MemoryTypeProperties(i)
			memoryPoolObj.Name("Flags").String(typeProps.PropertyFlags.String())
			stats.MemoryTypes[i].PrintJson(memoryPoolObj.Name("Stats"))

			memoryPoolObj.End()
		}
	}
}

func (a *Allocator) buildGeneralObj(objState *jwriter.ObjectState) {
	generalObj := objState.Name("General").Object()
	defer generalObj.End()

	deviceProps := a.deviceMemory.DeviceProperties()
	generalObj.Name("API").String("Vulkan")
	generalObj.Name("apiVersion").String(deviceProps.APIVersion.String())
	generalObj.Name("GPU").String(deviceProps.DriverName)
	generalObj.Name("deviceType").String(deviceProps.DriverType.String())
	generalObj.Name("maxMemoryAllocationCount").Int(deviceProps.Limits.MaxMemoryAllocationCount)
	generalObj.Name("bufferImageGranularity").Int(deviceProps.Limits.BufferImageGranularity)
	generalObj.Name("nonCoherentAtomSize").Int(deviceProps.Limits.NonCoherentAtomSize)
	generalObj.Name("memoryHeapCount").Int(a.deviceMemory.MemoryHeapCount())
	generalObj.Name("memoryTypeCount").Int(a.deviceMemory.MemoryTypeCount())
}

// CreateImage creates a brand new core1_0.Image for the provided core1_0.ImageCreateInfo, allocates memory
// to support it using the core1_0.ImageCreateInfo and AllocationCreateInfo, assigns the allocation to the provided
// Allocation, and binds the Image to the Allocation.
//
// Validation errors will return if outALloc is nil or if outAlloc already contains an active allocation.
// Other VKErrorUnknown errors indicate an issue with the imageInfo object, such as 0-sized dimensions
//
// imageInfo - A core1_0.ImageCreateInfo describing the Image to create
//
// allocInfo - Options indicating the sort of allocation being made
//
// outAlloc - A pointer to a valid Allocation that has not yet been allocated to (or has been recently freed). Passing
// an Allocation object to this method will cause it to escape to the heap, but because freed allocations can
// be reused, it is valid to get them from a sync.Pool
func (a *Allocator) CreateImage(imageInfo core1_0.ImageCreateInfo, allocInfo AllocationCreateInfo, outAlloc *Allocation) (image core1_0.Image, res common.VkResult, err error) {
	a.logger.Debug("Allocator::CreateImage")

	if outAlloc == nil {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to allocate into a nil Allocation")
	} else if outAlloc.memory != nil {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to overwrite an unfreed allocation")
	}

	if imageInfo.Extent.Width == 0 ||
		imageInfo.Extent.Height == 0 {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to create a 0-sized image")
	} else if imageInfo.Extent.Depth == 0 {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to create a 0-depth image")
	} else if imageInfo.MipLevels == 0 {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to create an image with 0 mip levels")
	} else if imageInfo.ArrayLayers == 0 {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to create an image with 0 array layers")
	}

	image, res, err = a.device.CreateImage(a.allocationCallbacks, imageInfo)
	if err != nil {
		return image, res, err
	}
	defer func() {
		if err != nil {
			image.Destroy(a.allocationCallbacks)
		}
	}()

	suballocation := SuballocationImageLinear
	if imageInfo.Tiling == core1_0.ImageTilingOptimal {
		suballocation = SuballocationImageOptimal
	}

	var memReqs core1_0.MemoryRequirements
	requiresDedicated, prefersDedicated, err := a.getImageMemoryRequirements(image, &memReqs)
	if err != nil {
		return image, core1_0.VKErrorUnknown, err
	}

	bufferOrImage := uint32(imageInfo.Usage)

	// Attempt to create a one-length slice for the provided alloc pointer
	outAllocSlice := unsafe.Slice(outAlloc, 1)
	res, err = a.multiAllocateMemory(
		&memReqs,
		requiresDedicated,
		prefersDedicated,
		nil,
		image,
		&bufferOrImage,
		&allocInfo,
		suballocation,
		outAllocSlice,
	)
	if err != nil {
		return image, res, err
	}
	defer func() {
		if err != nil {
			freeErr := outAlloc.free()
			if freeErr != nil {
				a.logger.LogAttrs(context.Background(), slog.LevelError, "failed to free temporary alloc after error", slog.Any("error", freeErr))
			}
		}
	}()

	if allocInfo.Flags&AllocationCreateDontBind == 0 {
		res, err = outAlloc.BindImageMemory(image)
	}

	return image, res, err
}
