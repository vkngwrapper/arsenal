package vam

import (
	"math/bits"

	"github.com/pkg/errors"
	"github.com/vkngwrapper/arsenal/memutils"
)

const (
	MaxLowBufferImageGranularity uint = 256
)

type regionInfo struct {
	allocType  suballocationType
	allocCount uint16
}

type validationContext struct {
	regionAllocs []uint16
}

type blockBufferImageGranularity struct {
	bufferImageGranularity uint
	regionInfo             []regionInfo
}

func (g *blockBufferImageGranularity) Init(size int) {
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

func (g *blockBufferImageGranularity) Destroy() {
	if g.regionInfo != nil {
		g.regionInfo = nil
	}
}

func (g *blockBufferImageGranularity) AllocationsConflict(
	firstAllocType uint32,
	secondAllocType uint32,
) bool {
	subAllocType1 := suballocationType(firstAllocType)
	subAllocType2 := suballocationType(secondAllocType)

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

func (g *blockBufferImageGranularity) RoundUpAllocRequest(allocType uint32, allocSize int, allocAlignment uint) (int, uint) {
	if g.bufferImageGranularity > 1 {
		// This behavior is slightly different from VMA's- you might think it's a bug.
		// However, it's actually an optimization.  SuballocationImageUnknown
		// and SuballocationUnknown can never share with other allocations, so just
		// aligning up reduces the number of tiny unusable allocations between blocks
		// and is a pretty substantial performance improvement when enough of these
		// are floating around
		suballocType := suballocationType(allocType)
		imageRoundUp := g.bufferImageGranularity <= MaxLowBufferImageGranularity && suballocType == SuballocationImageOptimal
		generalRoundUp := suballocType == SuballocationImageUnknown || suballocType == SuballocationUnknown

		if imageRoundUp || generalRoundUp {
			if allocAlignment < g.bufferImageGranularity {
				allocAlignment = g.bufferImageGranularity
			}

			allocSize = memutils.AlignUp(allocSize, g.bufferImageGranularity)
		}
	}

	return allocSize, allocAlignment
}

func (g *blockBufferImageGranularity) CheckConflictAndAlignUp(
	allocOffset, allocSize, regionOffset, regionSize int,
	allocType uint32,
) (int, bool) {
	if !g.IsEnabled() {
		return allocOffset, false
	}

	startSlot := g.getStartSlot(allocOffset)
	for g.regionInfo[startSlot].allocCount > 0 &&
		g.AllocationsConflict(uint32(g.regionInfo[startSlot].allocType), allocType) {

		allocOffset = memutils.AlignUp(allocOffset, g.bufferImageGranularity)

		if regionSize < allocSize+allocOffset-regionOffset {
			return allocOffset, true
		}

		startSlot++
	}

	endSlot := g.getEndSlot(allocOffset, allocSize)
	if endSlot != startSlot && g.regionInfo[endSlot].allocCount > 0 &&
		g.AllocationsConflict(uint32(g.regionInfo[endSlot].allocType), allocType) {
		return allocOffset, true
	}

	return allocOffset, false
}

func (g *blockBufferImageGranularity) AllocRegions(allocType uint32, offset, size int) {
	if !g.IsEnabled() {
		return
	}

	startRegion := g.getStartSlot(offset)
	g.allocRegion(&g.regionInfo[startRegion], suballocationType(allocType))

	endRegion := g.getEndSlot(offset, size)
	if startRegion != endRegion {
		g.allocRegion(&g.regionInfo[endRegion], suballocationType(allocType))
	}
}

func (g *blockBufferImageGranularity) FreeRegions(offset, size int) {
	if !g.IsEnabled() {
		return
	}

	startRegion := g.getStartSlot(offset)
	g.regionInfo[startRegion].allocCount--
	if g.regionInfo[startRegion].allocCount == 0 {
		g.regionInfo[startRegion].allocType = SuballocationFree
	}

	endRegion := g.getEndSlot(offset, size)
	if startRegion != endRegion {
		g.regionInfo[endRegion].allocCount--
		if g.regionInfo[endRegion].allocCount == 0 {
			g.regionInfo[endRegion].allocType = SuballocationFree
		}
	}
}

func (g *blockBufferImageGranularity) Clear() {
	if g.regionInfo != nil {
		g.regionInfo = make([]regionInfo, len(g.regionInfo))
	}
}

func (g *blockBufferImageGranularity) StartValidation() any {
	context := &validationContext{}

	if g.IsEnabled() {
		context.regionAllocs = make([]uint16, len(g.regionInfo))
	}

	return context
}

func (g *blockBufferImageGranularity) Validate(anyCtx any, offset, size int) error {
	if !g.IsEnabled() {
		return nil
	}

	ctx := anyCtx.(*validationContext)
	start := g.getStartSlot(offset)
	ctx.regionAllocs[start]++
	if g.regionInfo[start].allocCount < 1 {
		return errors.Errorf("no allocations in start region %d", start)
	}

	end := g.getEndSlot(offset, size)
	if start != end {
		ctx.regionAllocs[end]++
		if g.regionInfo[end].allocCount < 1 {
			return errors.Errorf("no allocations in end region %d", end)
		}
	}

	return nil
}

func (g *blockBufferImageGranularity) FinishValidation(anyCtx any) error {
	if !g.IsEnabled() {
		return nil
	}

	ctx := anyCtx.(*validationContext)

	for regionIndex, region := range g.regionInfo {
		if ctx.regionAllocs[regionIndex] != region.allocCount {
			return errors.Errorf("allocation count mismatch on region %d", regionIndex)
		}
	}
	ctx.regionAllocs = nil

	return nil
}

func (g *blockBufferImageGranularity) allocRegion(region *regionInfo, allocType suballocationType) {
	if region.allocCount == 0 || (region.allocCount > 0 && region.allocType == SuballocationFree) {
		region.allocType = allocType
	}

	region.allocCount++
}

func (g *blockBufferImageGranularity) IsEnabled() bool {
	return g.bufferImageGranularity > MaxLowBufferImageGranularity
}

func (g *blockBufferImageGranularity) getStartSlot(offset int) int {
	return g.offsetToRegionIndex(offset & int(^(g.bufferImageGranularity - 1)))
}

func (g *blockBufferImageGranularity) getEndSlot(offset int, size int) int {
	return g.offsetToRegionIndex((offset + size - 1) & int(^(g.bufferImageGranularity - 1)))
}

func (g *blockBufferImageGranularity) offsetToRegionIndex(offset int) int {
	return offset >> (63 - bits.LeadingZeros64(uint64(g.bufferImageGranularity)))
}
