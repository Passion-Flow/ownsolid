package taskdeliveries

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

// FenceAfterRestoreWithin borrows the restore owner's stopped, bounded actual
// transaction. Root must stop/join transport workers and fence physical node
// execution through its existing owner before resuming service. No NATS I/O,
// task/lease mutation or independent commit is performed here.
func FenceAfterRestoreWithin(ctx context.Context, q transaction.Executor, n *nodes.Service, tenant string) error {
	if ctx == nil || q == nil || n == nil || !taskbus.ValidID(tenant) {
		return fail("TASK_BUS_COMPOSITION_INVALID")
	}
	if _, pool := q.(transaction.Store); pool {
		return fail("STORAGE_UNAVAILABLE")
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return fail("STORAGE_UNAVAILABLE")
	}
	if q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL {
		return fail("STORAGE_UNAVAILABLE")
	}
	if e := verifySchemaWithin(ctx, q); e != nil {
		return e
	}
	var installed string
	if err := q.QueryRow(ctx, statement(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete FOR SHARE", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1 FOR SHARE")).Scan(&installed); err != nil {
		return safe(err)
	}
	if installed != tenant {
		return fail("TASK_BUS_COMPOSITION_INVALID")
	}
	var last *storedDelivery
	for {
		pg := "SELECT " + pgFields + " FROM task_event_outbox WHERE tenant_id=$1 AND state NOT IN('received','obsolete')"
		my := "SELECT " + myFields + " FROM task_event_outbox WHERE tenant_id=? AND state NOT IN('received','obsolete')"
		args := []any{tenant}
		if last != nil {
			pg += " AND (workspace_id,node_id,task_id,id)>($2::uuid,$3::uuid,$4::uuid,$5::uuid)"
			my += " AND (workspace_id,node_id,task_id,id)>(?,?,?,?)"
			args = append(args, last.WorkspaceID, last.NodeID, last.TaskID, last.ID)
		}
		pg += " ORDER BY workspace_id,node_id,task_id,id LIMIT 1"
		my += " ORDER BY workspace_id,node_id,task_id,id LIMIT 1"
		value, err := scanDelivery(q.QueryRow(ctx, statement(q, pg, my, args...)))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return safe(err)
		}
		if _, _, err = n.TaskEventHistorySourceWithin(ctx, q, value.Tenant, value.WorkspaceID, value.NodeID, value.TaskID); err != nil {
			return safe(err)
		}
		current, e := deliveryWithin(ctx, q, value.Tenant, value.WorkspaceID, value.NodeID, value.ID, true)
		if e != nil {
			return safe(e)
		}
		if current.State != "received" && current.State != "obsolete" {
			unknown := current.OutcomeUnknown || current.State == "sent" || current.State == "published"
			if e = deadWithin(ctx, q, current, "expiry", "TASK_BUS_RESTORE_FENCED", unknown); e != nil {
				return safe(e)
			}
		}
		last = &value
	}
}
