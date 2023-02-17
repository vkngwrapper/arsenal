//go:build debug_arsenal_memory

package utils

import "unsafe"

const (
	DebugMargin                   int    = 16
	CorruptionDetectionMagicValue uint32 = 0x7F84E666
)

func WriteMagicValue(data unsafe.Pointer, offset int) {
	dest := unsafe.Add(data, offset)
	marginSize := DebugMargin / unsafe.Sizeof(uint32)
	for i := 0; i < marginSize; i++ {
		*(*uint32)(dest) = CorruptionDetectionMagicValue
	}
}

func ValidateMagicValue(data unsafe.Pointer, offset int) bool {
	source := unsafe.Add(data, offset)
	marginSize := DebugMargin / unsafe.Sizeof(uint32)
	for i := 0; i < marginSize; i++ {
		value := (*uint32)(source)
		if *value != CorruptionDetectionMagicValue {
			return false
		}
		source = unsafe.Add(source, unsafe.Sizeof(uint32))
	}

	return true
}
