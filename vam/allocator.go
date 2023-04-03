package vam

import (
	"github.com/cockroachdb/errors"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
	"github.com/vkngwrapper/arsenal/vam/internal/utils"
	"github.com/vkngwrapper/arsenal/vam/internal/vulkan"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/core1_1"
	"github.com/vkngwrapper/core/v2/driver"
	"github.com/vkngwrapper/extensions/v2/khr_buffer_device_address"
	"github.com/vkngwrapper/extensions/v2/khr_dedicated_allocation"
	"github.com/vkngwrapper/extensions/v2/khr_external_memory"
	"golang.org/x/exp/slog"
	"math"
	"math/bits"
	"unsafe"
)

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

func (a *Allocator) calcAllocationParams(
	o *AllocationCreateInfo,
	requiresDedicatedAllocation bool,
) (common.VkResult, error) {
	hostAccessFlags := o.Flags & (memutils.AllocationCreateHostAccessSequentialWrite | memutils.AllocationCreateHostAccessRandom)
	if hostAccessFlags == (memutils.AllocationCreateHostAccessSequentialWrite | memutils.AllocationCreateHostAccessRandom) {
		return core1_0.VKErrorUnknown, errors.New("AllocationCreateHostAccessSequentialWrite and AllocationCreateHostAccessRandom cannot both be specified")
	}

	if hostAccessFlags == 0 && (o.Flags&memutils.AllocationCreateHostAccessAllowTransferInstead) != 0 {
		return core1_0.VKErrorUnknown, errors.New("if AllocationCreateHostAccessAllowTransferInstead is specified, " +
			"either AllocationCreateHostAccessSequentialWrite or AllocationCreateHostAccessRandom must be specified as well")
	}

	if o.Usage == MemoryUsageAuto || o.Usage == MemoryUsageAutoPreferDevice || o.Usage == MemoryUsageAutoPreferHost {
		if hostAccessFlags == 0 && o.Flags&memutils.AllocationCreateMapped != 0 {
			return core1_0.VKErrorUnknown, errors.New("when using MemoryUsageAuto* with AllocationCreateMapped, either " +
				"AllocationCreateHostAccessSequentialWrite or AllocationCreateHostAccessRandom must be specified as well")
		}
	}

	// GPU lazily allocated requires dedicated allocations
	if requiresDedicatedAllocation || o.Usage == MemoryUsageGPULazilyAllocated {
		o.Flags |= memutils.AllocationCreateDedicatedMemory
	}

	if o.Pool != nil {
		if o.Pool.blockList.HasExplicitBlockSize() && o.Flags&memutils.AllocationCreateDedicatedMemory != 0 {
			return core1_0.VKErrorUnknown, errors.New("specified memutils.AllocationCreateDedicatedMemory with a pool that does not support it")
		}

		o.Priority = o.Pool.blockList.priority
	}

	if o.Flags&memutils.AllocationCreateDedicatedMemory != 0 && o.Flags&memutils.AllocationCreateNeverAllocate != 0 {
		return core1_0.VKErrorUnknown, errors.New("AllocationCreateDedicatedMemory and AllocationCreateNeverAllocate cannot be specified together")
	}

	if o.Usage != MemoryUsageAuto && o.Usage != MemoryUsageAutoPreferDevice && o.Usage != MemoryUsageAutoPreferHost {
		if hostAccessFlags == 0 {
			o.Flags |= memutils.AllocationCreateHostAccessRandom
		}
	}

	return core1_0.VKSuccess, nil
}

