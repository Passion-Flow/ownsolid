package taskdeliveries

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ExpireNext reconciles a confirmed publication against real broker readback and
// current SQL receipt/source facts. Prepared/uncertain intents use ProcessNext.
func (s *Service) ExpireNext(ctx context.Context) (worked bool, err error) {
	defer func() {
		if err != nil {
			worked = false
		}
	}()
	run, done, err := s.begin(ctx)
	if err != nil {
		return false, err
	}
	defer done()
	store, err := s.repository()
	if err != nil {
		return false, err
	}
	bounded, cancel := context.WithTimeout(run, time.Duration(s.Configuration.RequestSeconds)*time.Second)
	value, err := scanDelivery(store.QueryRow(bounded, statement(store,
		"SELECT "+pgFields+" FROM task_event_outbox WHERE tenant_id=$1 AND namespace_id=$2 AND state='published' AND expires_at<=clock_timestamp() ORDER BY expires_at,id LIMIT 1",
		"SELECT "+myFields+" FROM task_event_outbox WHERE tenant_id=? AND namespace_id=? AND state='published' AND expires_at<=UTC_TIMESTAMP(6) ORDER BY expires_at,id LIMIT 1", s.lifecycle.tenant, s.Configuration.NamespaceID)))
	cancel()
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, safe(err)
	}
	publication, found, err := s.Transport.Lookup(run, envelope(value))
	if err != nil {
		return false, safe(err)
	}
	if found && (publication.Identity != *value.BrokerIdentity || publication.Stream != s.Configuration.Stream) {
		return false, fail("TASK_BUS_IDENTITY_CHANGED")
	}
	bounded, cancel = context.WithTimeout(run, time.Duration(s.Configuration.RequestSeconds)*time.Second)
	defer cancel()
	err = store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error {
		if _, _, e := s.Nodes.TaskEventHistorySourceWithin(ctx, q, value.Tenant, value.WorkspaceID, value.NodeID, value.TaskID); e != nil {
			return e
		}
		current, e := deliveryWithin(ctx, q, value.Tenant, value.WorkspaceID, value.NodeID, value.ID, true)
		if e != nil {
			return e
		}
		if current.State != "published" || current.Generation != value.Generation || current.DeliveryID != value.DeliveryID {
			return nil
		}
		now, e := nowWithin(ctx, q)
		if e != nil {
			return e
		}
		if now.Before(current.ExpiresAt) {
			return nil
		}
		if e = deadWithin(ctx, q, current, "expiry", "TRANSPORT_RETENTION_EXPIRED", false); e != nil {
			return e
		}
		worked = true
		return nil
	})
	return worked, safe(err)
}

// CleanupNext removes one exact resolved terminal task's transport history after
// all broker/retry/ACK lifetimes and configured history retention. Unknown and
// active physical tasks remain retained; this does not delete a broker message.
func (s *Service) CleanupNext(ctx context.Context) (worked bool, err error) {
	defer func() {
		if err != nil {
			worked = false
		}
	}()
	run, done, err := s.begin(ctx)
	if err != nil {
		return false, err
	}
	defer done()
	store, err := s.repository()
	if err != nil {
		return false, err
	}
	bounded, cancel := context.WithTimeout(run, time.Duration(s.Configuration.RequestSeconds)*time.Second)
	defer cancel()
	var c candidate
	err = store.QueryRow(bounded, statement(store, `SELECT o.tenant_id::text,o.workspace_id::text,o.node_id::text,o.task_id::text,o.id::text FROM task_event_outbox o JOIN node_tasks t ON (t.tenant_id,t.workspace_id,t.node_id,t.id)=(o.tenant_id,o.workspace_id,o.node_id,o.task_id) WHERE o.tenant_id=$1 AND o.namespace_id=$2 AND o.state IN('received','obsolete','dead') AND NOT o.outcome_unknown AND o.claim_id IS NULL AND o.updated_at<clock_timestamp()-make_interval(secs=>$3) AND o.expires_at<clock_timestamp()-make_interval(secs=>$4) AND t.state IN('succeeded','failed','cancelled') ORDER BY o.updated_at,o.id LIMIT 1`,
		`SELECT o.tenant_id,o.workspace_id,o.node_id,o.task_id,o.id FROM task_event_outbox o JOIN node_tasks t ON t.tenant_id=o.tenant_id AND t.workspace_id=o.workspace_id AND t.node_id=o.node_id AND t.id=o.task_id WHERE o.tenant_id=? AND o.namespace_id=? AND o.state IN('received','obsolete','dead') AND o.outcome_unknown=0 AND o.claim_id IS NULL AND o.updated_at<TIMESTAMPADD(SECOND,-?,UTC_TIMESTAMP(6)) AND o.expires_at<TIMESTAMPADD(SECOND,-?,UTC_TIMESTAMP(6)) AND t.state IN('succeeded','failed','cancelled') ORDER BY o.updated_at,o.id LIMIT 1`, s.lifecycle.tenant, s.Configuration.NamespaceID, s.Configuration.HistoryRetentionSeconds, s.Configuration.StreamRetentionSeconds+s.Configuration.AckWaitSeconds)).Scan(&c.tenant, &c.workspace, &c.node, &c.task, &c.event)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, safe(err)
	}
	err = store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error {
		_, task, e := s.Nodes.TaskEventHistorySourceWithin(ctx, q, c.tenant, c.workspace, c.node, c.task)
		if e != nil {
			return e
		}
		value, e := deliveryWithin(ctx, q, c.tenant, c.workspace, c.node, c.event, true)
		if e != nil {
			return e
		}
		now, e := nowWithin(ctx, q)
		if e != nil {
			return e
		}
		if task.State != "succeeded" && task.State != "failed" && task.State != "cancelled" || value.State != "received" && value.State != "obsolete" && value.State != "dead" || value.OutcomeUnknown || value.ClaimID != nil || !value.UpdatedAt.Before(now.Add(-time.Duration(s.Configuration.HistoryRetentionSeconds)*time.Second)) || !value.ExpiresAt.Before(now.Add(-time.Duration(s.Configuration.StreamRetentionSeconds+s.Configuration.AckWaitSeconds)*time.Second)) {
			return nil
		}
		// Exact ownership predicates remain on every dependent delete. Immutable
		// receipts are removed only with their aged resolved parent in this transaction.
		for _, table := range []string{"task_event_dead_letters", "task_event_receipts"} {
			if _, e = q.Exec(ctx, statement(q, "DELETE FROM "+table+" WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND task_id=$4 AND event_id=$5", "DELETE FROM "+table+" WHERE tenant_id=? AND workspace_id=? AND node_id=? AND task_id=? AND event_id=?", value.Tenant, value.WorkspaceID, value.NodeID, value.TaskID, value.ID)); e != nil {
				return e
			}
		}
		count, e := q.Exec(ctx, statement(q, "DELETE FROM task_event_outbox WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4", "DELETE FROM task_event_outbox WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?", value.Tenant, value.WorkspaceID, value.NodeID, value.ID))
		if e != nil {
			return e
		}
		if count != 1 {
			return fail("TASK_EVENT_SOURCE_CHANGED")
		}
		if e = audit(ctx, q, value.Tenant, value.WorkspaceID, "", "system", "task_event.history_removed", value.ID, "Removed exact resolved terminal transport history after configured retention", uuid.NewString()); e != nil {
			return e
		}
		worked = true
		return nil
	})
	return worked, safe(err)
}
