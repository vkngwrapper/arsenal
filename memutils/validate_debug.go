//go:build debug_mem_utils

package memutils

import "unsafe"

const (
	// DebugMargin is the number of bytes of debug data that should be placed between allocations in blocks managed
	// by memutils
	DebugMargin int = 16
	// corruptionDetectionMagicValue is a 4-byte pattern that should be copied into debug data placed between
	// allocations in blocks managed by memutils
	corruptionDetectionMagicValue uint32 = 0x7F84E666
)

// WriteMagicValue writes an easy-to-identify marker across DebugMargin bytes at the provided pointer and offset.
// This method no-ops unless the debug_mem_utils build tag is present.
func WriteMagicValue(data unsafe.Pointer, offset int) {
	dest := unsafe.Add(data, offset)
	marginSize := DebugMargin / int(unsafe.Sizeof(uint32(0)))
	for i := 0; i < marginSize; i++ {
		*(*uint32)(dest) = corruptionDetectionMagicValue
		dest = unsafe.Add(dest, unsafe.Sizeof(uint32(0)))
	}
}

// ValidateMagicValue verifies that the easy-to-identify marker written by WriteMagicValue is still present.
// It returns true if the value is still present and false otherwise.
// This method no-ops unless the debug_mem_utils build tag is present.
func ValidateMagicValue(data unsafe.Pointer, offset int) bool {
	source := unsafe.Add(data, offset)
	marginSize := DebugMargin / int(unsafe.Sizeof(uint32(0)))
	for i := 0; i < marginSize; i++ {
		value := (*uint32)(source)
		if *value != corruptionDetectionMagicValue {
			return false
		}
		source = unsafe.Add(source, unsafe.Sizeof(uint32(0)))
	}

	return true
}

// DebugValidate will call Validate on the provided object and panics if any errors are returned. This
// method no-ops unless the debug_mem_utils build tag is present
func DebugValidate(validatable Validatable) {
	err := validatable.Validate()
	if err != nil {
		panic(err)
	}
}

// DebugCheckPow2 will verify that the numerical value passed in is a power of two, and panics if it is not.
// This method no-ops unless the debug_mem_utils build tag is present.
func DebugCheckPow2[T Number](value T, name string) {
	err := CheckPow2[T](value, name)
	if err != nil {
		panic(err)
	}
}
