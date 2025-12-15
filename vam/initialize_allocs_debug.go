//go:build debug_init_allocs

package vam

import (
	"fmt"
	"unsafe"

	"github.com/vkngwrapper/arsenal/vam/internal/vulkan"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/core1_0"
)

const (
	// InitializeAllocs causes all new allocations to be filled with deterministic data.
	// If you are concerned that nondeterministic initailization of memory is causing a bug,
	// you can activate this to help diagnose the issue.  It impacts performance and should
	// generally be left deactivated.
	InitializeAllocs bool = true
)

func (a *Allocation) fillAllocation(pattern uint8) {
	if !InitializeAllocs || !a.IsMappingAllowed() ||
		a.parentAllocator.deviceMemory.MemoryTypeProperties(a.memoryTypeIndex).PropertyFlags&core1_0.MemoryPropertyHostVisible == 0 {
		// Don't fill allocations that can't be filled, or if memory debugging is turned off
		return
	}

	data, _, err := a.Map()
	if err != nil {
		panic(fmt.Sprintf("failed when attempting to map memory during debug pattern fill: %+v", err))
	}

	dataSlice := ([]uint8)(unsafe.Slice((*uint8)(data), a.size))
	for i := 0; i < a.size; i++ {
		dataSlice[i] = pattern
	}
	_, err = a.flushOrInvalidate(0, common.WholeSize, vulkan.CacheOperationFlush)
	if err != nil {
		panic(fmt.Sprintf("failed when attempting to flush host cache during debug pattern fill: %+v", err))
	}

	err = a.Unmap()
	if err != nil {
		panic(fmt.Sprintf("failed when attempting to unmap memory during debug pattern fill: %+v", err))
	}
}
