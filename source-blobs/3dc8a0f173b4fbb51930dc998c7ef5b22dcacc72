package sso

import (
	"context"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Cleanup expires a bounded set of diagnostic intents and retains results for
// the existing configured SSO retention period. It grants no actor authority.
func (t *ConnectorSignInTests) Cleanup(ctx context.Context) error {
	if ctx == nil || t == nil || t.service == nil {
		return fail("SERVICE_NOT_READY")
	}
	s := t.service
	store, err := s.catalogRepository()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy.CleanupTimeoutSeconds))
	defer cancel()
	return maintenanceError(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		tenant, err := connectorTenant(ctx, q, connectorReadFence)
		if err != nil {
			return err
		}
		ids, err := maintenanceIDs(ctx, q, transaction.SQL(transaction.PostgreSQL, `SELECT id::text FROM sso_connector_sign_in_tests WHERE tenant_id=$1 AND state IN ('pending','claimed') AND expires_at<=clock_timestamp() ORDER BY expires_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`, tenant, s.policy.CleanupBatchSize), s.policy.CleanupBatchSize)
		if err != nil {
			return err
		}
		for _, id := range ids {
			changed, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_connector_sign_in_tests SET state='expired',error_code='SSO_FLOW_EXPIRED',finished_at=clock_timestamp(),secret_key_id=NULL,secret_nonce=NULL,secret_ciphertext=NULL WHERE tenant_id=$1 AND id=$2 AND state IN ('pending','claimed')`, tenant, id))
			if err != nil {
				return err
			}
			if changed != 1 {
				return fail("STORAGE_UNAVAILABLE")
			}
		}
		old, err := maintenanceIDs(ctx, q, transaction.SQL(transaction.PostgreSQL, `SELECT id::text FROM sso_connector_sign_in_tests WHERE tenant_id=$1 AND state IN ('succeeded','failed','expired') AND finished_at<clock_timestamp()-$2::bigint*interval '1 day' ORDER BY finished_at,id LIMIT $3 FOR UPDATE SKIP LOCKED`, tenant, s.policy.FlowRetentionDays, s.policy.CleanupBatchSize), s.policy.CleanupBatchSize)
		if err != nil {
			return err
		}
		for _, id := range old {
			changed, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, "DELETE FROM sso_connector_sign_in_tests WHERE tenant_id=$1 AND id=$2 AND state IN ('succeeded','failed','expired')", tenant, id))
			if err != nil {
				return err
			}
			if changed != 1 {
				return fail("STORAGE_UNAVAILABLE")
			}
		}
		if len(ids)+len(old) == 0 {
			return nil
		}
		return audit.WriteWithin(ctx, q, "", "", "system", "sso.flow_cleanup", "success", "installation", "", "interactive_tests", "")
	}))
}

// Run joins cleanup to its caller's lifecycle; it never starts a background task.
func (t *ConnectorSignInTests) Run(ctx context.Context, report func(error)) {
	if ctx == nil || t == nil || t.service == nil {
		if report != nil {
			report(fail("SERVICE_NOT_READY"))
		}
		return
	}
	ticker := time.NewTicker(config.Seconds(t.service.policy.CleanupSeconds))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := t.Cleanup(ctx)
			if ctx.Err() != nil {
				return
			}
			if err != nil && report != nil {
				report(err)
			}
		}
	}
}
