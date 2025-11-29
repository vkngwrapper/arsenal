package vam

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGranularityInitEven(t *testing.T) {
	var granularity blockBufferImageGranularity
	granularity.bufferImageGranularity = 1024
	granularity.Init(4096)

	require.Len(t, granularity.regionInfo, 4)
}

func TestGranularityInitExtra(t *testing.T) {
	var granularity blockBufferImageGranularity
	granularity.bufferImageGranularity = 1024
	granularity.Init(4097)

	require.Len(t, granularity.regionInfo, 5)
}

func TestGranularityInitLow(t *testing.T) {
	var granularity blockBufferImageGranularity
	granularity.bufferImageGranularity = 128
	granularity.Init(1024)

	require.Nil(t, granularity.regionInfo)
}

var conflictTestCases = map[string]struct {
	Type1    uint32
	Type2    uint32
	Conflict bool
}{
	"Frees Dont Conflict": {
		Type1:    uint32(SuballocationFree),
		Type2:    uint32(SuballocationFree),
		Conflict: false,
	},
	"Unknowns Conflict": {
		Type1:    uint32(SuballocationUnknown),
		Type2:    uint32(SuballocationUnknown),
		Conflict: true,
	},
	"Frees Dont Conflict With Unknown": {
		Type1:    uint32(SuballocationUnknown),
		Type2:    uint32(SuballocationFree),
		Conflict: false,
	},
	"Buffers Dont Conflict With Linear Image": {
		Type1:    uint32(SuballocationBuffer),
		Type2:    uint32(SuballocationImageLinear),
		Conflict: false,
	},
	"Buffers Conflict With Unknown Image": {
		Type1:    uint32(SuballocationImageUnknown),
		Type2:    uint32(SuballocationBuffer),
		Conflict: true,
	},
	"Unknown Image Conflicts With Optimal Image": {
		Type1:    uint32(SuballocationImageOptimal),
		Type2:    uint32(SuballocationImageUnknown),
		Conflict: true,
	},
	"Free Doesnt Conflict With Buffer": {
		Type1:    uint32(SuballocationBuffer),
		Type2:    uint32(SuballocationFree),
		Conflict: false,
	},
	"Free Doesnt Conflict With Unknown Image": {
		Type1:    uint32(SuballocationFree),
		Type2:    uint32(SuballocationImageUnknown),
		Conflict: false,
	},
	"Linear Image Conflicts With Optimal Image": {
		Type1:    uint32(SuballocationImageOptimal),
		Type2:    uint32(SuballocationImageLinear),
		Conflict: true,
	},
}

func TestGranularityConflict(t *testing.T) {
	for testName, testCase := range conflictTestCases {
		t.Run(testName, func(t *testing.T) {
			var granularity blockBufferImageGranularity
			require.Equal(t, testCase.Conflict, granularity.AllocationsConflict(testCase.Type1, testCase.Type2))
		})
	}
}

var roundUpTestCases = map[string]struct {
	bufferImageGranularity uint
	allocType              uint32
	inputAlignment         uint
	inputSize              int
	outputAlignment        uint
	outputSize             int
}{
	"Optimal Image Align": {
		bufferImageGranularity: 128,
		allocType:              uint32(SuballocationImageOptimal),
		inputAlignment:         8,
		inputSize:              130,
		outputAlignment:        128,
		outputSize:             256,
	},
	"Zero Buffer Image Granularity Dont Align": {
		bufferImageGranularity: 0,
		allocType:              uint32(SuballocationImageOptimal),
		inputAlignment:         8,
		inputSize:              130,
		outputAlignment:        8,
		outputSize:             130,
	},
	"High Buffer Image Granularity Dont Align": {
		bufferImageGranularity: 300,
		allocType:              uint32(SuballocationImageOptimal),
		inputAlignment:         8,
		inputSize:              130,
		outputAlignment:        8,
		outputSize:             130,
	},
	"Buffer Dont Align": {
		bufferImageGranularity: 128,
		allocType:              uint32(SuballocationBuffer),
		inputAlignment:         8,
		inputSize:              130,
		outputAlignment:        8,
		outputSize:             130,
	},
}

