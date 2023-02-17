package allocator

import (
	"fmt"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
	"sync/atomic"
)

type CurrentBudgetData struct {
	blockCount      [common.MaxMemoryHeaps]uint32
	allocationCount [common.MaxMemoryHeaps]uint32
	blockBytes      [common.MaxMemoryHeaps]uint64
	allocationBytes [common.MaxMemoryHeaps]uint64

	// TODO: Memory budget
}

func (d *CurrentBudgetData) AddAllocation(heapIndex int, allocationSize int) {
	atomic.AddUint64(&d.allocationBytes[heapIndex], uint64(allocationSize))
	atomic.AddUint32(&d.allocationCount[heapIndex], 1)

	//TODO: Memory budget
}

func (d *CurrentBudgetData) RemoveAllocation(heapIndex int, allocationSize int) {
	if d.allocationBytes[heapIndex] < uint64(allocationSize) {
		panic(fmt.Sprintf("allocation bytes budget for heapIndex %d went negative", heapIndex))
	}
	atomic.AddUint64(&d.allocationBytes[heapIndex], uint64(-allocationSize))
	if d.allocationCount[heapIndex] == 0 {
		panic(fmt.Sprintf("allocation count budget for heapIndex %d went negative", heapIndex))
	}

	atomic.AddUint32(&d.allocationCount[heapIndex], -1)

	//Todo: Memory budget
}

func (d *CurrentBudgetData) AddBlockAllocation(heapIndex int, allocationSize int) {
	atomic.AddUint64(&d.blockBytes[heapIndex], uint64(allocationSize))
	atomic.AddUint32(&d.blockCount[heapIndex], 1)
}

func (d *CurrentBudgetData) AddBlockAllocationWithBudget(heapIndex, allocationSize, maxAllocatable int) (common.VkResult, error) {
	for {
		currentVal := atomic.LoadUint64(&d.blockBytes[heapIndex])
		targetVal := currentVal + uint64(allocationSize)

		if targetVal > uint64(maxAllocatable) {
			return core1_0.VKErrorOutOfDeviceMemory, core1_0.VKErrorOutOfDeviceMemory.ToError()
		}

		if atomic.CompareAndSwapUint64(&d.blockBytes[heapIndex], currentVal, targetVal) {
			break
		}
	}

	atomic.AddUint32(&d.blockCount[heapIndex], 1)
	return core1_0.VKSuccess, nil
}

func (d *CurrentBudgetData) RemoveBlockAllocation(heapIndex, allocationSize int) {
	if d.blockBytes[heapIndex] < uint64(allocationSize) {
		panic(fmt.Sprintf("block bytes budget for heapIndex %d went negative", heapIndex))
	}
	atomic.AddUint64(&d.blockBytes[heapIndex], uint64(-allocationSize))
	if d.blockCount[heapIndex] == 0 {
		panic(fmt.Sprintf("block count budget for heapIndex %d went negative", heapIndex))
	}

	atomic.AddUint32(&d.blockCount[heapIndex], -1)
}
