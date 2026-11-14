package transaction

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

type ErrorKind string

const (
	Unavailable        ErrorKind = "STORAGE_UNAVAILABLE"
	NoRows             ErrorKind = "STORAGE_ROW_NOT_FOUND"
	UniqueConflict     ErrorKind = "STORAGE_UNIQUE_CONFLICT"
	ForeignKeyConflict ErrorKind = "STORAGE_FOREIGN_KEY_CONFLICT"
	CheckConflict      ErrorKind = "STORAGE_CHECK_CONFLICT"
	Deadlock           ErrorKind = "STORAGE_DEADLOCK"
	LockTimeout        ErrorKind = "STORAGE_LOCK_TIMEOUT"
	Cancelled          ErrorKind = "STORAGE_CANCELLED"
	Timeout            ErrorKind = "STORAGE_TIMEOUT"
	CommitUnknown      ErrorKind = "STORAGE_COMMIT_OUTCOME_UNKNOWN"
	CommitRolledBack   ErrorKind = "STORAGE_COMMIT_ROLLED_BACK"
)

// Error exposes no vendor message, query, arguments or unwrap to a raw cause.
// Repositories classify constraint errors in the context of their owned operation.
type Error struct {
	Kind       ErrorKind
	SQLState   string
	Number     uint16
	NativeCode int32
}

func (e *Error) Error() string { return string(e.Kind) }
func (e *Error) Is(target error) bool {
	return e.Kind == NoRows && (target == sql.ErrNoRows || target == pgx.ErrNoRows) ||
		e.Kind == Cancelled && target == context.Canceled || e.Kind == Timeout && target == context.DeadlineExceeded
}

func normalize(err error) error {
	if err == nil {
		return nil
	}
	// The native DM graph is bounded and checked before ordinary cancellation.
	// Do not truncate negative native codes into the unrelated MySQL Number.
	if dm, ok := dameng.SafeError(err).(*dameng.Error); ok && dm.Kind == dameng.CommitUnknown {
		return &Error{Kind: CommitUnknown, SQLState: dm.SQLState, NativeCode: dm.NativeCode}
	}
	var existing *Error
	if errors.As(err, &existing) {
		return existing
	}
	out := &Error{Kind: Unavailable}
	switch {
	case errors.Is(err, context.Canceled):
		out.Kind = Cancelled
	case errors.Is(err, context.DeadlineExceeded):
		out.Kind = Timeout
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, pgx.ErrNoRows):
		out.Kind = NoRows
	case errors.Is(err, mysqlstore.ErrCommitOutcomeUnknown):
		out.Kind = CommitUnknown
	default:
		var pg *pgconn.PgError
		var my *mysqlstore.Error
		switch {
		case errors.As(err, &pg):
			out.SQLState = pg.Code
			switch pg.Code {
			case "23505":
				out.Kind = UniqueConflict
			case "23503":
				out.Kind = ForeignKeyConflict
			case "23514", "23502":
				out.Kind = CheckConflict
			case "40P01", "40001":
				out.Kind = Deadlock
			case "55P03":
				out.Kind = LockTimeout
			case "57014":
				out.Kind = Timeout
			}
		case errors.As(mysqlstore.SafeError(err), &my):
			out.Number = my.Number
			if my.SQLState != [5]byte{} {
				out.SQLState = string(my.SQLState[:])
			}
			switch my.Number {
			case 1062:
				out.Kind = UniqueConflict
			case 1451, 1452:
				out.Kind = ForeignKeyConflict
			case 3819, 1048:
				out.Kind = CheckConflict
			case 1213:
				out.Kind = Deadlock
			case 1205:
				out.Kind = LockTimeout
			}
		}
	}
	return out
}

func normalizeDameng(err error) error {
	if err == nil {
		return nil
	}
	dm := dameng.SafeError(err).(*dameng.Error)
	out := &Error{Kind: Unavailable, SQLState: dm.SQLState, NativeCode: dm.NativeCode}
	switch dm.Kind {
	case dameng.CommitUnknown:
		out.Kind = CommitUnknown
	case dameng.Cancelled:
		out.Kind = Cancelled
	case dameng.Timeout:
		out.Kind = Timeout
	case dameng.NoRows:
		out.Kind = NoRows
	case dameng.UniqueConflict:
		out.Kind = UniqueConflict
	case dameng.CheckConflict:
		out.Kind = CheckConflict
	case dameng.Deadlock:
		out.Kind = Deadlock
	}
	return out
}
