package metadata

// AllocationStrategy exposes several options for choosing the location of a new memory allocation.
// You can choose several and memutils will select one of them based on its own preferences. If none is
// chosen, a balanced strategy will be used.
type AllocationStrategy uint32

const (
	// AllocationStrategyMinMemory selects the allocation strategy that chooses the smallest-possible
	// free range for the allocation to minimize memory usage and fragmentation, possibly at the expense of
	// allocation time
	AllocationStrategyMinMemory AllocationStrategy = 1 << iota
	// AllocationStrategyMinTime selects the allocation strategy that chooses the first suitable free
	// range for the allocation- not necessarily in terms of the smallest offset, but the one that is easiest
	// and fastest to find to minimize allocation time, possibly at the expense of allocation quality.
	AllocationStrategyMinTime
	// AllocationStrategyMinOffset selects the allocation strategy that chooses the lowest offset in
	// available space. This is not the most efficient strategy, but achieves highly packed data. Used internally
	// by defragmentation, not recommended in typical usage.
	AllocationStrategyMinOffset
)
