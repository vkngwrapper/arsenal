package memory

import (
	"github.com/vkngwrapper/core/v2/common"
)

type PoolCreateInfo struct {
	MemoryTypeIndex int
	Flags           PoolCreateFlags

	BlockSize     int
	MinBlockCount int
	MaxBlockCount int

	Priority               float32
	MinAllocationAlignment uint
	MemoryAllocateNext     common.Options
}

type Pool interface {
}
