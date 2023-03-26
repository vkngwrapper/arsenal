package vulkan

import (
	"fmt"
	"github.com/cockroachdb/errors"
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/vkngwrapper/arsenal/memory"
	"github.com/vkngwrapper/arsenal/memory/internal/metadata"
	"github.com/vkngwrapper/arsenal/memory/internal/utils"
	"github.com/vkngwrapper/arsenal/memory/internal/vulkan"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"unsafe"
)

type AllocationType byte

const (
	AllocationTypeNone AllocationType = iota
	AllocationTypeBlock
	AllocationTypeDedicated
)

var allocationTypeMapping = make(map[AllocationType]string)

func (t AllocationType) String() string {
	return allocationTypeMapping[t]
}

func init() {
	allocationTypeMapping[AllocationTypeNone] = "AllocationTypeNone"
	allocationTypeMapping[AllocationTypeBlock] = "AllocationTypeBlock"
	allocationTypeMapping[AllocationTypeDedicated] = "AllocationTypeDedicated"
}

type AllocationFlags uint32

const (
	AllocationPersistentMap AllocationFlags = 1 << iota
	AllocationMappingAllowed
)

var allocationFlagsMapping = common.NewFlagStringMapping[AllocationFlags]()

func init() {
	allocationFlagsMapping.Register(AllocationPersistentMap, "AllocationPersistentMap")
	allocationFlagsMapping.Register(AllocationMappingAllowed, "AllocationMappingAllowed")
}

type BlockData struct {
	handle metadata.BlockAllocationHandle
	block  *deviceMemoryBlock
}

type DedicatedData struct {
	parentPool memory.Pool
	nextAlloc  *Allocation
	prevAlloc  *Allocation
}

type Allocation struct {
	alignment uint
	size      int
	userData  any
	name      string
	flags     AllocationFlags

	memoryTypeIndex   int
	allocationType    AllocationType
	suballocationType metadata.SuballocationType
	deviceMemory      *vulkan.DeviceMemoryProperties
	memory            *vulkan.SynchronizedMemory

	blockData     BlockData
	dedicatedData DedicatedData
}

func (a *Allocation) init(deviceMemory *vulkan.DeviceMemoryProperties, mappingAllowed bool) {
	var flags AllocationFlags
	if mappingAllowed {
		flags = AllocationMappingAllowed
	}
	a.alignment = 1
	a.size = 0
	a.userData = nil
	a.name = ""
	a.flags = flags

	a.memoryTypeIndex = 0
	a.allocationType = 0
	a.suballocationType = 0
	a.deviceMemory = deviceMemory
	a.memory = nil
	a.blockData.handle = 0
	a.blockData.block = nil
	a.dedicatedData.parentPool = nil
	a.dedicatedData.nextAlloc = nil
	a.dedicatedData.prevAlloc = nil
}

func (a *Allocation) initBlockAllocation(
	block *deviceMemoryBlock,
	allocHandle metadata.BlockAllocationHandle,
	alignment uint,
	size int,
	memoryTypeIndex int,
	suballocationType metadata.SuballocationType,
	mapped bool,
) error {
	if a.allocationType != 0 {
		return errors.New("attempting to init an allocation that has already been initialized")
	}
	if block == nil || block.memory == nil {
		return errors.New("attempting to init a block allocation using a nil memory block")
	}
	a.allocationType = AllocationTypeBlock
	a.alignment = alignment
	a.size = size
	a.memoryTypeIndex = memoryTypeIndex
	if mapped && !a.IsMappingAllowed() {
		return errors.New("attempting to initialize an allocation for mapping that was created without mapping capabilities")
	} else if mapped {
		a.flags |= AllocationPersistentMap
	}

	a.suballocationType = suballocationType
	a.memory = block.memory
	a.blockData.handle = allocHandle
	a.blockData.block = block

	return nil
}

func (a *Allocation) initDedicatedAllocation(
	parentPool memory.Pool,
	memoryTypeIndex int,
	memory *vulkan.SynchronizedMemory,
	suballocationType metadata.SuballocationType,
	mappedData unsafe.Pointer,
	size int,
) error {
	if a.allocationType != 0 {
		return errors.New("attempting to init an allocation that has already been initialized")
	}
	if memory == nil {
		return errors.New("attempting to init a dedicated allocation using a nil device memory")
	}
	a.allocationType = AllocationTypeDedicated
	a.alignment = 0
	a.size = size
	a.memoryTypeIndex = memoryTypeIndex
	a.suballocationType = suballocationType
	if mappedData != nil && !a.IsMappingAllowed() {
		return errors.New("attempting to initialize an allocation for mapping that was created without mapping capabilities")
	} else if mappedData != nil {
		a.flags |= AllocationPersistentMap
	}

	a.dedicatedData.parentPool = parentPool
	a.memory = memory

	return nil
}

