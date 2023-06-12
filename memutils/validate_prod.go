//go:build !debug_mem_utils

package memutils

import "unsafe"

const (
	// DebugMargin is the number of bytes of debug data that should be placed between allocations in blocks managed
	// by memutils
	DebugMargin int = 0
)

// ValidateMagicValue verifies that the easy-to-identify marker written by WriteMagicValue is still present.
// It returns true if the value is still present and false otherwise.
// This method no-ops unless the debug_mem_utils build tag is present.
func ValidateMagicValue(data unsafe.Pointer, offset int) bool {
	return true
}

// WriteMagicValue writes an easy-to-identify marker across DebugMargin bytes at the provided pointer and offset.
// This method no-ops unless the debug_mem_utils build tag is present.
func WriteMagicValue(data unsafe.Pointer, offset int) {
}

// DebugValidate will call Validate on the provided object and panics if any errors are returned. This
// method no-ops unless the debug_mem_utils build tag is present
func DebugValidate(validatable Validatable) {
}

// DebugCheckPow2 will verify that the numerical value passed in is a power of two, and panics if it is not.
// This method no-ops unless the debug_mem_utils build tag is present.
func DebugCheckPow2(value uint, name string) {

}
