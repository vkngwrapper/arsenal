package metadata

import (
	"fmt"
	"github.com/cockroachdb/errors"
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"golang.org/x/exp/slog"
	"math"
	"sort"
	"unsafe"
)

type SecondVectorMode uint32

const (
	SecondVectorModeEmpty SecondVectorMode = iota
	SecondVectorModeRingBuffer
	SecondVectorModeDoubleStack
)

var secondVectorModeMapping = map[SecondVectorMode]string{
	SecondVectorModeEmpty:       "SecondVectorModeEmpty",
	SecondVectorModeRingBuffer:  "SecondVectorModeRingBuffer",
	SecondVectorModeDoubleStack: "SecondVectorModeDoubleStack",
}

func (m SecondVectorMode) String() string {
	return secondVectorModeMapping[m]
}

type LinearBlockMetadata struct {
	BlockMetadataBase

	sumFreeSize      int
	suballocations0  []Suballocation
	suballocations1  []Suballocation
	firstVectorIndex int
	secondVectorMode SecondVectorMode

	// Number of items in the first vector with nil allocations at the beginning
	firstNullItemsBeginCount int
	// Number of other items in the first vector with nil allocations in the middle
	firstNullItemsMiddleCount int
	// Number of items in the second vector with nil allocations
	secondNullItemsCount int
}

func (m *LinearBlockMetadata) Destroy() {}

var _ BlockMetadata = &LinearBlockMetadata{}

func NewLinearBlockMetadata(bufferImageGranularity int, isVirtual bool) *LinearBlockMetadata {
	return &LinearBlockMetadata{
		BlockMetadataBase: NewBlockMetadata(bufferImageGranularity, isVirtual),
		secondVectorMode:  SecondVectorModeEmpty,
		suballocations0:   []Suballocation{},
		suballocations1:   []Suballocation{},
	}
}

func (m *LinearBlockMetadata) SumFreeSize() int {
	return m.sumFreeSize
}

func (m *LinearBlockMetadata) IsEmpty() bool {
	return m.AllocationCount() == 0
}

func (m *LinearBlockMetadata) AllocationOffset(allocHandle BlockAllocationHandle) (int, error) {
	return int(allocHandle) - 1, nil
}

func (m *LinearBlockMetadata) Init(size int) {
	m.BlockMetadataBase.Init(size)
	m.sumFreeSize = size
}

func (m *LinearBlockMetadata) Validate() error {
	firstVector := *m.accessSuballocationsFirst()
	secondVector := *m.accessSuballocationsSecond()

	if len(secondVector) == 0 && m.secondVectorMode != SecondVectorModeEmpty {
		return errors.New("the second vector mode isn't SecondVectorModeEmpty, but the second vector is empty")
	} else if len(secondVector) != 0 && m.secondVectorMode == SecondVectorModeEmpty {
		return errors.New("the second vector mode is SecondVectorModeEmpty, but the second vector isn't empty")
	}

	if len(firstVector) != 0 {
		if firstVector[m.firstNullItemsBeginCount].Type == SuballocationFree {
			return errors.Newf("there should only be %d free items at the beginning of the primary metadata, but there seem to be more", m.firstNullItemsBeginCount)
		}

		if firstVector[len(firstVector)-1].Type == SuballocationFree {
			return errors.New("there should not be lingering free items at the end of the primary metadata")
		}
	}

	if len(secondVector) != 0 {
		if secondVector[len(secondVector)-1].Type == SuballocationFree {
			return errors.New("there should not be lingering free items at the end of the secondary metadata")
		}
	}

	if m.firstNullItemsBeginCount+m.firstNullItemsMiddleCount > len(firstVector) {
		return errors.Newf("metadata indicates that there are %d free items in the primary metadata, but there are only %d total items", m.firstNullItemsMiddleCount+m.firstNullItemsBeginCount, len(firstVector))
	}

	if m.secondNullItemsCount > len(secondVector) {
		return errors.Newf("metadata indicates that there are %d free items in the secondary metadata, but there are only %d total items", m.secondNullItemsCount, len(secondVector))
	}

	var sumUsedSize, offset int
	debugMargin := m.DebugMargin()

	if m.secondVectorMode == SecondVectorModeRingBuffer {
		if len(firstVector) == 0 && len(secondVector) != 0 {
			return errors.New("invalid ring buffer setup")
		}

		var nullItemSecondCount int
		for suballocIndex, suballoc := range secondVector {
			isFree := suballoc.Type == SuballocationFree

			if suballoc.Offset < offset {
				return errors.Newf("suballoc at index %d in the secondary ring buffer has offset %d- this collides with previous suballocations, expected offset %d", suballocIndex, suballoc.Offset, offset)
			}

			if !isFree {
				sumUsedSize += suballoc.Size
			} else {
				nullItemSecondCount++
			}

			offset = suballoc.Offset + suballoc.Size + debugMargin
		}

		if nullItemSecondCount != m.secondNullItemsCount {
			return errors.Newf("counted %d null items in the secondary ring buffer, but metadata indicates we should have %d", nullItemSecondCount, m.secondNullItemsCount)
		}
	}

	nullItemsFirstCount := m.firstNullItemsBeginCount
	for suballocIndex := m.firstNullItemsBeginCount; suballocIndex < len(firstVector); suballocIndex++ {
		suballoc := firstVector[suballocIndex]
		isFree := suballoc.Type == SuballocationFree

		if suballoc.Offset < offset {
			return errors.Newf("suballoc at index %d in the primary vector has offset %d- this collides with previous suballocations, expected offset %d", suballocIndex, suballoc.Offset, offset)
		}

		if !isFree {
			sumUsedSize += suballoc.Size
		} else {
			nullItemsFirstCount++
		}

		offset = suballoc.Offset + suballoc.Size + debugMargin
	}

	if nullItemsFirstCount != m.firstNullItemsBeginCount+m.firstNullItemsMiddleCount {
		return errors.Newf("counted %d null items in the primary vector, but metadata indicates we should have %d", nullItemsFirstCount, m.firstNullItemsMiddleCount+m.firstNullItemsBeginCount)
	}

	if m.secondVectorMode == SecondVectorModeDoubleStack {
		var nullItemSecondCount int
		for suballocIndex, suballoc := range secondVector {
			isFree := suballoc.Type == SuballocationFree

			if suballoc.Offset < offset {
				return errors.Newf("suballoc at index %d in the secondary ring buffer has offset %d- this collides with previous suballocations, expected offset %d", suballocIndex, suballoc.Offset, offset)
			}

			if !isFree {
				sumUsedSize += suballoc.Size
			} else {
				nullItemSecondCount++
			}

			offset = suballoc.Offset + suballoc.Size + debugMargin
		}

		if nullItemSecondCount != m.secondNullItemsCount {
			return errors.Newf("counted %d null items in the secondary ring buffer, but metadata indicates we should have %d", nullItemSecondCount, m.secondNullItemsCount)
		}
	}

	if offset > m.Size() {
		return errors.Newf("calculated a combined maximum memory offset of %d, but the metadata indicates a total size of %d, which is smaller", offset, m.Size())
	}

	if m.sumFreeSize != m.Size()-sumUsedSize {
		return errors.Newf("the metadata's free size %d and the calculated total block size %d don't add up to the metadata-reported size of %d", m.sumFreeSize, sumUsedSize, m.Size())
	}

	return nil
}

