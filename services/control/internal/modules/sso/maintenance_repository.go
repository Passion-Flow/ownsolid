package sso

import (
	"context"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const maintenanceRetentionDay = 24 * time.Hour

func maintenanceError(e error) error {
	if e == nil {
		return nil
	}
	if outcome, found := transaction.CommitOutcome(e); found {
		return fail(string(outcome))
	}
	switch e.Error() {
	case string(transaction.Unavailable), string(transaction.Deadlock), string(transaction.LockTimeout),
		string(transaction.Cancelled), string(transaction.Timeout), string(transaction.CommitUnknown), string(transaction.CommitRolledBack):
		return fail(e.Error())
	default:
		return fail(string(transaction.Unavailable))
	}
}

// InvalidateAuthoritiesWithin borrows the actual offline restore transaction.
// The quiescent operator owns its deadline,rollback and commit;this helper cannot commit.
func InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	if q == nil || q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL && q.Dialect() != transaction.Dameng || ctx == nil {
		return fail("SSO_TRANSACTION_STORAGE_MISMATCH")
	}
	_, e := q.Exec(ctx, sourceSQL(q,
		`UPDATE sso_flows SET state='failed',error_code='SSO_FLOW_UNUSABLE',finished_at=clock_timestamp(),verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE state IN ('pending','claimed')`,
		`UPDATE sso_flows SET state='failed',error_code='SSO_FLOW_UNUSABLE',finished_at=UTC_TIMESTAMP(6),verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE state IN ('pending','claimed')`, "UPDATE SSO_FLOWS SET STATE='failed',ERROR_CODE='SSO_FLOW_UNUSABLE',FINISHED_AT=CURRENT_TIMESTAMP(6),VERIFIER_KEY_ID=NULL,VERIFIER_NONCE=NULL,VERIFIER_CIPHERTEXT=NULL WHERE STATE IN ('pending','claimed')"))
	return maintenanceError(e)
}

