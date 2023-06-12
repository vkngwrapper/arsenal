package memutils

import (
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"math"
)

// Statistics is a type that represents metrics for the current state of a memory pool
type Statistics struct {
	// The number of memory pages active in the pool
	BlockCount int
	// The number of allocations that have been assigned in the pool
	AllocationCount int
	// The number of bytes that are in all memory pages in the pool
	BlockBytes int
	// The number of bytes in the pool that have been assigned to allocations.  Free bytes
	// are not counted
	AllocationBytes int
}

// Clear zeroes out all metrics
func (s *Statistics) Clear() {
	s.BlockCount = 0
	s.AllocationCount = 0
	s.BlockBytes = 0
	s.AllocationBytes = 0
}

// AddStatistics sums another Statistics object's metrics into this Statistics object
func (s *Statistics) AddStatistics(other *Statistics) {
	s.BlockCount += other.BlockCount
	s.AllocationCount += other.AllocationCount
	s.BlockBytes += other.BlockBytes
	s.AllocationBytes += other.AllocationBytes
}

// PrintJson writes a json object that contains this object's metrics
func (s *Statistics) PrintJson(writer *jwriter.Writer) {
	objState := writer.Object()
	defer objState.End()

	s.printJsonObj(&objState)
}

func (s *Statistics) printJsonObj(objState *jwriter.ObjectState) {
	objState.Name("BlockCount").Int(s.BlockCount)
	objState.Name("BlockBytes").Int(s.BlockBytes)
	objState.Name("AllocationCount").Int(s.AllocationCount)
	objState.Name("AllocationBytes").Int(s.AllocationBytes)
}

// DetailedStatistics is a superset of Statistics that includes additional fields
type DetailedStatistics struct {
	Statistics
	// UnusedRangeCount is the number of contiguous regions of free space in the pool
	UnusedRangeCount int
	// AllocationSizeMin is the size, in bytes, of the smallest allocation in the pool
	AllocationSizeMin int
	// AllocationSizeMax is the size, in bytes, of the largest allocation in the pool
	AllocationSizeMax int
	// UnusedRangeSizeMin is the size, in bytes, of the smallest contiguous region of free space in the pool
	UnusedRangeSizeMin int
	// UnusedRangeSizeMax is the size, in bytes, of the largest contiguous region of free space in the pool
	UnusedRangeSizeMax int
}

// Clear zeroes out all metrics
func (s *DetailedStatistics) Clear() {
	s.Statistics.Clear()
	s.UnusedRangeCount = 0
	s.AllocationSizeMin = math.MaxInt
	s.AllocationSizeMax = 0
	s.UnusedRangeSizeMin = math.MaxInt
	s.UnusedRangeSizeMax = 0
}

// AddUnusedRange adds a contiguous region of free space to this object's metrics
//
// size - The size, in bytes, of the region
func (s *DetailedStatistics) AddUnusedRange(size int) {
	s.UnusedRangeCount++

	if size < s.UnusedRangeSizeMin {
		s.UnusedRangeSizeMin = size
	}

	if size > s.UnusedRangeSizeMax {
		s.UnusedRangeSizeMax = size
	}
}

// AddAllocation adds an allocation to this object's metrics
//
// size - The size, in bytes, of the allocation
func (s *DetailedStatistics) AddAllocation(size int) {
	s.AllocationCount++
	s.AllocationBytes += size

	if size < s.AllocationSizeMin {
		s.AllocationSizeMin = size
	}

	if size > s.AllocationSizeMax {
		s.AllocationSizeMax = size
	}
}

// AddDetailedStatistics sums another DetailedStatistics object's metrics into this DetailedStatistics object
func (s *DetailedStatistics) AddDetailedStatistics(other *DetailedStatistics) {
	s.Statistics.AddStatistics(&other.Statistics)
	s.UnusedRangeCount += other.UnusedRangeCount

	if other.UnusedRangeSizeMin < s.UnusedRangeSizeMin {
		s.UnusedRangeSizeMin = other.UnusedRangeSizeMin
	}

	if other.UnusedRangeSizeMax > s.UnusedRangeSizeMax {
		s.UnusedRangeSizeMax = other.UnusedRangeSizeMax
	}

	if other.AllocationSizeMin < s.AllocationSizeMin {
		s.AllocationSizeMin = other.AllocationSizeMin
	}

	if other.AllocationSizeMax > s.AllocationSizeMax {
		s.AllocationSizeMax = other.AllocationSizeMax
	}
}

// PrintJson writes a json object that contains this object's metrics
func (s *DetailedStatistics) PrintJson(writer *jwriter.Writer) {
	objState := writer.Object()
	defer objState.End()

	s.Statistics.printJsonObj(&objState)
	objState.Name("UnusedRangeCount").Int(s.UnusedRangeCount)

	if s.AllocationCount > 1 {
		objState.Name("AllocationSizeMin").Int(s.AllocationSizeMin)
		objState.Name("AllocationSizeMax").Int(s.AllocationSizeMax)
	}
	if s.UnusedRangeCount > 1 {
		objState.Name("UnusedRangeSizeMin").Int(s.UnusedRangeSizeMin)
		objState.Name("UnusedRangeSizeMax").Int(s.UnusedRangeSizeMax)
	}
}
