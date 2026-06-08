package errs

import "errors"

var ErrNotFound = errors.New("not found")

// ErrConstraint is returned when a delete is blocked by a foreign-key constraint.
var ErrConstraint = errors.New("constraint violation")
