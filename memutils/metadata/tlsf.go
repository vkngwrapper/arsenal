package metadata

import (
	"fmt"
	"math"
	"math/bits"
	"sync"
	"unsafe"

	"github.com/dolthub/swiss"
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/pkg/errors"
	"github.com/vkngwrapper/arsenal/memutils"
)

const (
	smallBufferSize        = 256
	secondLevelIndex uint8 = 5
	memoryClassShift       = 7
	maxMemoryClasses       = 65 - memoryClassShift
)

var blockAllocator = sync.Pool{
	New: func() any {
		return &tlsfBlock{}
	},
}

type tlsfBlock struct {
	offset       int
	size         int
	prevPhysical *tlsfBlock
	nextPhysical *tlsfBlock

	prevFree *tlsfBlock
	nextFree *tlsfBlock

	userData    any
	blockHandle BlockAllocationHandle
}

func (b *tlsfBlock) MarkFree() {
	b.prevFree = nil
}

func (b *tlsfBlock) MarkTaken() {
	b.prevFree = b
}

func (b *tlsfBlock) IsFree() bool {
	return b.prevFree != b
}

// TLSFBLockMetadata is a BlockMetadata implementation of the two-level segregated
// fit algorithm, seen here: http://www.gii.upv.es/tlsf/
//
// TLSF is a general-purpose random-access memory allocator that is both fast and efficient.
type TLSFBlockMetadata struct {
	BlockMetadataBase

	allocCount        int
	blocksFreeCount   int
	blocksFreeSize    int
	isFreeBitmap      uint32
	memoryClasses     int
	innerIsFreeBitmap [maxMemoryClasses]uint32

	// For speed reasons, we use the tlsfblock pointer itself as the BlockAllocationHandle (that's how it's
	// done in C too).  However, if we don't store tlsfBlock somewhere, go will garbage collect it, so
	// we use this map.  We just store it in here for safekeeping and then delete it when the block is getting
	// deleted
	handleKey *swiss.Map[BlockAllocationHandle, *tlsfBlock]
	freeList  []*tlsfBlock
	nullBlock *tlsfBlock
	tailBlock *tlsfBlock
}

var _ BlockMetadata = &TLSFBlockMetadata{}

// NewTLSFBlockMetadata creates a new TLSFBlockMetadata using granularity properties. The
// properties are passed to NewBlockMetadata.
func NewTLSFBlockMetadata(bufferImageGranularity int, granularityHandler GranularityCheck) *TLSFBlockMetadata {
	return &TLSFBlockMetadata{
		BlockMetadataBase: NewBlockMetadata(bufferImageGranularity, granularityHandler),
	}
}

func (m *TLSFBlockMetadata) allocateBlock() *tlsfBlock {
	b := blockAllocator.Get().(*tlsfBlock)
	b.offset = 0
	b.size = 0
	b.prevPhysical = nil
	b.nextPhysical = nil
	b.nextFree = nil
	b.prevFree = nil
	b.userData = nil
	b.blockHandle = BlockAllocationHandle(uintptr(unsafe.Pointer(b)))
	m.handleKey.Put(b.blockHandle, b)
	return b
}

func (m *TLSFBlockMetadata) freeBlock(b *tlsfBlock) {
	m.handleKey.Delete(b.blockHandle)
	blockAllocator.Put(b)
}

func (m *TLSFBlockMetadata) getBlock(handle BlockAllocationHandle) (*tlsfBlock, error) {
	return (*tlsfBlock)(unsafe.Pointer(uintptr(handle))), nil
}

// Init prepares this structure for allocations and sizes the block in bytes based on the parameter size.
func (m *TLSFBlockMetadata) Init(size int) {
	m.BlockMetadataBase.Init(size)
	m.handleKey = swiss.NewMap[BlockAllocationHandle, *tlsfBlock](42)

	m.nullBlock = m.allocateBlock()
	m.nullBlock.size = size
	m.nullBlock.MarkFree()
	m.tailBlock = m.nullBlock
	memoryClass := m.sizeToMemoryClass(size)
	sli := m.sizeToSecondIndex(size, memoryClass)

	listSize := 1
	sliMask := int(uint(1) << secondLevelIndex)
	if memoryClass != 0 {
		listSize = int(memoryClass-1)*sliMask + int(sli+1)
	}

	listSize += 4

	m.memoryClasses = int(memoryClass + 2)
	m.freeList = make([]*tlsfBlock, listSize)
}

