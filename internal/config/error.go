package config

import "fmt"

type Error struct {
	Field string
	Cause error
}

func (e *Error) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("configuration error: %v", e.Cause)
	}
	return fmt.Sprintf("invalid configuration %s: %v", e.Field, e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }

func invalid(field, message string) error {
	return &Error{Field: field, Cause: fmt.Errorf("%s", message)}
}
