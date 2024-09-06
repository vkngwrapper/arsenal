package metadata

import (
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/driver"
	"golang.org/x/exp/slog"
	"unsafe"
)

type BlockMetadata interface {
	Init(size int)
	Size() int
	SupportsRandomAccess() bool

	Validate() error
	AllocationCount() int
	FreeRegionsCount() int
	SumFreeSize() int
	MayHaveFreeBlock(allocType uint32, size int) bool
	GranularityHandler() GranularityCheck

	IsEmpty() bool

	VisitAllBlocks(handleBlock func(handle BlockAllocationHandle, offset int, size int, userData any, free bool) error) error
	AllocationListBegin() (BlockAllocationHandle, error)
	FindNextAllocation(allocHandle BlockAllocationHandle) (BlockAllocationHandle, error)
	FindNextFreeRegionSize(allocHandle BlockAllocationHandle) (int, error)
	AllocationOffset(allocHandle BlockAllocationHandle) (int, error)
	AllocationUserData(allocHandle BlockAllocationHandle) (any, error)
	SetAllocationUserData(allocHandle BlockAllocationHandle, userData any) error

	AddDetailedStatistics(stats *memutils.DetailedStatistics)
	AddStatistics(stats *memutils.Statistics)

	Clear()
	DebugLogAllAllocations(log *slog.Logger, logFunc func(log *slog.Logger, offset int, size int, userData any))
	PrintDetailedMapHeader(json jwriter.ObjectState)

	CheckCorruption(blockData unsafe.Pointer) (common.VkResult, error)
	CreateAllocationRequest(
		allocSize int, allocAlignment uint,
		upperAddress bool,
		allocType uint32,
		strategy AllocationStrategy,
		maxOffset int,
	) (bool, AllocationRequest, error)
	Alloc(request AllocationRequest, allocType uint32, userData any) error

	Free(allocHandle BlockAllocationHandle) error
}

type BlockMetadataBase struct {
	size                  int
	allocationCallbacks   *driver.AllocationCallbacks
	bufferImageGranlarity int
	granularityHandler    GranularityCheck
}

func NewBlockMetadata(bufferImageGranularity int, granularityHandler GranularityCheck) BlockMetadataBase {
	return BlockMetadataBase{
		size:                  0,
		bufferImageGranlarity: bufferImageGranularity,
		granularityHandler:    granularityHandler,
	}
}

func (m *BlockMetadataBase) Init(size int) {
	m.size = size
}

func (m *BlockMetadataBase) Size() int { return m.size }

func (m *BlockMetadataBase) GranularityHandler() GranularityCheck {
	return m.granularityHandler
}

func (m *BlockMetadataBase) PrintDetailedMap_Header(json jwriter.ObjectState, unusedBytes, allocationCount, unusedRangeCount int) {
	json.Name("TotalBytes").Int(m.Size())
	json.Name("UnusedBytes").Int(unusedBytes)
	json.Name("Allocations").Int(allocationCount)
	json.Name("UnusedRanges").Int(unusedRangeCount)
}
