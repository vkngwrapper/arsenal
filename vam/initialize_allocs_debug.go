//go:build debug_init_allocs

package vam

const (
	// InitializeAllocs causes all new allocations to be filled with deterministic data.
	// If you are concerned that nondeterministic initailization of memory is causing a bug,
	// you can activate this to help diagnose the issue.  It impacts performance and should
	// generally be left deactivated.
	InitializeAllocs bool = true
)
