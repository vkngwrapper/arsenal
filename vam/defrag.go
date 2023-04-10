package vam

import (
	"errors"
	"fmt"
	"github.com/dolthub/swiss"
	"github.com/vkngwrapper/arsenal/memutils/defrag"
	"github.com/vkngwrapper/core/v2/common"
	"golang.org/x/exp/slog"
)

type DefragmentationFlags uint32

const (
	DefragmentationFlagAlgorithmFast DefragmentationFlags = 1 << iota
	DefragmentationFlagAlgorithmBalanced
	DefragmentationFlagAlgorithmFull
	DefragmentationFlagAlgorithmExtensive

	DefragmentationFlagAlgorithmMask = DefragmentationFlagAlgorithmFast |
		DefragmentationFlagAlgorithmBalanced |
		DefragmentationFlagAlgorithmFull |
		DefragmentationFlagAlgorithmExtensive
)

var defragmentationFlagsMapping = map[DefragmentationFlags]string{
	DefragmentationFlagAlgorithmFast:      "DefragmentationFlagAlgorithmFast",
	DefragmentationFlagAlgorithmBalanced:  "DefragmentationFlagAlgorithmBalanced",
	DefragmentationFlagAlgorithmFull:      "DefragmentationFlagAlgorithmFull",
	DefragmentationFlagAlgorithmExtensive: "DefragmentationFlagAlgorithmExtensive",
}

func (f DefragmentationFlags) String() string {
	return defragmentationFlagsMapping[f]
}

type DefragmentationInfo struct {
	Flags DefragmentationFlags
	Pool  *Pool

	MaxBytesPerPass       int
	MaxAllocationsPerPass int
}

type DefragmentationContext struct {
	context defrag.MetadataDefragContext[Allocation]

	logger                    *slog.Logger
	poolMemoryBlockList       *memoryBlockList
	allocatorMemoryBlockLists [common.MaxMemoryTypes]*memoryBlockList

	mapTransfers *swiss.Map[*deviceMemoryBlock, int]
}

func (c *DefragmentationContext) init(blockListCount int, o *DefragmentationInfo) {
	c.context.MaxPassBytes = o.MaxBytesPerPass
	c.context.MaxPassAllocations = o.MaxAllocationsPerPass
	c.context.Handler = c.completePassForMove
	c.mapTransfers = swiss.NewMap[*deviceMemoryBlock, int](42)

	algorithm := o.Flags & DefragmentationFlagAlgorithmMask
	switch algorithm {
	case DefragmentationFlagAlgorithmFast:
		c.context.Algorithm = defrag.AlgorithmFast
	case 0, DefragmentationFlagAlgorithmBalanced:
		c.context.Algorithm = defrag.AlgorithmBalanced
	case DefragmentationFlagAlgorithmFull:
		c.context.Algorithm = defrag.AlgorithmFull
	case DefragmentationFlagAlgorithmExtensive:
		c.context.Algorithm = defrag.AlgorithmExtensive
	default:
		panic(fmt.Sprintf("unknown defragmentation algorithm: %s", algorithm.String()))
	}

	c.context.Init(blockListCount)
}

func (c *DefragmentationContext) initForPool(pool *Pool, o *DefragmentationInfo) {
	c.init(1, o)
	c.poolMemoryBlockList = &pool.blockList
	c.logger = pool.logger
	pool.blockList.incrementalSort = false
	pool.blockList.SortByFreeSize()
}

func (c *DefragmentationContext) initForAllocator(allocator *Allocator, o *DefragmentationInfo) {
	c.init(common.MaxMemoryTypes, o)
	c.allocatorMemoryBlockLists = allocator.memoryBlockLists
	c.logger = allocator.logger

	for i := 0; i < common.MaxMemoryTypes; i++ {
		if c.allocatorMemoryBlockLists[i] != nil {
			c.allocatorMemoryBlockLists[i].incrementalSort = false
			c.allocatorMemoryBlockLists[i].SortByFreeSize()
		}
	}
}

