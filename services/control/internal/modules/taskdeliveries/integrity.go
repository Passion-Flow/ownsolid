package taskdeliveries

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

// verifyDeadLettersWithin streams retained history with current canonical event
// bytes. Historical delivery UUIDs and retry links are facts, never reconstructed
// from a current generation or inferred from a broker sequence/hash alone.
func (s *Service) verifyDeadLettersWithin(ctx context.Context, q transaction.Executor, tenant string) error {
	var invalid int
	if err := q.QueryRow(ctx, statement(q, "SELECT count(*) FROM task_event_outbox o LEFT JOIN node_tasks t ON (t.tenant_id,t.workspace_id,t.node_id,t.id)=(o.tenant_id,o.workspace_id,o.node_id,o.task_id) WHERE t.id IS NULL OR t.operation<>o.operation", "SELECT count(*) FROM task_event_outbox o LEFT JOIN node_tasks t ON t.tenant_id=o.tenant_id AND t.workspace_id=o.workspace_id AND t.node_id=o.node_id AND t.id=o.task_id WHERE t.id IS NULL OR t.operation<>o.operation")).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fail("TASK_EVENT_SOURCE_CHANGED")
	}
	sources, err := q.Query(ctx, statement(q, "SELECT o.event_bytes,t.created_at FROM task_event_outbox o JOIN node_tasks t ON (t.tenant_id,t.workspace_id,t.node_id,t.id)=(o.tenant_id,o.workspace_id,o.node_id,o.task_id)", "SELECT o.event_bytes,t.created_at FROM task_event_outbox o JOIN node_tasks t ON t.tenant_id=o.tenant_id AND t.workspace_id=o.workspace_id AND t.node_id=o.node_id AND t.id=o.task_id"))
	if err != nil {
		return err
	}
	for sources.Next() {
		var raw []byte
		var created time.Time
		var event taskbus.Event
		if err = sources.Scan(&raw, &created); err != nil {
			sources.Close()
			return err
		}
		if len(raw) > taskbus.MaximumEnvelopeBytes || json.Unmarshal(raw, &event) != nil || !event.TaskCreatedAt.Equal(created.UTC()) {
			sources.Close()
			return fail("TASK_EVENT_SOURCE_CHANGED")
		}
	}
	err = sources.Err()
	sources.Close()
	if err != nil {
		return err
	}
	if err := q.QueryRow(ctx, statement(q, `SELECT count(*) FROM task_event_dead_letters WHERE tenant_id<>$1 OR broker_identity<>$2 OR consumer_name<>$3`,
		`SELECT count(*) FROM task_event_dead_letters WHERE tenant_id<>? OR broker_identity<>? OR consumer_name<>?`, tenant, s.Transport.Identity(), s.Configuration.Consumer)).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fail("TASK_BUS_IDENTITY_CHANGED")
	}
	if err := q.QueryRow(ctx, statement(q, `SELECT count(*) FROM task_event_outbox o WHERE o.generation>1 AND NOT EXISTS(SELECT 1 FROM task_event_dead_letters d WHERE d.event_id=o.id AND d.generation=o.generation-1 AND d.retry_generation=o.generation AND d.retry_delivery_id=o.delivery_id)`,
		`SELECT count(*) FROM task_event_outbox o WHERE o.generation>1 AND NOT EXISTS(SELECT 1 FROM task_event_dead_letters d WHERE d.event_id=o.id AND d.generation=o.generation-1 AND d.retry_generation=o.generation AND d.retry_delivery_id=o.delivery_id)`)).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fail("TASK_EVENT_RECEIPT_CONFLICT")
	}
	for _, pair := range [][2]string{
		{"SELECT count(*) FROM task_event_dead_letters d LEFT JOIN task_event_outbox o ON (o.tenant_id,o.workspace_id,o.node_id,o.task_id,o.id)=(d.tenant_id,d.workspace_id,d.node_id,d.task_id,d.event_id) WHERE d.event_id IS NOT NULL AND o.id IS NULL", "SELECT count(*) FROM task_event_dead_letters d LEFT JOIN task_event_outbox o ON o.tenant_id=d.tenant_id AND o.workspace_id=d.workspace_id AND o.node_id=d.node_id AND o.task_id=d.task_id AND o.id=d.event_id WHERE d.event_id IS NOT NULL AND o.id IS NULL"},
		{"SELECT count(*) FROM task_event_receipts r LEFT JOIN task_event_outbox o ON (o.tenant_id,o.workspace_id,o.node_id,o.task_id,o.id)=(r.tenant_id,r.workspace_id,r.node_id,r.task_id,r.event_id) WHERE o.id IS NULL", "SELECT count(*) FROM task_event_receipts r LEFT JOIN task_event_outbox o ON o.tenant_id=r.tenant_id AND o.workspace_id=r.workspace_id AND o.node_id=r.node_id AND o.task_id=r.task_id AND o.id=r.event_id WHERE o.id IS NULL"},
	} {
		if err := q.QueryRow(ctx, statement(q, pair[0], pair[1])).Scan(&invalid); err != nil {
			return err
		}
		if invalid != 0 {
			return fail("TASK_EVENT_RECEIPT_CONFLICT")
		}
	}
	rows, err := q.Query(ctx, statement(q, `SELECT d.id::text,d.event_id::text,d.tenant_id::text,d.workspace_id::text,d.node_id::text,d.task_id::text,d.generation,d.delivery_id::text,d.kind,d.envelope_sha256,d.error_code,d.created_at,d.retry_key::text,d.retry_delivery_id::text,d.retry_generation,d.retried_at,o.event_bytes,o.logical_sha256,o.generation,o.delivery_id::text,n.delivery_id::text FROM task_event_dead_letters d JOIN task_event_outbox o ON o.id=d.event_id LEFT JOIN task_event_dead_letters n ON n.event_id=d.event_id AND n.generation=d.retry_generation WHERE d.event_id IS NOT NULL ORDER BY d.id`,
		`SELECT d.id,d.event_id,d.tenant_id,d.workspace_id,d.node_id,d.task_id,d.generation,d.delivery_id,d.kind,d.envelope_sha256,d.error_code,d.created_at,d.retry_key,d.retry_delivery_id,d.retry_generation,d.retried_at,o.event_bytes,o.logical_sha256,o.generation,o.delivery_id,n.delivery_id FROM task_event_dead_letters d JOIN task_event_outbox o ON o.id=d.event_id LEFT JOIN task_event_dead_letters n ON n.event_id=d.event_id AND n.generation=d.retry_generation WHERE d.event_id IS NOT NULL ORDER BY d.id`))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, eventID, scopeTenant, workspace, node, task, delivery, kind, hash, code, logical, currentDelivery string
		var generation, currentGeneration int
		var created time.Time
		var retryKey, retryDelivery, nextDelivery *string
		var retryGeneration *int
		var retried *time.Time
		var raw []byte
		if err = rows.Scan(&id, &eventID, &scopeTenant, &workspace, &node, &task, &generation, &delivery, &kind, &hash, &code, &created, &retryKey, &retryDelivery, &retryGeneration, &retried, &raw, &logical, &currentGeneration, &currentDelivery, &nextDelivery); err != nil {
			return err
		}
		var event taskbus.Event
		if len(raw) > taskbus.MaximumEnvelopeBytes || json.Unmarshal(raw, &event) != nil {
			return fail("TASK_EVENT_INVALID")
		}
		canonical, e := event.Canonical()
		if e != nil || !bytes.Equal(canonical, raw) || digest(raw) != logical || !taskbus.ValidID(id) || !taskbus.ValidID(delivery) || event.ID != eventID || event.TenantID != scopeTenant || scopeTenant != tenant || event.WorkspaceID != workspace || event.NodeID != node || event.TaskID != task || event.NamespaceID != s.Configuration.NamespaceID || generation < 1 || generation > currentGeneration || created.Before(event.CreatedAt) || !storedErrorCode(code) || kind != "publish" && kind != "consume" && kind != "expiry" {
			return fail("TASK_EVENT_INVALID")
		}
		envelopeBytes, e := (taskbus.Envelope{DeliveryID: delivery, Event: event}).Canonical()
		if e != nil || digest(envelopeBytes) != hash {
			return fail("TASK_EVENT_RECEIPT_CONFLICT")
		}
		if generation == currentGeneration && delivery != currentDelivery {
			return fail("TASK_EVENT_RECEIPT_CONFLICT")
		}
		if retryKey == nil {
			if generation < currentGeneration || retryDelivery != nil || retryGeneration != nil || retried != nil {
				return fail("TASK_EVENT_RECEIPT_CONFLICT")
			}
		} else {
			if !taskbus.ValidID(*retryKey) || retryDelivery == nil || !taskbus.ValidID(*retryDelivery) || retryGeneration == nil || *retryGeneration != generation+1 || *retryGeneration > currentGeneration || retried == nil || retried.Before(created) {
				return fail("TASK_EVENT_RECEIPT_CONFLICT")
			}
			if *retryGeneration == currentGeneration {
				if *retryDelivery != currentDelivery {
					return fail("TASK_EVENT_RECEIPT_CONFLICT")
				}
			} else if nextDelivery == nil || *nextDelivery != *retryDelivery {
				return fail("TASK_EVENT_RECEIPT_CONFLICT")
			}
		}
	}

	if err = rows.Err(); err != nil {
		return err
	}
	rows.Close()
	poison, err := q.Query(ctx, statement(q, "SELECT id::text,error_code,envelope_sha256,broker_sequence::text FROM task_event_dead_letters WHERE event_id IS NULL", "SELECT id,error_code,envelope_sha256,CAST(broker_sequence AS CHAR) FROM task_event_dead_letters WHERE event_id IS NULL"))
	if err != nil {
		return err
	}
	defer poison.Close()
	for poison.Next() {
		var id, code, hash, sequence string
		if err = poison.Scan(&id, &code, &hash, &sequence); err != nil {
			return err
		}
		seq, e := strconv.ParseUint(sequence, 10, 64)
		if !taskbus.ValidID(id) || !storedErrorCode(code) || !digestHex(hash) || e != nil || seq == 0 || strconv.FormatUint(seq, 10) != sequence {
			return fail("TASK_EVENT_INVALID")
		}
	}
	return poison.Err()
}
