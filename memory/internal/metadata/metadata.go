package metadata

import (
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/vkngwrapper/arsenal/memory"
	"github.com/vkngwrapper/arsenal/memory/internal/utils"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/driver"
	"golang.org/x/exp/slog"
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

	VisitAllBlocks(handleBlock func(handle BlockAllocationHandle, offset int, size int, userData any, free bool))
	AllocationListBegin() (BlockAllocationHandle, error)
	FindNextAllocation(allocHandle BlockAllocationHandle) (BlockAllocationHandle, error)
	FindNextFreeRegionSize(allocHandle BlockAllocationHandle) (int, error)
	AllocationOffset(allocHandle BlockAllocationHandle) (int, error)
	AllocationUserData(allocHandle BlockAllocationHandle) (any, error)
	SetAllocationUserData(allocHandle BlockAllocationHandle, userData any) error

	AddDetailedStatistics(stats *memory.DetailedStatistics)
	AddStatistics(stats *memory.Statistics)

	Clear()
	DebugLogAllAllocations(log *slog.Logger, logFunc func(log *slog.Logger, offset int, size int, userData any))
	PrintDetailedMapHeader(json jwriter.ObjectState) error

	CheckCorruption(blockData unsafe.Pointer) (common.VkResult, error)
	PopulateAllocationRequest(
		allocSize int, allocAlignment uint,
		upperAddress bool,
		allocType SuballocationType,
		strategy memory.AllocationCreateFlags,
		allocRequest *AllocationRequest,
	) (bool, error)
	Alloc(request *AllocationRequest, allocType SuballocationType, userData any) error

	Free(allocHandle BlockAllocationHandle) error
}

type blockMetadataBase struct {
	size                  int
	allocationCallbacks   *driver.AllocationCallbacks
	bufferImageGranlarity int
	isVirtual             bool
}

func newBlockMetadata(bufferImageGranularity int, isVirtual bool) blockMetadataBase {
	return blockMetadataBase{
		size:                  0,
		bufferImageGranlarity: bufferImageGranularity,
		isVirtual:             isVirtual,
	}
}

func (m *blockMetadataBase) Init(size int) {
	m.size = size
}

func (m *blockMetadataBase) IsVirtual() bool {
	return m.isVirtual
}

func (m *blockMetadataBase) Size() int { return m.size }

func (m *blockMetadataBase) printDetailedMap_Header(json jwriter.ObjectState, unusedBytes, allocationCount, unusedRangeCount int) {
	json.Name("TotalBytes").Int(m.Size())
	json.Name("UnusedBytes").Int(unusedBytes)
	json.Name("Allocations").Int(allocationCount)
	json.Name("UnusedRanges").Int(unusedRangeCount)
}

func (m *blockMetadataBase) getDebugMargin() int {
	if m.isVirtual {
		return 0
	}

	return utils.DebugMargin
}