func (m *LinearBlockMetadata) AllocationCount() int {
	first := *m.accessSuballocationsFirst()
	second := *m.accessSuballocationsSecond()

	return len(first) - m.firstNullItemsBeginCount - m.firstNullItemsMiddleCount + len(second) - m.secondNullItemsCount
}

func (m *LinearBlockMetadata) FreeRegionsCount() int {
	// This function is used for defragmentation, which is disabled for this algorithm
	return math.MaxInt
}

func (m *LinearBlockMetadata) VisitAllBlocks(handleBlock func(handle BlockAllocationHandle, offset int, size int, userData any, free bool) error) error {
	size := m.Size()
	firstVector := *m.accessSuballocationsFirst()
	secondVector := *m.accessSuballocationsSecond()
	lastOffset := 0

	if m.secondVectorMode == SecondVectorModeRingBuffer {
		freeSpaceSecondToFirstEnd := firstVector[m.firstNullItemsBeginCount].Offset
		nextAllocSecondIndex := 0

		for lastOffset < freeSpaceSecondToFirstEnd {
			// Find the next taken allocation or move nextAllocIndex to the end
			for nextAllocSecondIndex < len(secondVector) && secondVector[nextAllocSecondIndex].UserData == nil {
				nextAllocSecondIndex++
			}

			// If we found a taken allocation
			if nextAllocSecondIndex < len(secondVector) {
				suballoc := secondVector[nextAllocSecondIndex]

				// Process all the free space before the allocation
				if lastOffset < suballoc.Offset {
					// There was free space since the last taken allocation
					err := handleBlock(BlockAllocationHandle(lastOffset), lastOffset, suballoc.Offset-lastOffset, nil, true)
					if err != nil {
						return err
					}
				}

				// Process the allocation
				err := handleBlock(BlockAllocationHandle(suballoc.Offset), suballoc.Offset, suballoc.Size, suballoc.UserData, false)
				if err != nil {
					return err
				}

				// Iterate
				lastOffset = suballoc.Offset + suballoc.Size
				nextAllocSecondIndex++
			} else {
				// Process free space after the final allocation
				if lastOffset < freeSpaceSecondToFirstEnd {
					err := handleBlock(BlockAllocationHandle(lastOffset), lastOffset, freeSpaceSecondToFirstEnd-lastOffset, nil, true)
					if err != nil {
						return err
					}
				}

				lastOffset = freeSpaceSecondToFirstEnd
			}
		}
	}

	nextAllocFirstIndex := m.firstNullItemsBeginCount

	// What offset does the first vector end on?  Usually it's wherever the memory range ends
	freeSpaceFirstToSecondEnd := size

	if m.secondVectorMode == SecondVectorModeDoubleStack {
		// However, with double stacks, it ends at the end of the second vector
		freeSpaceFirstToSecondEnd = secondVector[len(secondVector)-1].Offset
	}

	for lastOffset < freeSpaceFirstToSecondEnd {
		// Find the next taken allocation or move nextAllocIndex to the end
		for nextAllocFirstIndex < len(firstVector) && firstVector[nextAllocFirstIndex].UserData == nil {
			nextAllocFirstIndex++
		}

		// Found taken allocation
		if nextAllocFirstIndex < len(firstVector) {
			suballoc := firstVector[nextAllocFirstIndex]

			// Process free space before the allocation
			if lastOffset < suballoc.Offset {
				// There was free space since the last taken allocation
				err := handleBlock(BlockAllocationHandle(lastOffset), lastOffset, suballoc.Offset-lastOffset, nil, true)
				if err != nil {
					return err
				}
			}

			// Process this allocation
			err := handleBlock(BlockAllocationHandle(suballoc.Offset), suballoc.Offset, suballoc.Size, suballoc.UserData, false)
			if err != nil {
				return err
			}

			// Iterate
			lastOffset = suballoc.Offset + suballoc.Size
			nextAllocFirstIndex++
		} else {
			// Process free space after the final allocation
			err := handleBlock(BlockAllocationHandle(lastOffset), lastOffset, freeSpaceFirstToSecondEnd-lastOffset, nil, true)
			if err != nil {
				return err
			}

			lastOffset = freeSpaceFirstToSecondEnd
		}
	}

	if m.secondVectorMode == SecondVectorModeDoubleStack {
		nextAllocSecondIndex := len(secondVector) - 1
		for lastOffset < size {
			// Find the next taken allocation or move nextAllocIndex to the end
			for nextAllocSecondIndex >= 0 && secondVector[nextAllocSecondIndex].UserData == nil {
				nextAllocSecondIndex--
			}

			// Found taken allocaiton
			if nextAllocSecondIndex >= 0 {
				suballoc := secondVector[nextAllocSecondIndex]

				// Process free space before the allocation
				if lastOffset < suballoc.Offset {
					err := handleBlock(BlockAllocationHandle(lastOffset), lastOffset, suballoc.Offset-lastOffset, nil, true)
					if err != nil {
						return err
					}
				}

				// Process this allocation
				err := handleBlock(BlockAllocationHandle(suballoc.Offset), suballoc.Offset, suballoc.Size, suballoc.UserData, false)
				if err != nil {
					return err
				}

				// Iterate
				lastOffset = suballoc.Offset + suballoc.Size
				nextAllocSecondIndex--
			} else {
				// Process free space after the final allocation
				err := handleBlock(BlockAllocationHandle(lastOffset), lastOffset, size-lastOffset, nil, true)
				if err != nil {
					return err
				}

				lastOffset = size
			}
		}
	}

	return nil
}

