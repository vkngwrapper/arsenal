package metadata

import (
	"fmt"
	"github.com/cockroachdb/errors"
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"go.uber.org/zap"
	"math"
	"math/bits"
	"sync"
	"sync/atomic"
	"unsafe"
)

const (
	SecondLevelIndex int = 5
	SmallBufferSize  int = 256
	MemoryClassShift     = 7
	MaxMemoryClasses     = 65 - MemoryClassShift
)

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

type TLSFBlockMetadata struct {
	BlockMetadataBase

	allocCount        int
	blocksFreeCount   int
	blocksFreeSize    int
	isFreeBitmap      uint32
	memoryClasses     int
	innerIsFreeBitmap [MaxMemoryClasses]uint32
	listsCount        int

	nextAllocationHandle BlockAllocationHandle
	handleKey            map[BlockAllocationHandle]*tlsfBlock
	freeList             []*tlsfBlock
	nullBlock            *tlsfBlock
	granularityHandler   BlockBufferImageGranularity
	blockAllocator       sync.Pool
}

var _ BlockMetadata = &TLSFBlockMetadata{}

func NewTLSFBlockMetadata(bufferImageGranularity int, isVirtual bool) *TLSFBlockMetadata {
	return &TLSFBlockMetadata{
		BlockMetadataBase: NewBlockMetadata(bufferImageGranularity, isVirtual),

		blockAllocator: sync.Pool{
			New: func() any {
				return &tlsfBlock{}
			},
		},
	}
}

func (m *TLSFBlockMetadata) Destroy() {
	m.granularityHandler.Destroy()
}

func (m *TLSFBlockMetadata) allocateBlock() *tlsfBlock {
	b := m.blockAllocator.Get().(*tlsfBlock)
	b.offset = 0
	b.size = 0
	b.prevPhysical = nil
	b.nextPhysical = nil
	b.nextFree = nil
	b.prevFree = nil
	b.userData = nil
	b.blockHandle = BlockAllocationHandle(atomic.AddUint64((*uint64)(&m.nextAllocationHandle), 1))
	m.handleKey[b.blockHandle] = b
	return b
}

func (m *TLSFBlockMetadata) freeBlock(b *tlsfBlock) {
	delete(m.handleKey, b.blockHandle)
	m.blockAllocator.Put(b)
}

func (m *TLSFBlockMetadata) getBlock(handle BlockAllocationHandle) (*tlsfBlock, error) {
	block, ok := m.handleKey[handle]
	if !ok {
		return nil, errors.New("received a handle that was incompatible with this metadata")
	}
	return block, nil
}

func (m *TLSFBlockMetadata) Init(size int) {
	m.BlockMetadataBase.Init(size)
	m.handleKey = make(map[BlockAllocationHandle]*tlsfBlock)

	if !m.isVirtual {
		m.granularityHandler.Init(size)
	}

	m.nullBlock = m.allocateBlock()
	m.nullBlock.size = size
	m.nullBlock.MarkFree()
	memoryClass := m.sizeToMemoryClass(size)
	sli := m.sizeToSecondIndex(size, memoryClass)

	listSize := 1
	if memoryClass != 0 {
		listSize = (memoryClass-1)*(1<<SecondLevelIndex) + sli + 1
	}

	if m.isVirtual {
		listSize += 1 << SecondLevelIndex
	} else {
		listSize += 4
	}

	m.memoryClasses = memoryClass + 2
	m.freeList = make([]*tlsfBlock, listSize)
}

