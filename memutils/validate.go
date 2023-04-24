package memutils

const (
	CreatedFillPattern   uint8 = 0xDC
	DestroyedFillPattern uint8 = 0xEF
)

type Validatable interface {
	Validate() error
}
