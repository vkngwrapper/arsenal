package memory

import (
	"github.com/cockroachdb/errors"
	"github.com/vkngwrapper/arsenal/memory/internal/metadata"
	"github.com/vkngwrapper/arsenal/memory/internal/utils"
	"github.com/vkngwrapper/arsenal/memory/internal/vulkan"
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
	"sync/atomic"
	"unsafe"
)

type CacheOperation uint32

const (
	CacheOperationFlush CacheOperation = iota
	CacheOperationInvalidate
)

var cacheOperationMapping = make(map[CacheOperation]string)

func (o CacheOperation) String() string {
	return cacheOperationMapping[o]
}

func init() {
	cacheOperationMapping[CacheOperationFlush] = "CacheOperationFlush"
	cacheOperationMapping[CacheOperationInvalidate] = "CacheOperationInvalidate"
}

type Allocator interface {
	AllocationCallbacks() *driver.AllocationCallbacks
	FreeVulkanMemory(memoryIndex int, size int, memory *vulkan.SynchronizedMemory)
	UseMutex() bool

	BindVulkanBuffer(memory core1_0.DeviceMemory, offset int, buffer core1_0.Buffer, bindOptions common.Options) (common.VkResult, error)
	BindVulkanImage(memory core1_0.DeviceMemory, offset int, buffer core1_0.Image, bindOptions common.Options) (common.VkResult, error)
}

type VulkanAllocator struct {
	useMutex            bool
	logger              *slog.Logger
	instance            core1_0.Instance
	physicalDevice      core1_0.PhysicalDevice
	device              core1_0.Device
	allocationCallbacks *driver.AllocationCallbacks
	memoryCallbacks     *MemoryCallbackOptions

	createFlags   CreateFlags
	extensionData *vulkan.ExtensionData

	preferredLargeHeapBlockSize      int
	gpuDefragmentationMemoryTypeBits uint32
	globalMemoryTypeBits             uint32
	nextPoolId                       int

	deviceMemory         *vulkan.DeviceMemoryProperties
	memoryBlockLists     [common.MaxMemoryTypes]memoryBlockList
	dedicatedAllocations [common.MaxMemoryTypes]dedicatedAllocationList
}

