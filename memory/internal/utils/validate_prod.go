//go:build !debug_arsenal_memory

package utils

import "unsafe"

const (
	DebugMargin int = 0
)

func ValidateMagicValue(data unsafe.Pointer, offset int) bool {
	return true
}

func WriteMagicValue(data unsafe.Pointer, offset int) {
}
