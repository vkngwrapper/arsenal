package vam

import "github.com/vkngwrapper/core/v2/common"

type PoolCreateFlags int32

var poolCreateFlagsMapping = common.NewFlagStringMapping[PoolCreateFlags]()

func (f PoolCreateFlags) Register(str string) {
	poolCreateFlagsMapping.Register(f, str)
}
func (f PoolCreateFlags) String() string {
	return poolCreateFlagsMapping.FlagsToString(f)
}

const (
	// PoolCreateIgnoreBufferImageGranularity indicates to the memory pool that you always allocate
	// only buffers and linear images or only optimal images out of this pool, so Buffer/Image Granularity
	// can be ignored.
	//
	// Some allocator methods (CreateBuffer, CreateImage, AllocateMemoryForBuffer) have enough info to
	// set buffer/image granularity precisely, so this flag is unnecessarily.  However, some methods
	// (AllocateMemoryForImage and AllocateMemory) may defensively allocate memory subptimally since
	// they do not have this information. You can set this flag to notify images that you only
	// intend to allocate buffers, linear images, and optimal images so that these defensiv emeasures
	// are unnecessary. Allocations will be faster and more optimal
	PoolCreateIgnoreBufferImageGranularity PoolCreateFlags = 1 << iota
	// PoolCreateLinearAlgorithm enables alternative, linear allocation algorithm in this pool.
	// The algorithm always creates new allocations after the last one and doesn't reuse space from
	// allocations freed in between. It trades memory consumption for a simplifies algorithm and data
	// structure, which has better performance and uses less memory for metadata. This flag can be used
	// to achieve the behavior of free-at-once, stack, ring buffer, and double stack.
	PoolCreateLinearAlgorithm

	PoolCreateAlgorithmMask = PoolCreateLinearAlgorithm
)

func init() {
	PoolCreateIgnoreBufferImageGranularity.Register("PoolCreateIgnoreBufferImageGranularity")
	PoolCreateLinearAlgorithm.Register("PoolCreateLinearAlgorithm")
}
