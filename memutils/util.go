package memutils

import (
	cerrors "github.com/pkg/errors"
)

type Number interface {
	~int | ~uint
}

// CheckPow2 verifies whether the provided number is a power of 2 and returns an error if not- the name
// will be used to identify the non-power-of-two number in the error message for convenience
func CheckPow2[T Number](number T, name string) error {
	if number&(number-1) != 0 {
		return cerrors.Wrapf(PowerOfTwoError, "%s is %d", name, number)
	}
	return nil
}

// AlignUp will increase the provided value until it matches the provided alignment, if necessary,
// and returns the increased value.
func AlignUp(value int, alignment uint) int {
	DebugCheckPow2(alignment, "AlignUp Input")
	return (value + int(alignment) - 1) & int(^(alignment - 1))
}

// AlignDown will reduce the provided value until it matches the provided alignment, if necessary,
// and returns the reduced value.
func AlignDown(value int, alignment uint) int {
	DebugCheckPow2(alignment, "AlignDown Input")
	return value & int(^(alignment - 1))
}