func (c *DefragmentationContext) BeginAllocationPass() []defrag.DefragmentationMove[Allocation] {
	c.logger.Debug("DefragmentationContext::BeginAllocationPass")

	if c.poolMemoryBlockList != nil {
		c.context.BlockListCollectMoves(0, c.poolMemoryBlockList)
	} else {
		for i := 0; i < common.MaxMemoryTypes; i++ {
			if c.allocatorMemoryBlockLists[i] != nil {
				c.context.BlockListCollectMoves(i, c.allocatorMemoryBlockLists[i])
			}
		}
	}

	return c.context.Moves()
}

func (c *DefragmentationContext) EndAllocationPass() (bool, error) {
	c.logger.Debug("DefragmentationContext::EndAllocationPass")

	done := true

	var err error
	var allErrors []error
	if c.poolMemoryBlockList != nil {
		done, err = c.completePassForBlockList(0, c.poolMemoryBlockList)
		if err != nil {
			allErrors = append(allErrors, err)
		}
	} else {
		for i := 0; i < common.MaxMemoryTypes; i++ {
			if c.allocatorMemoryBlockLists[i] != nil {
				thisOneDone, err := c.completePassForBlockList(i, c.allocatorMemoryBlockLists[i])
				if err != nil {
					allErrors = append(allErrors, err)
				}
				done = done && thisOneDone
			}
		}
	}

	if len(allErrors) == 1 {
		err = allErrors[0]
	} else if len(allErrors) > 0 {
		err = errors.Join(allErrors...)
	}

	return done, err
}

func (c *DefragmentationContext) Finish(outStats *defrag.DefragmentationStats) {
	c.logger.Debug("DefragmentationContext::Finish")

	if outStats != nil {
		c.context.PopulateStats(outStats)
	}

	if c.poolMemoryBlockList != nil {
		c.poolMemoryBlockList.incrementalSort = true
	} else {
		for i := 0; i < common.MaxMemoryTypes; i++ {
			if c.allocatorMemoryBlockLists[i] != nil {
				c.allocatorMemoryBlockLists[i].incrementalSort = true
			}
		}
	}
}

func (c *DefragmentationContext) completePassForBlockList(stateIndex int, blockList *memoryBlockList) (bool, error) {
	c.mapTransfers.Clear()

	done, err := c.context.BlockListCompletePass(stateIndex, blockList)

	c.mapTransfers.Iter(
		func(block *deviceMemoryBlock, mapCount int) bool {
			_, _, err := block.memory.Map(mapCount, 0, -1, 0)
			if err != nil {
				panic(fmt.Sprintf("unexpected failure when attempting to transfer map references during defrag: %+v", err))
			}

			return false
		})

	return done, err
}

func (c *DefragmentationContext) completePassForMove(move defrag.DefragmentationMove[Allocation]) error {

	switch move.MoveOperation {
	case defrag.DefragmentationMoveCopy:
		mapCount, err := move.SrcAllocation.swapBlockAllocation(move.DstTmpAllocation)
		if err != nil {
			return err
		}

		if mapCount > 0 {
			existingMapCount, ok := c.mapTransfers.Get(move.SrcAllocation.blockData.block)
			if ok {
				c.mapTransfers.Put(move.SrcAllocation.blockData.block, existingMapCount+mapCount)
			} else {
				c.mapTransfers.Put(move.SrcAllocation.blockData.block, mapCount)
			}
		}
	case defrag.DefragmentationMoveDestroy:
		err := move.SrcAllocation.free()
		if err != nil {
			panic(fmt.Sprintf("failed to free source allocation on Destroy move: %+v", err))
		}
	}

	err := move.DstTmpAllocation.free()
	if err != nil {
		panic(fmt.Sprintf("failed to free temporary defrag allocation: %+v", err))
	}

	return nil
}
