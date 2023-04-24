//go:build !debug_mem_utils

package memutils

import "unsafe"

const (
	DebugMargin int = 0
)

func ValidateMagicValue(data unsafe.Pointer, offset int) bool {
	return true
}

func WriteMagicValue(data unsafe.Pointer, offset int) {
}

func DebugValidate(validatable Validatable) {
}

func DebugCheckPow2(value uint, name string) {

}
