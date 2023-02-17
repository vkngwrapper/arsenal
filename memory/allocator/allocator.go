package allocator

import (
	"github.com/cockroachdb/errors"
	"github.com/vkngwrapper/arsenal/memory/allocation"
	"github.com/vkngwrapper/arsenal/memory/internal/utils"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/core/v2/core1_1"
	"github.com/vkngwrapper/core/v2/driver"
	"math/bits"
	"sync/atomic"
)

type Allocator interface {
	AllocationCallbacks() *driver.AllocationCallbacks
	FreeVulkanMemory(memoryIndex int, size int, memory core1_0.DeviceMemory)
	UseMutex() bool
}

type VulkanAllocator struct {
	instance            core1_0.Instance
	physicalDevice      core1_0.PhysicalDevice
	device              core1_0.Device
	allocationCallbacks *driver.AllocationCallbacks
	memoryCallbacks     *MemoryCallbackOptions

	createFlags   CreateFlags
	extensionData *ExtensionData

	deviceProperties *core1_0.PhysicalDeviceProperties
	memoryProperties *core1_0.PhysicalDeviceMemoryProperties

	preferredLargeHeapBlockSize      int
	heapLimits                       []int
	externalMemoryHandleTypes        []core1_1.ExternalMemoryHandleTypeFlags
	gpuDefragmentationMemoryTypeBits uint32
	globalMemoryTypeBits             uint32
	nextPoolId                       int

	memoryBlockLists     [common.MaxMemoryTypes]*memoryBlockList
	dedicatedAllocations [common.MaxMemoryTypes]*dedicatedAllocationList
	budget               CurrentBudgetData
	deviceMemoryCount    uint32
}

func (a *VulkanAllocator) MemoryTypeCount() int {
	return len(a.memoryProperties.MemoryTypes)
}

func (a *VulkanAllocator) MemoryHeapCount() int {
	return len(a.memoryProperties.MemoryHeaps)
}

func (a *VulkanAllocator) MemoryTypeIndexToHeapIndex(memTypeIndex int) (int, error) {
	if memTypeIndex < 0 || memTypeIndex >= len(a.memoryProperties.MemoryTypes) {
		return -1, errors.Newf("invalid memory type index: %d", memTypeIndex)
	}

	return a.memoryProperties.MemoryTypes[memTypeIndex].HeapIndex, nil
}

func (a *VulkanAllocator) memoryTypeMinimumAlignment(memTypeIndex int) int {
	memTypeFlags := a.memoryProperties.MemoryTypes[memTypeIndex].PropertyFlags

	if (memTypeFlags&core1_0.MemoryPropertyHostVisible | core1_0.MemoryPropertyHostCoherent) == core1_0.MemoryPropertyHostVisible {
		// Memory is non-coherent
		alignment := a.deviceProperties.Limits.NonCoherentAtomSize
		if alignment < 1 {
			return 1
		}
		return alignment
	}

	return 1
}

func (a *VulkanAllocator) calcAllocationParams(
	o *allocation.AllocationCreateInfo,
	requiresDedicatedAllocation bool,
	prefersDedicatedAllocation bool,
) (common.VkResult, error) {
	hostAccessFlags := o.Flags & (allocation.AllocationCreateHostAccessSequentialWrite | allocation.AllocationCreateHostAccessRandom)
	if hostAccessFlags == (allocation.AllocationCreateHostAccessSequentialWrite | allocation.AllocationCreateHostAccessRandom) {
		return core1_0.VKErrorUnknown, errors.New("AllocationCreateHostAccessSequentialWrite and AllocationCreateHostAccessRandom cannot both be specified")
	}

	if hostAccessFlags == 0 && (o.Flags&allocation.AllocationCreateHostAccessAllowTransferInstead) != 0 {
		return core1_0.VKErrorUnknown, errors.New("if AllocationCreateHostAccessAllowTransferInstead is specified, " +
			"either AllocationCreateHostAccessSequentialWrite or AllocationCreateHostAccessRandom must be specified as well")
	}

	if o.Usage == allocation.MemoryUsageAuto || o.Usage == allocation.MemoryUsageAutoPreferDevice || o.Usage == allocation.MemoryUsageAutoPreferHost {
		if hostAccessFlags == 0 && o.Flags&allocation.AllocationCreateMapped != 0 {
			return core1_0.VKErrorUnknown, errors.New("when using MemoryUsageAuto* with AllocationCreateMapped, either " +
				"AllocationCreateHostAccessSequentialWrite or AllocationCreateHostAccessRandom must be specified as well")
		}
	}

	// GPU lazily allocated requires dedicated allocations
	if requiresDedicatedAllocation || o.Usage == allocation.MemoryUsageGPULazilyAllocated {
		o.Flags |= allocation.AllocationCreateDedicatedMemory
	}

	if o.Pool != nil {
		// TODO: Pool
	}

	if o.Flags&allocation.AllocationCreateDedicatedMemory != 0 && o.Flags&allocation.AllocationCreateNeverAllocate != 0 {
		return core1_0.VKErrorUnknown, errors.New("AllocationCreateDedicatedMemory and AllocationCreateNeverAllocate cannot be specified together")
	}

	if o.Usage != allocation.MemoryUsageAuto && o.Usage != allocation.MemoryUsageAutoPreferDevice && o.Usage != allocation.MemoryUsageAutoPreferHost {
		if hostAccessFlags == 0 {
			o.Flags |= allocation.AllocationCreateHostAccessRandom
		}
	}

	return core1_0.VKSuccess, nil
}

