package recovery

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/taskdeliveries"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func fenceRestoredTaskEventsWithin(ctx context.Context, q transaction.Executor, accounts *identity.Service) error {
	pg := "SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('task_event_outbox','task_event_receipts','task_event_dead_letters')"
	my := "SELECT count(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('task_event_outbox','task_event_receipts','task_event_dead_letters')"
	query := pg
	if q.Dialect() == transaction.MySQL {
		query = my
	}
	var tables int
	if e := q.QueryRow(ctx, transaction.SQL(q.Dialect(), query)).Scan(&tables); e != nil {
		return e
	}
	if tables == 0 && !accounts.Config.TaskEvents.Enabled {
		return nil
	}
	if tables != 3 {
		return errors.New("TASK_BUS_SCHEMA_REJECTED")
	}
	var tenant string
	if e := q.QueryRow(ctx, transaction.SQL(q.Dialect(), "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete")).Scan(&tenant); e != nil {
		return e
	}
	w := &workspaces.Service{DB: accounts.DB, Identity: accounts}
	n := &nodes.Service{DB: accounts.DB, Identity: accounts, Workspaces: w}
	return taskdeliveries.FenceAfterRestoreWithin(ctx, q, n, tenant)
}