// Validate performs internal consistency checks on the metadata. These checks may be expensive, depending
// on the implementation. When the implementation is functioning correctly, it should not be possible
// for this method to return an error, but this may assist in diagnosing issues with the implementation.
func (m *TLSFBlockMetadata) Validate() error {
	if m.SumFreeSize() > m.Size() {
		return errors.New("invalid metadata free size")
	}

	calculatedSize := m.nullBlock.size
	calculatedFreeSize := m.nullBlock.size
	var allocCount, freeCount, freeListCount int

	// Check integrity of free lists
	for listIndex := 0; listIndex < len(m.freeList); listIndex++ {
		block := m.freeList[listIndex]
		if block == nil {
			continue
		}

		if !block.IsFree() {
			return errors.Errorf("block at offset %d is in the free list but is not free", block.offset)
		}

		if block.prevFree != nil {
			return errors.Errorf("block at offset %d is the head of a free list but has a previous block", block.offset)
		}

		freeListCount++
		for block.nextFree != nil {
			if !block.nextFree.IsFree() {
				return errors.Errorf("block at offset %d is in the free list but it is not free", block.nextFree.offset)
			}
			if block.nextFree.prevFree != block {
				return errors.Errorf("block at offset %d lists the block at offset %d as its next block, but the reverse reference is broken", block.offset, block.nextFree.offset)
			}

			freeListCount++
			block = block.nextFree
		}
	}

	if m.nullBlock.nextPhysical != nil {
		return errors.New("null block must be the tail of its physical block chain")
	}

	if m.nullBlock.prevPhysical != nil && m.nullBlock.prevPhysical.nextPhysical != m.nullBlock {
		return errors.New("null block has a physical block before it in its chain, but the reverse reference is broken")
	}

	nextOffset := m.nullBlock.offset
	validateCtx := m.granularityHandler.StartValidation()

	for prev := m.nullBlock.prevPhysical; prev != nil; prev = prev.prevPhysical {
		if prev.offset+prev.size != nextOffset {
			return errors.Errorf("physical block at offset %d does not end at the next block's start offset", prev.offset)
		}

		nextOffset = prev.offset
		calculatedSize += prev.size

		if prev.IsFree() {
			freeCount++

			calculatedFreeSize += prev.size
		} else {
			allocCount++

			err := m.granularityHandler.Validate(validateCtx, prev.offset, prev.size)
			if err != nil {
				return err
			}
		}

		if prev.prevPhysical != nil && prev.prevPhysical.nextPhysical != prev {
			return errors.Errorf("block at offset %d has a previous physical block, but the reverse reference is broken", prev.offset)
		}
	}

	if freeListCount != freeCount {
		return errors.Errorf("the number of free blocks in the physical list and the number of blocks in the free list do not match! free list size: %d, physical list free blocks: %d", freeListCount, freeCount)
	}

	err := m.granularityHandler.FinishValidation(validateCtx)
	if err != nil {
		return err
	}

	if nextOffset != 0 {
		return errors.Errorf("the first physical block should have an offset of 0, but instead it has an offset of %d", nextOffset)
	}

	if calculatedSize != m.size {
		return errors.Errorf("the full size of the metadata is %d, but the blocks only added up to %d", m.size, calculatedSize)
	}

	if calculatedFreeSize != m.SumFreeSize() {
		return errors.Errorf("the free size of the metadata is %d, but the free blocks only added up to %d", m.SumFreeSize(), calculatedFreeSize)
	}

	if allocCount != m.allocCount {
		return errors.Errorf("the allocation count of the metadata is %d, but the taken blocks only added up to %d", m.allocCount, allocCount)
	}

	if freeCount != m.blocksFreeCount {
		return errors.Errorf("the free block count of the metadata is %d, but there were only %d free blocks", m.blocksFreeCount, freeCount)
	}

	return nil
}

// SupportsRandomAccess returns a boolean indicating whether the implementation allows allocations
// to be made in arbitrary sections of the managed block.  This implementation always returns
// true.
func (m *TLSFBlockMetadata) SupportsRandomAccess() bool {
	return true
}

// AddDetailedStatistics sums this block's allocation statistics into the statistics currently present
// in the provided memutils.DetailedStatistics object.
func (m *TLSFBlockMetadata) AddDetailedStatistics(stats *memutils.DetailedStatistics) {
	stats.BlockCount++
	stats.BlockBytes += m.size
	if m.nullBlock.size > 0 {
		stats.AddUnusedRange(m.nullBlock.size)
	}

	for block := m.nullBlock.prevPhysical; block != nil; block = block.prevPhysical {
		if block.IsFree() {
			stats.AddUnusedRange(block.size)
		} else {
			stats.AddAllocation(block.size)
		}
	}
}

// AddStatistics sums this block's allocation statistics into the statistics currently present in the
// provided memutils.Statistics object.
func (m *TLSFBlockMetadata) AddStatistics(stats *memutils.Statistics) {
	stats.BlockCount++
	stats.AllocationCount += m.allocCount
	stats.BlockBytes += m.size
	stats.AllocationBytes += m.size - m.SumFreeSize()
}

func (m *TLSFBlockMetadata) getListIndexFromSize(size int) int {
	memoryClass := m.sizeToMemoryClass(size)
	secondIndex := m.sizeToSecondIndex(size, memoryClass)
	return m.getListIndex(memoryClass, secondIndex)
}

