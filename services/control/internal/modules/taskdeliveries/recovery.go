package taskdeliveries

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

type RetryRequest struct {
	Generation int    `json:"generation"`
	Reason     string `json:"reason"`
}

func retryFingerprint(tenant, namespace, workspace, node, event string, input RetryRequest) []byte {
	raw, _ := json.Marshal(struct {
		Contract, Tenant, Namespace, Workspace, Node, Event string
		Generation                                          int
		Reason                                              string
	}{"task-event-retry:v1", tenant, namespace, workspace, node, event, input.Generation, input.Reason})
	sum := sha256.Sum256(raw)
	return sum[:]
}

// Retry authorizes a new transport generation only. It never creates, resets,
// claims or executes a node task and never extends the immutable event lifetime.
func (s *Service) Retry(ctx context.Context, a identity.Auth, workspace, node, event string, input RetryRequest, key, request string) (RetryReceipt, error) {
	if !admin(a) {
		return RetryReceipt{}, fail("ACCESS_DENIED")
	}
	if !taskbus.ValidID(event) || !taskbus.ValidID(key) || !taskbus.ValidID(request) || input.Generation < 1 || input.Generation >= 2147483647 || !workspaces.ValidReason(input.Reason) {
		return RetryReceipt{}, fail("VALIDATION_FAILED")
	}
	run, done, err := s.begin(ctx)
	if err != nil {
		return RetryReceipt{}, err
	}
	defer done()
	var out RetryReceipt
	err = s.humanWithin(run, a, workspace, node, func(ctx context.Context, q transaction.Executor, n nodes.Node) error {
		value, err := deliveryWithin(ctx, q, n.TenantID, n.WorkspaceID, n.ID, event, true)
		if err != nil {
			return err
		}
		task, err := s.sourceWithin(ctx, q, value)
		if err != nil {
			return err
		}
		fingerprint := retryFingerprint(n.TenantID, s.Configuration.NamespaceID, n.WorkspaceID, n.ID, event, input)
		var original []byte
		var priorEvent, delivery string
		var generation int
		var requested time.Time
		err = q.QueryRow(ctx, statement(q, `SELECT event_id::text,retry_fingerprint,retry_delivery_id::text,retry_generation,retried_at FROM task_event_dead_letters WHERE retry_actor=$1 AND retry_key=$2 FOR UPDATE`,
			`SELECT event_id,retry_fingerprint,retry_delivery_id,retry_generation,retried_at FROM task_event_dead_letters WHERE retry_actor=? AND retry_key=? FOR UPDATE`, a.Me.User.ID, key)).Scan(&priorEvent, &original, &delivery, &generation, &requested)
		if err == nil {
			if priorEvent != event || !bytes.Equal(original, fingerprint) {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out = RetryReceipt{EventID: event, DeliveryID: delivery, Generation: generation, RequestedAt: requested.UTC()}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now, err := nowWithin(ctx, q)
		if err != nil {
			return err
		}
		if n.Status != "active" || task.State != "pending" && task.State != "running" || value.Generation != input.Generation || value.State != "dead" || !now.Before(value.ExpiresAt) || value.ClaimID != nil {
			return fail("TASK_EVENT_RETRY_REFUSED")
		}
		var receipts int
		if err = q.QueryRow(ctx, statement(q, "SELECT count(*) FROM task_event_receipts WHERE event_id=$1", "SELECT count(*) FROM task_event_receipts WHERE event_id=?", event)).Scan(&receipts); err != nil {
			return err
		}
		if receipts != 0 {
			return fail("TASK_EVENT_RECEIPT_CONFLICT")
		}
		var id, hash string
		var priorKey *string
		err = q.QueryRow(ctx, statement(q, `SELECT id::text,envelope_sha256,retry_key::text FROM task_event_dead_letters WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND task_id=$4 AND event_id=$5 AND generation=$6 FOR UPDATE`,
			`SELECT id,envelope_sha256,retry_key FROM task_event_dead_letters WHERE tenant_id=? AND workspace_id=? AND node_id=? AND task_id=? AND event_id=? AND generation=? FOR UPDATE`, n.TenantID, n.WorkspaceID, n.ID, value.TaskID, event, input.Generation)).Scan(&id, &hash, &priorKey)
		if err != nil {
			return err
		}
		raw, err := envelope(value).Canonical()
		if err != nil || hash != digest(raw) || priorKey != nil {
			return fail("TASK_EVENT_RETRY_REFUSED")
		}
		delivery, generation = uuid.NewString(), input.Generation+1
		count, err := q.Exec(ctx, statement(q, `UPDATE task_event_dead_letters SET retry_actor=$1,retry_key=$2,retry_reason=$3,retry_fingerprint=$4,retry_delivery_id=$5,retry_generation=$6,retried_at=$7 WHERE id=$8 AND retry_key IS NULL`,
			`UPDATE task_event_dead_letters SET retry_actor=?,retry_key=?,retry_reason=?,retry_fingerprint=?,retry_delivery_id=?,retry_generation=?,retried_at=? WHERE id=? AND retry_key IS NULL`, a.Me.User.ID, key, input.Reason, fingerprint, delivery, generation, now, id))
		if err != nil {
			return err
		}
		if count != 1 {
			return fail("TASK_EVENT_RETRY_REFUSED")
		}
		count, err = q.Exec(ctx, statement(q, `UPDATE task_event_outbox SET delivery_id=$1,generation=$2,state='prepared',outcome_unknown=false,publish_attempts=0,consume_attempts=0,broker_sequence=NULL,error_code=NULL,not_before=$3,updated_at=$3 WHERE tenant_id=$4 AND workspace_id=$5 AND node_id=$6 AND id=$7 AND state='dead' AND generation=$8`,
			`UPDATE task_event_outbox SET delivery_id=?,generation=?,state='prepared',outcome_unknown=0,publish_attempts=0,consume_attempts=0,broker_sequence=NULL,error_code=NULL,not_before=?,updated_at=? WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND state='dead' AND generation=?`, retryUpdateArgs(q, value, delivery, generation, now)...))
		if err != nil {
			return err
		}
		if count != 1 {
			return fail("TASK_EVENT_SOURCE_CHANGED")
		}
		if err = audit(ctx, q, value.Tenant, value.WorkspaceID, a.Me.User.ID, a.Me.Audience, "task_event.retry_requested", event, input.Reason, request); err != nil {
			return err
		}
		out = RetryReceipt{EventID: event, DeliveryID: delivery, Generation: generation, RequestedAt: now}
		return nil
	})
	if err != nil {
		return RetryReceipt{}, err
	}
	return out, nil
}
func retryUpdateArgs(q transaction.Executor, value storedDelivery, delivery string, generation int, now time.Time) []any {
	args := []any{delivery, generation, now}
	if q.Dialect() == transaction.MySQL {
		args = append(args, now)
	}
	return append(args, value.Tenant, value.WorkspaceID, value.NodeID, value.ID, value.Generation)
}
