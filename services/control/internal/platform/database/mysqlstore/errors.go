package mysqlstore

import (
	"context"
	"database/sql"
	"errors"

	driver "github.com/go-sql-driver/mysql"
)

var ErrCommitOutcomeUnknown = errors.New("MYSQL_COMMIT_OUTCOME_UNKNOWN")

// Error carries only safe numeric vendor metadata. Raw server text/parameters
// and connection strings are never attached or unwrapped by this boundary.
type Error struct {
	code     string
	Number   uint16
	SQLState [5]byte
}

func (e *Error) Error() string { return e.code }
func (e *Error) Is(target error) bool {
	return target == context.Canceled && e.code == "MYSQL_CANCELLED" || target == context.DeadlineExceeded && e.code == "MYSQL_TIMEOUT" || target == sql.ErrNoRows && e.code == "MYSQL_ROW_NOT_FOUND"
}

func SafeError(err error) error {
	if err == nil {
		return nil
	}
	var existing *Error
	if errors.As(err, &existing) {
		return existing
	}
	out := &Error{code: "MYSQL_OPERATION_FAILED"}
	switch {
	case errors.Is(err, context.Canceled):
		out.code = "MYSQL_CANCELLED"
	case errors.Is(err, context.DeadlineExceeded):
		out.code = "MYSQL_TIMEOUT"
	case errors.Is(err, sql.ErrNoRows):
		out.code = "MYSQL_ROW_NOT_FOUND"
	default:
		var mysql *driver.MySQLError
		if errors.As(err, &mysql) {
			out.Number = mysql.Number
			out.SQLState = mysql.SQLState
			switch mysql.Number {
			case 1062:
				out.code = "MYSQL_UNIQUE_CONFLICT"
			case 1451, 1452:
				out.code = "MYSQL_FOREIGN_KEY_CONFLICT"
			case 3819:
				out.code = "MYSQL_CHECK_CONFLICT"
			case 1213:
				out.code = "MYSQL_DEADLOCK"
			case 1205:
				out.code = "MYSQL_LOCK_TIMEOUT"
			}
		}
	}
	return out
}
