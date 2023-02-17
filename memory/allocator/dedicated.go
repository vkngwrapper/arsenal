package allocator

import (
	"github.com/vkngwrapper/core/v2/core1_1"
	"sync"
)

type DedicationAllocationFuncs interface {
	BufferMemoryRequirements2(o core1_1.BufferMemoryRequirementsInfo2, out *core1_1.MemoryRequirements2) error
}

type DedicatedAllocation struct {
	pool
}

type dedicatedAllocationList struct {
	useMutex bool
	mutex    sync.Mutex
	allocationList
}
