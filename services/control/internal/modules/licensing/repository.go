package licensing

import (
	"context"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

// Both actual stores retain microseconds. Normalize before comparing the fence,
// so rounding a nanosecond wall clock cannot create a false zero-tolerance rollback.
func observeTime(wall, observed time.Time, toleranceSeconds int) (time.Time, bool, error) {
	wall = wall.UTC().Truncate(time.Microsecond)
	observed = observed.UTC().Truncate(time.Microsecond)
	if wall.IsZero() || observed.IsZero() || wall.Year() < 1970 || wall.Year() > 9999 || observed.Year() < 1970 || observed.Year() > 9999 || toleranceSeconds < 0 || toleranceSeconds > wire.MaximumRollbackToleranceSeconds {
		return time.Time{}, false, fail("LICENSE_STORED_STATE_INVALID")
	}
	effective := wall
	if observed.After(effective) {
		effective = observed
	}
	return effective, wall.Before(observed.Add(-time.Duration(toleranceSeconds) * time.Second)), nil
}

func licenseSQL(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, args...)
	default:
		return transaction.SQL(q.Dialect(), "")
	}
}
func licenseHistorySQL(q transaction.Executor) transaction.Statement {
	if q.Dialect() == transaction.Dameng {
		// The bound is a fixed versioned history invariant; all business values bind.
		return transaction.SQL(transaction.Dameng, `SELECT "SEQUENCE","LICENSE_ID","DEPLOYMENT_ID","ARTIFACT_SHA256",CASE WHEN OCTET_LENGTH("ARTIFACT")<=? THEN "ARTIFACT" ELSE NULL END,"PREDECESSOR_ID" FROM "PLATFORM_LICENSE_ARTIFACTS" ORDER BY "SEQUENCE" LIMIT `+strconv.Itoa(wire.MaximumRetainedImports+1), wire.MaximumArtifactBytes)
	}
	return licenseSQL(q, `SELECT sequence,license_id::text,deployment_id::text,artifact_sha256,CASE WHEN octet_length(artifact)<=$1 THEN artifact ELSE NULL END,COALESCE(predecessor_id::text,'') FROM platform_license_artifacts ORDER BY sequence LIMIT $2`, `SELECT sequence,license_id,deployment_id,artifact_sha256,CASE WHEN OCTET_LENGTH(artifact)<=? THEN artifact ELSE NULL END,COALESCE(predecessor_id,'') FROM platform_license_artifacts ORDER BY sequence LIMIT ?`, "", wire.MaximumArtifactBytes, wire.MaximumRetainedImports+1)
}
func licensePredecessor(q transaction.Executor, id string) any {
	if q.Dialect() == transaction.Dameng && id == "" {
		return nil
	}
	return id
}

func repositoryError(e error) error {
	if e == nil {
		return nil
	}
	switch e.Error() {
	case "ACCESS_DENIED", "AUTH_SESSION_EXPIRED", "VALIDATION_FAILED",
		"LICENSE_CONFIGURATION_REQUIRED", "LICENSE_STORED_STATE_INVALID", "LICENSE_ARTIFACT_INVALID",
		"LICENSE_DEPLOYMENT_MISMATCH", "LICENSE_ID_CONFLICT", "LICENSE_CURRENT_CHANGED",
		"LICENSE_IMPORT_CAPACITY_REQUIRED", "LICENSE_PREDECESSOR_REQUIRED",
		"LICENSE_PENDING_SUCCESSOR_EXISTS", "LICENSE_RENEWAL_WINDOW_INVALID",
		string(transaction.Unavailable), string(transaction.Deadlock), string(transaction.LockTimeout),
		string(transaction.Cancelled), string(transaction.Timeout), string(transaction.CommitUnknown), string(transaction.CommitRolledBack):
		return fail(e.Error())
	default:
		return fail(string(transaction.Unavailable))
	}
}

func (s *Service) within(ctx context.Context, operation func(context.Context, transaction.Executor) error) error {
	if s == nil || s.identity == nil || ctx == nil || operation == nil || s.now == nil || s.policy.Validate() != nil || s.operationTimeout <= 0 {
		return fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()
	store, e := s.identity.Repository()
	if e != nil || store == nil || store.Dialect() != transaction.PostgreSQL && store.Dialect() != transaction.MySQL && store.Dialect() != transaction.Dameng {
		return fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	return repositoryError(store.Within(ctx, false, operation))
}
