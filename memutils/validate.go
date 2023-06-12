package memutils

type Validatable interface {
	Validate() error
}
