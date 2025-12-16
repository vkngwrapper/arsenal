package vam

import (
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/pkg/errors"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/arsenal/vam/internal/utils"
)

type dedicatedAllocationList struct {
	mutex utils.OptionalRWMutex

	count              int
	allocationListHead *Allocation
	allocationListTail *Allocation
}

func (l *dedicatedAllocationList) Init(useMutex bool) {
	l.mutex = utils.OptionalRWMutex{UseMutex: useMutex}
}

func (l *dedicatedAllocationList) Validate() error {
	declaredCount := l.count
	actualCount := 0

	l.mutex.RLock()
	defer l.mutex.RUnlock()

	for alloc := l.allocationListHead; alloc != nil; alloc = alloc.nextDedicatedAlloc() {
		actualCount++
	}

	if declaredCount != actualCount {
		return errors.Errorf("the listed number of dedicated allocations in the list (%d) does not match the actual number of allocations (%d)", declaredCount, actualCount)
	}

	return nil
}

func (l *dedicatedAllocationList) AddDetailedStatistics(stats *memutils.DetailedStatistics) {
	for item := l.allocationListHead; item != nil; item = item.nextDedicatedAlloc() {
		size := item.size
		stats.Statistics.BlockCount++
		stats.Statistics.BlockBytes += size
		stats.AddAllocation(size)
	}
}

func (l *dedicatedAllocationList) BuildStatsString(writer *jwriter.Writer) {
	l.mutex.RLock()
	defer l.mutex.RUnlock()

	s := writer.Array()
	defer s.End()

	for alloc := l.allocationListHead; alloc != nil; alloc = alloc.nextDedicatedAlloc() {

		o := s.Object()
		alloc.printParameters(&o)
		o.End()
	}
}

func (l *dedicatedAllocationList) IsEmpty() bool {
	l.mutex.RLock()
	defer l.mutex.RUnlock()

	return l.count == 0
}

func (l *dedicatedAllocationList) Register(alloc *Allocation) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	l.pushAllocation(alloc)
}

func (l *dedicatedAllocationList) Unregister(alloc *Allocation) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	l.removeAllocation(alloc)
}

func (l *dedicatedAllocationList) removeAllocation(alloc *Allocation) {
	prev := alloc.prevDedicatedAlloc()
	next := alloc.nextDedicatedAlloc()

	if prev != nil {
		prev.setNext(next)
	} else {
		l.allocationListHead = next
	}

	if next != nil {
		next.setPrev(prev)
	} else {
		l.allocationListTail = prev
	}

	alloc.setNext(nil)
	alloc.setPrev(nil)

	l.count--
}

func (l *dedicatedAllocationList) pushAllocation(alloc *Allocation) {
	if l.count == 0 {
		l.allocationListHead = alloc
		l.allocationListTail = alloc
		l.count = 1
	} else {
		alloc.setPrev(l.allocationListTail)
		l.allocationListTail.setNext(alloc)

		l.allocationListTail = alloc
		l.count++
	}
}