func (m *TLSFBlockMetadata) getListIndex(memoryClass uint8, secondIndex uint16) int {
	if memoryClass == 0 {
		return int(secondIndex)
	}

	i := uint32(memoryClass-1)*uint32(uint(1)<<secondLevelIndex) + uint32(secondIndex)

	return int(i) + 4
}

// AllocationCount returns the number of suballocations currently live in the implementation. This number
// should generally be the number of successful allocations minus the number of successful frees.
func (m *TLSFBlockMetadata) AllocationCount() int {
	return m.allocCount
}

// FreeRegionsCount returns the number of unique regions of free memory in the block.
// That means all the free segments between allocations and the big free block at the end.
func (m *TLSFBlockMetadata) FreeRegionsCount() int {
	var hasNullBlock int
	if m.nullBlock.size > 0 {
		hasNullBlock = 1
	}
	return m.blocksFreeCount + hasNullBlock
}

// SumFreeSize returns the number of free bytes of memory in the block.
func (m *TLSFBlockMetadata) SumFreeSize() int {
	return m.blocksFreeSize + m.nullBlock.size
}

// IsEmpty will return true if this block has no live suballocations
func (m *TLSFBlockMetadata) IsEmpty() bool {
	return m.nullBlock.offset == 0
}

func (m *TLSFBlockMetadata) sizeToMemoryClass(size int) uint8 {
	if size > smallBufferSize {
		mostSignificantBit := uint8(63 - bits.LeadingZeros64(uint64(size)))
		return mostSignificantBit - memoryClassShift
	}

	return 0
}

func (m *TLSFBlockMetadata) sizeToSecondIndex(size int, memoryClass uint8) uint16 {
	if memoryClass != 0 {
		mask := uint(1) << secondLevelIndex
		indexVal := uint(size) >> (memoryClass + memoryClassShift - secondLevelIndex)
		return uint16(indexVal ^ mask)
	}

	return uint16((size - 1) / 64)
}

func (m *TLSFBlockMetadata) sizeForNextList(allocSize int) int {
	// Round up to the next block
	sizeForNextList := allocSize

	smallSizeStep := smallBufferSize / 4
	if allocSize > smallBufferSize {
		mostSignificantBit := 63 - bits.LeadingZeros64(uint64(allocSize))
		sizeForNextList += int(uint(1) << (mostSignificantBit - int(secondLevelIndex)))
	} else if allocSize > smallBufferSize-smallSizeStep {
		sizeForNextList = smallBufferSize + 1
	} else {
		sizeForNextList += smallSizeStep
	}

	return sizeForNextList
}

// MayHaveFreeBlock should return a heuristic indicating whether the block could possibly support a new
// allocation of the provided type and size. allocType is a value that has meaning within the memory
// system consuming BlockMetadata. The implementation may or may not care, and could potentially pass
// the value back to some callback or interface provided by the consumer. The size parameter is the size
// in bytes of the hypothetical allocation.
//
// This method is used by memutils.defrag to very rapidly determine whether it can ignore blocks when
// trying to reposition allocations. As a result, the most important requirement for the implementation
// is that this method be fast and not produce false negatives. False positives are ok, but ideal defrag performance
// requires that this method balance runtime with the likelihood of false positives.
//
// It is completely acceptable for consumers to use this method for the same purpose as memutils.defrag
// (determine whether a block can be ignored while attempting to rapidly make allocations of a particular
// size).
func (m *TLSFBlockMetadata) MayHaveFreeBlock(allocType uint32, size int) bool {
	if m.nullBlock.size >= size {
		return true
	}

	if size > m.blocksFreeSize {
		return false
	}

	roundedSize, _ := m.granularityHandler.RoundUpAllocRequest(allocType, size, 1)
	memoryClass := m.sizeToMemoryClass(roundedSize)
	freeMap := m.isFreeBitmap & (math.MaxUint32 << memoryClass)
	return freeMap != 0
}

