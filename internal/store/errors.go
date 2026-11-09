package store

import "fmt"

// DuplicateError indicates a uniqueness conflict surfaced by a friendly name.
type DuplicateError struct {
	Kind string
	Name string
}

func (e *DuplicateError) Error() string {
	if e.Name == "" {
		return fmt.Sprintf("%s already exists", e.Kind)
	}
	return fmt.Sprintf("%s %q already exists", e.Kind, e.Name)
}

// ErrDuplicate creates a duplicate error.
func ErrDuplicate(kind, name string) error {
	return &DuplicateError{Kind: kind, Name: name}
}
