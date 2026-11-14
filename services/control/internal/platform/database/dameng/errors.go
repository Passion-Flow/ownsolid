package dameng

import (
	"context"
	"database/sql"
	"errors"
)

const (
	Unavailable    = "DAMENG_STORAGE_UNAVAILABLE"
	Cancelled      = "DAMENG_STORAGE_CANCELLED"
	Timeout        = "DAMENG_STORAGE_TIMEOUT"
	NoRows         = "DAMENG_STORAGE_ROW_NOT_FOUND"
	UniqueConflict = "DAMENG_STORAGE_UNIQUE_CONFLICT"
	CheckConflict  = "DAMENG_STORAGE_CHECK_CONFLICT"
	Deadlock       = "DAMENG_STORAGE_DEADLOCK"
	CommitUnknown  = "DAMENG_STORAGE_COMMIT_OUTCOME_UNKNOWN"
)

var ErrCommitOutcomeUnknown = errors.New(CommitUnknown)

// Error carries only classified outcomes and native numeric facts. DM error
// codes are signed 32-bit integers; they must never pass through MySQL's uint16.
type Error struct {
	Kind       string
	SQLState   string
	NativeCode int32
}

func (e *Error) Error() string    { return e.Kind }
func (e *Error) String() string   { return e.Kind }
func (e *Error) GoString() string { return e.Kind }
func (e *Error) Is(target error) bool {
	return e.Kind == CommitUnknown && target == ErrCommitOutcomeUnknown || e.Kind == Cancelled && target == context.Canceled || e.Kind == Timeout && target == context.DeadlineExceeded || e.Kind == NoRows && target == sql.ErrNoRows
}

const maximumErrorNodes = 64

// FailureCode is for command receipts. It never formats arbitrary error text.
func FailureCode(err error) string {
	if err == nil {
		return ""
	}
	switch value := err.(type) {
	case *inputError:
		if value != nil {
			return value.code
		}
	case *ODBCError:
		if value != nil {
			return value.code
		}
	}
	return SafeError(err).(*Error).Kind
}

// SafeError inspects a bounded error graph without formatting vendor diagnostics.
// Unknown COMMIT dominates cancellation and all other recognized outcomes.
func SafeError(err error) error {
	if err == nil {
		return nil
	}
	out := &Error{Kind: Unavailable}
	pending := []error{err}
	visited := 0
	for len(pending) > 0 {
		if visited >= maximumErrorNodes {
			return &Error{Kind: Unavailable}
		}
		visited++
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if current == nil {
			return &Error{Kind: Unavailable}
		}
		switch value := current.(type) {
		case *Error:
			if value == nil {
				return &Error{Kind: Unavailable}
			}
			switch value.Kind {
			case Unavailable, Cancelled, Timeout, NoRows, UniqueConflict, CheckConflict, Deadlock, CommitUnknown:
			default:
				return &Error{Kind: Unavailable}
			}
			if value.Kind == CommitUnknown {
				return &Error{Kind: CommitUnknown, SQLState: value.SQLState, NativeCode: value.NativeCode}
			}
			if out.Kind == Unavailable {
				copy := *value
				out = &copy
			}
		case *ODBCError:
			if value == nil {
				return &Error{Kind: Unavailable}
			}
			if value.commitSent || value.code == "DAMENG_ODBC_COMMIT_UNKNOWN" {
				return &Error{Kind: CommitUnknown, SQLState: value.sqlState, NativeCode: value.nativeCode}
			}
			if out.Kind == Unavailable {
				out.SQLState = value.sqlState
				out.NativeCode = value.nativeCode
				switch {
				case value.sqlState == "40001":
					out.Kind = Deadlock
				case value.sqlState == "HYT00" || value.sqlState == "HYT01":
					out.Kind = Timeout
				case value.sqlState == "23000" && value.nativeCode == -6602:
					out.Kind = UniqueConflict
				case value.sqlState == "23000":
					out.Kind = CheckConflict
				}
			}
		}
		if current == ErrCommitOutcomeUnknown {
			return &Error{Kind: CommitUnknown}
		}
		if out.Kind == Unavailable {
			switch current {
			case context.Canceled:
				out.Kind = Cancelled
			case context.DeadlineExceeded:
				out.Kind = Timeout
			case sql.ErrNoRows:
				out.Kind = NoRows
			}
		}
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			children := joined.Unwrap()
			if len(children) == 0 || len(children) > maximumErrorNodes-visited-len(pending) {
				return &Error{Kind: Unavailable}
			}
			for i := len(children) - 1; i >= 0; i-- {
				pending = append(pending, children[i])
			}
		} else if wrapped, ok := current.(interface{ Unwrap() error }); ok {
			pending = append(pending, wrapped.Unwrap())
		}
	}
	return out
}
