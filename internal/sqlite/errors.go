package sqlite

import (
	"errors"

	libsqlite "modernc.org/sqlite"
)

// SQLITE_CONSTRAINT_FOREIGNKEY = 787 (FK violation with deferred/NO ACTION)
// SQLITE_CONSTRAINT_TRIGGER   = 1811 (FK violation via ON DELETE RESTRICT, which SQLite
// enforces as an immediate trigger rather than a deferred check)
const (
	sqliteFKConstraint      = 787
	sqliteConstraintTrigger = 1811
)

// isFKConstraintErr reports whether err is a SQLite foreign-key constraint failure.
func isFKConstraintErr(err error) bool {
	var sqlErr *libsqlite.Error
	if !errors.As(err, &sqlErr) {
		return false
	}
	return sqlErr.Code() == sqliteFKConstraint || sqlErr.Code() == sqliteConstraintTrigger
}