func (m *LinearBlockMetadata) AddDetailedStatistics(stats *memutils.DetailedStatistics) {
	stats.Statistics.BlockCount++
	stats.Statistics.BlockBytes += m.Size()

	_ = m.VisitAllBlocks(
		func(handle BlockAllocationHandle, offset int, size int, userData any, free bool) error {
			if free {
				stats.AddUnusedRange(size)
			} else {
				stats.AddAllocation(size)
			}

			return nil
		})
}

func (m *LinearBlockMetadata) AddStatistics(stats *memutils.Statistics) {

	size := m.Size()
	stats.BlockCount++
	stats.BlockBytes += size
	stats.AllocationBytes += size - m.sumFreeSize

	_ = m.VisitAllBlocks(
		func(handle BlockAllocationHandle, offset int, size int, userData any, free bool) error {
			if !free {
				stats.AllocationCount++
			}

			return nil
		})
}

func (m *LinearBlockMetadata) PrintDetailedMapHeader(json jwriter.ObjectState) error {
	// first pass
	size := m.Size()
	var unusedRangeCount, usedBytes, allocCount int

	_ = m.VisitAllBlocks(
		func(handle BlockAllocationHandle, offset int, size int, userData any, free bool) error {
			if free {
				unusedRangeCount++
			} else {
				usedBytes += size
				allocCount++
			}

			return nil
		})

	unusedBytes := size - usedBytes
	m.PrintDetailedMap_Header(json, unusedBytes, allocCount, unusedRangeCount)

	return nil
}

func (m *LinearBlockMetadata) CreateAllocationRequest(
	allocSize int, allocAlignment uint,
	upperAddress bool,
	allocType SuballocationType,
	strategy memutils.AllocationCreateFlags,
) (bool, AllocationRequest, error) {
	if allocSize <= 0 {
		return false, AllocationRequest{}, errors.New("allocation size must be greater than 0")
	}
	if allocType == SuballocationFree {
		return false, AllocationRequest{}, errors.New("allocation type cannot be SuballocationFree")
	}
	memutils.DebugValidate(m)

	allocRequest := AllocationRequest{
		Size: allocSize,
	}
	if upperAddress {
		success, err := m.populateAllocationRequestUpper(allocSize, allocAlignment, allocType, &allocRequest)
		return success, allocRequest, err
	}

	success := m.populateAllocationRequestLower(allocSize, allocAlignment, allocType, &allocRequest)
	return success, allocRequest, nil
}