func maintenanceIDs(ctx context.Context, q transaction.Executor, statement transaction.Statement, limit int) ([]string, error) {
	if ctx == nil || q == nil || limit < 1 {
		return nil, fail("STORAGE_UNAVAILABLE")
	}
	rows, e := q.Query(ctx, statement)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
	seen := make(map[string]bool)
	for rows.Next() {
		var id string
		if e := rows.Scan(&id); e != nil {
			return nil, e
		}
		if len(ids) >= limit || !canonicalID(id) || seen[id] {
			return nil, fail("STORAGE_UNAVAILABLE")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if e := rows.Err(); e != nil {
		return nil, e
	}
	return ids, nil
}

func cleanupFlowsWithin(ctx context.Context, q transaction.Executor, batch, retentionDays int) error {
	if ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL && q.Dialect() != transaction.Dameng || batch < 1 || retentionDays < 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	policy := config.DefaultSSOPolicy()
	policy.CleanupBatchSize, policy.FlowRetentionDays = batch, retentionDays
	if policy.Validate() != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	var now time.Time
	if e := q.QueryRow(ctx, sourceSQL(q, `SELECT clock_timestamp()`, `SELECT UTC_TIMESTAMP(6)`, "SELECT CURRENT_TIMESTAMP(6) FROM DUAL")).Scan(&now); e != nil {
		return e
	}
	if now.IsZero() || now.Year() < 1970 || now.Year() > 9999 {
		return fail("STORAGE_UNAVAILABLE")
	}
	if q.Dialect() == transaction.Dameng && !damengTimestamp(now) {
		return fail("STORAGE_UNAVAILABLE")
	}
	now = now.UTC().Truncate(time.Microsecond)
	expired, e := maintenanceIDs(ctx, q, sourceBoundedSQL(q,
		`SELECT id::text FROM sso_flows WHERE state IN ('pending','claimed') AND expires_at<=$1 ORDER BY expires_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`,
		`SELECT id FROM sso_flows WHERE state IN ('pending','claimed') AND expires_at<=? ORDER BY expires_at,id LIMIT ? FOR UPDATE SKIP LOCKED`,
		"SELECT ID FROM SSO_FLOWS WHERE STATE IN ('pending','claimed') AND EXPIRES_AT<=? ORDER BY EXPIRES_AT,ID FOR UPDATE OF ID ",
		" SKIP LOCKED",
		batch,
		batch,
		now), batch)
	if e != nil {
		return e
	}
	for _, id := range expired {
		changed, e := q.Exec(ctx, sourceSQL(q,
			`UPDATE sso_flows SET state='expired',error_code='SSO_FLOW_EXPIRED',finished_at=$1,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$2 AND state IN ('pending','claimed') AND expires_at<=$3`,
			`UPDATE sso_flows SET state='expired',error_code='SSO_FLOW_EXPIRED',finished_at=?,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=? AND state IN ('pending','claimed') AND expires_at<=?`, "UPDATE SSO_FLOWS SET STATE='expired',ERROR_CODE='SSO_FLOW_EXPIRED',FINISHED_AT=?,VERIFIER_KEY_ID=NULL,VERIFIER_NONCE=NULL,VERIFIER_CIPHERTEXT=NULL WHERE ID=? AND STATE IN ('pending','claimed') AND EXPIRES_AT<=?", now, id, now))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("STORAGE_UNAVAILABLE")
		}
	}
	cutoff := now.Add(-time.Duration(retentionDays) * maintenanceRetentionDay)
	if q.Dialect() == transaction.Dameng && !damengTimestamp(cutoff) {
		return fail("STORAGE_UNAVAILABLE")
	}
	retained, e := maintenanceIDs(ctx, q, sourceBoundedSQL(q,
		`SELECT f.id::text FROM sso_flows f WHERE f.state IN ('succeeded','failed','expired') AND f.finished_at<$1 AND NOT EXISTS(SELECT 1 FROM sessions se WHERE se.sso_flow_id=f.id) ORDER BY f.finished_at,f.id LIMIT $2 FOR UPDATE OF f SKIP LOCKED`,
		`SELECT f.id FROM sso_flows f WHERE f.state IN ('succeeded','failed','expired') AND f.finished_at<? AND NOT EXISTS(SELECT 1 FROM sessions se WHERE se.sso_flow_id=f.id) ORDER BY f.finished_at,f.id LIMIT ? FOR UPDATE SKIP LOCKED`,
		"SELECT F.ID FROM SSO_FLOWS F WHERE F.STATE IN ('succeeded','failed','expired') AND F.FINISHED_AT<? AND NOT EXISTS(SELECT 1 FROM SESSIONS SE WHERE SE.SSO_FLOW_ID=F.ID) ORDER BY F.FINISHED_AT,F.ID FOR UPDATE OF F.ID ",
		" SKIP LOCKED",
		batch,
		batch,
		cutoff), batch)
	if e != nil {
		return e
	}
	for _, id := range retained {
		changed, e := q.Exec(ctx, sourceSQL(q,
			`DELETE FROM sso_flows WHERE id=$1 AND state IN ('succeeded','failed','expired') AND finished_at<$2 AND NOT EXISTS(SELECT 1 FROM sessions se WHERE se.sso_flow_id=sso_flows.id)`,
			`DELETE FROM sso_flows WHERE id=? AND state IN ('succeeded','failed','expired') AND finished_at<? AND NOT EXISTS(SELECT 1 FROM sessions se WHERE se.sso_flow_id=sso_flows.id)`, "DELETE FROM SSO_FLOWS WHERE ID=? AND STATE IN ('succeeded','failed','expired') AND FINISHED_AT<? AND NOT EXISTS(SELECT 1 FROM SESSIONS SE WHERE SE.SSO_FLOW_ID=SSO_FLOWS.ID)", id, cutoff))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("STORAGE_UNAVAILABLE")
		}
	}
	if len(expired)+len(retained) == 0 {
		return nil
	}
	return audit.WriteWithin(ctx, q, "", "", "system", "sso.flow_cleanup", "success", "installation", "", "", "")
}
