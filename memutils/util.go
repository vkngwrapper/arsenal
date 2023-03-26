package memutils

import (
	cerrors "github.com/cockroachdb/errors"
	"gopkg.in/errgo.v2/fmt/errors"
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
	return (value + int(alignment) - 1) & int(^(alignment - 1))
}

func AlignDown(value int, alignment uint) int {
	return value & int(^(alignment - 1))
}

func BlocksOnSamePage(resourceOffset1, resourceSize1, resourceOffset2, pagesize int) (bool, error) {
	if resourceOffset1+resourceSize1 > resourceOffset2 {
		return false, errors.Newf("resource 1 must be before resource 2 in memory, but resource1 ends at offset %d and resource 2 is at offset %d", resourceOffset1+resourceSize1, resourceOffset2)
	}
	if resourceSize1 < 1 {
		return false, errors.Newf("resource 1 must have a positive size, but has a size of %d", resourceSize1)
	}
	if pagesize < 1 {
		return false, errors.Newf("the page size must be positive, but is %d", pagesize)
	}

	resource1End := resourceOffset1 + resourceSize1 - 1
	resource1EndPage := resource1End & ^(pagesize - 1)
	resource2StartPage := resourceOffset2 & ^(pagesize - 1)

	return resource1EndPage == resource2StartPage, nil
}