func (m *LinearBlockMetadata) CheckCorruption(blockData unsafe.Pointer) (common.VkResult, error) {
	if m.isVirtual {
		return core1_0.VKErrorUnknown, errors.New("cannot check corruption on virtual blocks")
	}

	firstVector := *m.accessSuballocationsFirst()

	for i := m.firstNullItemsBeginCount; i < len(firstVector); i++ {
		suballoc := firstVector[i]
		if suballoc.Type != SuballocationFree && !memutils.ValidateMagicValue(blockData, suballoc.Offset+suballoc.Size) {
			return core1_0.VKErrorUnknown, errors.New("MEMORY CORRUPTION DETECTED AFTER VALIDATED ALLOCATION!")
		}
	}

	secondVector := *m.accessSuballocationsSecond()
	for i := 0; i < len(secondVector); i++ {
		suballoc := secondVector[i]
		if suballoc.Type != SuballocationFree && !memutils.ValidateMagicValue(blockData, suballoc.Offset+suballoc.Size) {
			return core1_0.VKErrorUnknown, errors.New("MEMORY CORRUPTION DETECTED AFTER VALIDATED ALLOCATION!")
		}
	}

	return core1_0.VKSuccess, nil
}

func (m *LinearBlockMetadata) Alloc(req AllocationRequest, allocType SuballocationType, userData any) error {
	offset := int(req.BlockAllocationHandle) - 1
	newSuballoc := Suballocation{
		Offset:   offset,
		Size:     req.Size,
		UserData: userData,
		Type:     allocType,
	}

	switch req.Type {
	case AllocationRequestUpperAddress:
		if m.secondVectorMode == SecondVectorModeRingBuffer {
			return errors.New("critical error: trying to use linear allocator as double stack while it was already being used as a ring buffer")
		}
		secondVector := m.accessSuballocationsSecond()
		*secondVector = append(*secondVector, newSuballoc)
		m.secondVectorMode = SecondVectorModeDoubleStack
		break
	case AllocationRequestEndOf1st:
		firstVector := m.accessSuballocationsFirst()

		if len(*firstVector) > 0 {
			lastItem := (*firstVector)[len(*firstVector)-1]
			if offset < lastItem.Offset+lastItem.Size {
				return errors.New("attempted to allocate memory in the middle of active memory")
			}
		}

		if offset+req.Size > m.size {
			return errors.New("attempted to allocate memory past the end of the block")
		}

		*firstVector = append(*firstVector, newSuballoc)
		break
	case AllocationRequestEndOf2nd:
		firstVector := *m.accessSuballocationsFirst()
		// New allocation at the end of 2-part ring buffer, so place it before the first allocation
		// from the first vector
		if len(firstVector) == 0 {
			return errors.New("attempted to allocate memory into the second part of the a buffer, but the first part had no allocations")
		}
		if offset+req.Size > firstVector[m.firstNullItemsBeginCount].Offset {
			return errors.New("attempted to allocate memory into the second part of a ring buffer, but the allocation extended into the first part of the ring buffer")
		}
		secondVector := m.accessSuballocationsSecond()
		switch m.secondVectorMode {
		case SecondVectorModeEmpty:
			if len(*secondVector) > 0 {
				return errors.New("the second vector was marked as empty, but was not empty")
			}

			m.secondVectorMode = SecondVectorModeRingBuffer
			break
		case SecondVectorModeRingBuffer:
			if len(*secondVector) == 0 {
				return errors.New("the second vector was marked as a ring buffer, but was empty")
			}
			break
		case SecondVectorModeDoubleStack:
			return errors.New("attempted to allocate as a ring buffer when the vector was marked as a stack")
		}

		*secondVector = append(*secondVector, newSuballoc)
		break
	default:
		return errors.Newf("attempted to allocate a request of type %s, but that type isn't supported by the Linear metadata", req.Type)
	}

	m.sumFreeSize -= newSuballoc.Size
	return nil
}

