package vam

import (
	cerrors "errors"
	"fmt"
	"unsafe"

	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/pkg/errors"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/memutils/metadata"
	"github.com/vkngwrapper/arsenal/vam/internal/utils"
	"github.com/vkngwrapper/arsenal/vam/internal/vulkan"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"github.com/vkngwrapper/extensions/v2/khr_buffer_device_address"
)

// ErrAllocationAlreadyFreed is an error that is wrapped and returned when carrying out device-memory-accessing
// operations against an Allocation that has already been freed. If one of these operations returns an error,
// you can use errors.Is and errors.As to determine whether the error occurred because you were attempting to
// interact with an already-freed allocation.
var ErrAllocationAlreadyFreed = cerrors.New("already freed")

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

// Allocation represents a single logical memory allocation made with Vulkan. This object *may*
// represent a single core1_0.DeviceMemory object, if it is a dedicated allocation, but more likely
// represents a small portion of a larger memory object. Your must allocate these on your own using
// whatever method you wish, and then pass a pointer to an Allocator to populate this object with
// useful data. This process will cause the Allocation to escape the stack, so Allocations will always
// live on the heap. However, reusing a freed Allocation is completely legal, so using object pools to manage
// the Allocation objects works just fine.
type Allocation struct {
	alignment uint
	size      int
	userData  any
	name      string
	flags     allocationFlags

	memoryTypeIndex   int
	allocationType    allocationType
	suballocationType suballocationType
	memory            *vulkan.SynchronizedMemory

	parentAllocator *Allocator

	blockData     blockData
	dedicatedData dedicatedData
	mapLock       utils.OptionalRWMutex
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
	suballocationType suballocationType,
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
	suballocationType suballocationType,
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

// SetName applies a name to the Allocation that can be used to identify it while debugging or for other
// diagnostic purposes
func (a *Allocation) SetName(name string) {
	a.name = name
}

// SetUserData attaches a piece of arbitrary data to Allocation that can be used to identify it while debugging
// or for other diagnostic purposes. In particular, if you have an object used to identify a resource by, for instance
// combining the allocation with a core1_0.Buffer or core1_0.Image, you may find it useful to make that the attached
// userdata. It especially comes in handy during defragmentation, where you will be given Allocation objects and
// asked to copy their data elsewhere.
func (a *Allocation) SetUserData(userData any) {
	a.userData = userData
}

// UserData retrieves the data attached with SetUserData
func (a *Allocation) UserData() any {
	return a.userData
}

// Name retrieves the name assigned with SetName
func (a *Allocation) Name() string {
	return a.name
}

// MemoryTypeIndex retrieves index of the memory type in the physical device that the underlying device memory
// is allocated to
func (a *Allocation) MemoryTypeIndex() int { return a.memoryTypeIndex }

// Size is the number of bytes claimed by this allocation
func (a *Allocation) Size() int { return a.size }

// Alignment is the alignment of this allocation's offset in the larger memory block, if any
func (a *Allocation) Alignment() uint { return a.alignment }

// Memory is the underlying core1_0.DeviceMemory this Allocation is assigned to
func (a *Allocation) Memory() core1_0.DeviceMemory { return a.memory.VulkanDeviceMemory() }
func (a *Allocation) isPersistentMap() bool        { return a.flags&allocationPersistentMap != 0 }

// IsMappingAllowed returns true if Map is a legal operation, or false otherwise
func (a *Allocation) IsMappingAllowed() bool { return a.flags&allocationMappingAllowed != 0 }

// MemoryType retrieves the core1_0.MemoryType object for the memory type in the physical device that the underlying
// device memory is allocated to
func (a *Allocation) MemoryType() core1_0.MemoryType {
	return a.parentAllocator.deviceMemory.MemoryTypeProperties(a.memoryTypeIndex)
}

// FindOffset retrieves the offset of the allocation in the larger memory block, if any. This method
// may not run at O(1), so if you need access to it frequently, you should probably cache it. Be aware
// that defragmentation may change the return value suddenly.
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

// Map attempts to retrieve a direct unsafe.Pointer to the memory underlying this Allocation. The pointer
// retrieved will point directly to the first byte of this allocation's memory, not to the first byte of
// the larger memory block or anything like that.
//
// As a device-memory-accessing method, Map will take out a read lock on this Allocation and may block
// if defragmentation is in progress or the Allocation is currently being freed. If the Allocation has
// already been freed, this method will return a VkResult of VkErrorUnknown, with an error that wraps
// ErrAllocationAlreadyFreed.
//
// This method will return VKErrorMemoryMapFailed if the allocation was created without mapping functionality
// being requested. Any other error value came from Vulkan.
//
// It is not legal for consumers of this library to Map allocations long-term, even if the memory
// was allocated with the persistent map option. Mapping takes out a read lock until Unmap is called,
// and so defragmentation and Free calls will halt until the memory is unmapped. Instead, feel free to
// call Map/Unmap as often as you like- you can use the Persistent Map option to prevent the underlying
// memory from "really" unmapping, and even if you don't, logic is in place to make the map persistent
// if Map/Unmap are called frequently.
//
// It is also not legal to hold the pointer after calling Unmap, even if you are using Persistent Map.
// Defragmentation may relocate allocation memory, and Map/Unmap are used to ensure that this does not
// cause you issues.
func (a *Allocation) Map() (unsafe.Pointer, common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::Map")

	return a.mapOptionalLock(true)
}

func (a *Allocation) mapOptionalLock(useLock bool) (unsafe.Pointer, common.VkResult, error) {
	if !a.IsMappingAllowed() {
		return nil, core1_0.VKErrorMemoryMapFailed, errors.New("attempted to perform a map for an allocation that does not permit mapping")
	}

	if useLock {
		a.mapLock.RLock()
	}

	if a.memory == nil {
		return nil, core1_0.VKErrorUnknown, errors.Wrap(ErrAllocationAlreadyFreed, "failed to map memory")
	}

	ptr, res, err := a.memory.Map(1, 0, common.WholeSize, 0)
	if err != nil || ptr == nil {
		return ptr, res, err
	}

	offset := a.FindOffset()

	return unsafe.Add(ptr, offset), res, nil
}

// Unmap unmaps a pointer previously received from Map and frees the read lock that was held against this
// Allocation so that defragmentation and Free operations can move forward. This method will return an error
// if it has been called more times than Map has been called for the underlying core1_0.DeviceMemory
func (a *Allocation) Unmap() error {
	a.parentAllocator.logger.Debug("Allocation::Unmap")

	a.mapLock.RUnlock()
	return a.memory.Unmap(1)
}

// Flush will flush the memory cache for a specified region of memory within this Allocation.
//
// As a device-memory-accessing method, Flush will briefly take out a read lock on this Allocation and may block
// if defragmentation is in progress or the Allocation is currently being freed. If the Allocation has
// already been freed, this method will return a VkResult of VkErrorUnknown, with an error that wraps
// ErrAllocationAlreadyFreed.
//
// All other return errors with VkErrorUnknown are due to the offset and size representing an invalid
// region within this Allocation.  Other error types came from Vulkan
//
// offset - The offset from the start of this Allocation to begin flushing
//
// size - The size of the region to flush
func (a *Allocation) Flush(offset, size int) (common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::Flush")

	return a.flushOrInvalidate(offset, size, vulkan.CacheOperationFlush)
}

// Invalidate will invalidate the memory cache for a specified region of memory within this Allocation
//
// As a device-memory-accessing method, Invalidate will briefly take out a read lock on this Allocation and may block
// if defragmentation is in progress or the Allocation is currently being freed. If the Allocation has
// already been freed, this method will return a VkResult of VkErrorUnknown, with an error that wraps
// ErrAllocationAlreadyFreed.
//
// All other return errors with VkErrorUnknown are due to the offset and size representing an invalid
// region within this Allocation.  Other error types came from Vulkan
//
// offset - The offset from the start of this Allocation to begin invalidating
//
// size - The size of the region to invalidate
func (a *Allocation) Invalidate(offset, size int) (common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::Invalidate")

	return a.flushOrInvalidate(offset, size, vulkan.CacheOperationInvalidate)
}

// BindBufferMemory will bind this Allocation's memory to a provided core1_0.Buffer
//
// As a device-memory-accessing method, BindBufferMemory will briefly take out a read lock on this Allocation and may block
// if defragmentation is in progress or the Allocation is currently being freed. If the Allocation has
// already been freed, this method will return a VkResult of VkErrorUnknown, with an error that wraps
// ErrAllocationAlreadyFreed.
//
// Other error types came from Vulkan.
func (a *Allocation) BindBufferMemory(buffer core1_0.Buffer) (common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::BindBufferMemory")

	return a.bindBufferMemory(0, buffer, nil)
}

// BindBufferMemoryWithOffset will bind a portion of this Allocation's memory, beginning at the provided offset,
// to a provided core1_0.Buffer.
//
// As a device-memory-accessing method, BindBufferMemoryWithOffset will briefly take out a read lock on this Allocation and may block
// if defragmentation is in progress or the Allocation is currently being freed. If the Allocation has
// already been freed, this method will return a VkResult of VkErrorUnknown, with an error that wraps
// ErrAllocationAlreadyFreed.
//
// Other error types came from Vulkan.
//
// offset - The offset to the beginning of the portion of memory to bind
//
// buffer - The core1_0.Buffer to bind the memory to
//
// next - A common.Options chain to add to the end of the BindBufferMemory2 options chain created by vam. nil is an acceptable value
func (a *Allocation) BindBufferMemoryWithOffset(offset int, buffer core1_0.Buffer, next common.Options) (common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::BindBufferMemoryWithOffset")

	return a.bindBufferMemory(offset, buffer, next)
}

func (a *Allocation) bindBufferMemory(offset int, buffer core1_0.Buffer, next common.Options) (common.VkResult, error) {
	if buffer == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to bind a nil buffer")
	}
	a.mapLock.RLock()
	defer a.mapLock.RUnlock()

	if a.memory == nil {
		return core1_0.VKErrorUnknown, errors.Wrap(ErrAllocationAlreadyFreed, "failed to bind buffer memory")
	}

	switch a.allocationType {
	case allocationTypeDedicated:
		return a.memory.BindVulkanBuffer(offset, buffer, next)
	case allocationTypeBlock:
		allocOffset := a.FindOffset()
		return a.memory.BindVulkanBuffer(offset+allocOffset, buffer, next)
	}

	return core1_0.VKErrorUnknown, errors.Errorf("attempted to bind an allocation with an unknown type: %s", a.allocationType.String())
}