// CreateAllocationRequest retrieves an AllocationRequest object indicating where and how the implementation
// would prefer to allocate the requested memory. That object can be passed to Alloc to commit the
// allocation.
//
// allocSize - the size in bytes of the requested allocation
// allocAlignment - the minimum alignment of the requested allocation. The implementation may increase
// the alignment above this value, but may not reduce it below this value
// upperAddress - In implementations that split the memory block into two tranches (such as
// LinearBlockMetadata and its double stack mode), this parameter indicates that the allocation should
// be made in the upper tranch if true. When there is only a single tranch of memory in the implementation,
// the implementation should return an error when this argument is true.
// allocType - Memory-system-dependent allocation type value. The consumer may care about this.
// Implementations usually have a consumer-provided "granularity handler" which may care about this.
// strategy - Whether to prioritize memory usage, memory offset, or allocation speed when choosing
// a place for the requested allocation.
// maxOffset - This parameter should usually be math.MaxInt. The requested allocation must fail
// if the allocation cannot be placed at an offset before the provided maxOffset. This is primarily
// used by memutils.defrag to make relocating an allocation within a block more performant.
func (m *TLSFBlockMetadata) CreateAllocationRequest(
	allocSize int, allocAlignment uint,
	upperAddress bool,
	allocType uint32,
	strategy AllocationStrategy,
	maxOffset int,
) (bool, AllocationRequest, error) {
	var allocRequest AllocationRequest

	if allocSize < 1 {
		return false, allocRequest, errors.Errorf("Invalid allocSize: %d", allocSize)
	}

	if upperAddress {
		return false, allocRequest, errors.New("AllocationCreateUpperAddress can only be used with the Linear algorithm")
	}

	memutils.DebugValidate(m)

	// Round up granularity
	allocSize, allocAlignment = m.granularityHandler.RoundUpAllocRequest(allocType, allocSize, allocAlignment)

	allocSize += memutils.DebugMargin

	// Is pool big enough?
	if allocSize > m.SumFreeSize() {
		return false, allocRequest, nil
	}

	// Any free blocks in the pool?
	if m.blocksFreeCount == 0 {
		if strategy&AllocationStrategyMinOffset != 0 && m.nullBlock.offset > maxOffset {
			return false, allocRequest, nil
		}

		success := m.checkBlock(m.nullBlock, len(m.freeList), allocSize, allocAlignment, allocType, &allocRequest)
		return success, allocRequest, nil
	}

	// Round up to the next block
	sizeForNextList := m.sizeForNextList(allocSize)

	nextListIndex := 0
	prevListIndex := 0
	doFullSearch := false
	var nextListBlock, prevListBlock *tlsfBlock

	// Check blocks according to the requested strategy
	if strategy&AllocationStrategyMinTime != 0 {
		// Check for larger block first
		nextListBlock, nextListIndex = m.findFreeBlock(sizeForNextList)

		if nextListBlock != nil {
			doFullSearch = true
			foundBlock := m.checkBlock(nextListBlock, nextListIndex, allocSize, allocAlignment, allocType, &allocRequest)
			if foundBlock {
				return foundBlock, allocRequest, nil
			}
		}

		// If not fitted then null block
		foundBlock := m.checkBlock(m.nullBlock, len(m.freeList), allocSize, allocAlignment, allocType, &allocRequest)
		if foundBlock {
			return foundBlock, allocRequest, nil
		}

		// Null block failed, search larger bucket
		for nextListBlock != nil {
			foundBlock = m.checkBlock(nextListBlock, nextListIndex, allocSize, allocAlignment, allocType, &allocRequest)
			if foundBlock {
				return foundBlock, allocRequest, nil
			}

			nextListBlock = nextListBlock.nextFree
		}

		// Failed again, check best fit bucket
		prevListBlock, prevListIndex = m.findFreeBlock(allocSize)

		for prevListBlock != nil {
			foundBlock = m.checkBlock(prevListBlock, prevListIndex, allocSize, allocAlignment, allocType, &allocRequest)
			if foundBlock {
				return foundBlock, allocRequest, nil
			}

			prevListBlock = prevListBlock.nextFree
		}
	} else if strategy&AllocationStrategyMinMemory != 0 {
		// Check best fit bucket
		prevListBlock, prevListIndex = m.findFreeBlock(allocSize)

		for prevListBlock != nil {
			foundBlock := m.checkBlock(prevListBlock, prevListIndex, allocSize, allocAlignment, allocType, &allocRequest)
			if foundBlock {
				return foundBlock, allocRequest, nil
			}

			prevListBlock = prevListBlock.nextFree
		}

		// If failed check null block
		foundBlock := m.checkBlock(m.nullBlock, len(m.freeList), allocSize, allocAlignment, allocType, &allocRequest)
		if foundBlock {
			return foundBlock, allocRequest, nil
		}

		// Check larger bucket
		nextListBlock, nextListIndex = m.findFreeBlock(sizeForNextList)

		for nextListBlock != nil {
			doFullSearch = true
			foundBlock = m.checkBlock(nextListBlock, nextListIndex, allocSize, allocAlignment, allocType, &allocRequest)
			if foundBlock {
				return foundBlock, allocRequest, nil
			}

			nextListBlock = nextListBlock.nextFree
		}
	} else if strategy&AllocationStrategyMinOffset != 0 {
		// Enumerate back to the first suitable block and then search forward- this is
		// different from VMA because it's more important to avoid unnecessary allocations in go
		// In VMA, it just makes a vector of block pointers and populates it by enumerating backward
		// through the list and then searches forward through the vector. In Go, in order to avoid
		// allocating a slice, we do it recursively.
		foundBlock := m.minOffsetCheckBlocks(allocSize, allocAlignment, allocType, &allocRequest, maxOffset)
		if foundBlock {
			return foundBlock, allocRequest, nil
		}

		// If failed, check null block
		foundBlock = m.checkBlock(m.nullBlock, len(m.freeList), allocSize, allocAlignment, allocType, &allocRequest)
		if foundBlock {
			return foundBlock, allocRequest, nil
		}
	} else {
		// Check larger bucket
		nextListBlock, nextListIndex = m.findFreeBlock(sizeForNextList)

		for nextListBlock != nil {
			doFullSearch = true
			foundBlock := m.checkBlock(nextListBlock, nextListIndex, allocSize, allocAlignment, allocType, &allocRequest)
			if foundBlock {
				return foundBlock, allocRequest, nil
			}

			nextListBlock = nextListBlock.nextFree
		}

		// If failed, check null block
		foundBlock := m.checkBlock(m.nullBlock, len(m.freeList), allocSize, allocAlignment, allocType, &allocRequest)
		if foundBlock {
			return foundBlock, allocRequest, nil
		}

		// Check best fit bucket
		prevListBlock, prevListIndex = m.findFreeBlock(allocSize)

		for prevListBlock != nil {
			foundBlock = m.checkBlock(prevListBlock, prevListIndex, allocSize, allocAlignment, allocType, &allocRequest)
			if foundBlock {
				return foundBlock, allocRequest, nil
			}

			prevListBlock = prevListBlock.nextFree
		}
	}

	if !doFullSearch {
		return false, allocRequest, nil
	}

	// Worst case, full search has to be done
	for nextListIndex++; nextListIndex < len(m.freeList); nextListIndex++ {
		nextListBlock = m.freeList[nextListIndex]
		for nextListBlock != nil {
			foundBlock := m.checkBlock(nextListBlock, nextListIndex, allocSize, allocAlignment, allocType, &allocRequest)
			if foundBlock {
				return foundBlock, allocRequest, nil
			}

			nextListBlock = nextListBlock.nextFree
		}
	}

	// No more memory to check
	return false, allocRequest, nil
}