func TestGranularityRoundUp(t *testing.T) {
	for testName, testCase := range roundUpTestCases {
		t.Run(testName, func(t *testing.T) {
			var granularity blockBufferImageGranularity
			granularity.bufferImageGranularity = testCase.bufferImageGranularity
			size, alignment := granularity.RoundUpAllocRequest(testCase.allocType, testCase.inputSize, testCase.inputAlignment)
			require.Equal(t, testCase.outputSize, size)
			require.Equal(t, testCase.outputAlignment, alignment)
		})
	}
}

func TestAllocs(t *testing.T) {
	var granularity blockBufferImageGranularity
	granularity.bufferImageGranularity = 1024
	granularity.Init(4096)

	granularity.AllocRegions(uint32(SuballocationBuffer), 0, 256)
	granularity.AllocRegions(uint32(SuballocationBuffer), 512, 1024)

	require.Equal(t, uint16(2), granularity.regionInfo[0].allocCount)
	require.Equal(t, uint16(1), granularity.regionInfo[1].allocCount)
	require.Equal(t, uint16(0), granularity.regionInfo[2].allocCount)
	require.Equal(t, SuballocationBuffer, granularity.regionInfo[0].allocType)
	require.Equal(t, SuballocationBuffer, granularity.regionInfo[1].allocType)
	require.Equal(t, SuballocationFree, granularity.regionInfo[2].allocType)

	granularity.FreeRegions(0, 256)
	require.Equal(t, uint16(1), granularity.regionInfo[0].allocCount)
	require.Equal(t, SuballocationBuffer, granularity.regionInfo[0].allocType)

	granularity.FreeRegions(512, 1024)
	require.Equal(t, uint16(0), granularity.regionInfo[0].allocCount)
	require.Equal(t, uint16(0), granularity.regionInfo[1].allocCount)
	require.Equal(t, SuballocationFree, granularity.regionInfo[0].allocType)
	require.Equal(t, SuballocationFree, granularity.regionInfo[1].allocType)
}

type testAlloc struct {
	suballocType suballocationType
	offset       int
	size         int
}

