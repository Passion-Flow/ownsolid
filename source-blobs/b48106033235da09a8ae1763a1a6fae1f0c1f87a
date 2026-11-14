package scheduling

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

var (
	// ErrReservationTransaction requires a borrowed PostgreSQL READ COMMITTED transaction.
	ErrReservationTransaction = errors.New("SCHEDULING_RESERVATION_TRANSACTION_REQUIRED")
	// ErrInvalidAttempt indicates incomplete or inconsistent owner identity.
	ErrInvalidAttempt = errors.New("SCHEDULING_ATTEMPT_INVALID")
	// ErrQuotaUnconfigured indicates that the platform has no allocation policy.
	ErrQuotaUnconfigured = errors.New("SCHEDULING_QUOTA_UNCONFIGURED")
	// ErrReservationQuota indicates insufficient workspace capacity for the whole demand.
	ErrReservationQuota = errors.New("SCHEDULING_RESERVATION_QUOTA_EXCEEDED")
	// ErrReservationConflict indicates a changed replay or another held attempt for the job.
	ErrReservationConflict = errors.New("SCHEDULING_RESERVATION_CONFLICT")
	// ErrReservationReleased prevents reusing a stopped attempt for a new dispatch.
	ErrReservationReleased = errors.New("SCHEDULING_RESERVATION_RELEASED")
	// ErrReservationNotFound indicates no hold or release for the exact scoped attempt.
	ErrReservationNotFound = errors.New("SCHEDULING_RESERVATION_NOT_FOUND")
)

// Attempt identifies an exact execution from the owner's authenticated scope.
type Attempt struct {
	TenantID    string
	WorkspaceID string
	ID          string
	Number      int
	Kind        Kind
	SourceID    string
}

func (a Attempt) valid() bool {
	for _, id := range []string{a.TenantID, a.WorkspaceID, a.ID} {
		if !canonicalReservationID(id) {
			return false
		}
	}
	return a.Number > 0 && a.Number <= math.MaxInt32 && validJobIdentity(a.Kind, a.SourceID)
}

func canonicalReservationID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func (a Attempt) scopeArgs() []any {
	return []any{a.TenantID, a.WorkspaceID, a.Kind, a.SourceID, a.Number}
}

func requireReservationTransaction(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL {
		return ErrReservationTransaction
	}
	if _, pool := q.(transaction.Store); pool {
		return ErrReservationTransaction
	}
	var isolation string
	if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SHOW transaction_isolation")).Scan(&isolation); err != nil {
		return err
	}
	if isolation != "read committed" {
		return ErrReservationTransaction
	}
	return nil
}

// lockReservationScope orders locks policy first, then workspace. Advisory hash
// collisions serialize unrelated scopes and cannot grant extra capacity.
func lockReservationScope(ctx context.Context, q transaction.Executor, a Attempt) (int64, error) {
	if err := requireReservationTransaction(ctx, q); err != nil {
		return 0, err
	}
	var revision int64
	if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		"SELECT revision FROM scheduling_quota_policy WHERE singleton FOR SHARE")).Scan(&revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrQuotaUnconfigured
		}
		return 0, err
	}
	const workspaceLockNamespace = 713090003
	_, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL,
		"SELECT pg_advisory_xact_lock($1,hashtext($2))", workspaceLockNamespace, a.TenantID+"/"+a.WorkspaceID))
	return revision, err
}

func reservationPayload(a Attempt, intent JobIntent) ([]byte, Resources, error) {
	if !a.valid() || a.Kind != intent.Kind || a.SourceID != intent.SourceID {
		return nil, nil, ErrInvalidAttempt
	}
	normalized, demand, err := intent.Normalize()
	if err != nil {
		return nil, nil, err
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return nil, nil, ErrInvalidJob
	}
	return body, demand, nil
}