func (m *TLSFBlockMetadata) minOffsetCheckBlocks(
	allocSize int,
	allocAlignment uint,
	allocType uint32,
	allocRequest *AllocationRequest,
	maxOffset int,
) bool {

	for block := m.tailBlock; block != nil; block = block.nextPhysical {
		if block.offset >= maxOffset {
			break
		}

		if block.IsFree() && block.size >= allocSize && block != m.nullBlock {
			if m.checkBlock(block, m.getListIndexFromSize(block.size), allocSize, allocAlignment, allocType, allocRequest) {
				return true
			}
		}
	}

	return false
}

func (m *TLSFBlockMetadata) checkBlock(
	block *tlsfBlock,
	listIndex int,
	allocSize int,
	allocAlignment uint,
	allocType uint32,
	allocRequest *AllocationRequest,
) bool {
	if !block.IsFree() {
		panic(fmt.Sprintf("block at offset %d is already taken", block.offset))
	}

	alignedOffset := memutils.AlignUp(block.offset, allocAlignment)

	if block.size < allocSize+alignedOffset-block.offset {
		return false
	}

	// Check for granularity conflicts
	var conflict bool
	alignedOffset, conflict = m.granularityHandler.CheckConflictAndAlignUp(alignedOffset, allocSize, block.offset, block.size, allocType)
	if conflict {
		return false
	}

	// Alloc will work
	allocRequest.Type = AllocationRequestTLSF
	allocRequest.BlockAllocationHandle = block.blockHandle
	allocRequest.Size = allocSize - memutils.DebugMargin
	allocRequest.AllocType = allocType
	allocRequest.AlgorithmData = uint64(alignedOffset)

	// Place block at the start of list if it's a normal block
	if listIndex != len(m.freeList) && block.prevFree != nil {
		block.prevFree.nextFree = block.nextFree
		if block.nextFree != nil {
			block.nextFree.prevFree = block.prevFree
		}

		block.prevFree = nil
		block.nextFree = m.freeList[listIndex]
		m.freeList[listIndex] = block
		if block.nextFree != nil {
			block.nextFree.prevFree = block
		}
	}

	return true
}

func (m *TLSFBlockMetadata) findFreeBlock(size int) (*tlsfBlock, int) {
	memoryClass := m.sizeToMemoryClass(size)
	innerFreeMap := m.innerIsFreeBitmap[memoryClass] & (math.MaxUint32 << m.sizeToSecondIndex(size, memoryClass))

	if innerFreeMap == 0 {
		// Check higher levels for available blocks
		freeMap := m.isFreeBitmap & (math.MaxUint32 << (memoryClass + 1))
		if freeMap == 0 {
			return nil, 0
		}

		// Find lowest free region
		memoryClass = uint8(bits.TrailingZeros64(uint64(freeMap)))
		innerFreeMap = m.innerIsFreeBitmap[memoryClass]
		if innerFreeMap == 0 {
			panic("free bitmap is in an invalid state")
		}
	}

	// Find lowest free subregion
	listIndex := m.getListIndex(memoryClass, uint16(bits.TrailingZeros64(uint64(innerFreeMap))))
	if m.freeList[listIndex] == nil {
		panic(fmt.Sprintf("free list index %d was listed as having free blocks, but no blocks were in the free list", listIndex))
	}

	return m.freeList[listIndex], listIndex
}

