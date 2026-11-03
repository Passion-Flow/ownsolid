package plugins

import (
	"context"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Cleanup removes bounded exact expired intent keys; provenance stays intact.
func (s *Service) Cleanup(ctx context.Context) (int, error) {
	count := 0
	e := s.storageTransaction(ctx, func(ctx context.Context, q transaction.Executor) error {
		var tenant string
		if e := q.QueryRow(ctx, pluginSQL(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1", nativePluginInstalledTenant)).Scan(&tenant); e != nil {
			return e
		}
		age := s.Identity.Config.Policy().Identity.IdempotencySeconds
		rows, e := q.Query(ctx, pluginSQL(q, `SELECT actor_id::text,idempotency_key::text FROM plugin_mutation_intents WHERE tenant_id=$1 AND created_at<clock_timestamp()-make_interval(secs=>$2) ORDER BY created_at,idempotency_key LIMIT $3 FOR UPDATE SKIP LOCKED`, `SELECT actor_id,idempotency_key FROM plugin_mutation_intents WHERE tenant_id=? AND created_at<TIMESTAMPADD(SECOND,-?,UTC_TIMESTAMP(6)) ORDER BY created_at,idempotency_key LIMIT ? FOR UPDATE SKIP LOCKED`, nativePluginIntentPage, tenant, age, s.policy().MaintenanceBatch))
		if e != nil {
			return e
		}
		type key struct{ actor, id string }
		keys := []key{}
		for rows.Next() {
			var k key
			if e = rows.Scan(&k.actor, &k.id); e != nil {
				rows.Close()
				return e
			}
			keys = append(keys, k)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		for _, k := range keys {
			n, e := q.Exec(ctx, pluginSQL(q, "DELETE FROM plugin_mutation_intents WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3", "DELETE FROM plugin_mutation_intents WHERE tenant_id=? AND actor_id=? AND idempotency_key=?", nativePluginIntentDelete, tenant, k.actor, k.id))
			if e != nil {
				return e
			}
			if n != 1 {
				return failure("STORAGE_UNAVAILABLE")
			}
			count++
		}
		if count > 0 {
			n, e := q.Exec(ctx, pluginStatement(q, `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,'system','plugin.intent_retention','success','installation',$2,'Expired bounded plugin mutation intents',$3)`, `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,'system','plugin.intent_retention','success','installation',?,'Expired bounded plugin mutation intents',?)`, nativePluginRetentionAudit, []any{uuid.NewString(), tenant, uuid.NewString()}, []any{uuid.NewString(), tenant, tenant, uuid.NewString()}))
			if e != nil {
				return e
			}
			if n != 1 {
				return failure("STORAGE_UNAVAILABLE")
			}
		}
		return nil
	})
	if e != nil {
		return 0, e
	}
	return count, nil
}
