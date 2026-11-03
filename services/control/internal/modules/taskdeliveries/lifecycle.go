package taskdeliveries

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

type lifecycle struct {
	mu            sync.Mutex
	active        sync.WaitGroup
	stopped       bool
	running       bool
	workerFailure string
	ctx           context.Context
	cancel        context.CancelFunc
	configuration string
	identity      *identity.Service
	workspaces    *workspaces.Service
	nodes         *nodes.Service
	transport     *taskbus.JetStream
	tenant        string
}

func configurationDigest(c taskbus.Configuration) string {
	raw, _ := json.Marshal(c)
	return digest(raw)
}

// NewService borrows product owners and takes transport ownership only after real
// schema/source/profile admission. Failure leaves transport cleanup to its caller.
func NewService(ctx context.Context, i *identity.Service, w *workspaces.Service, n *nodes.Service, c taskbus.Configuration, b *taskbus.JetStream) (*Service, error) {
	if ctx == nil {
		return nil, fail("TASK_BUS_COMPOSITION_INVALID")
	}
	c.DialAddresses = append([]string(nil), c.DialAddresses...)
	s := &Service{Identity: i, Workspaces: w, Nodes: n, Configuration: c, Transport: b}
	if _, err := s.repository(); err != nil {
		return nil, err
	}
	tenant, err := s.verifyStored(ctx)
	if err != nil {
		return nil, err
	}
	if err = b.Verify(ctx); err != nil {
		return nil, safe(err)
	}
	run, cancel := context.WithCancel(context.Background())
	s.lifecycle = &lifecycle{ctx: run, cancel: cancel, configuration: configurationDigest(c), identity: i, workspaces: w, nodes: n, transport: b, tenant: tenant}
	return s, nil
}
func (s *Service) begin(ctx context.Context) (context.Context, func(), error) {
	if s == nil || s.lifecycle == nil || ctx == nil {
		return nil, nil, fail("TASK_BUS_NOT_ADMITTED")
	}
	l := s.lifecycle
	l.mu.Lock()
	if l.stopped || l.configuration != configurationDigest(s.Configuration) || l.identity != s.Identity || l.workspaces != s.Workspaces || l.nodes != s.Nodes || l.transport != s.Transport || s.Nodes.TaskEvents != s {
		l.mu.Unlock()
		return nil, nil, fail("TASK_BUS_NOT_ADMITTED")
	}
	l.active.Add(1)
	l.mu.Unlock()
	run, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(l.ctx, cancel)
	return run, func() { stop(); cancel(); l.active.Done() }, nil
}
func (s *Service) Close(ctx context.Context) error {
	if s == nil || s.lifecycle == nil || ctx == nil {
		return fail("TASK_BUS_NOT_ADMITTED")
	}
	l := s.lifecycle
	l.mu.Lock()
	l.stopped = true
	l.cancel()
	l.mu.Unlock()
	done := make(chan struct{})
	go func() { l.active.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return safe(ctx.Err())
	case <-done:
	}
	return safe(l.transport.Close())
}
func (s *Service) VerifyStored(ctx context.Context) error {
	run, done, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer done()
	tenant, err := s.verifyStored(run)
	if err != nil {
		return err
	}
	if tenant != s.lifecycle.tenant {
		return fail("TASK_BUS_IDENTITY_CHANGED")
	}
	return safe(s.Transport.Verify(run))
}
func (s *Service) verifyStored(ctx context.Context) (string, error) {
	store, err := s.repository()
	if err != nil {
		return "", err
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(s.Configuration.RequestSeconds)*time.Second)
	defer cancel()
	var tenant string
	err = store.Within(bounded, true, func(ctx context.Context, q transaction.Executor) error {
		if err := q.QueryRow(ctx, statement(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1")).Scan(&tenant); err != nil {
			return err
		}
		if !taskbus.ValidID(tenant) || s.Transport.Tenant() != tenant {
			return fail("TASK_BUS_COMPOSITION_INVALID")
		}
		for _, pair := range [][2]string{
			{"SELECT " + pgFields + " FROM task_event_outbox LIMIT 0", "SELECT " + myFields + " FROM task_event_outbox LIMIT 0"},
			{"SELECT id,event_id,logical_sha256,delivery_id,broker_identity,broker_sequence,consumer_sequence,outcome,task_state,created_at FROM task_event_receipts LIMIT 0", "SELECT id,event_id,logical_sha256,delivery_id,broker_identity,broker_sequence,consumer_sequence,outcome,task_state,created_at FROM task_event_receipts LIMIT 0"},
			{"SELECT id,event_id,delivery_id,kind,outcome_unknown,envelope_sha256,retry_actor,retry_key,retry_reason,retry_delivery_id,retry_generation,retried_at FROM task_event_dead_letters LIMIT 0", "SELECT id,event_id,delivery_id,kind,outcome_unknown,envelope_sha256,retry_actor,retry_key,retry_reason,retry_delivery_id,retry_generation,retried_at FROM task_event_dead_letters LIMIT 0"},
		} {
			rows, err := q.Query(ctx, statement(q, pair[0], pair[1]))
			if err != nil {
				return err
			}
			rows.Close()
			if err = rows.Err(); err != nil {
				return err
			}
		}
		if err := verifySchemaWithin(ctx, q); err != nil {
			return err
		}
		var invalid int64
		if err := q.QueryRow(ctx, statement(q, "SELECT count(*) FROM task_event_outbox WHERE tenant_id<>$1 OR namespace_id<>$2 OR (broker_identity<>$3 OR consumer_name<>$4)", "SELECT count(*) FROM task_event_outbox WHERE tenant_id<>? OR namespace_id<>? OR (broker_identity<>? OR consumer_name<>?)", tenant, s.Configuration.NamespaceID, s.Transport.Identity(), s.Configuration.Consumer)).Scan(&invalid); err != nil {
			return err
		}
		if invalid != 0 {
			return fail("TASK_BUS_IDENTITY_CHANGED")
		}
		if err := q.QueryRow(ctx, statement(q, `SELECT count(*) FROM task_event_outbox o LEFT JOIN task_event_receipts r ON r.tenant_id=o.tenant_id AND r.workspace_id=o.workspace_id AND r.node_id=o.node_id AND r.task_id=o.task_id AND r.event_id=o.id WHERE (o.state IN('received','obsolete') AND (r.id IS NULL OR r.logical_sha256 IS DISTINCT FROM o.logical_sha256 OR r.outcome IS DISTINCT FROM o.state OR r.delivery_id IS DISTINCT FROM o.delivery_id OR r.generation IS DISTINCT FROM o.generation OR r.broker_identity IS DISTINCT FROM o.broker_identity OR r.consumer_name IS DISTINCT FROM o.consumer_name OR r.broker_sequence IS DISTINCT FROM o.broker_sequence)) OR (r.id IS NOT NULL AND o.state NOT IN('received','obsolete'))`,
			`SELECT count(*) FROM task_event_outbox o LEFT JOIN task_event_receipts r ON r.tenant_id=o.tenant_id AND r.workspace_id=o.workspace_id AND r.node_id=o.node_id AND r.task_id=o.task_id AND r.event_id=o.id WHERE (o.state IN('received','obsolete') AND (r.id IS NULL OR NOT(r.logical_sha256<=>o.logical_sha256) OR NOT(r.outcome<=>o.state) OR NOT(r.delivery_id<=>o.delivery_id) OR NOT(r.generation<=>o.generation) OR NOT(r.broker_identity<=>o.broker_identity) OR NOT(r.consumer_name<=>o.consumer_name) OR NOT(r.broker_sequence<=>o.broker_sequence))) OR (r.id IS NOT NULL AND o.state NOT IN('received','obsolete'))`)).Scan(&invalid); err != nil {
			return err
		}
		if invalid != 0 {
			return fail("TASK_EVENT_RECEIPT_CONFLICT")
		}
		// Verify all retained canonical event bytes in a streaming cursor. Do not
		// manufacture readiness from a column-only probe or return partial rows.
		rows, err := q.Query(ctx, statement(q, "SELECT "+pgFields+" FROM task_event_outbox ORDER BY id", "SELECT "+myFields+" FROM task_event_outbox ORDER BY id"))
		if err != nil {
			return err
		}
		for rows.Next() {
			if _, err = scanDelivery(rows); err != nil {
				rows.Close()
				return err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		return s.verifyDeadLettersWithin(ctx, q, tenant)
	})
	if err != nil {
		return "", safe(err)
	}
	return tenant, nil
}