// BlockJsonData populates a json object with information about this block
func (m *TLSFBlockMetadata) BlockJsonData(json jwriter.ObjectState) {
	blockCount := m.allocCount + m.blocksFreeCount
	blockList := make([]*tlsfBlock, blockCount)

	i := blockCount
	for block := m.nullBlock.prevPhysical; block != nil; block = block.prevPhysical {
		i--
		blockList[i] = block
	}

	if i != 0 {
		panic("the block metadata's block count does not match the number of physical blocks")
	}

	var stats memutils.DetailedStatistics
	stats.Clear()
	m.AddDetailedStatistics(&stats)

	m.WriteBlockJson(json, stats.BlockBytes-stats.AllocationBytes, stats.AllocationCount, stats.UnusedRangeCount)
}

// CheckCorruption accepts a pointer to the underlying memory that this block manages. It will return
// nil if anti-corruption memory markers are present for every suballocation in the block. This method
// is fairly expensive and so should only be run as part of some sort of diagnostic regime.
//
// Bear in mind that anti-corruption memory markers are only written when memutils is built with
// the build flag `debug_mem_utils`. This method will not return an error when that flag is not present,
// but it is expensive regardless of build flags and so should only be run when mem_utils.DebugMargin
// is not 0.
//
// Additionally, it is the responsibility of consumers to write the debug markers themselves after
// allocation, by calling memutils.WriteMagicValue with the same pointer sent to CheckCorruption.
// If the consumer has failed to write the anti-corruption markers, then this method will return an
// error.
func (m *TLSFBlockMetadata) CheckCorruption(blockData unsafe.Pointer) error {
	for block := m.nullBlock.prevPhysical; block != nil; block = block.prevPhysical {
		if !block.IsFree() {
			if !memutils.ValidateMagicValue(blockData, block.offset+block.size) {
				return errors.New("memory corruption detected after validated allocation")
			}
		}
	}

	return nil
}

// Alloc commits an AllocationRequest object, creating the suballocation within the block based
// on the data described in the AllocationRequest. The implementation must return an error if the
// allocation is no longer valid- i.e. the requested free region no longer exists, is not free,
// offset has changed, is no longer large enough to support the request, etc.
func (m *TLSFBlockMetadata) Alloc(req AllocationRequest, suballocType uint32, userData any) error {
	if req.Type != AllocationRequestTLSF {
		return errors.New("allocation request was received by an incompatible metadata")
	}

	// Get block and pop it from the free list
	currentBlock, err := m.getBlock(req.BlockAllocationHandle)
	offset := int(req.AlgorithmData)

	if err != nil {
		return err
	}
	if currentBlock.offset > offset {
		return errors.New("allocation request had a block allocation header that was incompatible with the requested offset")
	}

	if currentBlock != m.nullBlock {
		m.removeFreeBlock(currentBlock)
	}

	missingAlignment := offset - currentBlock.offset

	// Appending missing alignment to prev block or create a new one
	if missingAlignment != 0 {
		prevBlock := currentBlock.prevPhysical

		if prevBlock == nil {
			return errors.New("somehow had missing alignment at offset 0")
		}

		if prevBlock.IsFree() && prevBlock.size != memutils.DebugMargin {
			oldListIndex := m.getListIndexFromSize(prevBlock.size)
			prevBlock.size += missingAlignment

			// If the new block size moves the block around
			if oldListIndex != m.getListIndexFromSize(prevBlock.size) {
				prevBlock.size -= missingAlignment
				m.removeFreeBlock(prevBlock)

				prevBlock.size += missingAlignment
				m.insertFreeBlock(prevBlock)
			} else {
				m.blocksFreeSize += missingAlignment
			}
		} else {
			newBlock := m.allocateBlock()
			currentBlock.prevPhysical = newBlock
			prevBlock.nextPhysical = newBlock
			newBlock.prevPhysical = prevBlock
			newBlock.nextPhysical = currentBlock
			newBlock.size = missingAlignment
			newBlock.offset = currentBlock.offset
			newBlock.MarkTaken()

			m.insertFreeBlock(newBlock)
		}

		currentBlock.size -= missingAlignment
		currentBlock.offset += missingAlignment
	}

	size := req.Size + memutils.DebugMargin
	if currentBlock.size == size {
		if currentBlock == m.nullBlock {
			// Setup a new null block
			m.nullBlock = m.allocateBlock()
			m.nullBlock.size = 0
			m.nullBlock.offset = currentBlock.offset + size
			m.nullBlock.prevPhysical = currentBlock
			m.nullBlock.nextPhysical = nil
			m.nullBlock.MarkFree()
			m.nullBlock.prevFree = nil
			m.nullBlock.nextFree = nil
			currentBlock.nextPhysical = m.nullBlock
			currentBlock.MarkTaken()
		}
	} else if currentBlock.size < size {
		return errors.New("allocation request had a block allocation header too small for the request")
	} else {
		// Create a new free block
		newBlock := m.allocateBlock()
		newBlock.size = currentBlock.size - size
		newBlock.offset = currentBlock.offset + size
		newBlock.prevPhysical = currentBlock
		newBlock.nextPhysical = currentBlock.nextPhysical
		currentBlock.nextPhysical = newBlock
		currentBlock.size = size

		if currentBlock == m.nullBlock {
			m.nullBlock = newBlock
			m.nullBlock.MarkFree()
			m.nullBlock.nextFree = nil
			m.nullBlock.prevFree = nil
			currentBlock.MarkTaken()
		} else {
			newBlock.nextPhysical.prevPhysical = newBlock
			newBlock.MarkTaken()
			m.insertFreeBlock(newBlock)
		}
	}

	currentBlock.userData = userData

	if memutils.DebugMargin > 0 {
		currentBlock.size -= memutils.DebugMargin
		newBlock := m.allocateBlock()
		newBlock.size = memutils.DebugMargin
		newBlock.offset = currentBlock.offset + currentBlock.size
		newBlock.prevPhysical = currentBlock
		newBlock.nextPhysical = currentBlock.nextPhysical
		newBlock.MarkTaken()
		currentBlock.nextPhysical.prevPhysical = newBlock
		currentBlock.nextPhysical = newBlock
		m.insertFreeBlock(newBlock)
	}

	m.granularityHandler.AllocRegions(req.AllocType, currentBlock.offset, currentBlock.size)
	m.allocCount++

	return nil
}