func (m *LinearBlockMetadata) Free(allocHandle BlockAllocationHandle) error {
	firstVectorPtr := m.accessSuballocationsFirst()
	firstVector := *firstVectorPtr
	secondVectorPtr := m.accessSuballocationsSecond()
	secondVector := *secondVectorPtr

	offset := int(allocHandle) - 1

	if len(firstVector) > 0 {
		// We're freeing the first allocation, mark it as empty at the beginning
		firstSuballoc := &(firstVector[m.firstNullItemsBeginCount])
		if firstSuballoc.Offset == offset {
			firstSuballoc.Type = SuballocationFree
			firstSuballoc.UserData = nil
			m.sumFreeSize += firstSuballoc.Size
			m.firstNullItemsBeginCount++
			m.cleanupAfterFree()
			return nil
		}
	}

	// Last allocation in a ring buffer or top of upper stack, mark it empty at the end
	if m.secondVectorMode == SecondVectorModeRingBuffer || m.secondVectorMode == SecondVectorModeDoubleStack {
		lastSuballoc := secondVector[len(secondVector)-1]
		if lastSuballoc.Offset == offset {
			m.sumFreeSize += lastSuballoc.Size
			*secondVectorPtr = secondVector[0 : len(secondVector)-1]
			m.cleanupAfterFree()
			return nil
		}
	} else if m.secondVectorMode == SecondVectorModeEmpty {
		// Last allocation in first vector
		lastSuballoc := firstVector[len(firstVector)-1]
		if lastSuballoc.Offset == offset {
			m.sumFreeSize += lastSuballoc.Size
			*firstVectorPtr = firstVector[0 : len(firstVector)-1]
			m.cleanupAfterFree()
			return nil
		}
	}

	// Item from the middle of first vector
	virtualLen := len(firstVector) - m.firstNullItemsBeginCount
	virtualOut, found := sort.Find(virtualLen, func(virtualIndex int) int {
		index := virtualIndex + m.firstNullItemsBeginCount
		foundOffset := firstVector[index].Offset
		return offset - foundOffset
	})
	if found {
		out := virtualOut + m.firstNullItemsBeginCount
		suballoc := &(firstVector[out])
		suballoc.Type = SuballocationFree
		suballoc.UserData = nil
		m.firstNullItemsMiddleCount++
		m.sumFreeSize += suballoc.Size
		m.cleanupAfterFree()
		return nil
	}

	if m.secondVectorMode != SecondVectorModeEmpty {
		// Item from the middle of second vector
		out, found := sort.Find(len(secondVector), func(index int) int {
			foundOffset := firstVector[index].Offset
			return offset - foundOffset
		})
		if found {
			suballoc := &(secondVector[out])
			suballoc.Type = SuballocationFree
			suballoc.UserData = nil
			m.secondNullItemsCount++
			m.sumFreeSize += suballoc.Size
			m.cleanupAfterFree()
			return nil
		}
	}

	return errors.New("allocation to free not found in this allocator")
}

func (m *LinearBlockMetadata) AllocationUserData(allocHandle BlockAllocationHandle) (any, error) {
	suballoc, err := m.findSuballocation(int(allocHandle) - 1)
	if err != nil {
		return nil, err
	}
	return suballoc.UserData, nil
}

func (m *LinearBlockMetadata) AllocationListBegin() (BlockAllocationHandle, error) {
	return 0, errors.New("defragmentation cannot be performed on the linear metadata")
}

func (m *LinearBlockMetadata) FindNextAllocation(allocHandle BlockAllocationHandle) (BlockAllocationHandle, error) {
	return 0, errors.New("defragmentation cannot be performed on the linear metadata")
}

func (m *LinearBlockMetadata) FindNextFreeRegionSize(allocHandle BlockAllocationHandle) (int, error) {
	return 0, errors.New("defragmentation cannot be performed on the linear metadata")
}

func (m *LinearBlockMetadata) Clear() {
	m.sumFreeSize = m.size
	m.suballocations0 = m.suballocations0[:0]
	m.suballocations1 = m.suballocations1[:0]
	m.secondVectorMode = SecondVectorModeEmpty
	m.firstNullItemsMiddleCount = 0
	m.firstNullItemsBeginCount = 0
	m.secondNullItemsCount = 0
}

func (m *LinearBlockMetadata) SetAllocationUserData(allocHandle BlockAllocationHandle, userData any) error {
	suballoc, err := m.findSuballocation(int(allocHandle) - 1)
	if err != nil {
		return err
	}
	suballoc.UserData = userData
	return nil
}

func (m *LinearBlockMetadata) DebugLogAllAllocations(log *slog.Logger, logFunc func(log *slog.Logger, offset int, size int, userData any)) {
	firstVector := *m.accessSuballocationsFirst()
	for i := m.firstNullItemsBeginCount; i < len(firstVector); i++ {
		suballoc := firstVector[i]
		if suballoc.Type != SuballocationFree {
			logFunc(log, suballoc.Offset, suballoc.Size, suballoc.UserData)
		}
	}

	secondVector := *m.accessSuballocationsSecond()
	for i := 0; i < len(secondVector); i++ {
		suballoc := secondVector[i]
		if suballoc.Type != SuballocationFree {
			logFunc(log, suballoc.Offset, suballoc.Size, suballoc.UserData)
		}
	}
}

func (m *LinearBlockMetadata) findSuballocation(offset int) (*Suballocation, error) {

	// Check first vector
	firstVector := *m.accessSuballocationsFirst()
	out, found := sort.Find(len(firstVector), func(virtualIndex int) int {
		index := virtualIndex + m.firstNullItemsBeginCount
		return offset - firstVector[index].Offset
	})
	if found {
		return &(firstVector[out]), nil
	}

	if m.secondVectorMode != SecondVectorModeEmpty {
		secondVector := *m.accessSuballocationsSecond()
		out, found = sort.Find(len(secondVector), func(index int) int {
			return offset - secondVector[index].Offset
		})
		if found {
			return &(secondVector[out]), nil
		}
	}

	return nil, errors.New("allocation not found in linear allocator")
}

func (m *LinearBlockMetadata) shouldCompactFirstVector() bool {
	nullItemCount := m.firstNullItemsMiddleCount + m.firstNullItemsMiddleCount
	firstVector := *m.accessSuballocationsFirst()

	return len(firstVector) > 32 && nullItemCount*2 >= (len(firstVector)-nullItemCount)*3
}

