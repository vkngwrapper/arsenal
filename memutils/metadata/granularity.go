package metadata

type GranularityCheck interface {
	AllocPages(allocType uint32, offset, size int)
	FreePages(offset, size int)
	Clear()
	CheckConflictAndAlignUp(allocOffset, allocSize, blockOffset, blockSize int, allocType uint32) (int, bool)
	RoundUpAllocRequest(allocType uint32, allocSize int, allocAlignment uint) (int, uint)
	AllocationsConflict(firstAllocType uint32, secondAllocType uint32) bool

	StartValidation() any
	Validate(ctx any, offset, size int) error
	FinishValidation(ctx any) error
}