func (a *Allocation) SetName(name string) {
	a.name = name
}

func (a *Allocation) SetUserData(userData any) {
	a.userData = userData
}

func (a *Allocation) UserData() any {
	return a.userData
}

func (a *Allocation) Name() string {
	return a.name
}

func (a *Allocation) MemoryTypeIndex() int         { return a.memoryTypeIndex }
func (a *Allocation) Size() int                    { return a.size }
func (a *Allocation) Memory() core1_0.DeviceMemory { return a.memory.VulkanDeviceMemory() }
func (a *Allocation) isPersistentMap() bool        { return a.flags&AllocationPersistentMap != 0 }
func (a *Allocation) IsMappingAllowed() bool       { return a.flags&AllocationMappingAllowed != 0 }
func (a *Allocation) MemoryProperties() core1_0.MemoryPropertyFlags {
	return a.deviceMemory.MemoryTypeProperties(a.memoryTypeIndex).PropertyFlags
}

func (a *Allocation) mappedData() (unsafe.Pointer, error) {
	ptr := a.memory.MappedData()
	if ptr == nil {
		return nil, nil
	}

	offset, err := a.FindOffset()
	if err != nil {
		return nil, err
	}

	return unsafe.Add(ptr, offset), nil
}

func (a *Allocation) FindOffset() (int, error) {
	if a.allocationType == AllocationTypeBlock {
		return a.blockData.block.metadata.AllocationOffset(a.blockData.handle)
	}

	return 0, nil
}

func (a *Allocation) Map() (unsafe.Pointer, common.VkResult, error) {
	if !a.IsMappingAllowed() {
		return nil, core1_0.VKErrorMemoryMapFailed, errors.New("attempted to perform a map for an allocation that does not permit mapping")
	}

	ptr, res, err := a.memory.Map(1, 0, -1, 0)
	if err != nil || ptr == nil {
		return ptr, res, err
	}

	offset, err := a.FindOffset()
	if err != nil {
		return nil, core1_0.VKErrorUnknown, err
	}

	return unsafe.Add(ptr, offset), res, nil
}

func (a *Allocation) Unmap() error {
	return a.memory.Unmap(1)
}

func (a *Allocation) BindBufferMemory(offset int, buffer core1_0.Buffer, next common.Options) (common.VkResult, error) {
	switch a.allocationType {
	case AllocationTypeDedicated:
		return a.memory.BindVulkanBuffer(offset, buffer, next)
	case AllocationTypeBlock:
		allocOffset, err := a.FindOffset()
		if err != nil {
			return core1_0.VKErrorUnknown, err
		}
		return a.memory.BindVulkanBuffer(offset+allocOffset, buffer, next)
	}

	return core1_0.VKErrorUnknown, errors.Newf("attempted to bind an allocation with an unknown type: %s", a.allocationType.String())
}

func (a *Allocation) BindImageMemory(offset int, image core1_0.Image, next common.Options) (common.VkResult, error) {
	switch a.allocationType {
	case AllocationTypeDedicated:
		return a.memory.BindVulkanImage(offset, image, next)
	case AllocationTypeBlock:
		allocOffset, err := a.FindOffset()
		if err != nil {
			return core1_0.VKErrorUnknown, err
		}
		return a.memory.BindVulkanImage(offset+allocOffset, image, next)
	}

	return core1_0.VKErrorUnknown, errors.Newf("attempted to bind an allocation with an unknown type: %s", a.allocationType.String())
}

func (a *Allocation) PrintParameters(json *jwriter.ObjectState) {
	json.Name("Type").String(a.suballocationType.String())
	json.Name("Size").Int(a.size)
	//json.Name("Buffer Usage").String(a.bufferUsage.String())
	//json.Name("Image Usage").String(a.imageUsage.String())

	if a.userData != nil {
		json.Name("CustomData").String(fmt.Sprintf("%+v", a.userData))
	}

	if a.name != "" {
		json.Name("Name").String(a.name)
	}
}

