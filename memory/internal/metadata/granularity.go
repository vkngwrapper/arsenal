package metadata

import (
	"github.com/cockroachdb/errors"
	"github.com/vkngwrapper/arsenal/memory/internal/utils"
	"math/bits"
)

const (
	MaxLowBufferImageGranularity uint = 256
)

func IsBufferImageGranularityConflict(
	subAllocType1 SuballocationType,
	subAllocType2 SuballocationType,
) bool {
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
		g.regionInfo = make([]regionInfo, size)
	}
}

func (g *BlockBufferImageGranularity) Destroy() {
	if g.regionInfo != nil {
		g.regionInfo = nil
	}
}

func (g *BlockBufferImageGranularity) RoundupAllocRequest(allocType SuballocationType, allocSize int, allocAlignment uint) (int, uint, error) {
	if g.bufferImageGranularity > 1 &&
		g.bufferImageGranularity <= MaxLowBufferImageGranularity &&
		(allocType == SuballocationUnknown ||
			allocType == SuballocationImageUnknown ||
			allocType == SuballocationImageOptimal) {

		if allocAlignment < g.bufferImageGranularity {
			allocAlignment = g.bufferImageGranularity
		}

		var err error
		allocSize = utils.AlignUp(allocSize, g.bufferImageGranularity)
		if err != nil {
			return 0, 0, err
		}
	}

	return allocSize, allocAlignment, nil
}

func (g *BlockBufferImageGranularity) CheckConflictAndAlignUp(
	allocOffset, allocSize, blockOffset, blockSize int,
	allocType SuballocationType,
) (int, bool, error) {
	if !g.IsEnabled() {
		return allocOffset, false, nil
	}

	startPage := g.getStartPage(allocOffset)
	if g.regionInfo[startPage].allocCount > 0 &&
		IsBufferImageGranularityConflict(g.regionInfo[startPage].allocType, allocType) {
		var err error
		allocOffset = utils.AlignUp(allocOffset, g.bufferImageGranularity)
		if err != nil {
			return allocOffset, false, err
		}
		if blockSize < allocSize+allocOffset-blockOffset {
			return allocOffset, true, nil
		}

		startPage++
	}

	endPage := g.getEndPage(allocOffset, allocSize)
	if endPage != startPage && g.regionInfo[endPage].allocCount > 0 &&
		IsBufferImageGranularityConflict(g.regionInfo[endPage].allocType, allocType) {
		return allocOffset, true, nil
	}

	return allocOffset, false, nil
}

func (g *BlockBufferImageGranularity) AllocPages(allocType SuballocationType, offset, size int) {
	if !g.IsEnabled() {
		return
	}

	startPage := g.getStartPage(offset)
	g.allocPage(&g.regionInfo[startPage], allocType)

	endPage := g.getEndPage(offset, size)
	if startPage != endPage {
		g.allocPage(&g.regionInfo[endPage], allocType)
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

func (g *BlockBufferImageGranularity) startValidation(isVirtual bool) *validationContext {
	context := &validationContext{}

	if !isVirtual && g.IsEnabled() {
		context.pageAllocs = make([]uint16, len(g.regionInfo))
	}

	return context
}

func (g *BlockBufferImageGranularity) validate(ctx *validationContext, offset, size int) error {
	if !g.IsEnabled() {
		return nil
	}

	start := g.getStartPage(offset)
	ctx.pageAllocs[start]++
	if g.regionInfo[start].allocCount < 1 {
		return errors.Newf("no allocations in start page %d", start)
	}

	end := g.getEndPage(offset, size)
	if start != end {
		ctx.pageAllocs[end]++
		if g.regionInfo[end].allocCount < 1 {
			return errors.Newf("no allocations in end page %d", end)
		}
	}

	return nil
}

func (g *BlockBufferImageGranularity) finishValidation(ctx *validationContext) error {
	if !g.IsEnabled() {
		return nil
	}

	for regionIndex, region := range g.regionInfo {
		if ctx.pageAllocs[regionIndex] != region.allocCount {
			return errors.Newf("allocation count mismatch on page %d", regionIndex)
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
	return g.offsetToPageIndex((offset + size + 1) & int(^(g.bufferImageGranularity - 1)))
}

func (g *BlockBufferImageGranularity) offsetToPageIndex(offset int) int {
	return offset >> bits.LeadingZeros(g.bufferImageGranularity)
}
