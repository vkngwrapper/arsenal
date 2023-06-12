package vam

import (
	"github.com/pkg/errors"
	"github.com/vkngwrapper/arsenal/memutils"
	"math/bits"
)

const (
	MaxLowBufferImageGranularity uint = 256
)

type regionInfo struct {
	allocType  SuballocationType
	allocCount uint16
}

type validationContext struct {
	pageAllocs []uint16
}

type BlockBufferImageGranularity struct {
	bufferImageGranularity uint
	regionInfo             []regionInfo
}

func (g *BlockBufferImageGranularity) Init(size int) {
	if g.IsEnabled() {
		count := size / int(g.bufferImageGranularity)
		if size%int(g.bufferImageGranularity) > 0 {
			count++
		}

		if len(g.regionInfo) >= count {
			g.regionInfo = g.regionInfo[:count]
		} else {
			g.regionInfo = make([]regionInfo, count)
		}
	}
}

func (g *BlockBufferImageGranularity) Destroy() {
	if g.regionInfo != nil {
		g.regionInfo = nil
	}
}

func (g *BlockBufferImageGranularity) AllocationsConflict(
	firstAllocType uint32,
	secondAllocType uint32,
) bool {
	subAllocType1 := SuballocationType(firstAllocType)
	subAllocType2 := SuballocationType(secondAllocType)

	if subAllocType1 > subAllocType2 {
		subAllocType1, subAllocType2 = subAllocType2, subAllocType1
	}

	switch subAllocType1 {
	case SuballocationFree:
		return false
	case SuballocationUnknown:
		return true
	case SuballocationBuffer:
		return subAllocType2 == SuballocationImageUnknown || subAllocType2 == SuballocationImageOptimal
	case SuballocationImageUnknown:
		return subAllocType2 == SuballocationImageUnknown || subAllocType2 == SuballocationImageLinear ||
			subAllocType2 == SuballocationImageOptimal
	case SuballocationImageLinear:
		return subAllocType2 == SuballocationImageOptimal
	case SuballocationImageOptimal:
		return false
	}

	return false
}

func (g *BlockBufferImageGranularity) RoundUpAllocRequest(allocType uint32, allocSize int, allocAlignment uint) (int, uint) {
	suballocType := SuballocationType(allocType)

	if g.bufferImageGranularity > 1 &&
		g.bufferImageGranularity <= MaxLowBufferImageGranularity &&
		(suballocType == SuballocationUnknown ||
			suballocType == SuballocationImageUnknown ||
			suballocType == SuballocationImageOptimal) {

		if allocAlignment < g.bufferImageGranularity {
			allocAlignment = g.bufferImageGranularity
		}

		allocSize = memutils.AlignUp(allocSize, g.bufferImageGranularity)
	}

	return allocSize, allocAlignment
}

func (g *BlockBufferImageGranularity) CheckConflictAndAlignUp(
	allocOffset, allocSize, blockOffset, blockSize int,
	allocType uint32,
) (int, bool) {
	if !g.IsEnabled() {
		return allocOffset, false
	}

	startPage := g.getStartPage(allocOffset)
	if g.regionInfo[startPage].allocCount > 0 &&
		g.AllocationsConflict(uint32(g.regionInfo[startPage].allocType), allocType) {

		allocOffset = memutils.AlignUp(allocOffset, g.bufferImageGranularity)

		if blockSize < allocSize+allocOffset-blockOffset {
			return allocOffset, true
		}

		startPage++
	}

	endPage := g.getEndPage(allocOffset, allocSize)
	if endPage != startPage && g.regionInfo[endPage].allocCount > 0 &&
		g.AllocationsConflict(uint32(g.regionInfo[endPage].allocType), allocType) {
		return allocOffset, true
	}

	return allocOffset, false
}

func (g *BlockBufferImageGranularity) AllocPages(allocType uint32, offset, size int) {
	if !g.IsEnabled() {
		return
	}

	startPage := g.getStartPage(offset)
	g.allocPage(&g.regionInfo[startPage], SuballocationType(allocType))

	endPage := g.getEndPage(offset, size)
	if startPage != endPage {
		g.allocPage(&g.regionInfo[endPage], SuballocationType(allocType))
	}
}

func (g *BlockBufferImageGranularity) FreePages(offset, size int) {
	if !g.IsEnabled() {
		return
	}

	startPage := g.getStartPage(offset)
	g.regionInfo[startPage].allocCount--
	if g.regionInfo[startPage].allocCount == 0 {
		g.regionInfo[startPage].allocType = SuballocationFree
	}

	endPage := g.getEndPage(offset, size)
	if startPage != endPage {
		g.regionInfo[endPage].allocCount--
		if g.regionInfo[endPage].allocCount == 0 {
			g.regionInfo[endPage].allocType = SuballocationFree
		}
	}
}

func (g *BlockBufferImageGranularity) Clear() {
	if g.regionInfo != nil {
		g.regionInfo = make([]regionInfo, len(g.regionInfo))
	}
}

func (g *BlockBufferImageGranularity) StartValidation() any {
	context := &validationContext{}

	if g.IsEnabled() {
		context.pageAllocs = make([]uint16, len(g.regionInfo))
	}

	return context
}

func (g *BlockBufferImageGranularity) Validate(anyCtx any, offset, size int) error {
	if !g.IsEnabled() {
		return nil
	}

	ctx := anyCtx.(*validationContext)
	start := g.getStartPage(offset)
	ctx.pageAllocs[start]++
	if g.regionInfo[start].allocCount < 1 {
		return errors.Errorf("no allocations in start page %d", start)
	}

	end := g.getEndPage(offset, size)
	if start != end {
		ctx.pageAllocs[end]++
		if g.regionInfo[end].allocCount < 1 {
			return errors.Errorf("no allocations in end page %d", end)
		}
	}

	return nil
}

func (g *BlockBufferImageGranularity) FinishValidation(anyCtx any) error {
	if !g.IsEnabled() {
		return nil
	}

	ctx := anyCtx.(*validationContext)

	for regionIndex, region := range g.regionInfo {
		if ctx.pageAllocs[regionIndex] != region.allocCount {
			return errors.Errorf("allocation count mismatch on page %d", regionIndex)
		}
	}
	ctx.pageAllocs = nil

	return nil
}

func (g *BlockBufferImageGranularity) allocPage(page *regionInfo, allocType SuballocationType) {
	if page.allocCount == 0 || (page.allocCount > 0 && page.allocType == SuballocationFree) {
		page.allocType = allocType
	}

	page.allocCount++
}

func (g *BlockBufferImageGranularity) IsEnabled() bool {
	return g.bufferImageGranularity > MaxLowBufferImageGranularity
}

func (g *BlockBufferImageGranularity) getStartPage(offset int) int {
	return g.offsetToPageIndex(offset & int(^(g.bufferImageGranularity - 1)))
}

func (g *BlockBufferImageGranularity) getEndPage(offset int, size int) int {
	return g.offsetToPageIndex((offset + size - 1) & int(^(g.bufferImageGranularity - 1)))
}

func (g *BlockBufferImageGranularity) offsetToPageIndex(offset int) int {
	return offset >> (63 - bits.LeadingZeros64(uint64(g.bufferImageGranularity)))
}