func (a *Allocator) findMemoryPreferences(
	o *AllocationCreateInfo,
	bufferOrImageUsage *uint32,
) (requiredFlags, preferredFlags, notPreferredFlags core1_0.MemoryPropertyFlags, err error) {
	isIntegratedGPU := a.deviceMemory.IsIntegratedGPU()
	requiredFlags = o.RequiredFlags
	preferredFlags = o.PreferredFlags
	notPreferredFlags = 0

	switch o.Usage {
	case MemoryUsageGPULazilyAllocated:
		requiredFlags |= core1_0.MemoryPropertyLazilyAllocated
		break
	case MemoryUsageAuto, MemoryUsageAutoPreferDevice, MemoryUsageAutoPreferHost:
		{
			if bufferOrImageUsage == nil {
				return requiredFlags, preferredFlags, notPreferredFlags,
					errors.New("MemoryUsageAuto* usages can only be used for Buffer- and Image-oriented allocation functions")
			}
			transferUsages := uint32(core1_0.BufferUsageTransferDst) | uint32(core1_0.BufferUsageTransferSrc) |
				uint32(core1_0.ImageUsageTransferSrc) | uint32(core1_0.ImageUsageTransferDst)
			nonTransferUsages := ^transferUsages

			deviceAccess := (*bufferOrImageUsage & nonTransferUsages) != 0
			hostAccessSequentialWrite := o.Flags&memutils.AllocationCreateHostAccessSequentialWrite != 0
			hostAccessRandom := o.Flags&memutils.AllocationCreateHostAccessRandom != 0
			hostAccessAllowTransferInstead := o.Flags&memutils.AllocationCreateHostAccessAllowTransferInstead != 0
			preferDevice := o.Usage == MemoryUsageAutoPreferDevice
			preferHost := o.Usage == MemoryUsageAutoPreferHost

			if hostAccessRandom && !isIntegratedGPU && deviceAccess && hostAccessAllowTransferInstead && !preferHost {
				// CPU-accessible memory that should live on the device
				// Prefer DEVICE_LOCAL, HOST_VISIBLE would be nice but not as important so omitted
				preferredFlags |= core1_0.MemoryPropertyDeviceLocal | core1_0.MemoryPropertyHostCached
			} else if hostAccessRandom {
				// Other CPU-accessible memory
				requiredFlags |= core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCached
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

	// TODO: VK_AMD_device_coherent_memory
	return requiredFlags, preferredFlags, notPreferredFlags, nil
}

func (a *Allocator) FindMemoryTypeIndex(
	memoryTypeBits uint32,
	o AllocationCreateInfo,
) (int, common.VkResult, error) {
	a.logger.Debug("Allocator::FindMemoryTypeIndex")

	return a.findMemoryTypeIndex(memoryTypeBits, &o, nil)
}

func (a *Allocator) FindMemoryTypeIndexForBufferInfo(
	bufferInfo core1_0.BufferCreateInfo,
	o AllocationCreateInfo,
) (int, common.VkResult, error) {
	a.logger.Debug("Allocator::FindMemoryTypeIndexForBufferInfo")

	// TODO: Vulkan 1.3

	memReqs, res, err := a.getMemoryRequirementsFromBufferInfo(bufferInfo)
	if err != nil {
		return -1, res, err
	}

	usage := uint32(bufferInfo.Usage)
	return a.findMemoryTypeIndex(memReqs.MemoryTypeBits, &o, &usage)
}

func (a *Allocator) FindMemoryTypeIndexForImageInfo(
	imageInfo core1_0.ImageCreateInfo,
	o AllocationCreateInfo,
) (int, common.VkResult, error) {
	a.logger.Debug("Allocator::FindMemoryTYpeIndexForImageInfo")

	// TODO: Vulkan 1.3

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

	requiredFlags, preferredFlags, notPreferredFlags, err := a.findMemoryPreferences(o, bufferOrImageUsage)
	if err != nil {
		return 0, core1_0.VKErrorFeatureNotPresent, err
	}

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
	if options.Flags&memutils.AllocationCreateMapped != 0 &&
		a.deviceMemory.MemoryTypeProperties(memoryTypeIndex).PropertyFlags&core1_0.MemoryPropertyHostVisible == 0 {
		options.Flags &= ^memutils.AllocationCreateMapped
	}

	// Check budget if appropriate
	if options.Flags&memutils.AllocationCreateDedicatedMemory != 0 &&
		options.Flags&memutils.AllocationCreateWithinBudget != 0 {
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
	suballocationType metadata.SuballocationType,
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
		_, res, err = mem.Map(1, 0, -1, 0)
		if err != nil {
			return res, err
		}
	}

	alloc.init(a.deviceMemory, isMappingAllowed)
	alloc.initDedicatedAllocation(pool, memoryTypeIndex, mem, suballocationType, size)

	userDataStr, ok := userData.(string)
	if ok {
		alloc.SetName(userDataStr)
	} else {
		alloc.SetUserData(userData)
	}
	a.deviceMemory.AddAllocation(a.deviceMemory.MemoryTypeIndexToHeapIndex(memoryTypeIndex), size)

	if memutils.DebugMargin > 0 {
		alloc.fillAllocation(memutils.CreatedFillPattern)
	}

	return core1_0.VKSuccess, nil
}

func (a *Allocator) allocateDedicatedMemory(
	pool *Pool,
	size int,
	suballocationType metadata.SuballocationType,
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

	// TODO: Memory priority

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

		a.logger.Debug("    Allocated DedicatedMemory", slog.Int("Count", len(allocations)), slog.Int("MemoryTypeIndex", memoryTypeIndex))

		return core1_0.VKSuccess, nil
	}

	// Clean up allocations after error
	for allocIndex > 0 {
		allocIndex--

		currentAlloc := allocations[allocIndex]
		a.deviceMemory.FreeVulkanMemory(memoryTypeIndex, currentAlloc.Size(), currentAlloc.memory)
		a.deviceMemory.RemoveAllocation(a.deviceMemory.MemoryTypeIndexToHeapIndex(memoryTypeIndex), currentAlloc.Size())
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
	suballocationType metadata.SuballocationType,
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

	a.logger.Debug("Allocator::allocateMemoryOfType", slog.Int("MemoryTypeIndex", memoryTypeIndex), slog.Int("AllocationCount", len(allocations)), slog.Int("Size", size))

	finalCreateInfo := *createInfo

	res, err := a.calculateMemoryTypeParameters(&finalCreateInfo, memoryTypeIndex, size, len(allocations))
	if err != nil {
		return res, err
	}

	mappingAllowed := finalCreateInfo.Flags&(memutils.AllocationCreateHostAccessSequentialWrite|memutils.AllocationCreateHostAccessRandom) != 0

	if finalCreateInfo.Flags&memutils.AllocationCreateDedicatedMemory != 0 {
		return a.allocateDedicatedMemory(
			pool,
			size,
			suballocationType,
			dedicatedAllocations,
			memoryTypeIndex,
			finalCreateInfo.Flags&memutils.AllocationCreateMapped != 0,
			mappingAllowed,
			finalCreateInfo.Flags&memutils.AllocationCreateCanAlias != 0,
			finalCreateInfo.UserData,
			finalCreateInfo.Priority,
			dedicatedBuffer,
			dedicatedImage,
			dedicatedBufferOrImageUsage,
			allocations,
			blockAllocations.allocOptions,
		)
	}

	canAllocateDedicated := finalCreateInfo.Flags&memutils.AllocationCreateNeverAllocate == 0 && (pool == nil || !blockAllocations.HasExplicitBlockSize())

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
				finalCreateInfo.Flags&memutils.AllocationCreateMapped != 0,
				mappingAllowed,
				finalCreateInfo.Flags&memutils.AllocationCreateCanAlias != 0,
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
			finalCreateInfo.Flags&memutils.AllocationCreateMapped != 0,
			mappingAllowed,
			finalCreateInfo.Flags&memutils.AllocationCreateCanAlias != 0,
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
	suballocType metadata.SuballocationType,
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
			return core1_0.VKErrorUnknown, errors.Newf("attempted to allocate from unsupported memory type index %d", memoryTypeIndex)
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
		memoryTypeIndex, res, err = a.findMemoryTypeIndex(memoryBits, options, dedicatedBufferOrImageUsage)
	}

	return core1_0.VKErrorOutOfDeviceMemory, core1_0.VKErrorOutOfDeviceMemory.ToError()
}

func (a *Allocator) AllocateMemory(memoryRequirements *core1_0.MemoryRequirements, o AllocationCreateInfo, outAlloc *Allocation) (common.VkResult, error) {
	a.logger.Debug("Allocator::AllocateMemory")

	if outAlloc == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to allocate into a nil allocation")
	} else if memoryRequirements == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to allocate with nil memory requirements")
	}

	// Attempt to create a one-length slice for the provided alloc pointer
	outAllocSlice := unsafe.Slice(outAlloc, 1)
	return a.multiAllocateMemory(
		memoryRequirements,
		false,
		false,
		nil, nil, nil,
		&o,
		metadata.SuballocationUnknown,
		outAllocSlice,
	)
}