func (m *LinearBlockMetadata) cleanupAfterFree() {
	firstVectorPtr := m.accessSuballocationsFirst()
	firstVector := *firstVectorPtr
	secondVectorPtr := m.accessSuballocationsSecond()
	secondVector := *secondVectorPtr

	if m.IsEmpty() {
		m.suballocations0 = m.suballocations0[:0]
		m.suballocations1 = m.suballocations1[:0]
		m.firstNullItemsBeginCount = 0
		m.firstNullItemsMiddleCount = 0
		m.secondNullItemsCount = 0
		m.secondVectorMode = SecondVectorModeEmpty
		return
	}

	nullItemsCount := m.firstNullItemsBeginCount + m.firstNullItemsMiddleCount
	if nullItemsCount > len(firstVector) {
		panic(fmt.Sprintf("the metadata expects %d free allocations in the first vector, but only %d total allocations exist", nullItemsCount, len(firstVector)))
	}

	// FInd more null items at the beginning of the first vector
	for m.firstNullItemsBeginCount < len(firstVector) && firstVector[m.firstNullItemsBeginCount].Type == SuballocationFree {
		m.firstNullItemsBeginCount++
		m.firstNullItemsMiddleCount--
	}

	// Find more null items at the end of the first vector
	for m.firstNullItemsMiddleCount > 0 && firstVector[len(firstVector)-1].Type == SuballocationFree {
		m.firstNullItemsMiddleCount--
		firstVector = firstVector[:len(firstVector)-1]
		*firstVectorPtr = firstVector
	}

	// Find more null items at the end of the second vector
	for m.secondNullItemsCount > 0 && secondVector[len(secondVector)-1].Type == SuballocationFree {
		m.secondNullItemsCount--
		secondVector = secondVector[:len(secondVector)-1]
		*secondVectorPtr = secondVector
	}

	// Find more null items at the beginning of the second vector
	removeFromBeginning := 0
	for m.secondNullItemsCount > 0 && secondVector[0].Type == SuballocationFree {
		m.secondNullItemsCount--
		removeFromBeginning++
	}

	if removeFromBeginning > 0 {
		secondVector = secondVector[removeFromBeginning:]
		*secondVectorPtr = secondVector
	}

	if m.shouldCompactFirstVector() {
		nonNullItemCount := len(firstVector) - nullItemsCount
		srcIndex := m.firstNullItemsBeginCount
		for dstIndex := 0; dstIndex < nonNullItemCount; dstIndex++ {
			for firstVector[srcIndex].Type == SuballocationFree {
				srcIndex++
			}

			if dstIndex != srcIndex {
				firstVector[dstIndex] = firstVector[srcIndex]
			}
			srcIndex++
		}

		firstVector = firstVector[:nonNullItemCount]
		*firstVectorPtr = firstVector
		m.firstNullItemsBeginCount = 0
		m.firstNullItemsMiddleCount = 0
	}

	if len(secondVector) == 0 {
		m.secondVectorMode = SecondVectorModeEmpty
	}

	// First vector became empty
	if len(firstVector)-m.firstNullItemsBeginCount == 0 {
		*firstVectorPtr = []Suballocation{}
		m.firstNullItemsBeginCount = 0

		if len(secondVector) > 0 && m.secondVectorMode == SecondVectorModeRingBuffer {
			// Swap vectors
			m.secondVectorMode = SecondVectorModeEmpty
			m.firstNullItemsMiddleCount = m.secondNullItemsCount
			m.secondNullItemsCount = 0

			for m.firstNullItemsBeginCount < len(secondVector) && secondVector[m.firstNullItemsBeginCount].Type == SuballocationFree {
				m.firstNullItemsBeginCount++
				m.firstNullItemsMiddleCount--
			}
			m.firstVectorIndex ^= 1
		}
	}
}

func blocksOnSamePage(resourceOffset1, resourceSize1, resourceOffset2, pagesize int) bool {
	if resourceOffset1+resourceSize1 > resourceOffset2 {
		panic(fmt.Sprintf("resource 1 must be before resource 2 in memory, but resource1 ends at offset %d and resource 2 is at offset %d", resourceOffset1+resourceSize1, resourceOffset2))
	}
	if resourceSize1 < 1 {
		panic(fmt.Sprintf("resource 1 must have a positive size, but has a size of %d", resourceSize1))
	}
	if pagesize < 1 {
		panic(fmt.Sprintf("the page size must be positive, but is %d", pagesize))
	}

	resource1End := resourceOffset1 + resourceSize1 - 1
	resource1EndPage := resource1End & ^(pagesize - 1)
	resource2StartPage := resourceOffset2 & ^(pagesize - 1)

	return resource1EndPage == resource2StartPage
}

