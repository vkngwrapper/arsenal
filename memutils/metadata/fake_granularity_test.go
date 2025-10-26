package metadata

// A granularity check that always succeeds
type FakeGranularityCheck struct{}

func (c FakeGranularityCheck) AllocRegions(allocType uint32, offset, size int) {}
func (c FakeGranularityCheck) FreeRegions(offset, size int)                    {}
func (c FakeGranularityCheck) Clear()                                          {}
func (c FakeGranularityCheck) CheckConflictAndAlignUp(allocOffset, allocSize, regionOffset, regionSize int, allocType uint32) (int, bool) {
	return allocOffset, true
}
func (c FakeGranularityCheck) RoundUpAllocRequest(allocType uint32, allocSize int, allocAlignment uint) (int, uint) {
	return allocSize, allocAlignment
}
func (c FakeGranularityCheck) AllocationsConflict(firstAllocType uint32, secondAllocType uint32) bool {
	return false
}
func (c FakeGranularityCheck) StartValidation() any {
	return nil
}
func (c FakeGranularityCheck) Validate(ctx any, offset, size int) error {
	return nil
}
func (c FakeGranularityCheck) FinishValidation(ctx any) error {
	return nil
}