func (a *Allocator) AllocateMemorySlice(memoryRequirements *core1_0.MemoryRequirements, o AllocationCreateInfo, allocations []Allocation) (common.VkResult, error) {
	a.logger.Debug("Allocator::AllocateMemorySlice")

	if memoryRequirements == nil {
		return core1_0.VKErrorUnknown, errors.New("attempt to allocate with nil memory requirements")
	}

	if len(allocations) == 0 {
		return core1_0.VKSuccess, nil
	}

	return a.multiAllocateMemory(
		memoryRequirements,
		false,
		false,
		nil,
		nil,
		nil,
		&o,
		metadata.SuballocationUnknown,
		allocations,
	)
}

func (a *Allocator) AllocateMemoryForBuffer(buffer core1_0.Buffer, o AllocationCreateInfo, outAlloc *Allocation) (common.VkResult, error) {
	a.logger.Debug("Allocator::AllocateMemoryForBuffer")

	if buffer == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to allocate for a nil buffer")
	} else if outAlloc == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to allocate into a nil allocation")
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
		metadata.SuballocationBuffer,
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

func (a *Allocator) AllocateMemoryForImage(image core1_0.Image, o AllocationCreateInfo, outAlloc *Allocation) (common.VkResult, error) {
	a.logger.Debug("Allocator::AllocateMemoryForImage")

	if image == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to allocate for a nil image")
	} else if outAlloc == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to allocate into a nil allocation")
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
		metadata.SuballocationImageUnknown,
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

