package memutils

// Validatable is used by the DebugValidate method to allow it to act upon
// all types with a Validate method
type Validatable interface {
	Validate() error
}
