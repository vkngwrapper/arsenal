package metadata

import (
	"fmt"
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/vkngwrapper/arsenal/memory"
	"github.com/vkngwrapper/arsenal/memory/allocation"
	"github.com/vkngwrapper/arsenal/memory/internal/utils"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/driver"
	"log"
	"unsafe"
)

type BlockMetadata interface {
	Init(size int)
	IsVirtual() bool
	Size() int

	Validate() error
	AllocationCount() int
	FreeRegionsCount() int
	SumFreeSize() int

	IsEmpty() bool

	AllocationListBegin() (allocation.BlockAllocationHandle, error)
	FindNextAllocation(allocHandle allocation.BlockAllocationHandle) (allocation.BlockAllocationHandle, error)
	FindNextFreeRegionSize(allocHandle allocation.BlockAllocationHandle) (int, error)
	AllocationOffset(allocHandle allocation.BlockAllocationHandle) (int, error)
	PopulateAllocationInfo(allocHandle allocation.BlockAllocationHandle, info *allocation.VirtualAllocationInfo) error
	AllocationUserData(allocHandle allocation.BlockAllocationHandle) (any, error)
	SetAllocationUserData(allocHandle allocation.BlockAllocationHandle, userData any) error

	AddDetailedStatistics(stats *memory.DetailedStatistics)
	AddStatistics(stats *memory.Statistics)

	Clear()
	DebugLogAllAllocations(log *log.Logger)
	PrintDetailedMap(json *jwriter.ObjectState) error

	CheckCorruption(blockData unsafe.Pointer) (common.VkResult, error)
	PopulateAllocationRequest(
		allocSize int, allocAlignment uint,
		upperAddress bool,
		allocType allocation.SuballocationType,
		strategy allocation.AllocationCreateFlags,
		allocRequest *allocation.AllocationRequest,
	) (bool, error)
	Alloc(request *allocation.AllocationRequest, allocType allocation.SuballocationType, userData any) error

	Free(allocHandle allocation.BlockAllocationHandle) error
}

type blockMetadataBase struct {
	size                  int
	allocationCallbacks   *driver.AllocationCallbacks
	bufferImageGranlarity int
	isVirtual             bool
}

func newBlockMetadata(allocationCallbacks *driver.AllocationCallbacks, bufferImageGranularity int, isVirtual bool) blockMetadataBase {
	return blockMetadataBase{
		size:                  0,
		bufferImageGranlarity: bufferImageGranularity,
		isVirtual:             isVirtual,
		allocationCallbacks:   allocationCallbacks,
	}
}

func (m *blockMetadataBase) Init(size int) {
	m.size = size
}

func (m *blockMetadataBase) IsVirtual() bool {
	return m.isVirtual
}

func (m *blockMetadataBase) Size() int { return m.size }

func (m *blockMetadataBase) debugLogAllocation(logger *log.Logger, offset, size int, userData any) {
	if m.isVirtual {
		logger.Print("unfreed virtual allocation offset=%d size=%d userData=%+v", offset, size, userData)
		return
	}

	allocation := userData.(allocation.Allocation)
	userData = allocation.UserData()
	name := allocation.Name()
	if name == "" {
		name = "empty"
	}

	logger.Print("unfreed allocation offset=%d size=%d userData=+v name=%s type=%s",
		offset, size, userData, name, allocation.SuballocationType(),
	)
}

func (m *blockMetadataBase) printDetailedMap_Header(json *jwriter.ObjectState, unusedBytes, allocationCount, unusedRangeCount int) {
	json.Name("TotalBytes").Int(m.Size())
	json.Name("UnusedBytes").Int(unusedBytes)
	json.Name("Allocations").Int(allocationCount)
	json.Name("UnusedRanges").Int(unusedRangeCount)
}

func (m *blockMetadataBase) printDetailedMap_UnusedRange(json *jwriter.ArrayState, offset, size int) {
	obj := json.Object()
	defer obj.End()

	obj.Name("Offset").Int(offset)
	obj.Name("Type").String(allocation.SuballocationFree.String())
	obj.Name("Size").Int(size)
}

func (m *blockMetadataBase) printDetailedMap_Allocation(json *jwriter.ArrayState, offset, size int, userData any) {
	obj := json.Object()
	defer obj.End()

	obj.Name("Offset").Int(offset)

	if m.isVirtual {
		obj.Name("Size").Int(size)
	}

	var alloc allocation.Allocation
	var isAllocation bool
	if !m.isVirtual && userData != nil {
		alloc, isAllocation = userData.(allocation.Allocation)
	}

	if isAllocation {
		alloc.PrintParameters(&obj)
	} else if userData != nil {
		obj.Name("CustomData").String(fmt.Sprintf("%+v", userData))
	}
}

func (m *blockMetadataBase) getDebugMargin() int {
	if m.isVirtual {
		return 0
	}

	return utils.DebugMargin
}
