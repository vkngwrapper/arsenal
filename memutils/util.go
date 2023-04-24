package memutils

import (
	cerrors "github.com/cockroachdb/errors"
)

type Number interface {
	~int | ~uint
}

func CheckPow2[T Number](number T, name string) error {
	if number&(number-1) != 0 {
		return cerrors.Wrapf(PowerOfTwoError, "%s is %d", name, number)
	}
	return nil
}

func AlignUp(value int, alignment uint) int {
	DebugCheckPow2(alignment, "AlignUp Input")
	return (value + int(alignment) - 1) & int(^(alignment - 1))
}

func AlignDown(value int, alignment uint) int {
	DebugCheckPow2(alignment, "AlignDown Input")
	return value & int(^(alignment - 1))
}
