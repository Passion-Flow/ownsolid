package database

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// schemaUnavailable is the verdict of a check that could not run. It says nothing about the schema.
const schemaUnavailable = "SCHEMA_UNAVAILABLE"

// unanswered are the codes of a check that never reached a verdict: the database was slow, busy or
// unreachable. A serving process reports not ready for them and keeps running, because a lock held
// for a minute or a database restart says nothing about the schema it verified at startup.
var unanswered = map[string]bool{
	schemaUnavailable: true, "DATABASE_UNAVAILABLE": true, "DATABASE_NETWORK_UNREACHABLE": true, "DATABASE_DNS_UNRESOLVED": true, "DATABASE_CONNECTION_LIMIT_REACHED": true,
}

// Unanswered reports whether a verification failed without reaching a verdict.
func Unanswered(e error) bool { return e != nil && unanswered[e.Error()] }

// queryFailed classifies a failed verification query. A database that did not answer in time or at
// all is unavailable; one that answered with an error, such as a missing relation, gets the check's
// own code, because that is a real difference from the schema this release expects.
func queryFailed(e error, incompatible string) error {
	var failure *transaction.Error
	if errors.As(e, &failure) {
		switch failure.Kind {
		case transaction.LockTimeout, transaction.Timeout, transaction.Deadlock, transaction.Cancelled:
			return errors.New(schemaUnavailable)
		case transaction.Unavailable:
			// No SQLSTATE means no answer from the server; class 08 is a lost connection and 57P a
			// server that is shutting down or restarting.
			if failure.SQLState == "" || strings.HasPrefix(failure.SQLState, "08") || strings.HasPrefix(failure.SQLState, "57P") {
				return errors.New(schemaUnavailable)
			}
		}
	}
	return errors.New(incompatible)
}

// readinessLockMilliseconds is how long a readiness check waits for a lock. A probe runs every few
// seconds; waiting the full lock timeout would keep a pooled connection busy for most of that, and
// that connection belongs to the operator's reserve when agents hold every seat (ADR 0043).
const readinessLockMilliseconds = 250

// CheckRuntimeReady is CheckRuntimeWithin for a readiness probe: the same verdicts, but a lock it
// cannot get at once makes it unanswered rather than a wait. ctx must carry a deadline.
func CheckRuntimeReady(ctx context.Context, store transaction.Store) error {
	if store == nil {
		return errors.New(schemaUnavailable)
	}
	var verdict error
	e := store.Within(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		if _, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT set_config('lock_timeout',$1,true)", strconv.Itoa(readinessLockMilliseconds)+"ms")); e != nil {
			return e
		}
		verdict = CheckRuntimeWithin(ctx, q)
		return nil
	})
	var failure *transaction.Error
	if errors.As(e, &failure) {
		return queryFailed(e, schemaUnavailable)
	}
	if e != nil {
		// A refused transaction, such as one without a deadline, is a fault of the caller.
		return e
	}
	return verdict
}
