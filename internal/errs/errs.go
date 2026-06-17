// Package errs defines shared sentinel errors used across store and service layers.
package errs

import "errors"

// ErrNotFound is returned by store methods when the requested record does not exist.
var ErrNotFound = errors.New("not found")

// ErrConstraint is returned when a delete is blocked by a foreign-key constraint.
var ErrConstraint = errors.New("constraint violation")