func (m *TLSFBlockMetadata) Validate() error {
	if m.SumFreeSize() > m.Size() {
		return errors.New("invalid metadata free size")
	}

	calculatedSize := m.nullBlock.size
	calculatedFreeSize := m.nullBlock.size
	var allocCount, freeCount int

	// Check integrity of free lists
	for listIndex := 0; listIndex < len(m.freeList); listIndex++ {
		block := m.freeList[listIndex]
		if block == nil {
			continue
		}

		if !block.IsFree() {
			return errors.Newf("block at offset %d is in the free list but is not free", block.offset)
		}

		if block.prevFree != nil {
			return errors.Newf("block at offset %d is the head of a free list but has a previous block", block.offset)
		}
		for block.nextFree != nil {
			if !block.nextFree.IsFree() {
				return errors.Newf("block at offset %d is in the free list but it is not free", block.nextFree.offset)
			}
			if block.nextFree.prevFree != block {
				return errors.Newf("block at offset %d lists the block at offset %d as its next block, but the reverse reference is broken", block.offset, block.nextFree.offset)
			}

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
	validateCtx := m.granularityHandler.startValidation(m.isVirtual)

	for prev := m.nullBlock.prevPhysical; prev != nil; prev = prev.prevPhysical {
		if prev.offset+prev.size != nextOffset {
			return errors.Newf("physical block at offset %d does not end at the next block's start offset", prev.offset)
		}

		nextOffset = prev.offset
		calculatedSize += prev.size
		listIndex := m.getListIndexFromSize(prev.size)
		freeBlock := m.freeList[listIndex]
		if prev.IsFree() {
			freeCount++

			// Does the free block belong to the free list?
			var found bool
			for !found && freeBlock != nil {
				if freeBlock == prev {
					found = true
				}

				freeBlock = freeBlock.nextFree
			}

			if !found {
				return errors.Newf("free block with offset %d should have free list index %d but it was not present", prev.offset, listIndex)
			}

			calculatedFreeSize += prev.size
		} else {
			allocCount++

			// Ensure the block is not on the free list
			for freeBlock != nil {
				if freeBlock == prev {
					return errors.Newf("taken block with offset %d appeared in the free list at index %d", prev.offset, listIndex)
				}
				freeBlock = freeBlock.nextFree
			}

			if !m.isVirtual {
				err := m.granularityHandler.validate(validateCtx, prev.offset, prev.size)
				if err != nil {
					return err
				}
			}
		}

		if prev.prevPhysical != nil && prev.prevPhysical.nextPhysical != prev {
			return errors.Newf("block at offset %d has a previous physical block, but the reverse reference is broken", prev.offset)
		}
	}

	if !m.isVirtual {
		err := m.granularityHandler.finishValidation(validateCtx)
		if err != nil {
			return err
		}
	}

	if nextOffset != 0 {
		return errors.Newf("the first physical block should have an offset of 0, but instead it has an offset of %d", nextOffset)
	}

	if calculatedSize != m.size {
		return errors.Newf("the full size of the metadata is %d, but the blocks only added up to %d", m.size, calculatedSize)
	}

	if calculatedFreeSize != m.SumFreeSize() {
		return errors.Newf("the free size of the metadata is %d, but the free blocks only added up to %d", m.SumFreeSize(), calculatedFreeSize)
	}

	if allocCount != m.allocCount {
		return errors.Newf("the allocation count of the metadata is %d, but the taken blocks only added up to %d", m.allocCount, allocCount)
	}

	if freeCount != m.blocksFreeCount {
		return errors.Newf("the free block count of the metadata is %d, but there were only %d free blocks", m.blocksFreeCount, freeCount)
	}

	return nil
}

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

func (m *TLSFBlockMetadata) getListIndex(memoryClass int, secondIndex int) int {
	if memoryClass == 0 {
		return secondIndex
	}

	i := (memoryClass-1)*(1<<SecondLevelIndex) + secondIndex
	if m.isVirtual {
		return i + (1 << SecondLevelIndex)
	}

	return i + 4
}

func (m *TLSFBlockMetadata) AllocationCount() int {
	return m.allocCount
}

func (m *TLSFBlockMetadata) FreeRegionsCount() int {
	return m.blocksFreeSize + m.nullBlock.size
}

func (m *TLSFBlockMetadata) SumFreeSize() int {
	return m.blocksFreeSize + m.nullBlock.size
}

func (m *TLSFBlockMetadata) IsEmpty() bool {
	return m.nullBlock.offset == 0
}

func (m *TLSFBlockMetadata) sizeToMemoryClass(size int) int {
	if size > SmallBufferSize {
		return bits.LeadingZeros(uint(size)) - MemoryClassShift
	}

	return 0
}

func (m *TLSFBlockMetadata) sizeToSecondIndex(size int, memoryClass int) int {
	if memoryClass != 0 {
		return (size >> (memoryClass + MemoryClassShift - SecondLevelIndex)) ^ (1 << SecondLevelIndex)
	}

	if m.isVirtual {
		return (size - 1) / 8
	}

	return (size - 1) / 64
}

func (m *TLSFBlockMetadata) PopulateAllocationRequest(
	allocSize int, allocAlignment uint,
	upperAddress bool,
	allocType SuballocationType,
	strategy memutils.AllocationCreateFlags,
	allocRequest *AllocationRequest,
) (bool, error) {
	if allocSize < 1 {
		return false, errors.Newf("Invalid allocSize: %d", allocSize)
	}

	if upperAddress {
		return false, errors.New("AllocationCreateUpperAddress can only be used with the Linear algorithm")
	}

	// Round up granularity
	if !m.isVirtual {
		allocSize, allocAlignment = m.granularityHandler.RoundUpAllocRequest(allocType, allocSize, allocAlignment)
	}

	allocSize += m.DebugMargin()

	// Is pool big enough?
	if allocSize > m.SumFreeSize() {
		return false, nil
	}

	// Any free blocks in the pool?
	if m.blocksFreeCount == 0 {
		return m.checkBlock(m.nullBlock, m.listsCount, allocSize, allocAlignment, allocType, allocRequest), nil
	}

	// Round up to the next block
	sizeForNextList := allocSize
	smallSizeStepDivisor := 4
	if m.isVirtual {
		smallSizeStepDivisor = 1 << SecondLevelIndex
	}
	smallSizeStep := SmallBufferSize / smallSizeStepDivisor
	if allocSize > SmallBufferSize {
		sizeForNextList += 1<<bits.LeadingZeros(uint(allocSize)) - SecondLevelIndex
	} else if allocSize > SmallBufferSize-smallSizeStep {
		sizeForNextList = SmallBufferSize + 1
	} else {
		sizeForNextList += smallSizeStep
	}

	nextListIndex := 0
	prevListIndex := 0
	var nextListBlock, prevListBlock *tlsfBlock

	// Check blocks according to the requested strategy
	if strategy&memutils.AllocationCreateStrategyMinTime != 0 {
		// Check for larger block first
		nextListBlock, nextListIndex = m.findFreeBlock(sizeForNextList, nextListIndex)

		if nextListBlock != nil {
			foundBlock := m.checkBlock(nextListBlock, nextListIndex, allocSize, allocAlignment, allocType, allocRequest)
			if foundBlock {
				return foundBlock, nil
			}
		}

		// If not fitted then null block
		foundBlock := m.checkBlock(m.nullBlock, m.listsCount, allocSize, allocAlignment, allocType, allocRequest)
		if foundBlock {
			return foundBlock, nil
		}

		// Null block failed, search larger bucket
		for nextListBlock != nil {
			foundBlock = m.checkBlock(nextListBlock, nextListIndex, allocSize, allocAlignment, allocType, allocRequest)
			if foundBlock {
				return foundBlock, nil
			}

			nextListBlock = nextListBlock.nextFree
		}

		// Failed again, check best fit bucket
		prevListBlock, prevListIndex = m.findFreeBlock(allocSize, prevListIndex)

		for prevListBlock != nil {
			foundBlock = m.checkBlock(prevListBlock, prevListIndex, allocSize, allocAlignment, allocType, allocRequest)
			if foundBlock {
				return foundBlock, nil
			}

			prevListBlock = prevListBlock.nextFree
		}
	} else if strategy&memutils.AllocationCreateStrategyMinMemory != 0 {
		// Check best fit bucket
		prevListBlock, prevListIndex = m.findFreeBlock(allocSize, prevListIndex)

		for prevListBlock != nil {
			foundBlock := m.checkBlock(prevListBlock, prevListIndex, allocSize, allocAlignment, allocType, allocRequest)
			if foundBlock {
				return foundBlock, nil
			}

			prevListBlock = prevListBlock.nextFree
		}

		// If failed check null block
		foundBlock := m.checkBlock(m.nullBlock, m.listsCount, allocSize, allocAlignment, allocType, allocRequest)
		if foundBlock {
			return foundBlock, nil
		}

		// Check larger bucket
		nextListBlock, nextListIndex = m.findFreeBlock(sizeForNextList, nextListIndex)

		for nextListBlock != nil {
			foundBlock = m.checkBlock(nextListBlock, nextListIndex, allocSize, allocAlignment, allocType, allocRequest)
			if foundBlock {
				return foundBlock, nil
			}

			nextListBlock = nextListBlock.nextFree
		}
	} else if strategy&memutils.AllocationCreateStrategyMinOffset != 0 {
		// Enumerate back to the first suitable block and then search forward- this is
		// different from VMA because it's more important to avoid unnecessary allocations in go
		// In VMA, it just makes a vector of block pointers and populates it by enumerating backward
		// through the list and then searches forward through the vector. In Go, in order to avoid
		// allocating a slice, we do it recursively.
		foundBlock := m.minOffsetCheckBlocksBefore(m.nullBlock, allocSize, allocAlignment, allocType, allocRequest)
		if foundBlock {
			return foundBlock, nil
		}

		// If failed, check null block
		foundBlock = m.checkBlock(m.nullBlock, m.listsCount, allocSize, allocAlignment, allocType, allocRequest)
		if foundBlock {
			return foundBlock, nil
		}

		// Whole range searched, no more memory
		return false, nil
	} else {
		// Check larger bucket
		nextListBlock, nextListIndex = m.findFreeBlock(sizeForNextList, nextListIndex)

		for nextListBlock != nil {
			foundBlock := m.checkBlock(nextListBlock, nextListIndex, allocSize, allocAlignment, allocType, allocRequest)
			if foundBlock {
				return foundBlock, nil
			}

			nextListBlock = nextListBlock.nextFree
		}

		// If failed, check null block
		foundBlock := m.checkBlock(m.nullBlock, m.listsCount, allocSize, allocAlignment, allocType, allocRequest)
		if foundBlock {
			return foundBlock, nil
		}

		// Check best fit bucket
		prevListBlock, prevListIndex = m.findFreeBlock(allocSize, prevListIndex)

		for prevListBlock != nil {
			foundBlock = m.checkBlock(prevListBlock, prevListIndex, allocSize, allocAlignment, allocType, allocRequest)
			if foundBlock {
				return foundBlock, nil
			}

			prevListBlock = prevListBlock.nextFree
		}
	}

	// Worst case, full search has to be done
	for nextListIndex++; nextListIndex < m.listsCount; nextListIndex++ {
		nextListBlock = m.freeList[nextListIndex]
		for nextListBlock != nil {
			foundBlock := m.checkBlock(nextListBlock, nextListIndex, allocSize, allocAlignment, allocType, allocRequest)
			if foundBlock {
				return foundBlock, nil
			}

			nextListBlock = nextListBlock.nextFree
		}
	}

	// No more memory to check
	return false, nil
}

func (m *TLSFBlockMetadata) minOffsetCheckBlocksBefore(
	startBlock *tlsfBlock,
	allocSize int,
	allocAlignment uint,
	allocType SuballocationType,
	allocRequest *AllocationRequest,
) bool {

	for block := startBlock.prevPhysical; block != nil; block = block.prevPhysical {
		if block.IsFree() && block.size >= allocSize {
			foundBlock := m.minOffsetCheckBlocksBefore(block, allocSize, allocAlignment, allocType, allocRequest)
			if foundBlock {
				return true
			}

			break
		}
	}

	if startBlock == m.nullBlock {
		return false
	}

	return m.checkBlock(startBlock, m.getListIndexFromSize(startBlock.size), allocSize, allocAlignment, allocType, allocRequest)
}

func (m *TLSFBlockMetadata) checkBlock(
	block *tlsfBlock,
	listIndex int,
	allocSize int,
	allocAlignment uint,
	allocType SuballocationType,
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
	if !m.isVirtual {
		var conflict bool
		alignedOffset, conflict = m.granularityHandler.CheckConflictAndAlignUp(alignedOffset, allocSize, block.offset, block.size, allocType)
		if conflict {
			return conflict
		}
	}

	// Alloc will work
	allocRequest.Type = AllocationRequestTLSF
	allocRequest.BlockAllocationHandle = block.blockHandle
	allocRequest.Size = allocSize - m.DebugMargin()
	allocRequest.CustomData = allocType
	allocRequest.AlgorithmData = uint64(alignedOffset)

	// Place block at the start of list if it's a normal block
	if listIndex != m.listsCount && block.prevFree != nil {
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

func (m *TLSFBlockMetadata) findFreeBlock(size int, listIndex int) (*tlsfBlock, int) {
	memoryClass := m.sizeToMemoryClass(size)
	innerFreeMap := m.innerIsFreeBitmap[memoryClass] & (math.MaxUint32 << m.sizeToSecondIndex(size, memoryClass))

	if innerFreeMap == 0 {
		// Check higher levels for available blocks
		freeMap := m.isFreeBitmap & (math.MaxUint32 << (memoryClass + 1))
		if freeMap == 0 {
			return nil, listIndex
		}

		// Find lowest free region
		memoryClass = bits.TrailingZeros(uint(freeMap))
		innerFreeMap = m.innerIsFreeBitmap[memoryClass]
		if innerFreeMap == 0 {
			panic("free bitmap is in an invalid state")
		}
	}

	// Find lowest free subregion
	listIndex = m.getListIndex(memoryClass, bits.TrailingZeros(uint(innerFreeMap)))
	if m.freeList[listIndex] == nil {
		panic(fmt.Sprintf("free list index %d was listed as having free blocks, but no blocks were in the free list", listIndex))
	}

	return m.freeList[listIndex], listIndex
}

func (m *TLSFBlockMetadata) PrintDetailedMapHeader(json jwriter.ObjectState) error {
	blockCount := m.allocCount + m.blocksFreeCount
	blockList := make([]*tlsfBlock, blockCount)

	i := blockCount
	for block := m.nullBlock.prevPhysical; block != nil; block = block.prevPhysical {
		i--
		blockList[i] = block
	}

	if i != 0 {
		return errors.New("the block metadata's block count does not match the number of physical blocks")
	}

	var stats memutils.DetailedStatistics
	stats.Clear()
	m.AddDetailedStatistics(&stats)

	m.PrintDetailedMap_Header(json, stats.BlockBytes-stats.AllocationBytes, stats.AllocationCount, stats.UnusedRangeCount)

	return nil
}

func (m *TLSFBlockMetadata) CheckCorruption(blockData unsafe.Pointer) (common.VkResult, error) {
	for block := m.nullBlock.prevPhysical; block != nil; block = block.prevPhysical {
		if !block.IsFree() {
			if !memutils.ValidateMagicValue(blockData, block.offset+block.size) {
				return core1_0.VKErrorUnknown, errors.New("memory corruption detected after validated allocation")
			}
		}
	}

	return core1_0.VKSuccess, nil
}

func (m *TLSFBlockMetadata) Alloc(req *AllocationRequest, suballocType SuballocationType, userData any) error {
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

	debugMargin := m.DebugMargin()
	missingAlignment := offset - currentBlock.offset

	// Appending missing alignment to prev block or create a new one
	if missingAlignment != 0 {
		prevBlock := currentBlock.prevPhysical

		if prevBlock == nil {
			return errors.New("somehow had missing alignment at offset 0")
		}

		if prevBlock.IsFree() && prevBlock.size != debugMargin {
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

	size := req.Size + debugMargin
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

	if debugMargin > 0 {
		currentBlock.size -= debugMargin
		newBlock := m.allocateBlock()
		newBlock.size = debugMargin
		newBlock.offset = currentBlock.offset + currentBlock.size
		newBlock.prevPhysical = currentBlock
		newBlock.nextPhysical = currentBlock.nextPhysical
		newBlock.MarkTaken()
		currentBlock.nextPhysical.prevPhysical = newBlock
		currentBlock.nextPhysical = newBlock
		m.insertFreeBlock(newBlock)
	}

	if !m.isVirtual {
		allocType, isAllocType := req.CustomData.(SuballocationType)
		if !isAllocType {
			return errors.New("allocation request had invalid customdata for this metadata")
		}
		m.granularityHandler.AllocPages(allocType, currentBlock.offset, currentBlock.size)
		m.allocCount++
	}

	return nil
}

func (m *TLSFBlockMetadata) Free(allocHandle BlockAllocationHandle) error {
	block, err := m.getBlock(allocHandle)
	if err != nil {
		return err
	}
	if block.IsFree() {
		return errors.New("block is already free")
	}

	next := block.nextPhysical
	if !m.isVirtual {
		m.granularityHandler.FreePages(block.offset, block.size)
	}
	m.allocCount--

	debugMargin := m.DebugMargin()
	if debugMargin > 0 {
		m.removeFreeBlock(next)

		m.mergeBlock(next, block)

		block = next
		next = next.nextPhysical
	}

	// Try merging
	prev := block.prevPhysical
	if prev != nil && prev.IsFree() && prev.size != debugMargin {
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

	if index >= m.listsCount {
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
	}

	m.freeBlock(prev)
}

func (m *TLSFBlockMetadata) VisitAllBlocks(handleBlock func(handle BlockAllocationHandle, offset int, size int, userData any, free bool)) {
	for block := m.nullBlock; block != nil; block = block.prevPhysical {
		handleBlock(block.blockHandle, block.offset, block.size, block.userData, block.IsFree())
	}
}

func (m *TLSFBlockMetadata) AllocationListBegin() (BlockAllocationHandle, error) {
	if m.allocCount == 0 {
		return NoAllocation, nil
	}

	for block := m.nullBlock.prevPhysical; block != nil; block = block.prevPhysical {
		if !block.IsFree() {
			return block.blockHandle, nil
		}
	}

	return NoAllocation, errors.New("the metadata has an allocation but none could be found in the physical blocks")
}

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

func (m *TLSFBlockMetadata) FindNextFreeRegionSize(alloc BlockAllocationHandle) (int, error) {
	block, err := m.getBlock(alloc)
	if err != nil {
		return 0, err
	}
	if block.IsFree() {
		return 0, errors.New("provided block cannot be free")
	}

	if block.prevPhysical != nil && block.prevPhysical.IsFree() {
		return block.prevPhysical.size, nil
	}

	return 0, nil
}

func (m *TLSFBlockMetadata) Clear() {
	m.allocCount = 0
	m.blocksFreeCount = 0
	m.blocksFreeSize = 0
	m.isFreeBitmap = 0
	m.nullBlock.offset = 0
	m.nullBlock.size = m.size
	block := m.nullBlock.prevPhysical
	m.nullBlock.prevPhysical = nil

	for block != nil {
		prev := block.prevPhysical
		m.freeBlock(block)
		block = prev
	}

	m.freeList = make([]*tlsfBlock, m.listsCount)
	m.innerIsFreeBitmap = [MaxMemoryClasses]uint32{}
	m.granularityHandler.Clear()
}

func (m *TLSFBlockMetadata) DebugLogAllAllocations(logger *zap.Logger, logFunc func(log *zap.Logger, offset int, size int, userData any)) {
	for block := m.nullBlock.prevPhysical; block != nil; block = block.prevPhysical {
		if !block.IsFree() {
			logFunc(logger, block.offset, block.size, block.userData)
		}
	}
}

func (m *TLSFBlockMetadata) AllocationOffset(allocHandle BlockAllocationHandle) (int, error) {
	block, err := m.getBlock(allocHandle)
	if err != nil {
		return 0, err
	}

	return block.offset, nil
}

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
