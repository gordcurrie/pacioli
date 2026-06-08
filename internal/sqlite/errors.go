package sqlite

import (
	"errors"

	libsqlite "modernc.org/sqlite"
)

// SQLITE_CONSTRAINT_FOREIGNKEY = 787 (extended result code for FK violations)
const sqliteFKConstraint = 787

// isFKConstraintErr reports whether err is a SQLite foreign-key constraint failure.
func isFKConstraintErr(err error) bool {
	var sqlErr *libsqlite.Error
	return errors.As(err, &sqlErr) && sqlErr.Code() == sqliteFKConstraint
}
