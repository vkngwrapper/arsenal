package memory

import (
	"github.com/cockroachdb/errors"
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/vkngwrapper/arsenal/memory/internal/utils"
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

	var err error
	for alloc := l.allocationListHead; alloc != nil; alloc, err = alloc.nextDedicatedAlloc() {
		if err != nil {
			return err
		}
		actualCount++
	}

	if declaredCount != actualCount {
		return errors.Newf("the listed number of dedicated allocations in the list (%d) does not match the actual number of allocations (%d)", declaredCount, actualCount)
	}

	return nil
}

func (l *dedicatedAllocationList) AddDetailedStatistics(stats *DetailedStatistics) error {
	var err error
	for item := l.allocationListHead; item != nil; item, err = item.nextDedicatedAlloc() {
		if err != nil {
			return err
		}

		size := item.size
		stats.Statistics.BlockCount++
		stats.Statistics.BlockBytes += size
		stats.AddAllocation(size)
	}

	return nil
}

func (l *dedicatedAllocationList) AddStatistics(stats *Statistics) error {
	l.mutex.RLock()
	defer l.mutex.RUnlock()

	allocCount := l.count
	stats.BlockCount += allocCount
	stats.AllocationCount += allocCount

	var err error
	for item := l.allocationListHead; item != nil; item, err = item.nextDedicatedAlloc() {
		if err != nil {
			return err
		}

		size := item.size
		stats.BlockBytes += size
		stats.AllocationCount += size
	}

	return nil
}

func (l *dedicatedAllocationList) BuildStatsString(s *jwriter.ArrayState) error {
	l.mutex.RLock()
	defer l.mutex.RUnlock()

	var err error
	for alloc := l.allocationListHead; alloc != nil; alloc, err = alloc.nextDedicatedAlloc() {
		if err != nil {
			return err
		}

		o := s.Object()
		alloc.PrintParameters(&o)
		o.End()
	}

	return nil
}

func (l *dedicatedAllocationList) IsEmpty() bool {
	l.mutex.RLock()
	defer l.mutex.RUnlock()

	return l.count == 0
}

func (l *dedicatedAllocationList) Register(alloc *Allocation) error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	return l.pushAllocation(alloc)
}

func (l *dedicatedAllocationList) Unregister(alloc *Allocation) error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	return l.removeAllocation(alloc)
}

func (l *dedicatedAllocationList) removeAllocation(alloc *Allocation) error {
	prev, err := alloc.prevDedicatedAlloc()
	if err != nil {
		return err
	}

	next, err := alloc.nextDedicatedAlloc()
	if err != nil {
		return err
	}

	if prev != nil {
		err = prev.setNext(next)
		if err != nil {
			return err
		}
	} else {
		l.allocationListHead = next
	}

	if next != nil {
		err = next.setPrev(prev)
		if err != nil {
			return err
		}
	} else {
		l.allocationListTail = prev
	}

	err = alloc.setNext(nil)
	if err != nil {
		return err
	}

	err = alloc.setPrev(nil)
	if err != nil {
		return err
	}

	l.count--
	return nil
}

func (l *dedicatedAllocationList) pushAllocation(alloc *Allocation) error {
	if l.count == 0 {
		l.allocationListHead = alloc
		l.allocationListTail = alloc
		l.count = 1
	} else {
		err := alloc.setPrev(l.allocationListTail)
		if err != nil {
			return err
		}

		err = l.allocationListTail.setNext(alloc)
		if err != nil {
			return err
		}

		l.allocationListTail = alloc
		l.count++
	}

	return nil
}