// Free frees a suballocation within the block, causing it to become a free region once again.
//
// The implementation must return an error if the provided handle does not map to a live allocation
// within this block.
func (m *TLSFBlockMetadata) Free(allocHandle BlockAllocationHandle) error {
	block, err := m.getBlock(allocHandle)
	if err != nil {
		return err
	}
	if block.IsFree() {
		return errors.New("block is already free")
	}

	next := block.nextPhysical
	m.granularityHandler.FreeRegions(block.offset, block.size)
	m.allocCount--

	if memutils.DebugMargin > 0 {
		m.removeFreeBlock(next)

		m.mergeBlock(next, block)

		block = next
		next = next.nextPhysical
	}

	// Try merging
	prev := block.prevPhysical
	if prev != nil && prev.IsFree() && prev.size != memutils.DebugMargin {
		m.removeFreeBlock(prev)
		m.mergeBlock(block, prev)
	}

	if !next.IsFree() {
		m.insertFreeBlock(block)
	} else if next == m.nullBlock {
		m.mergeBlock(m.nullBlock, block)
	} else {
		m.removeFreeBlock(next)
		m.mergeBlock(next, block)

		m.insertFreeBlock(next)
	}

	return nil
}

func (m *TLSFBlockMetadata) removeFreeBlock(block *tlsfBlock) {
	if block == m.nullBlock {
		panic("cannot remove the null block")
	}
	if !block.IsFree() {
		panic("provided block is not free")
	}

	// Remove from free list chain
	if block.nextFree != nil {
		block.nextFree.prevFree = block.prevFree
	}
	if block.prevFree != nil {
		block.prevFree.nextFree = block.nextFree
	} else {
		memClass := m.sizeToMemoryClass(block.size)
		secondIndex := m.sizeToSecondIndex(block.size, memClass)
		index := m.getListIndex(memClass, secondIndex)

		if m.freeList[index] != block {
			panic("block was not in the free list at the expected location")
		}
		m.freeList[index] = block.nextFree
		if block.nextFree == nil {
			m.innerIsFreeBitmap[memClass] &= ^(1 << secondIndex)
			if m.innerIsFreeBitmap[memClass] == 0 {
				m.isFreeBitmap &= ^(1 << memClass)
			}
		}
	}

	// Set up block for use
	block.MarkTaken()
	block.userData = nil
	m.blocksFreeCount--
	m.blocksFreeSize -= block.size
}

func (m *TLSFBlockMetadata) insertFreeBlock(block *tlsfBlock) {
	if block == m.nullBlock {
		panic("cannot insert the null block")
	}

	if block.IsFree() {
		panic("block is already free")
	}

	memClass := m.sizeToMemoryClass(block.size)
	secondIndex := m.sizeToSecondIndex(block.size, memClass)
	index := m.getListIndex(memClass, secondIndex)

	if index >= len(m.freeList) {
		panic("invalid free list index found for block")
	}

	block.prevFree = nil
	block.nextFree = m.freeList[index]
	m.freeList[index] = block
	if block.nextFree != nil {
		block.nextFree.prevFree = block
	} else {
		m.innerIsFreeBitmap[memClass] |= 1 << secondIndex
		m.isFreeBitmap |= 1 << memClass
	}
	m.blocksFreeCount++
	m.blocksFreeSize += block.size
}

