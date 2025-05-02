package defrag

import "fmt"

// PassContext is an object used to track data for the current defragmentation
// pass across multiple relocations
type PassContext struct {
	// MaxPassBytes is the maximum number of bytes to relocate in each pass. There is no guarantee that
	// this many bytes will actually be relocated in any given pass, based on how easy it is to find additional
	// relocations to fit within the budget
	MaxPassBytes int
	// MaxPassAllocations is the maximum number of relocations to perform in each pass. There is no guarantee
	// that this many allocations will actually be relocated in any given pass, based on how easy it is to find
	// additional relocations to fit within the budget. This value is a close proxy for the number of go allocations
	// made for each pass, so managing this and MaxPassBytes can help you control memory and cpu throughput of the
	// defragmentation process.
	MaxPassAllocations int
	// Stats contains statistics for the current pass, such as bytes moved,
	// allocations performed, etc.
	Stats         DefragmentationStats
	ignoredAllocs int
}

const defragMaxAllocsToIgnore = 16

func (p *PassContext) checkCounters(bytes int) defragCounterStatus {
	// Ignore allocation if it will exceed max size for copy
	if p.Stats.BytesMoved+bytes > p.MaxPassBytes {
		p.ignoredAllocs++
		if p.ignoredAllocs < defragMaxAllocsToIgnore {
			return defragCounterIgnore
		} else {
			return defragCounterEnd
		}
	} else {
		p.ignoredAllocs = 0
	}

	return defragCounterPass
}

func (p *PassContext) incrementCounters(bytes int) bool {
	p.Stats.BytesMoved += bytes
	p.Stats.AllocationsMoved++

	// Early return when max found
	if p.Stats.AllocationsMoved >= p.MaxPassAllocations || p.Stats.BytesMoved >= p.MaxPassBytes {
		if p.Stats.AllocationsMoved != p.MaxPassAllocations && p.Stats.BytesMoved != p.MaxPassBytes {
			panic(fmt.Sprintf("somehow passed maximum pass thresholds: bytes %d, allocs %d", p.Stats.BytesMoved, p.Stats.AllocationsMoved))
		}

		return true
	}

	return false
}