func (m *LinearBlockMetadata) populateAllocationRequestLower(
	allocSize int, allocAlignment uint,
	allocType SuballocationType,
	allocRequest *AllocationRequest,
) bool {
	debugMargin := m.DebugMargin()
	firstVector := *m.accessSuballocationsFirst()
	secondVector := *m.accessSuballocationsSecond()

	if m.secondVectorMode == SecondVectorModeEmpty || m.secondVectorMode == SecondVectorModeDoubleStack {
		// Try to allocate at the end of the first vector
		var resultBaseOffset int
		if len(firstVector) > 0 {
			lastSubAlloc := firstVector[len(firstVector)-1]
			resultBaseOffset = lastSubAlloc.Offset + lastSubAlloc.Size + debugMargin
		}

		// Start from the beginning of free space and move forward
		resultOffset := resultBaseOffset
		resultOffset = memutils.AlignUp(resultOffset, allocAlignment)

		// Check previous suballocations for granularity conflict & align up if necessary
		if m.bufferImageGranlarity > 1 && m.bufferImageGranlarity != int(allocAlignment) && len(firstVector) > 0 {
			var bufferImageGranularityConflict bool

			for prevSuballocIndex := len(firstVector) - 1; prevSuballocIndex >= 0; prevSuballocIndex-- {
				prevSuballoc := firstVector[prevSuballocIndex]
				samePage := blocksOnSamePage(prevSuballoc.Offset, prevSuballoc.Size, resultOffset, m.bufferImageGranlarity)

				if !samePage {
					// We've passed beyond the bounds of the result offset's page
					break
				}

				if IsBufferImageGranularityConflict(prevSuballoc.Type, allocType) {
					bufferImageGranularityConflict = true
					break
				}
			}

			if bufferImageGranularityConflict {
				resultOffset = memutils.AlignUp(resultOffset, uint(m.bufferImageGranlarity))
			}
		}

		freeSpaceEnd := m.size

		if m.secondVectorMode == SecondVectorModeDoubleStack && len(secondVector) > 0 {
			// First vector only goes to the beginning of the second vector in a double stack
			freeSpaceEnd = secondVector[len(secondVector)-1].Offset
		}

		if resultOffset+allocSize+debugMargin <= freeSpaceEnd {
			// We have enough free space to allocate right here
			if (allocSize%m.bufferImageGranlarity > 0 || resultOffset%m.bufferImageGranlarity > 0) && m.secondVectorMode == SecondVectorModeDoubleStack {
				// If we have a double stack, check the second vector to see if there's buffer image granularity conflicts
				// With our intended spot
				for nextSuballocIndex := len(secondVector) - 1; nextSuballocIndex >= 0; nextSuballocIndex-- {
					nextSuballoc := secondVector[nextSuballocIndex]
					samePage := blocksOnSamePage(resultOffset, allocSize, nextSuballoc.Offset, m.bufferImageGranlarity)

					if !samePage {
						// We've passed beyond the bounds of the result offset's page
						break
					}

					if IsBufferImageGranularityConflict(allocType, nextSuballoc.Type) {
						// We're already as far back as we can manage, so there's no room to place this alloc
						return false
					}
				}
			}

			// We're good to allocate in the first vector
			allocRequest.BlockAllocationHandle = BlockAllocationHandle(resultOffset + 1)
			allocRequest.Type = AllocationRequestEndOf1st
			return true
		}
	}

	// In a ring buffer (or empty if we're out of space), we'll attempt to allocate at the end of the second vector
	if m.secondVectorMode == SecondVectorModeEmpty || m.secondVectorMode == SecondVectorModeRingBuffer {
		if len(firstVector) == 0 {
			panic("attempting to allocate into the second vector, but the first is not empty")
		}

		var resultBaseOffset int
		if len(secondVector) > 0 {
			lastSuballoc := secondVector[len(secondVector)-1]
			resultBaseOffset = lastSuballoc.Offset + lastSuballoc.Size + debugMargin
		}

		resultOffset := memutils.AlignUp(resultBaseOffset, allocAlignment)

		// Check previous suballocations for image granularity conflicts
		if m.bufferImageGranlarity > 1 && m.bufferImageGranlarity != int(allocAlignment) && len(secondVector) > 0 {
			var bufferImageGranularityConflict bool
			for prevSuballocIndex := len(secondVector) - 1; prevSuballocIndex >= 0; prevSuballocIndex-- {
				prevSuballoc := secondVector[prevSuballocIndex]
				samePage := blocksOnSamePage(prevSuballoc.Offset, prevSuballoc.Size, resultOffset, m.bufferImageGranlarity)

				if samePage {
					if IsBufferImageGranularityConflict(prevSuballoc.Type, allocType) {
						bufferImageGranularityConflict = true
						break
					}
				} else {
					// We've passed beyond the bounds of the result offset's page
					break
				}
			}

			if bufferImageGranularityConflict {
				resultOffset = memutils.AlignUp(resultOffset, uint(m.bufferImageGranlarity))
			}
		}

		// See if there's enough space before the beginning of the first vector
		firstVectorIndex := m.firstNullItemsBeginCount
		if (firstVectorIndex == len(firstVector) && resultOffset+allocSize+debugMargin <= m.size) ||
			(firstVectorIndex < len(firstVector) && resultOffset+allocSize+debugMargin <= firstVector[firstVectorIndex].Offset) {

			// Check next suballocations for image granularity conflicts
			for nextSuballocIndex := firstVectorIndex; nextSuballocIndex < len(firstVector); nextSuballocIndex++ {
				nextSuballoc := firstVector[nextSuballocIndex]
				samePage := blocksOnSamePage(resultOffset, allocSize, nextSuballoc.Offset, m.bufferImageGranlarity)

				if samePage {
					if IsBufferImageGranularityConflict(allocType, nextSuballoc.Type) {
						// We're back as far as we can be and still have a buffer image granularity conflict with the next
						// suballoc
						return false
					}
				} else {
					// We've passed beyond the bounds of the result offset's page
					break
				}
			}

			// We're good to allocate in the second vector
			allocRequest.BlockAllocationHandle = BlockAllocationHandle(resultOffset + 1)
			allocRequest.Type = AllocationRequestEndOf2nd
			return true
		}
	}

	// No good place to allocate
	return false
}