// BindImageMemory will bind this Allocation's memory to a provided core1_0.Image
//
// As a device-memory-accessing method, BindImageMemory will briefly take out a read lock on this Allocation and may block
// if defragmentation is in progress or the Allocation is currently being freed. If the Allocation has
// already been freed, this method will return a VkResult of VkErrorUnknown, with an error that wraps
// ErrAllocationAlreadyFreed.
//
// Other error types came from Vulkan.
func (a *Allocation) BindImageMemory(image core1_0.Image) (common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::BindImageMemory")

	return a.bindImageMemory(0, image, nil)
}

// BindImageMemoryWithOffset will bind a portion of this Allocation's memory, beginning at the provided offset,
// to a provided core1_0.Image.
//
// As a device-memory-accessing method, BindImageMemoryWithOffset will briefly take out a read lock on this Allocation and may block
// if defragmentation is in progress or the Allocation is currently being freed. If the Allocation has
// already been freed, this method will return a VkResult of VkErrorUnknown, with an error that wraps
// ErrAllocationAlreadyFreed.
//
// Other error types came from Vulkan.
//
// offset - The offset to the beginning of the portion of memory to bind
//
// image - The core1_0.Image to bind the memory to
//
// next - A common.Options chain to add to the end of the BindImageMemory2 options chain created by vam. nil is an acceptable value
func (a *Allocation) BindImageMemoryWithOffset(offset int, image core1_0.Image, next common.Options) (common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::BindImageMemoryWIthOffset")

	return a.bindImageMemory(offset, image, next)
}

