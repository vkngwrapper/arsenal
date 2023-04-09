package metadata

import (
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/core/v2/common"
	"github.com/vkngwrapper/core/v2/driver"
	"go.uber.org/zap"
	"unsafe"
)

type BlockMetadata interface {
	Init(size int)
	Destroy()
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

	AddDetailedStatistics(stats *memutils.DetailedStatistics)
	AddStatistics(stats *memutils.Statistics)

	Clear()
	DebugLogAllAllocations(log *zap.Logger, logFunc func(log *zap.Logger, offset int, size int, userData any))
	PrintDetailedMapHeader(json jwriter.ObjectState) error

	CheckCorruption(blockData unsafe.Pointer) (common.VkResult, error)
	PopulateAllocationRequest(
		allocSize int, allocAlignment uint,
		upperAddress bool,
		allocType SuballocationType,
		strategy memutils.AllocationCreateFlags,
		allocRequest *AllocationRequest,
	) (bool, error)
	Alloc(request *AllocationRequest, allocType SuballocationType, userData any) error

	Free(allocHandle BlockAllocationHandle) error
}

type BlockMetadataBase struct {
	size                  int
	allocationCallbacks   *driver.AllocationCallbacks
	bufferImageGranlarity int
	isVirtual             bool
}

func NewBlockMetadata(bufferImageGranularity int, isVirtual bool) BlockMetadataBase {
	return BlockMetadataBase{
		size:                  0,
		bufferImageGranlarity: bufferImageGranularity,
		isVirtual:             isVirtual,
	}
}

func (m *BlockMetadataBase) Init(size int) {
	m.size = size
}

func (m *BlockMetadataBase) IsVirtual() bool {
	return m.isVirtual
}

func (m *BlockMetadataBase) Size() int { return m.size }

func (m *BlockMetadataBase) PrintDetailedMap_Header(json jwriter.ObjectState, unusedBytes, allocationCount, unusedRangeCount int) {
	json.Name("TotalBytes").Int(m.Size())
	json.Name("UnusedBytes").Int(unusedBytes)
	json.Name("Allocations").Int(allocationCount)
	json.Name("UnusedRanges").Int(unusedRangeCount)
}

func (m *BlockMetadataBase) DebugMargin() int {
	if m.isVirtual {
		return 0
	}

	return memutils.DebugMargin
}
