package vam

import (
	"fmt"
	"github.com/cockroachdb/errors"
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
	"github.com/vkngwrapper/arsenal/vam/internal/vulkan"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"unsafe"
)

type allocationType byte

const (
	allocationTypeNone allocationType = iota
	allocationTypeBlock
	allocationTypeDedicated
)

var allocationTypeMapping = make(map[allocationType]string)

func (t allocationType) String() string {
	return allocationTypeMapping[t]
}

func init() {
	allocationTypeMapping[allocationTypeNone] = "allocationTypeNone"
	allocationTypeMapping[allocationTypeBlock] = "allocationTypeBlock"
	allocationTypeMapping[allocationTypeDedicated] = "allocationTypeDedicated"
}

type allocationFlags uint32

const (
	allocationPersistentMap allocationFlags = 1 << iota
	allocationMappingAllowed
)

var allocationFlagsMapping = common.NewFlagStringMapping[allocationFlags]()

func init() {
	allocationFlagsMapping.Register(allocationPersistentMap, "allocationPersistentMap")
	allocationFlagsMapping.Register(allocationMappingAllowed, "allocationMappingAllowed")
}

type blockData struct {
	handle metadata.BlockAllocationHandle
	block  *deviceMemoryBlock
}

type dedicatedData struct {
	parentPool *Pool
	nextAlloc  *Allocation
	prevAlloc  *Allocation
}

type Allocation struct {
	alignment uint
	size      int
	userData  any
	name      string
	flags     allocationFlags

	memoryTypeIndex   int
	allocationType    allocationType
	suballocationType metadata.SuballocationType
	mapCount          int
	memory            *vulkan.SynchronizedMemory

	parentAllocator *Allocator

	blockData     blockData
	dedicatedData dedicatedData
}

func (a *Allocation) init(allocator *Allocator, mappingAllowed bool) {
	var flags allocationFlags
	if mappingAllowed {
		flags = allocationMappingAllowed
	}
	a.alignment = 1
	a.size = 0
	a.userData = nil
	a.name = ""
	a.flags = flags

	a.memoryTypeIndex = 0
	a.allocationType = 0
	a.suballocationType = 0
	a.parentAllocator = allocator
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
) {
	if a.allocationType != 0 {
		panic("attempting to init an allocation that has already been initialized")
	}
	if block == nil || block.memory == nil {
		panic("attempting to init a block allocation using a nil memory block")
	}
	a.allocationType = allocationTypeBlock
	a.alignment = alignment
	a.size = size
	a.memoryTypeIndex = memoryTypeIndex
	if mapped && !a.IsMappingAllowed() {
		panic("attempting to initialize an allocation for mapping that was created without mapping capabilities")
	} else if mapped {
		a.flags |= allocationPersistentMap
	}

	a.suballocationType = suballocationType
	a.memory = block.memory
	a.blockData.handle = allocHandle
	a.blockData.block = block
}