func (a *Allocation) bindImageMemory(offset int, image core1_0.Image, next common.Options) (common.VkResult, error) {
	if image == nil {
		return core1_0.VKErrorUnknown, errors.New("attempted to bind a nil image")
	}

	a.mapLock.RLock()
	defer a.mapLock.RUnlock()

	if a.memory == nil {
		return core1_0.VKErrorUnknown, errors.Wrap(ErrAllocationAlreadyFreed, "failed to bind image memory")
	}

	switch a.allocationType {
	case allocationTypeDedicated:
		return a.memory.BindVulkanImage(offset, image, next)
	case allocationTypeBlock:
		allocOffset := a.FindOffset()
		return a.memory.BindVulkanImage(offset+allocOffset, image, next)
	}

	return core1_0.VKErrorUnknown, errors.Errorf("attempted to bind an allocation with an unknown type: %s", a.allocationType.String())
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
	if size == 0 || size < common.WholeSize || !a.parentAllocator.deviceMemory.IsMemoryTypeHostNonCoherent(a.memoryTypeIndex) {
		return false, nil
	}

	nonCoherentAtomSize := a.parentAllocator.deviceMemory.DeviceProperties().Limits.NonCoherentAtomSize
	allocationSize := a.Size()

	if offset > allocationSize {
		return false, errors.Errorf("offset %d is past the end of the allocation, which is size %d", offset, allocationSize)
	}
	if size > 0 && (offset+size) > allocationSize {
		return false, errors.Errorf("offset %d places the end of the block %d past the end of the allocation, which is size %d", offset, offset+size, allocationSize)
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
		if size == common.WholeSize {
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

	return false, errors.Errorf("attempted to get the flush or invalidate range of an allocation with invalid type %s", a.allocationType.String())
}

func (a *Allocation) flushOrInvalidate(offset, size int, operation vulkan.CacheOperation) (common.VkResult, error) {
	a.mapLock.RLock()
	defer a.mapLock.RUnlock()

	if a.memory == nil {
		return core1_0.VKErrorUnknown, errors.Wrap(ErrAllocationAlreadyFreed, "failed to flush or invalidate memory")
	}

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

// ParentPool returns the Pool used to create this Allocation, if anyy
func (a *Allocation) ParentPool() *Pool {
	switch a.allocationType {
	case allocationTypeBlock:
		return a.blockData.block.parentPool
	case allocationTypeDedicated:
		return a.dedicatedData.parentPool
	}

	panic(fmt.Sprintf("invalid allocation type: %s", a.allocationType.String()))
}

// Free frees this Allocation and, potentially, the underlying memory. It is valid to reuse this Allocation
// immediately after freeing it by passing it to another Allocator method that allocates memory
//
// Free briefly takes out a write lock against this Allocation, so it will block if any device-memory-accessing
// methods are in flight or if this memory is currently mapped with Map. It will also block those methods from
// continuing while it is running (once it completes, they will fail, as the Allocation has been freed).
//
// If the Allocation has already been freed, this method will return a VkResult of VkErrorUnknown, with an
// error that wraps ErrAllocationAlreadyFreed. If this Allocation was persistently mapped, this method will
// also return any errors returned by Unmap.
func (a *Allocation) Free() error {
	a.parentAllocator.logger.Debug("Allocation::Free")

	return a.free()
}

func (a *Allocation) free() error {
	a.mapLock.Lock()
	defer a.mapLock.Unlock()
	if a.memory == nil {
		return errors.Wrap(ErrAllocationAlreadyFreed, "failed to free memory")
	}

	// Attempt to create a one-length slice for the provided alloc pointer
	allocSlice := unsafe.Slice(a, 1)
	err := a.parentAllocator.multiFreeMemory(
		allocSlice,
	)
	if err == nil {
		a.memory = nil
	}
	return err
}

func (a *Allocation) swapBlockAllocation(alloc *Allocation) error {
	if alloc == nil {
		panic("tried to swap blocks with a nil allocation")
	} else if a.allocationType != allocationTypeBlock {
		panic("tried to swap blocks but this is not a block allocation")
	} else if alloc.allocationType != allocationTypeBlock {
		panic(fmt.Sprintf("tried to swap blocks with a non-block allocation: %s", alloc.allocationType.String()))
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

	return nil
}

// DestroyImage is roughly equivalent to calling image.Destroy() followed by Free. The main benefit
// of this method is that image.Destroy() will use the allocation callbacks passed to the Allocator
// that was used to allocate this memory.
func (a *Allocation) DestroyImage(image core1_0.Image) error {
	a.parentAllocator.logger.Debug("Allocation::DestroyImage")

	if image != nil {
		image.Destroy(a.parentAllocator.allocationCallbacks)
	}

	return a.free()
}

// DestroyBuffer is roughly equivalent to calling buffer.Destroy() followed by Free. The main benefit
// of this method is that buffer.Destroy() will use the allocation callbacks passed to the Allocator
// that was used to allocate this memory.
func (a *Allocation) DestroyBuffer(buffer core1_0.Buffer) error {
	a.parentAllocator.logger.Debug("Allocation::DestroyBuffer")

	if buffer != nil {
		buffer.Destroy(a.parentAllocator.allocationCallbacks)
	}

	return a.free()
}

// CreateAliasingBuffer creates and binds a new core1_0.Buffer for this Allocation. It can be used for anything,
// but it is usually used for Aliasing Buffers, which you can read about [here](https://gpuopen-librariesandsdks.github.io/VulkanMemoryAllocator/html/resource_aliasing.html).
//
// As a device-memory-accessing method, CreateAliasingBuffer will briefly take out a read lock on this Allocation and may block
// if defragmentation is in progress or the Allocation is currently being freed. If the Allocation has
// already been freed, this method will return a VkResult of VkErrorUnknown, with an error that wraps
// ErrAllocationAlreadyFreed.
//
// This method will return VKErrorExtensionNotPresent if the bufferInfo.Usage attempts to use Shader Device Address
// but the khr_buffer_device_address extension is not available (and core 1.2 is not active). Other VKErrorUnknown
// errors indicate an issue with the bufferInfo object, such as 0 size or a requested size that cannot fit within
// this Allocation. Other errors come from Vulkan.
//
// bufferInfo - the core1_0.BufferCreateInfo that will be passed to core1_0.Device.CreateBuffer
func (a *Allocation) CreateAliasingBuffer(bufferInfo core1_0.BufferCreateInfo) (core1_0.Buffer, common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::CreateAliasingBuffer")

	return a.createAliasingBuffer(0, &bufferInfo)
}

// CreateAliasingBufferWithOffset creates and binds a new core1_0.Buffer for a portion of this Allocation that
// begins at the provided offset. It can be used for anything, but it is usually used for Aliasing Buffers,
// which you can read about [here](https://gpuopen-librariesandsdks.github.io/VulkanMemoryAllocator/html/resource_aliasing.html).
//
// As a device-memory-accessing method, CreateAliasingBufferWithOffset will briefly take out a read lock on this Allocation and may block
// if defragmentation is in progress or the Allocation is currently being freed. If the Allocation has
// already been freed, this method will return a VkResult of VkErrorUnknown, with an error that wraps
// ErrAllocationAlreadyFreed.
//
// This method will return VKErrorExtensionNotPresent if the bufferInfo.Usage attempts to use Shader Device Address
// but the khr_buffer_device_address extension is not available (and core 1.2 is not active). Other VKErrorUnknown
// errors indicate an issue with the bufferInfo object, such as 0 size or a requested size that cannot fit within
// this Allocation. Other errors come from Vulkan.
//
// offset - The offset from the beginning of this Allocation to the start of the created core1_0.Buffer
//
// bufferInfo - the core1_0.BufferCreateInfo that will be passed to core1_0.Device.CreateBuffer
func (a *Allocation) CreateAliasingBufferWithOffset(offset int, bufferInfo core1_0.BufferCreateInfo) (core1_0.Buffer, common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::CreateAliasingBufferWithOffset")

	return a.createAliasingBuffer(offset, &bufferInfo)
}

func (a *Allocation) createAliasingBuffer(offset int, bufferInfo *core1_0.BufferCreateInfo) (buffer core1_0.Buffer, res common.VkResult, err error) {
	if bufferInfo == nil {
		panic("nil bufferInfo")
	}

	a.mapLock.RLock()
	defer a.mapLock.RUnlock()

	if bufferInfo.Size == 0 {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to create a buffer of 0 size")
	} else if offset+bufferInfo.Size > a.Size() {
		return nil, core1_0.VKErrorUnknown, errors.Errorf("attempted to create a buffer that was too big to fit in its allocation: offset %d, size %d, buffer would have ended at %d but allocation is only %d bytes", offset, bufferInfo.Size, offset+bufferInfo.Size, a.Size())
	} else if bufferInfo.Usage&khr_buffer_device_address.BufferUsageShaderDeviceAddress != 0 && a.parentAllocator.extensionData.BufferDeviceAddress == nil {
		return nil, core1_0.VKErrorExtensionNotPresent, errors.New("attempted to use BufferUsageShaderDeviceAddress, but khr_buffer_device_address is not loaded")
	} else if a.memory == nil {
		return nil, core1_0.VKErrorUnknown, errors.Wrap(ErrAllocationAlreadyFreed, "failed to create aliasing buffer")
	}

	buffer, res, err = a.parentAllocator.device.CreateBuffer(a.parentAllocator.allocationCallbacks, *bufferInfo)
	if err != nil {
		return buffer, res, err
	}
	defer func() {
		if err != nil {
			buffer.Destroy(a.parentAllocator.allocationCallbacks)
		}
	}()

	res, err = a.bindBufferMemory(offset, buffer, nil)
	return buffer, res, err
}

// CreateAliasingImage creates and binds a new core1_0.Image for this Allocation.
// It can be used for anything, but it is usually used for Aliasing Images,
// which you can read about [here](https://gpuopen-librariesandsdks.github.io/VulkanMemoryAllocator/html/resource_aliasing.html).
//
// As a device-memory-accessing method, CreateAliasingImage will briefly take out a read lock on this Allocation and may block
// if defragmentation is in progress or the Allocation is currently being freed. If the Allocation has
// already been freed, this method will return a VkResult of VkErrorUnknown, with an error that wraps
// ErrAllocationAlreadyFreed.
//
// Other VKErrorUnknown errors indicate an issue with the imageInfo object, such as 0-sized dimensions.
// Other errors come from Vulkan.
//
// imageInfo - the core1_0.ImageCreateInfo that will be passed to core1_0.Device.CreateImage
func (a *Allocation) CreateAliasingImage(imageInfo core1_0.ImageCreateInfo) (core1_0.Image, common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::CreateAliasingImage")

	return a.createAliasingImage(0, &imageInfo)
}

// CreateAliasingImageWithOffset creates and binds a new core1_0.Image for the portion of this Allocation.
// beginning at the provided offset.
// It can be used for anything, but it is usually used for Aliasing Images,
// which you can read about [here](https://gpuopen-librariesandsdks.github.io/VulkanMemoryAllocator/html/resource_aliasing.html).
//
// As a device-memory-accessing method, CreateAliasingImageWithOffset will briefly take out a read lock on this Allocation and may block
// if defragmentation is in progress or the Allocation is currently being freed. If the Allocation has
// already been freed, this method will return a VkResult of VkErrorUnknown, with an error that wraps
// ErrAllocationAlreadyFreed.
//
// Other VKErrorUnknown errors indicate an issue with the imageInfo object, such as 0-sized dimensions.
// Other errors come from Vulkan.
//
// offset - The offset from the beginning of this Allocation to the start of the created core1_0.Image
//
// imageInfo - the core1_0.ImageCreateInfo that will be passed to core1_0.Device.CreateImage
func (a *Allocation) CreateAliasingImageWithOffset(offset int, imageInfo core1_0.ImageCreateInfo) (core1_0.Image, common.VkResult, error) {
	a.parentAllocator.logger.Debug("Allocation::CreateAliasingImageWithOffset")

	return a.createAliasingImage(offset, &imageInfo)
}

func (a *Allocation) createAliasingImage(offset int, imageInfo *core1_0.ImageCreateInfo) (image core1_0.Image, res common.VkResult, err error) {
	if imageInfo == nil {
		panic("nil imageInfo")
	}

	a.mapLock.RLock()
	defer a.mapLock.RUnlock()

	if imageInfo.Extent.Width == 0 || imageInfo.Extent.Height == 0 {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to create 0-sized image")
	} else if imageInfo.Extent.Depth == 0 {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to create 0-depth image")
	} else if imageInfo.MipLevels == 0 {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to create an image with 0 mip levels")
	} else if imageInfo.ArrayLayers == 0 {
		return nil, core1_0.VKErrorUnknown, errors.New("attempted to create an image with 0 array layers")
	} else if a.memory == nil {
		return nil, core1_0.VKErrorUnknown, errors.Wrap(ErrAllocationAlreadyFreed, "failed to create aliasing buffer")
	}

	image, res, err = a.parentAllocator.device.CreateImage(a.parentAllocator.allocationCallbacks, *imageInfo)
	if err != nil {
		return image, res, err
	}
	defer func() {
		if err != nil {
			image.Destroy(a.parentAllocator.allocationCallbacks)
		}
	}()

	res, err = a.bindImageMemory(offset, image, nil)
	return image, res, err
}