// ReserveWithin takes all requested resources in the owner's existing
// transaction. False with no error means an exact replay of a held attempt.
// The caller must roll back on error and publish no result until commit.
func ReserveWithin(ctx context.Context, q transaction.Executor, a Attempt, intent JobIntent) (bool, error) {
	body, demand, err := reservationPayload(a, intent)
	if err != nil {
		return false, err
	}
	requested, err := json.Marshal(demand)
	if err != nil {
		return false, ErrInvalidResources
	}
	revision, err := lockReservationScope(ctx, q, a)
	if err != nil {
		return false, err
	}

	args := append(a.scopeArgs(), a.ID, body, requested)
	var same, released bool
	err = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT attempt_id=$6 AND intent=$7::jsonb AND demand=$8::jsonb,released_at IS NOT NULL
 FROM scheduling_reservations WHERE tenant_id=$1 AND workspace_id=$2 AND kind=$3 AND source_id=$4 AND attempt_number=$5`,
		args...)).Scan(&same, &released)
	switch {
	case err == nil && !same:
		return false, ErrReservationConflict
	case err == nil && released:
		return false, ErrReservationReleased
	case err == nil:
		return false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}

	_, tree, err := readQuotaPolicyWithin(ctx, q)
	if err != nil {
		return false, err
	}
	if tree == nil {
		return false, ErrQuotaUnconfigured
	}
	workspace, err := tree.workspace(a.TenantID, a.WorkspaceID)
	if err != nil {
		return false, err
	}
	// The quota tree guarantees every workspace's ceiling fits its ancestors,
	// and policy changes cannot strand held usage outside the new workspace limits.
	if err := checkWorkspaceCapacity(ctx, q, a, workspace.Limits.Burst, demand); err != nil {
		return false, err
	}
	loans, reclaimed, err := borrowingForReservation(ctx, q, a, tree, workspace, body, demand)
	if err != nil {
		return false, err
	}
	if err := requireDurableReservationCommit(ctx, q); err != nil {
		return false, err
	}
	inserted, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL,
		`INSERT INTO scheduling_reservations
 (tenant_id,workspace_id,kind,source_id,attempt_number,attempt_id,intent,demand,quota_revision)
 VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9) ON CONFLICT DO NOTHING`,
		append(args, revision)...))
	if err != nil {
		return false, err
	}
	if inserted != 1 {
		return false, ErrReservationConflict
	}
	if err := recordBorrowingWithin(ctx, q, a, loans, reclaimed); err != nil {
		return false, err
	}
	return true, nil
}

// ReleaseWithin ends a single-replica attempt in the owner's transaction
// after the owner has verified that it stopped or was never delivered.
// An already released exact attempt replays with false and no error.
// Active reservations with multiple replicas require ReleaseGangWithin.
func ReleaseWithin(ctx context.Context, q transaction.Executor, a Attempt) (bool, error) {
	return releaseWithin(ctx, q, a, nil)
}

func releaseWithin(ctx context.Context, q transaction.Executor, a Attempt, evidence []ReplicaRelease) (bool, error) {
	if !a.valid() {
		return false, ErrInvalidAttempt
	}
	if _, err := lockReservationScope(ctx, q, a); err != nil {
		return false, err
	}
	if err := requireDurableReservationCommit(ctx, q); err != nil {
		return false, err
	}
	args := append(a.scopeArgs(), a.ID)
	const exactAttempt = " WHERE tenant_id=$1 AND workspace_id=$2 AND kind=$3 AND source_id=$4 AND attempt_number=$5 AND attempt_id=$6"
	var body []byte
	var released bool
	err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		"SELECT intent,released_at IS NOT NULL FROM scheduling_reservations"+exactAttempt, args...)).Scan(&body, &released)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrReservationNotFound
	}
	if err != nil || released && len(evidence) == 0 {
		return false, err
	}
	var intent JobIntent
	if json.Unmarshal(body, &intent) != nil || intent.Kind != a.Kind || intent.SourceID != a.SourceID {
		return false, ErrQuotaPolicyUnavailable
	}
	normalized, _, err := intent.Normalize()
	if err != nil {
		return false, ErrQuotaPolicyUnavailable
	}
	if err := checkReplicaReleases(a.ID, normalized.Groups, evidence); err != nil || released {
		return false, err
	}
	changed, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL,
		"UPDATE scheduling_reservations SET released_at=clock_timestamp()"+exactAttempt+" AND released_at IS NULL", args...))
	if err != nil {
		return false, err
	}
	if changed != 1 {
		return false, ErrReservationNotFound
	}
	return true, nil
}

func requireDurableReservationCommit(ctx context.Context, q transaction.Executor) error {
	_, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, "SET LOCAL synchronous_commit = on"))
	return err
}

func checkWorkspaceCapacity(ctx context.Context, q transaction.Executor, a Attempt, ceiling, demand Resources) error {
	used, err := readWorkspaceUsageWithin(ctx, q, a.TenantID, a.WorkspaceID)
	if err != nil {
		return err
	}
	if limits := workspaceCapacityLimits(demand, ceiling, used); len(limits) != 0 {
		return &reservationQuotaError{limits: limits}
	}
	return nil
}

func readWorkspaceUsageWithin(ctx context.Context, q transaction.Executor, tenant, workspace string) (Resources, error) {
	rows, err := q.Query(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT r.key,sum(r.value::numeric)::text
 FROM scheduling_reservations s CROSS JOIN LATERAL jsonb_each_text(s.demand) r
 WHERE s.tenant_id=$1 AND s.workspace_id=$2 AND s.released_at IS NULL GROUP BY r.key`, tenant, workspace))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	used := make(Resources)
	for rows.Next() {
		var resource Resource
		var raw string
		if err := rows.Scan(&resource, &raw); err != nil {
			return nil, err
		}
		quantity, err := parseReservationUsage(resource, raw)
		if err != nil {
			return nil, err
		}
		used[resource] = quantity
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return used, nil
}

func parseReservationUsage(resource Resource, raw string) (int64, error) {
	quantity, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || quantity < 0 || !resource.valid() {
		return 0, ErrQuotaPolicyUnavailable
	}
	return quantity, nil
}