var checkAndAlignTestCases = map[string]struct {
	allocs       []testAlloc
	suballocType suballocationType
	allocOffset  int
	allocSize    int
	regionOffset int
	regionSize   int
	outputOffset int
	conflict     bool
}{
	"No Allocs Succeeds": {
		suballocType: SuballocationBuffer,
		allocOffset:  0,
		allocSize:    100,
		regionOffset: 0,
		regionSize:   100,
		outputOffset: 0,
		conflict:     false,
	},
	"Existing Conflicting Alloc": {
		allocs: []testAlloc{
			{
				suballocType: SuballocationImageUnknown,
				offset:       100,
				size:         100,
			},
		},
		suballocType: SuballocationBuffer,
		allocOffset:  0,
		allocSize:    100,
		regionOffset: 0,
		regionSize:   100,
		outputOffset: 0,
		conflict:     true,
	},
	"Existing Conflicting Alloc Left Side": {
		allocs: []testAlloc{
			{
				suballocType: SuballocationImageUnknown,
				offset:       0,
				size:         100,
			},
		},
		suballocType: SuballocationBuffer,
		allocOffset:  100,
		allocSize:    100,
		regionOffset: 100,
		regionSize:   100,
		outputOffset: 100,
		conflict:     true,
	},
	"Nudge Alloc Right": {
		allocs: []testAlloc{
			{
				suballocType: SuballocationImageUnknown,
				offset:       0,
				size:         100,
			},
		},
		suballocType: SuballocationBuffer,
		allocOffset:  100,
		allocSize:    100,
		regionOffset: 100,
		regionSize:   2000,
		outputOffset: 1024,
		conflict:     false,
	},
	"Nudge Right Into Other Conflict": {
		allocs: []testAlloc{
			{
				suballocType: SuballocationImageUnknown,
				offset:       0,
				size:         100,
			},
			{
				suballocType: SuballocationImageOptimal,
				offset:       2038,
				size:         100,
			},
		},
		suballocType: SuballocationBuffer,
		allocOffset:  100,
		allocSize:    100,
		regionOffset: 100,
		regionSize:   2000,
		outputOffset: 1024,
		conflict:     true,
	},
	"Nudge Right Into Other Conflict MultiBlock": {
		allocs: []testAlloc{
			{
				suballocType: SuballocationImageUnknown,
				offset:       2560,
				size:         100,
			},
			{
				suballocType: SuballocationImageOptimal,
				offset:       0,
				size:         100,
			},
		},
		suballocType: SuballocationBuffer,
		allocOffset:  100,
		allocSize:    1500,
		regionOffset: 100,
		regionSize:   2360,
		outputOffset: 1024,
		conflict:     true,
	},
	"Buffer and Linear Coexist": {
		allocs: []testAlloc{
			{
				suballocType: SuballocationImageLinear,
				offset:       500,
				size:         100,
			},
		},
		suballocType: SuballocationBuffer,
		allocOffset:  0,
		allocSize:    100,
		regionOffset: 0,
		regionSize:   500,
		outputOffset: 0,
		conflict:     false,
	},
	"Buffer and Linear Coexist Right Side": {
		allocs: []testAlloc{
			{
				suballocType: SuballocationImageLinear,
				offset:       1500,
				size:         100,
			},
		},
		suballocType: SuballocationBuffer,
		allocOffset:  0,
		allocSize:    1500,
		regionOffset: 0,
		regionSize:   1500,
		outputOffset: 0,
		conflict:     false,
	},
	"Buffer Nudged Right Into Linear": {
		allocs: []testAlloc{
			{
				suballocType: SuballocationImageOptimal,
				offset:       0,
				size:         100,
			},
			{
				suballocType: SuballocationImageLinear,
				offset:       1500,
				size:         100,
			},
		},
		suballocType: SuballocationBuffer,
		allocOffset:  100,
		allocSize:    200,
		regionOffset: 100,
		regionSize:   1400,
		outputOffset: 1024,
		conflict:     false,
	},
}

func TestGranularityCheckAndAlign(t *testing.T) {
	for testName, testCase := range checkAndAlignTestCases {
		t.Run(testName, func(t *testing.T) {
			var granularity blockBufferImageGranularity
			granularity.bufferImageGranularity = 1024
			granularity.Init(4096)

			for _, alloc := range testCase.allocs {
				granularity.AllocRegions(uint32(alloc.suballocType), alloc.offset, alloc.size)
			}

			offset, conflict := granularity.CheckConflictAndAlignUp(testCase.allocOffset, testCase.allocSize, testCase.regionOffset, testCase.regionSize, uint32(testCase.suballocType))
			require.Equal(t, testCase.conflict, conflict)

			if !conflict {
				require.Equal(t, testCase.outputOffset, offset)
			}
		})
	}
}

func TestValidation(t *testing.T) {
	var granularity blockBufferImageGranularity
	granularity.bufferImageGranularity = 1024
	granularity.Init(4096)

	granularity.AllocRegions(uint32(SuballocationBuffer), 0, 100)
	granularity.AllocRegions(uint32(SuballocationImageLinear), 500, 100)
	granularity.AllocRegions(uint32(SuballocationImageOptimal), 1024, 500)
	granularity.AllocRegions(uint32(SuballocationUnknown), 2048, 100)

	ctx := granularity.StartValidation()
	err := granularity.Validate(ctx, 0, 100)
	require.NoError(t, err)
	err = granularity.Validate(ctx, 500, 100)
	require.NoError(t, err)
	err = granularity.Validate(ctx, 1024, 500)
	require.NoError(t, err)
	err = granularity.Validate(ctx, 2048, 100)
	require.NoError(t, err)
	err = granularity.FinishValidation(ctx)
	require.NoError(t, err)
}