func (m *LinearBlockMetadata) populateAllocationRequestUpper(
	allocSize int, allocAlignment uint,
	allocType SuballocationType,
	allocRequest *AllocationRequest,
) (bool, error) {
	firstVector := *m.accessSuballocationsFirst()
	secondVector := *m.accessSuballocationsSecond()

	if m.secondVectorMode == SecondVectorModeRingBuffer {
		return false, errors.New("ring buffers cannot allocate using upperAddress, that is reserved for double stacks")
	}

	if allocSize > m.size {
		// Too big
		return false, nil
	}

	baseOffset := m.size - allocSize
	// If there are items in the second vector, we need to put this item before the last item in the second vector
	if len(secondVector) > 0 {
		lastAlloc := secondVector[len(secondVector)-1]
		baseOffset = lastAlloc.Offset - allocSize
		if allocSize > lastAlloc.Offset {
			// Allocation can't fit into the upper end of the ring
			return false, nil
		}
	}

	// Start from the base offset and move forward
	resultOffset := baseOffset
	debugMargin := m.DebugMargin()

	// Apply debug margin to the end of the allocation
	if debugMargin > 0 {
		if resultOffset < debugMargin {
			// No room when including the debug margin
			return false, nil
		}
		resultOffset -= debugMargin
	}

	// Apply alignment
	resultOffset = memutils.AlignUp(resultOffset, allocAlignment)

	// Check next suballocations from second vector for BufferImageGranularity conflicts. Increase alignment if
	// necessary
	if m.bufferImageGranlarity > 1 && m.bufferImageGranlarity != int(allocAlignment) && len(secondVector) > 0 {
		var bufferImageGranularityConflict bool
		for nextSuballocIndex := len(secondVector) - 1; nextSuballocIndex >= 0; nextSuballocIndex-- {
			nextSuballoc := secondVector[nextSuballocIndex]
			samePage := blocksOnSamePage(resultOffset, allocSize, nextSuballoc.Offset, m.bufferImageGranlarity)

			if !samePage {
				// We've passed beyond the bounds of the result offset's page
				break
			}

			if IsBufferImageGranularityConflict(nextSuballoc.Type, allocType) {
				bufferImageGranularityConflict = true
				break
			}
		}

		if bufferImageGranularityConflict {
			resultOffset = memutils.AlignDown(resultOffset, uint(m.bufferImageGranlarity))
		}
	}

	// We have a good offset & size for the second vector, but we need to check whether it collides with the first
	firstVectorEndOffset := 0
	if len(firstVector) > 0 {
		lastSuballoc := firstVector[len(firstVector)-1]
		firstVectorEndOffset = lastSuballoc.Offset + lastSuballoc.Size
	}

	if firstVectorEndOffset+debugMargin > resultOffset {
		// We backed the result offset into the end of the first vector
		return false, nil
	}

	if m.bufferImageGranlarity > 1 {
		// Check first vector for granularity conflicts
		for prevSuballocIndex := len(firstVector) - 1; prevSuballocIndex >= 0; prevSuballocIndex-- {
			prevSuballoc := firstVector[prevSuballocIndex]
			samePage := blocksOnSamePage(prevSuballoc.Offset, prevSuballoc.Size, resultOffset, m.bufferImageGranlarity)

			if !samePage {
				// We've passed beyond the bounds of the result offset's page
				break
			}

			if IsBufferImageGranularityConflict(allocType, prevSuballoc.Type) {
				// Conflict with a block at the end of the first vector, and there's no room to maneuver
				// (we're already as far forward as we can go)
				return false, nil
			}
		}
	}

	// Everything is good, populate the request
	allocRequest.BlockAllocationHandle = BlockAllocationHandle(resultOffset + 1)
	allocRequest.Type = AllocationRequestUpperAddress
	return true, nil
}

func (m *LinearBlockMetadata) accessSuballocationsFirst() *[]Suballocation {
	if m.firstVectorIndex != 0 {
		return &m.suballocations1
	}

	return &m.suballocations0
}

func (m *LinearBlockMetadata) accessSuballocationsSecond() *[]Suballocation {
	if m.firstVectorIndex != 0 {
		return &m.suballocations0
	}

	return &m.suballocations1
}
