package vam

import (
	"log/slog"

	"github.com/pkg/errors"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/core/v2/common"
)

// PoolCreateInfo is an options structure that is used to specify the behavior for a new
// custom Pool object.
type PoolCreateInfo struct {
	// MemoryTypeIndex indicates the memory type to be used by the new Pool. A Pool can only
	// use a single memory type, unlike Allocator objects, which will select the correct one
	// to use during an allocation.
	MemoryTypeIndex int
	// Flags indicates optional behaviors to use.
	Flags PoolCreateFlags

	// BlockSize indicates a static size to use for each block in a custom Pool. If 0, block sizes
	// will be chosen based on the same heuristics used when creating new memory blocks for an Allocator,
	// which is usually the right decision.
	BlockSize int
	// MinBlockCount indicates the minimum number of memory blocks that will remain in this Pool at all times.
	// This many blocks will be pre-allocated upon Pool creation and freeing Allocation objects will not
	// cause the number of blocks to drop below this level.
	MinBlockCount int
	// MaxBlockCount indicates the maximum number of memory blocks that will be in this Pool at a time. If
	// an allocation would require the number of blocks to increase beyond this amount, the allocation will
	// fail with VKErrorOutOfDeviceMemory. 0 indicates no maximum.
	MaxBlockCount int

	// Priority is the ext_memory_priority priority value used for memory blocks created with this Pool
	Priority float32
	// MinAllocationAlignment is the alignment that memory allocated to this Pool will use if it is greater
	// than the alignment that would otherwise be chosen for an Allocation
	MinAllocationAlignment uint
	// MemoryAllocateNext is an option stack that will be added to the end of allocation operations for this
	// Pool
	MemoryAllocateNext common.Options
}

// Pool is a structure that can be created to allow allocations that have unusual properties compared to the
// default behavior of allocations made via the Allocator. There are many features that can be unliked
// using Pool objects, but it is almost always better, more performant, and more straightforward to use
// the Allocator instead. Only use a Pool if there is no way you can do without them.
//
// Some possibilities:
//
// * Perform some allocations with the PoolCreateLinearAlgorithm in order to create an arena, stack, or
// queue of quickly allocated-and-deallocated tiny allocations without having to worry about defragmentation.
// * Have a managed pool of memory that is allocated differently from ordinary memory using PoolCreateInfo.MemoryAllocateNext
// * Have a pool of memory that is allocated to blocks but has a different ext_memory_priority priority than Allocator
// memory.
type Pool struct {
	logger               *slog.Logger
	blockList            memoryBlockList
	dedicatedAllocations dedicatedAllocationList
	parentAllocator      *Allocator

	id   int
	name string
	prev *Pool
	next *Pool
}

// SetName applies a name to the Pool that can be used to identify it while debugging or for other
// diagnostic purposes
func (p *Pool) SetName(name string) {
	p.logger.Debug("Pool::SetName")

	p.name = name
}

func (p *Pool) setID(id int) error {
	if p.id != 0 {
		return errors.New("attempted to set id on a vulkan pool that already has one")
	}
	p.id = id
	return nil
}

// Destroy annihilates this Pool. It should be called before calling Allocator.Destroy. It will
// if any Allocation objects have not yet been freed, so it's a reasonably good way to locate memory leaks.
func (p *Pool) Destroy() error {
	p.logger.Debug("Pool::Destroy")

	p.parentAllocator.poolsMutex.Lock()
	defer p.parentAllocator.poolsMutex.Unlock()

	return p.destroyAfterLock()
}

func (p *Pool) destroyAfterLock() error {
	memutils.DebugValidate(&p.dedicatedAllocations)
	if p.dedicatedAllocations.count > 0 {
		return errors.Errorf("the pool still has %d dedicated allocations that remain unfreed", p.dedicatedAllocations.count)
	}

	err := p.blockList.Destroy()
	if err != nil {
		return err
	}

	next := p.next
	if p.next != nil {
		p.next.prev = p.prev
	}
	if p.prev != nil {
		p.prev.next = next
	}

	if p.parentAllocator.pools == p {
		p.parentAllocator.pools = next
	}

	return nil
}

// CheckCorruption verifies the internal consistency and integrity of this pool's memory.
// This will return VKErrorFeatureNotPresent if the pool's memory type does not have corruption
// check active, or VKSuccess if no corruption was found. All other errors indicate some form of corruption.
//
// For corruption detection to be active, the memory type has to support memory mapping, and the binary needs
// to have been built with the debug_mem_utils build tag
func (p *Pool) CheckCorruption() (common.VkResult, error) {
	p.logger.Debug("Pool::CheckCorruption")
	return p.blockList.CheckCorruption()
}

// ID returns an integer id that is unique to this pool across the life of your application. It can be used
// for debugging or other purposes.
func (p *Pool) ID() int {
	return p.id
}

// Name returns a string name that was previously set with SetName
func (p *Pool) Name() string {
	p.logger.Debug("Pool::Name")

	return p.name
}
