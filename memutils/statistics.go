package memutils

import (
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"math"
)

type Statistics struct {
	BlockCount      int
	AllocationCount int
	BlockBytes      int
	AllocationBytes int
}

func (s *Statistics) Clear() {
	s.BlockCount = 0
	s.AllocationCount = 0
	s.BlockBytes = 0
	s.AllocationBytes = 0
}

func (s *Statistics) AddStatistics(other *Statistics) {
	s.BlockCount += other.BlockCount
	s.AllocationCount += other.AllocationCount
	s.BlockBytes += other.BlockBytes
	s.AllocationBytes += other.AllocationBytes
}

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

type DetailedStatistics struct {
	Statistics
	UnusedRangeCount   int
	AllocationSizeMin  int
	AllocationSizeMax  int
	UnusedRangeSizeMin int
	UnusedRangeSizeMax int
}

func (s *DetailedStatistics) Clear() {
	s.Statistics.Clear()
	s.UnusedRangeCount = 0
	s.AllocationSizeMin = math.MaxInt
	s.AllocationSizeMax = 0
	s.UnusedRangeSizeMin = math.MaxInt
	s.UnusedRangeSizeMax = 0
}

func (s *DetailedStatistics) AddUnusedRange(size int) {
	s.UnusedRangeCount++

	if size < s.UnusedRangeSizeMin {
		s.UnusedRangeSizeMin = size
	}

	if size > s.UnusedRangeSizeMax {
		s.UnusedRangeSizeMax = size
	}
}

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
