package sso

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"strconv"
	"time"
)

const damengConnectorColumns = "ID,NAME,PROVIDER,ORIGIN,CLIENT_ID,ENABLED,REVISION,AUTH_VERSION,CREATED_AT,UPDATED_AT"
const damengBindingColumns = "B.ID,B.CONNECTOR_ID,C.NAME,C.PROVIDER,B.SUBJECT,B.ACTIVE,B.REVISION,B.CREATED_AT,B.UPDATED_AT"
const damengFlowColumns = "ID,TENANT_ID,CONNECTOR_ID,AUDIENCE,MODE,STATE,CONNECTOR_REVISION,AUTH_VERSION,BROWSER_HASH,PREVIOUS_COOKIE_HASH,PREVIOUS_SESSION_ID,INITIATING_USER_ID,INITIATING_SESSION_ID,INITIATING_EPOCH,CREDENTIAL_VERSION,REASON,CLAIM_ID,EXPIRES_AT"

// Native scalar timestamps use UTC, the supported year range and microseconds.
// Callers must reject lossy input instead of rounding a keyset boundary.
func damengTimestamp(value time.Time) bool {
	_, offset := value.Zone()
	return !value.IsZero() && value.Year() >= 1970 && value.Year() <= 9999 && offset == 0 && value.Nanosecond()%int(time.Microsecond) == 0
}

// sourceBoundedSQL emits only an already bounded integer as native syntax.
// User data remains bound. Native LIMIT and lock-row-count binds are unqualified;
// PostgreSQL/MySQL retain their existing final limit argument.
func sourceBoundedSQL(q transaction.Executor, pg, my, dmPrefix, dmSuffix string, limit, maximum int, args ...any) transaction.Statement {
	if q == nil || maximum < 1 || limit < 1 || limit > maximum {
		return transaction.Statement{}
	}
	if q.Dialect() == transaction.Dameng {
		return transaction.SQL(transaction.Dameng, dmPrefix+strconv.Itoa(limit)+dmSuffix, args...)
	}
	return sourceSQL(q, pg, my, "", append(args, limit)...)
}

// sourceOutcome resolves bounded commit graphs before any context/error-kind
// handling. It grants no retry or compensating-mutation authority.
func sourceOutcome(e error) error {
	if outcome, found := transaction.CommitOutcome(e); found {
		return fail(string(outcome))
	}
	return e
}