func (m *TLSFBlockMetadata) mergeBlock(block *tlsfBlock, prev *tlsfBlock) {
	if block.prevPhysical != prev {
		panic("cannot merge separate physical regions")
	}
	if prev.IsFree() {
		panic("cannot merge a block that belongs to the free list")
	}

	block.offset = prev.offset
	block.size += prev.size
	block.prevPhysical = prev.prevPhysical
	if block.prevPhysical != nil {
		block.prevPhysical.nextPhysical = block
	} else {
		m.tailBlock = block
	}

	m.freeBlock(prev)
}

// VisitAllRegions will call the provided callback once for each allocation and free region in
// the block.  Depending on implementation, this can be extremely slow and should generally not
// be done except for diagnostic purposes.
func (m *TLSFBlockMetadata) VisitAllRegions(handleBlock func(handle BlockAllocationHandle, offset int, size int, userData any, free bool) error) error {
	for block := m.nullBlock; block != nil; block = block.prevPhysical {
		err := handleBlock(block.blockHandle, block.offset, block.size, block.userData, block.IsFree())
		if err != nil {
			return err
		}
	}

	return nil
}

// AllocationListBegin will retrieve the handle very first allocation in the block, if any. If none exist, the
// BlockAllocationHandle value NoAllocation will be returned.
func (m *TLSFBlockMetadata) AllocationListBegin() (BlockAllocationHandle, error) {
	if m.allocCount == 0 {
		return NoAllocation, nil
	}

	for block := m.nullBlock.prevPhysical; block != nil; block = block.prevPhysical {
		if !block.IsFree() {
			return block.blockHandle, nil
		}
	}

	panic("the metadata has an allocation but none could be found in the physical blocks")
}

// FindNextAllocation accepts a BlockAllocationHandle that maps to a live allocation within the block
// and returns the handle for the next live allocation within the block, if any. If none exist, the
// BlockAllocationHandle value NoAllocation will be returned.
//
// The implementation must return an error if the provided allocHandle does not map to a live allocation within this block.
func (m *TLSFBlockMetadata) FindNextAllocation(alloc BlockAllocationHandle) (BlockAllocationHandle, error) {
	startBlock, err := m.getBlock(alloc)
	if err != nil {
		return NoAllocation, err
	}
	if startBlock.IsFree() {
		return NoAllocation, errors.New("provided block cannot be free")
	}

	for block := startBlock.prevPhysical; block != nil; block = block.prevPhysical {
		if !block.IsFree() {
			return block.blockHandle, nil
		}
	}

	return NoAllocation, nil
}

// Clear instantly frees all allocations and and resets the state of the metadata
func (m *TLSFBlockMetadata) Clear() {
	m.allocCount = 0
	m.blocksFreeCount = 0
	m.blocksFreeSize = 0
	m.isFreeBitmap = 0
	m.nullBlock.offset = 0
	m.nullBlock.size = m.size
	block := m.nullBlock.prevPhysical
	m.nullBlock.prevPhysical = nil
	m.tailBlock = m.nullBlock

	for block != nil {
		prev := block.prevPhysical
		m.freeBlock(block)
		block = prev
	}

	m.freeList = make([]*tlsfBlock, len(m.freeList))
	m.innerIsFreeBitmap = [maxMemoryClasses]uint32{}
	m.granularityHandler.Clear()
}

// AllocationOffset accepts a BlockAllocationHandle that maps to a live region of memory
// (allocated or free) within the block and returns the offset in bytes within the block for that
// region of memory.
//
// An error will be returned if the provided handle does not map to a live region of
// memory within this block.
func (m *TLSFBlockMetadata) AllocationOffset(allocHandle BlockAllocationHandle) (int, error) {
	block, err := m.getBlock(allocHandle)
	if err != nil {
		return 0, err
	}

	return block.offset, nil
}

// AllocationUserData accepts a BlockAllocationHandle that maps to a live allocation within the block
// and returns the userdata value provided by the consumer for that allocation.
//
// An error will be returned if the provided handle does not map to a live allocation
// within this block.
func (m *TLSFBlockMetadata) AllocationUserData(allocHandle BlockAllocationHandle) (any, error) {
	block, err := m.getBlock(allocHandle)
	if err != nil {
		return nil, err
	}

	if block.IsFree() {
		return nil, errors.New("user data cannot be retrieved for a free block")
	}

	return block.userData, nil
}

// SetAllocationUserData accepts a BlockAllocationHandle that maps to a live allocation within the
// block and a userData value. The allocation's userData is changed to the provided userData.
//
// An error will be returned if the provided handle does not map to a live allocation
// within this block.
func (m *TLSFBlockMetadata) SetAllocationUserData(allocHandle BlockAllocationHandle, userData any) error {
	block, err := m.getBlock(allocHandle)
	if err != nil {
		return err
	}

	if block.IsFree() {
		return errors.New("user data cannot be set for a free block")
	}

	block.userData = userData
	return nil
}
