//go:build debug_mem_utils

package memutils

import "unsafe"

const (
	DebugMargin                   int    = 16
	CorruptionDetectionMagicValue uint32 = 0x7F84E666
)

func WriteMagicValue(data unsafe.Pointer, offset int) {
	dest := unsafe.Add(data, offset)
	marginSize := DebugMargin / int(unsafe.Sizeof(uint32(0)))
	for i := 0; i < marginSize; i++ {
		*(*uint32)(dest) = CorruptionDetectionMagicValue
		dest = unsafe.Add(dest, unsafe.Sizeof(uint32(0)))
	}
}

func ValidateMagicValue(data unsafe.Pointer, offset int) bool {
	source := unsafe.Add(data, offset)
	marginSize := DebugMargin / int(unsafe.Sizeof(uint32(0)))
	for i := 0; i < marginSize; i++ {
		value := (*uint32)(source)
		if *value != CorruptionDetectionMagicValue {
			return false
		}
		source = unsafe.Add(source, unsafe.Sizeof(uint32(0)))
	}

	return true
}

func DebugValidate(validatable Validatable) {
	err := validatable.Validate()
	if err != nil {
		panic(err)
	}
}

func DebugCheckPow2[T Number](value T, name string) {
	err := CheckPow2[T](value, name)
	if err != nil {
		panic(err)
	}
}