func (a *VulkanAllocator) isIntegratedGPU() bool {
	return a.deviceProperties.DriverType == core1_0.PhysicalDeviceTypeIntegratedGPU
}

func (a *VulkanAllocator) findMemoryPreferences(
	o allocation.AllocationCreateInfo,
	bufferOrImageUsage *uint32,
) (requiredFlags, preferredFlags, notPreferredFlags core1_0.MemoryPropertyFlags, err error) {
	isIntegratedGPU := a.isIntegratedGPU()
	requiredFlags = o.RequiredFlags
	preferredFlags = o.PreferredFlags
	notPreferredFlags = 0

	switch o.Usage {
	case allocation.MemoryUsageGPULazilyAllocated:
		requiredFlags |= core1_0.MemoryPropertyLazilyAllocated
		break
	case allocation.MemoryUsageAuto, allocation.MemoryUsageAutoPreferDevice, allocation.MemoryUsageAutoPreferHost:
		{
			if bufferOrImageUsage == nil {
				return requiredFlags, preferredFlags, notPreferredFlags,
					errors.New("MemoryUsageAuto* usages can only be used for Buffer- and Image-oriented allocation functions")
			}
			transferUsages := uint32(core1_0.BufferUsageTransferDst) | uint32(core1_0.BufferUsageTransferSrc) |
				uint32(core1_0.ImageUsageTransferSrc) | uint32(core1_0.ImageUsageTransferDst)
			nonTransferUsages := ^transferUsages

			deviceAccess := (*bufferOrImageUsage & nonTransferUsages) != 0
			hostAccessSequentialWrite := o.Flags&allocation.AllocationCreateHostAccessSequentialWrite != 0
			hostAccessRandom := o.Flags&allocation.AllocationCreateHostAccessRandom != 0
			hostAccessAllowTransferInstead := o.Flags&allocation.AllocationCreateHostAccessAllowTransferInstead != 0
			preferDevice := o.Usage == allocation.MemoryUsageAutoPreferDevice
			preferHost := o.Usage == allocation.MemoryUsageAutoPreferHost

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
	o allocation.AllocationCreateInfo,
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

	for memTypeIndex := 0; memTypeIndex < a.MemoryTypeCount(); memTypeIndex++ {
		memTypeBit := 1 << memTypeIndex

		if memTypeBit&memoryTypeBits == 0 {
			// This memory type is banned by the bitmask
			continue
		}

		flags := a.memoryProperties.MemoryTypes[memTypeIndex].PropertyFlags
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

func (a *VulkanAllocator) allocateVulkanMemory(
	allocateInfo core1_0.MemoryAllocateInfo,
) (mem core1_0.DeviceMemory, res common.VkResult, err error) {
	newDeviceCount := atomic.AddUint32(&a.deviceMemoryCount, 1)
	defer func() {
		// If we failed out, roll back the device increment
		if err != nil {
			atomic.AddUint32(&a.deviceMemoryCount, -1)
		}
	}()

	if int(newDeviceCount) > a.deviceProperties.Limits.MaxMemoryAllocationCount {
		return nil, core1_0.VKErrorTooManyObjects, core1_0.VKErrorTooManyObjects.ToError()
	}

	heapIndex, err := a.MemoryTypeIndexToHeapIndex(allocateInfo.MemoryTypeIndex)
	if err != nil {
		return nil, core1_0.VKErrorUnknown, err
	}

	heapLimit := a.heapLimits[heapIndex]
	if heapLimit == 0 {
		a.budget.AddBlockAllocation(heapIndex, allocateInfo.AllocationSize)
	} else {
		maxSize := heapLimit
		heapSize := a.memoryProperties.MemoryHeaps[heapIndex].Size
		if heapLimit < heapSize {
			maxSize = heapSize
		}
		res, err = a.budget.AddBlockAllocationWithBudget(heapIndex, allocateInfo.AllocationSize, maxSize)
		if err != nil {
			return nil, res, err
		}
	}
	defer func() {
		// If we failed out, roll back the block allocation
		if err != nil {
			a.budget.RemoveBlockAllocation(heapIndex, allocateInfo.AllocationSize)
		}
	}()

	mem, res, err = a.device.AllocateMemory(a.allocationCallbacks, allocateInfo)
	if err != nil {
		return mem, res, err
	}

	// TODO: Memory budget

	if a.memoryCallbacks != nil && a.memoryCallbacks.Allocate != nil {
		a.memoryCallbacks.Allocate(
			a,
			allocateInfo.MemoryTypeIndex,
			mem,
			allocateInfo.AllocationSize,
			a.memoryCallbacks.UserData,
		)
	}

	return mem, res, err
}

func (a *VulkanAllocator) freeVulkanMemory(memoryType int, size int, memory core1_0.DeviceMemory) error {
	if a.memoryCallbacks != nil && a.memoryCallbacks.Free != nil {
		a.memoryCallbacks.Free(
			a,
			memoryType,
			memory,
			size,
			a.memoryCallbacks.UserData,
		)
	}

	a.device.FreeMemory(memory, a.allocationCallbacks)

	heapIndex, err := a.MemoryTypeIndexToHeapIndex(memoryType)
	if err != nil {
		return err
	}

	a.budget.RemoveBlockAllocation(heapIndex, size)
	atomic.AddUint32(&a.deviceMemoryCount, -1)

	return nil
}

func (a *VulkanAllocator) multiAllocateMemory(
	memoryRequirements core1_0.MemoryRequirements,
	requiresDedicatedAllocation bool,
	prefersDedicatedAllocation bool,
	dedicatedBuffer core1_0.Buffer,
	dedicatedImage core1_0.Image,
	dedicatedBufferOrImageUsage *uint32,
	o allocation.AllocationCreateInfo,
	suballocType allocation.SuballocationType,
	allocationCount int,
) ([]allocation.Allocation, common.VkResult, error) {
	allocations := make([]allocation.Allocation, 0, allocationCount)

	err := utils.CheckPow2(memoryRequirements.Alignment, "core1_0.MemoryRequirements.Alignment")
	if err != nil {
		return nil, core1_0.VKErrorUnknown, err
	}

	if memoryRequirements.Size < 1 {
		return nil, core1_0.VKErrorInitializationFailed, core1_0.VKErrorInitializationFailed.ToError()
	}

	res, err := a.calcAllocationParams(&o, requiresDedicatedAllocation, prefersDedicatedAllocation)
	if err != nil {
		return nil, res, err
	}

	if o.Pool != nil {
		//TODO: Pool
	}

	memoryBits := memoryRequirements.MemoryTypeBits
	memoryTypeIndex, res, err := a.findMemoryTypeIndex(memoryBits, o, dedicatedBufferOrImageUsage)
	if err != nil {
		return nil, res, err
	}

	for err != nil {
		//Todo: Block vectors
	}

	return allocations, core1_0.VKErrorOutOfDeviceMemory, core1_0.VKErrorOutOfDeviceMemory.ToError()
}

func (a *VulkanAllocator) AllocateMemory(memoryRequirements core1_0.MemoryRequirements, o allocation.AllocationCreateInfo) (allocation.Allocation, common.VkResult, error) {
	allocations, res, err := a.multiAllocateMemory(
		memoryRequirements,
		false,
		false,
		nil, nil, nil,
		o,
		allocation.SuballocationUnknown,
		1,
	)
	if len(allocations) > 0 {
		return allocations[0], res, err
	}
	return nil, res, err
}

func (a *VulkanAllocator) BindBufferMemory(alloc allocation.Allocation, allocationLocalOffset int, buffer core1_0.Buffer, next common.Options) (common.VkResult, error) {
	switch alloc.AllocationType() {
	case allocation.AllocationTypeDedicated:
		return a.BindVulkanBuffer(alloc.)
	}
}