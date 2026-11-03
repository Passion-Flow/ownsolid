package taskdeliveries

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

// RecordTaskQueuedWithin is the approved same-transaction node hook. There is no
// broker I/O, task claim or independent commit inside this borrowed callback.
func (s *Service) RecordTaskQueuedWithin(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task) error {
	run, done, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer done()
	if q == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	if _, pool := q.(transaction.Store); pool {
		return fail("STORAGE_UNAVAILABLE")
	}
	if _, bounded := run.Deadline(); !bounded {
		return fail("STORAGE_UNAVAILABLE")
	}
	store, err := s.repository()
	if err != nil {
		return err
	}
	if q.Dialect() != store.Dialect() {
		return fail("TASK_BUS_COMPOSITION_INVALID")
	}
	if !taskbus.InitialOperation(t.Operation) {
		return nil
	}
	if n.TenantID != s.lifecycle.tenant || t.TenantID != n.TenantID || t.WorkspaceID != n.WorkspaceID || t.NodeID != n.ID {
		return fail("TASK_EVENT_INVALID")
	}
	current, task, err := s.Nodes.TaskEventSourceWithin(run, q, n.TenantID, n.WorkspaceID, n.ID, t.ID)
	if err != nil {
		return safe(err)
	}
	if current.Status != "active" || task.State != "pending" || task.Attempt != 0 || task.Operation != t.Operation || !task.CreatedAt.Equal(t.CreatedAt) {
		return fail("TASK_EVENT_SOURCE_CHANGED")
	}
	prior, err := scanDelivery(q.QueryRow(run, statement(q, "SELECT "+pgFields+" FROM task_event_outbox WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND task_id=$4 AND event_kind=$5 FOR UPDATE", "SELECT "+myFields+" FROM task_event_outbox WHERE tenant_id=? AND workspace_id=? AND node_id=? AND task_id=? AND event_kind=? FOR UPDATE", task.TenantID, task.WorkspaceID, task.NodeID, task.ID, taskbus.NodeTaskQueued)))
	if err == nil {
		if prior.Event.Operation != task.Operation || !prior.Event.TaskCreatedAt.Equal(task.CreatedAt) || prior.Namespace != s.Configuration.NamespaceID || prior.ConsumerName != s.Configuration.Consumer {
			return fail("TASK_EVENT_SOURCE_CHANGED")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return safe(err)
	}
	now, err := nowWithin(run, q)
	if err != nil {
		return safe(err)
	}
	event := taskbus.Event{Schema: taskbus.EventSchema, ID: uuid.NewString(), NamespaceID: s.Configuration.NamespaceID, TenantID: task.TenantID, WorkspaceID: task.WorkspaceID, NodeID: task.NodeID, TaskID: task.ID, Kind: taskbus.NodeTaskQueued, Operation: task.Operation, TaskCreatedAt: task.CreatedAt.UTC(), CreatedAt: now}
	raw, err := event.Canonical()
	if err != nil {
		return err
	}
	delivery := uuid.NewString()
	count, err := q.Exec(run, statement(q, `INSERT INTO task_event_outbox(id,tenant_id,workspace_id,node_id,task_id,namespace_id,event_kind,operation,event_bytes,logical_sha256,delivery_id,generation,state,outcome_unknown,publish_attempts,consume_attempts,broker_identity,consumer_name,not_before,expires_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,1,'prepared',false,0,0,$12,$13,$14,$15,$14,$14)`,
		`INSERT INTO task_event_outbox(id,tenant_id,workspace_id,node_id,task_id,namespace_id,event_kind,operation,event_bytes,logical_sha256,delivery_id,generation,state,outcome_unknown,publish_attempts,consume_attempts,broker_identity,consumer_name,not_before,expires_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,1,'prepared',0,0,0,?,?,?,?,?,?)`, enqueueArgs(q, event, raw, delivery, s.Transport.Identity(), s.Configuration.Consumer, now, s.Configuration.RetryLifetimeSeconds)...))
	if err != nil {
		return safe(err)
	}
	if count != 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	return safe(audit(run, q, event.TenantID, event.WorkspaceID, "", "system", "task_event.queued", event.ID, "Persisted exact node task transport intent", uuid.NewString()))
}
func enqueueArgs(q transaction.Executor, event taskbus.Event, raw []byte, delivery, broker, consumer string, now time.Time, lifetime int) []any {
	args := []any{event.ID, event.TenantID, event.WorkspaceID, event.NodeID, event.TaskID, event.NamespaceID, event.Kind, event.Operation, raw, digest(raw), delivery, broker, consumer, now, now.Add(time.Duration(lifetime) * time.Second)}
	if q.Dialect() == transaction.MySQL {
		args = append(args, now, now)
	}
	return args
}