func (a *Allocation) initDedicatedAllocation(
	parentPool *Pool,
	memoryTypeIndex int,
	memory *vulkan.SynchronizedMemory,
	suballocationType metadata.SuballocationType,
	size int,
) {
	if a.allocationType != 0 {
		panic("attempting to init an allocation that has already been initialized")
	}
	if memory == nil {
		panic("attempting to init a dedicated allocation using a nil device memory")
	}
	a.allocationType = allocationTypeDedicated
	a.alignment = 0
	a.size = size
	a.memoryTypeIndex = memoryTypeIndex
	a.suballocationType = suballocationType
	if memory.MappedData() != nil && !a.IsMappingAllowed() {
		panic("attempting to initialize an allocation for mapping that was created without mapping capabilities")
	} else if memory.MappedData() != nil {
		a.flags |= allocationPersistentMap
	}

	a.dedicatedData.parentPool = parentPool
	a.memory = memory
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
func (a *Allocation) Alignment() uint              { return a.alignment }
func (a *Allocation) Memory() core1_0.DeviceMemory { return a.memory.VulkanDeviceMemory() }
func (a *Allocation) isPersistentMap() bool        { return a.flags&allocationPersistentMap != 0 }
func (a *Allocation) IsMappingAllowed() bool       { return a.flags&allocationMappingAllowed != 0 }
func (a *Allocation) MemoryType() core1_0.MemoryType {
	return a.parentAllocator.deviceMemory.MemoryTypeProperties(a.memoryTypeIndex)
}

func (a *Allocation) FindOffset() int {
	a.parentAllocator.logger.Debug("Allocation::FindOffset")

	if a.allocationType == allocationTypeBlock {
		offset, err := a.blockData.block.metadata.AllocationOffset(a.blockData.handle)
		if err != nil {
			panic(fmt.Sprintf("failed to locate offset for handle %+v: %+v", a.blockData.handle, err))
		}

		return offset
	}

	return 0
}

func (a *Allocation) Map() (unsafe.Pointer, common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::Map")

	if !a.IsMappingAllowed() {
		return nil, core1_0.VKErrorMemoryMapFailed, errors.New("attempted to perform a map for an allocation that does not permit mapping")
	}

	a.mapCount++
	ptr, res, err := a.memory.Map(1, 0, -1, 0)
	if err != nil || ptr == nil {
		return ptr, res, err
	}

	offset := a.FindOffset()

	return unsafe.Add(ptr, offset), res, nil
}

func (a *Allocation) Unmap() error {
	a.parentAllocator.logger.Debug("Allocation::Unmap")

	a.mapCount--
	return a.memory.Unmap(1)
}

func (a *Allocation) Flush(offset, size int) (common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::Flush")

	return a.flushOrInvalidate(offset, size, vulkan.CacheOperationFlush)
}

func (a *Allocation) Invalidate(offset, size int) (common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::Invalidate")

	return a.flushOrInvalidate(offset, size, vulkan.CacheOperationInvalidate)
}

func (a *Allocation) BindBufferMemory(offset int, buffer core1_0.Buffer, next common.Options) (common.VkResult, error) {
	switch a.allocationType {
	case allocationTypeDedicated:
		return a.memory.BindVulkanBuffer(offset, buffer, next)
	case allocationTypeBlock:
		allocOffset := a.FindOffset()
		return a.memory.BindVulkanBuffer(offset+allocOffset, buffer, next)
	}

	return core1_0.VKErrorUnknown, errors.Newf("attempted to bind an allocation with an unknown type: %s", a.allocationType.String())
}

func (a *Allocation) BindImageMemory(offset int, image core1_0.Image, next common.Options) (common.VkResult, error) {
	switch a.allocationType {
	case allocationTypeDedicated:
		return a.memory.BindVulkanImage(offset, image, next)
	case allocationTypeBlock:
		allocOffset := a.FindOffset()
		return a.memory.BindVulkanImage(offset+allocOffset, image, next)
	}

	return core1_0.VKErrorUnknown, errors.Newf("attempted to bind an allocation with an unknown type: %s", a.allocationType.String())
}

func (a *Allocation) printParameters(json *jwriter.ObjectState) {
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

	// A size of -1 indicates the whole allocation
	if size == 0 || size < -1 || !a.parentAllocator.deviceMemory.IsMemoryTypeHostNonCoherent(a.memoryTypeIndex) {
		return false, nil
	}

	nonCoherentAtomSize := a.parentAllocator.deviceMemory.DeviceProperties().Limits.NonCoherentAtomSize
	allocationSize := a.Size()

	if offset > allocationSize {
		return false, errors.Newf("offset %d is past the end of the allocation, which is size %d", offset, allocationSize)
	}
	if size > 0 && (offset+size) > allocationSize {
		return false, errors.Newf("offset %d places the end of the block %d past the end of the allocation, which is size %d", offset, offset+size, allocationSize)
	}

	outRange.Next = nil
	outRange.Memory = a.Memory()
	outRange.Offset = memutils.AlignDown(offset, uint(nonCoherentAtomSize))

	switch a.allocationType {
	case allocationTypeDedicated:
		outRange.Size = allocationSize - outRange.Offset
		if size > 0 {
			alignedSize := memutils.AlignUp(size+(offset-outRange.Offset), uint(nonCoherentAtomSize))
			if alignedSize < outRange.Size {
				outRange.Size = alignedSize
			}
		}
		return true, nil
	case allocationTypeBlock:
		// Calculate Size within the allocation
		if size == -1 {
			size = allocationSize - outRange.Offset
		}

		outRange.Size = memutils.AlignUp(size+(offset-outRange.Offset), uint(nonCoherentAtomSize))

		// Adjust offset and size to the block
		allocationOffset := a.FindOffset()

		if allocationOffset%nonCoherentAtomSize != 0 {
			panic(fmt.Sprintf("the allocation has an invalid offset %d for non-coherent memory, which has an alignment of %d", allocationOffset, nonCoherentAtomSize))
		}

		blockSize := a.blockData.block.metadata.Size()
		outRange.Offset += allocationOffset

		restOfBlock := blockSize - outRange.Offset
		if restOfBlock < outRange.Size {
			outRange.Size = restOfBlock
		}
		return true, nil
	}

	return false, errors.Newf("attempted to get the flush or invalidate range of an allocation with invalid type %s", a.allocationType.String())
}

func (a *Allocation) flushOrInvalidate(offset, size int, operation vulkan.CacheOperation) (common.VkResult, error) {
	var memRange core1_0.MappedMemoryRange
	success, err := a.flushOrInvalidateRange(offset, size, &memRange)
	if err != nil {
		return core1_0.VKErrorUnknown, err
	} else if !success {
		// Can't flush/invalidate this
		return core1_0.VKSuccess, nil
	}

	return a.parentAllocator.deviceMemory.FlushOrInvalidateAllocations([]core1_0.MappedMemoryRange{memRange}, operation)
}

func (a *Allocation) fillAllocation(pattern uint8) {
	if memutils.DebugMargin == 0 || !a.IsMappingAllowed() ||
		a.parentAllocator.deviceMemory.MemoryTypeProperties(a.memoryTypeIndex).PropertyFlags&core1_0.MemoryPropertyHostVisible == 0 {
		// Don't fill allocations that can't be filled, or if memory debugging is turned off
		return
	}

	data, _, err := a.Map()
	if err != nil {
		panic(fmt.Sprintf("failed when attempting to map memory during debug pattern fill: %+v", err))
	}

	dataSlice := ([]uint8)(unsafe.Slice((*uint8)(data), a.size))
	for i := 0; i < a.size; i++ {
		dataSlice[i] = pattern
	}
	_, err = a.flushOrInvalidate(0, -1, vulkan.CacheOperationFlush)
	if err != nil {
		panic(fmt.Sprintf("failed when attempting to flush host cache during debug pattern fill: %+v", err))
	}

	err = a.Unmap()
	if err != nil {
		panic(fmt.Sprintf("failed when attempting to unmap memory during debug pattern fill: %+v", err))
	}
}

func (a *Allocation) nextDedicatedAlloc() *Allocation {
	if a.allocationType != allocationTypeDedicated {
		panic("attempted to get the next dedicated allocation in the linked list, but this is not a dedicated allocation")
	}
	return a.dedicatedData.nextAlloc
}

func (a *Allocation) setNext(alloc *Allocation) {
	if a.allocationType != allocationTypeDedicated {
		panic("attempted to set the next dedicated allocation in the linked list, but this is not a dedicated allocation")
	}

	a.dedicatedData.nextAlloc = alloc
}

func (a *Allocation) prevDedicatedAlloc() *Allocation {
	if a.allocationType != allocationTypeDedicated {
		panic("attempted to get the prev dedicated allocation in the linked list, but this is not a dedicated allocation")
	}

	return a.dedicatedData.prevAlloc
}

func (a *Allocation) setPrev(alloc *Allocation) {
	if a.allocationType != allocationTypeDedicated {
		panic("attempted to set the prev dedicated allocation in the linked list, but this is not a dedicated allocation")
	}

	a.dedicatedData.prevAlloc = alloc
}

func (a *Allocation) ParentPool() *Pool {
	switch a.allocationType {
	case allocationTypeBlock:
		return a.blockData.block.parentPool
	case allocationTypeDedicated:
		return a.dedicatedData.parentPool
	}

	panic(fmt.Sprintf("invalid allocation type: %s", a.allocationType.String()))
}

func (a *Allocation) Free() error {
	a.parentAllocator.logger.Debug("Allocation::Free")

	// Attempt to create a one-length slice for the provided alloc pointer
	allocSlice := unsafe.Slice(a, 1)
	return a.parentAllocator.multiFreeMemory(
		allocSlice,
	)
}

func (a *Allocation) swapBlockAllocation(alloc *Allocation) (int, error) {
	if alloc == nil {
		panic("tried to swap blocks with a nil allocation")
	} else if a.allocationType != allocationTypeBlock {
		panic("tried to swap blocks but this is not a block allocation")
	} else if alloc.allocationType != allocationTypeBlock {
		panic(fmt.Sprintf("tried to swap blocks with a non-block allocation: %s", alloc.allocationType.String()))
	}

	if a.mapCount != 0 {
		err := a.memory.Unmap(a.mapCount)
		if err != nil {
			return 0, err
		}
	}

	err := a.blockData.block.metadata.SetAllocationUserData(a.blockData.handle, alloc)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when attempting to set current metadata during block swap: %+v", err))
	}
	a.blockData, alloc.blockData = alloc.blockData, a.blockData
	err = a.blockData.block.metadata.SetAllocationUserData(a.blockData.handle, a)
	if err != nil {
		panic(fmt.Sprintf("unexpected error when attempting to set new metadata during block swap: %+v", err))
	}

	return a.mapCount, nil
}
