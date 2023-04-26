package vam

import (
	"github.com/pkg/errors"
	"github.com/vkngwrapper/arsenal/memutils"
	"github.com/vkngwrapper/core/v2/common"
	"golang.org/x/exp/slog"
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

func (p *Pool) SetName(name string) {
	p.logger.Debug("Pool::SetName")

	p.name = name
}

func (p *Pool) SetID(id int) error {
	if p.id != 0 {
		return errors.New("attempted to set id on a vulkan pool that already has one")
	}
	p.id = id
	return nil
}

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

func (p *Pool) CheckCorruption() (common.VkResult, error) {
	p.logger.Debug("Pool::CheckCorruption")
	return p.blockList.CheckCorruption()
}

func (p *Pool) ID() int {
	return p.id
}

func (p *Pool) Name() string {
	p.logger.Debug("Pool::Name")

	return p.name
}