func (a *Allocation) flushOrInvalidateRange(offset, size int, outRange *core1_0.MappedMemoryRange) (bool, error) {
	if size == 0 || size < -1 || !a.deviceMemory.IsMemoryTypeHostNonCoherent(a.memoryTypeIndex) {
		return false, nil
	}

	nonCoherentAtomSize := a.deviceMemory.DeviceProperties().Limits.NonCoherentAtomSize
	allocationSize := a.size

	if offset > allocationSize {
		return false, errors.Newf("offset %d is past the end of the allocation, which is size %d", offset, allocationSize)
	}
	if size > 0 && (offset+size) > allocationSize {
		return false, errors.Newf("offset %d places the end of the block %d past the end of the allocation, which is size %d", offset, offset+size, allocationSize)
	}

	outRange.Next = nil
	outRange.Memory = a.Memory()
	outRange.Offset = utils.AlignDown(offset, uint(nonCoherentAtomSize))
	outRange.Size = allocationSize - outRange.Offset

	switch a.allocationType {
	case AllocationTypeDedicated:
		if size > 0 {
			alignedSize := utils.AlignUp(size+(offset-outRange.Offset), uint(nonCoherentAtomSize))
			if alignedSize < outRange.Size {
				outRange.Size = alignedSize
			}
		}
		return true, nil
	case AllocationTypeBlock:
		// Calculate Size within the allocation
		if size == -1 {
			size = outRange.Size
		}

		outRange.Size = utils.AlignUp(size+(offset-outRange.Offset), uint(nonCoherentAtomSize))

		// Adjust offset and size to the block
		allocationOffset, err := a.FindOffset()
		if err != nil {
			return false, err
		}

		if allocationOffset%nonCoherentAtomSize != 0 {
			return false, errors.Newf("the allocation has an invalid offset %d for non-coherent memory, which has an alignment of %d", allocationOffset, nonCoherentAtomSize)
		}

		blockSize := a.blockData.block.metadata.Size()
		outRange.Offset += allocationOffset
		if blockSize-outRange.Offset < outRange.Size {
			outRange.Size = blockSize - outRange.Offset
		}
		return true, nil
	}

	return false, errors.Newf("attempted to get the flush or invalidate range of an allocation with invalid type %s", a.allocationType.String())
}

func (a *Allocation) FlushOrInvalidateAllocation(offset, size int, operation vulkan.CacheOperation) (common.VkResult, error) {
	var memRange core1_0.MappedMemoryRange
	success, err := a.flushOrInvalidateRange(offset, size, &memRange)
	if err != nil {
		return core1_0.VKErrorUnknown, err
	} else if !success {
		// Can't flush/invalidate this
		return core1_0.VKSuccess, nil
	}

	return a.deviceMemory.FlushOrInvalidateAllocations([]core1_0.MappedMemoryRange{memRange}, operation)
}

func (a *Allocation) fillAllocation(pattern uint8) (common.VkResult, error) {
	if utils.DebugMargin == 0 || !a.IsMappingAllowed() ||
		a.deviceMemory.MemoryTypeProperties(a.memoryTypeIndex).PropertyFlags&core1_0.MemoryPropertyHostVisible == 0 {
		// Don't fill allocations that can't be filled, or if memory debugging is turned off
		return core1_0.VKSuccess, nil
	}

	data, res, err := a.Map()
	if err != nil {
		return res, err
	}

	for i := 0; i < a.size; i++ {
		*(*uint8)(data) = pattern
		data = unsafe.Add(data, 1)
	}
	res, err = a.FlushOrInvalidateAllocation(0, -1, vulkan.CacheOperationFlush)
	if err != nil {
		return res, err
	}

	err = a.Unmap()
	if err != nil {
		return core1_0.VKErrorUnknown, err
	}
	return core1_0.VKSuccess, nil
}

func (a *Allocation) nextDedicatedAlloc() (*Allocation, error) {
	if a.allocationType != AllocationTypeDedicated {
		return nil, errors.New("attempted to get the next dedicated allocation in the linked list, but this is not a dedicated allocation")
	}
	return a.dedicatedData.nextAlloc, nil
}

func (a *Allocation) setNext(alloc *Allocation) error {
	if a.allocationType != AllocationTypeDedicated {
		return errors.New("attempted to set the next dedicated allocation in the linked list, but this is not a dedicated allocation")
	}

	a.dedicatedData.nextAlloc = alloc
	return nil
}

func (a *Allocation) prevDedicatedAlloc() (*Allocation, error) {
	if a.allocationType != AllocationTypeDedicated {
		return nil, errors.New("attempted to get the prev dedicated allocation in the linked list, but this is not a dedicated allocation")
	}

	return a.dedicatedData.prevAlloc, nil
}

func (a *Allocation) setPrev(alloc *Allocation) error {
	if a.allocationType != AllocationTypeDedicated {
		return errors.New("attempted to set the prev dedicated allocation in the linked list, but this is not a dedicated allocation")
	}

	a.dedicatedData.prevAlloc = alloc
	return nil
}
