package memutils

import "github.com/pkg/errors"

// PowerOfTwoError is the error returned from CheckPow2 or other methods if the number being tested is not a power of two
var PowerOfTwoError error = errors.New("number must be a power of two")