func (a *VulkanAllocator) calcAllocationParams(
	o *AllocationCreateInfo,
	requiresDedicatedAllocation bool,
	prefersDedicatedAllocation bool,
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
		// TODO: Pool
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

func (a *VulkanAllocator) isIntegratedGPU() bool {
	return a.deviceMemory.DeviceProperties().DriverType == core1_0.PhysicalDeviceTypeIntegratedGPU
}

func (a *VulkanAllocator) findMemoryPreferences(
	o AllocationCreateInfo,
	bufferOrImageUsage *uint32,
) (requiredFlags, preferredFlags, notPreferredFlags core1_0.MemoryPropertyFlags, err error) {
	isIntegratedGPU := a.isIntegratedGPU()
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

func (a *VulkanAllocator) findMemoryTypeIndex(
	memoryTypeBits uint32,
	o AllocationCreateInfo,
	bufferOrImageUsage *uint32,
) (int, common.VkResult, error) {
	memoryTypeBits &= a.globalMemoryTypeBits
	if o.MemoryTypeBits != 0 {
		memoryTypeBits &= o.MemoryTypeBits
	}

	requiredFlags, preferredFlags, notPreferredFlags, err := a.findMemoryPreferences(o, bufferOrImageUsage)
	if err != nil {
		return 0, core1_0.VKErrorUnknown, err
	}

	bestMemoryTypeIndex := -1
	minCost := 100000

	for memTypeIndex := 0; memTypeIndex < a.deviceMemory.MemoryTypeCount(); memTypeIndex++ {
		memTypeBit := 1 << memTypeIndex

		if memTypeBit&memoryTypeBits == 0 {
			// This memory type is banned by the bitmask
			continue
		}

		flags := a.deviceMemory.MemoryTypeProperties(memTypeIndex).PropertyFlags
		if requiredFlags & ^flags != 0 {
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

func (a *VulkanAllocator) calculateMemoryTypeParameters(
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

		budget := []vulkan.Budget{{}}
		a.deviceMemory.HeapBudgets(heapIndex, budget)
		if budget[0].Usage+size*allocationCount > budget[0].Budget {
			return core1_0.VKErrorOutOfDeviceMemory, core1_0.VKErrorOutOfDeviceMemory.ToError()
		}
	}

	return core1_0.VKSuccess, nil
}

func (a *VulkanAllocator) allocateDedicatedMemoryPage(
	pool *VulkanPool,
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
		return res, err
	}
	defer func() {
		if err != nil {
			a.deviceMemory.FreeVulkanMemory(memoryTypeIndex, size, mem)
		}
	}()

	var mappedPtr unsafe.Pointer
	if doMap {
		mappedPtr, res, err = mem.Map(1, 0, -1, 0)
		if err != nil {
			return res, err
		}
	}

	alloc.init(a.deviceMemory, isMappingAllowed)
	err = alloc.initDedicatedAllocation(pool, memoryTypeIndex, mem, suballocationType, mappedPtr, size)
	if err != nil {
		return core1_0.VKErrorUnknown, err
	}

	userDataStr, ok := userData.(string)
	if ok {
		alloc.SetName(userDataStr)
	} else {
		alloc.SetUserData(userData)
	}

	if utils.DebugMargin > 0 {
		res, err = alloc.fillAllocation(utils.CreatedFillPattern)
		if err != nil {
			return res, err
		}
	}

	return core1_0.VKSuccess, nil
}

func (a *VulkanAllocator) allocateDedicatedMemory(
	pool *VulkanPool,
	size int,
	suballocationType metadata.SuballocationType,
	dedicatedAllocations *dedicatedAllocationList,
	memoryTypeIndex int,
	doMap, isMappingAllowed, canAliasMemory bool,
	userData any,
	priority float32,
	dedicatedBuffer core1_0.Buffer,
	dedicatedBufferUsage core1_0.BufferUsageFlags,
	dedicatedImage core1_0.Image,
	allocations []Allocation,
	options common.Options,
) (common.VkResult, error) {
	if allocations == nil || len(allocations) == 0 {
		return core1_0.VKErrorUnknown, errors.New("attempted to make allocations to an empty list")
	}
	if dedicatedBuffer != nil && dedicatedImage != nil {
		return core1_0.VKErrorUnknown, errors.New("both buffer and image were passed in- only one is permitted")
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
			canContainBufferWithDeviceAddress = dedicatedBufferUsage == -1 ||
				dedicatedBufferUsage&khr_buffer_device_address.BufferUsageShaderDeviceAddress != 0
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
		exportMemoryAllocInfo.Next = allocInfo.Next
		allocInfo.Next = exportMemoryAllocInfo
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
			err = dedicatedAllocations.Register(&allocations[registerIndex])
			if err != nil {
				res = core1_0.VKErrorUnknown
				break
			}
		}

		if err == nil {
			return core1_0.VKSuccess, nil
		}
	}

	// Clean up allocations after error
	for allocIndex > 0 {
		allocIndex--

		currentAlloc := allocations[allocIndex]
		a.deviceMemory.FreeVulkanMemory(memoryTypeIndex, currentAlloc.Size(), currentAlloc.memory)
	}

	return res, err
}

func (a *VulkanAllocator) allocateMemoryOfType(
	pool *VulkanPool,
	size int,
	alignment uint,
	dedicatedPreferred bool,
	dedicatedBuffer core1_0.Buffer,
	dedicatedBufferUsage core1_0.BufferUsageFlags,
	dedicatedImage core1_0.Image,
	createInfo AllocationCreateInfo,
	memoryTypeIndex int,
	suballocationType metadata.SuballocationType,
	dedicatedAllocations *dedicatedAllocationList,
	blockAllocations *memoryBlockList,
	allocations []Allocation,
) (common.VkResult, error) {
	if len(allocations) == 0 {
		return core1_0.VKErrorUnknown, errors.New("attempted to make allocations to an empty list")
	}

	finalCreateInfo := createInfo

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
			dedicatedBufferUsage,
			dedicatedImage,
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
			atomic.LoadUint32(&a.deviceMemoryCount) > uint32(a.deviceMemory.DeviceProperties().Limits.MaxMemoryAllocationCount*3/4) {
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
				dedicatedBufferUsage,
				dedicatedImage,
				allocations,
				blockAllocations.allocOptions,
			)
			if err == nil {
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
			dedicatedBufferUsage,
			dedicatedImage,
			allocations,
			blockAllocations.allocOptions,
		)
		if err == nil {
			return res, err
		}
	}

	return res, err
}

func (a *VulkanAllocator) multiAllocateMemory(
	memoryRequirements core1_0.MemoryRequirements,
	requiresDedicatedAllocation bool,
	prefersDedicatedAllocation bool,
	dedicatedBuffer core1_0.Buffer,
	dedicatedImage core1_0.Image,
	dedicatedBufferOrImageUsage *uint32,
	options *AllocationCreateInfo,
	suballocType metadata.SuballocationType,
	outAllocations []Allocation,
) (common.VkResult, error) {
	err := utils.CheckPow2(memoryRequirements.Alignment, "core1_0.MemoryRequirements.Alignment")
	if err != nil {
		return core1_0.VKErrorUnknown, err
	}

	if memoryRequirements.Size < 1 {
		return core1_0.VKErrorInitializationFailed, core1_0.VKErrorInitializationFailed.ToError()
	}

	res, err := a.calcAllocationParams(options, requiresDedicatedAllocation, prefersDedicatedAllocation)
	if err != nil {
		return res, err
	}

	if options.Pool != nil {
		//TODO: Pool
	}

	memoryBits := memoryRequirements.MemoryTypeBits
	memoryTypeIndex, res, err := a.findMemoryTypeIndex(memoryBits, *options, dedicatedBufferOrImageUsage)
	if err != nil {
		return res, err
	}

	for err != nil {
		//Todo: Block vectors
	}

	return core1_0.VKErrorOutOfDeviceMemory, core1_0.VKErrorOutOfDeviceMemory.ToError()
}

func (a *VulkanAllocator) AllocateMemory(memoryRequirements core1_0.MemoryRequirements, o AllocationCreateInfo, outAlloc *Allocation) (common.VkResult, error) {
	res, err := a.multiAllocateMemory(
		memoryRequirements,
		false,
		false,
		nil, nil, nil,
		&o,
		metadata.SuballocationUnknown,
		1,
	)
	return res, err
}

func (a *VulkanAllocator) FreeDedicatedMemory(alloc *Allocation) error {
	if alloc == nil {
		return errors.New("attempted to free nil allocation")
	} else if alloc.allocationType != AllocationTypeDedicated {
		return errors.New("attempted to free dedicated memory for a non-dedicated allocation")
	}

	memoryTypeIndex := alloc.MemoryTypeIndex()
	heapIndex := a.deviceMemory.MemoryTypeIndexToHeapIndex(memoryTypeIndex)

	parentPool := alloc.dedicatedData.parentPool
	if parentPool == nil {
		// Default pool
		err := a.dedicatedAllocations[memoryTypeIndex].Unregister(alloc)
		if err != nil {
			return err
		}
	} else {
		// Custom pool
		parentPool.dedicatedAllocations.Unregister(allloc)
	}

	memory := alloc.Memory()
	err = a.freeVulkanMemory(memoryTypeIndex, alloc.Size(), memory)
	if err != nil {
		return err
	}

	a.deviceMemory.RemoveAllocation(heapIndex, alloc.Size())
	// TODO: Free allocation object?
	return nil
}