func (a *Allocator) freeDedicatedMemory(alloc *Allocation) error {
	if alloc == nil {
		return errors.New("attempted to free nil allocation")
	} else if alloc.allocationType != allocationTypeDedicated {
		return errors.New("attempted to free dedicated memory for a non-dedicated allocation")
	}

	memoryTypeIndex := alloc.MemoryTypeIndex()
	heapIndex := a.deviceMemory.MemoryTypeIndexToHeapIndex(memoryTypeIndex)

	parentPool := alloc.dedicatedData.parentPool
	if parentPool == nil {
		// Default pool
		a.dedicatedAllocations[memoryTypeIndex].Unregister(alloc)
	} else {
		// Custom pool
		parentPool.dedicatedAllocations.Unregister(alloc)
	}

	a.deviceMemory.FreeVulkanMemory(memoryTypeIndex, alloc.Size(), alloc.memory)

	a.deviceMemory.RemoveAllocation(heapIndex, alloc.Size())

	return nil
}

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
				break
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
		memBit := 1 << pool.blockList.memoryTypeIndex
		if memBit&memoryTypeBits == 0 {
			continue
		}

		innerRes, err := pool.blockList.CheckCorruption()
		switch innerRes {
		case core1_0.VKErrorFeatureNotPresent:
			break
		case core1_0.VKSuccess:
			res = core1_0.VKSuccess
			break
		default:
			if err != nil {
				return res, err
			}
		}
	}

	return res, res.ToError()
}

func (a *Allocator) CreatePool(createInfo PoolCreateInfo) (*Pool, common.VkResult, error) {

	a.logger.Debug("Allocator::CreatePool",
		slog.Int("MemoryTypeIndex", createInfo.MemoryTypeIndex),
		slog.String("Flags", createInfo.Flags.String()),
	)

	if createInfo.MaxBlockCount == 0 {
		createInfo.MaxBlockCount = math.MaxInt
	}
	if createInfo.MinBlockCount > createInfo.MaxBlockCount {
		return nil, core1_0.VKErrorUnknown, errors.Newf("provided MinBlockCount %d was greater than provided MaxBlockCount %d", createInfo.MinBlockCount, createInfo.MaxBlockCount)
	}

	memTypeBits := 1 << createInfo.MemoryTypeIndex
	if createInfo.MemoryTypeIndex >= a.deviceMemory.MemoryTypeCount() || memTypeBits&a.globalMemoryTypeBits == 0 {
		return nil, core1_0.VKErrorFeatureNotPresent, core1_0.VKErrorFeatureNotPresent.ToError()
	}

	if createInfo.MinAllocationAlignment > 0 {
		err := memutils.CheckPow2(createInfo.MinAllocationAlignment, "createInfo.MinAllocationAlignment")
		if err != nil {
			return nil, core1_0.VKErrorUnknown, err
		}
	}

	preferredBlockSize, err := a.calculatePreferredBlockSize(createInfo.MemoryTypeIndex)
	if err != nil {
		return nil, core1_0.VKErrorUnknown, err
	}

	pool := &Pool{
		logger: a.logger,
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
		a.logger,
		createInfo.MemoryTypeIndex,
		blockSize,
		createInfo.MinBlockCount,
		createInfo.MaxBlockCount,
		bufferImageGranularity,
		createInfo.BlockSize != 0,
		createInfo.Flags&PoolCreateAlgorithmMask,
		createInfo.Priority,
		alignment,
		a.extensionData,
		a.deviceMemory,
		createInfo.MemoryAllocateNext,
	)

	res, err := pool.blockList.CreateMinBlocks()
	if err != nil {
		destroyErr := pool.Destroy()
		if err != nil {
			a.logger.Error("error attempting to destroy pool after creation failure", slog.Any("error", destroyErr))
		}
		return nil, res, err
	}

	a.poolsMutex.Lock()
	defer a.poolsMutex.Unlock()

	err = pool.SetID(a.nextPoolId)
	if err != nil {
		destroyErr := pool.destroyAfterLock()
		if destroyErr != nil {
			a.logger.Error("error attempting to destroy pool after failing to set id", slog.Any("error", destroyErr))
		}

		return nil, core1_0.VKErrorUnknown, err
	}
	pool.next = a.pools
	a.pools.prev = pool
	a.pools = pool

	return pool, core1_0.VKSuccess, nil
}
