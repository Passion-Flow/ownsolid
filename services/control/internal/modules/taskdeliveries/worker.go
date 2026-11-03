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

type candidate struct{ tenant, workspace, node, task, event string }
type publishClaim struct {
	value              storedDelivery
	claim              string
	expired, exhausted bool
	found              bool
	handled            bool
}

// Four bounded stages can remain after the SQL intent timestamp: claim commit,
// exact broker readback, immutable publication, and final SQL/audit commit.
const publishFenceStages = 4

func envelope(value storedDelivery) taskbus.Envelope {
	return taskbus.Envelope{DeliveryID: value.DeliveryID, Event: value.Event}
}
func nullable(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func (s *Service) nextCandidate(ctx context.Context, store transaction.Store) (candidate, error) {
	var c candidate
	err := store.QueryRow(ctx, statement(store, `SELECT tenant_id::text,workspace_id::text,node_id::text,task_id::text,id::text FROM task_event_outbox WHERE tenant_id=$1 AND namespace_id=$2 AND state IN('prepared','sent') AND not_before<=clock_timestamp() AND (claim_expires_at IS NULL OR claim_expires_at<=clock_timestamp()) ORDER BY created_at,id LIMIT 1`,
		`SELECT tenant_id,workspace_id,node_id,task_id,id FROM task_event_outbox WHERE tenant_id=? AND namespace_id=? AND state IN('prepared','sent') AND not_before<=UTC_TIMESTAMP(6) AND (claim_expires_at IS NULL OR claim_expires_at<=UTC_TIMESTAMP(6)) ORDER BY created_at,id LIMIT 1`, s.lifecycle.tenant, s.Configuration.NamespaceID)).Scan(&c.tenant, &c.workspace, &c.node, &c.task, &c.event)
	if err != nil {
		return candidate{}, err
	}
	for _, id := range []string{c.tenant, c.workspace, c.node, c.task, c.event} {
		if !taskbus.ValidID(id) {
			return candidate{}, fail("TASK_EVENT_INVALID")
		}
	}
	return c, nil
}
func (s *Service) candidateWithin(ctx context.Context, q transaction.Executor, c candidate) (storedDelivery, nodes.Node, nodes.Task, error) {
	n, t, sourceErr := s.Nodes.TaskEventSourceWithin(ctx, q, c.tenant, c.workspace, c.node, c.task)
	if sourceErr != nil && sourceErr.Error() != "NODE_UNAVAILABLE" {
		return storedDelivery{}, nodes.Node{}, nodes.Task{}, sourceErr
	}
	value, err := deliveryWithin(ctx, q, c.tenant, c.workspace, c.node, c.event, true)
	if err != nil {
		return storedDelivery{}, nodes.Node{}, nodes.Task{}, err
	}
	if value.TaskID != c.task || value.Namespace != s.Configuration.NamespaceID || value.ConsumerName != s.Configuration.Consumer {
		return storedDelivery{}, nodes.Node{}, nodes.Task{}, fail("TASK_EVENT_SOURCE_CHANGED")
	}
	if sourceErr != nil {
		return value, n, t, sourceErr
	}
	if t.Operation != value.Event.Operation || !t.CreatedAt.Equal(value.Event.TaskCreatedAt) {
		return value, n, t, fail("TASK_EVENT_SOURCE_CHANGED")
	}
	return value, n, t, nil
}
func (s *Service) claimPublish(ctx context.Context, store transaction.Store, c candidate) (publishClaim, error) {
	var out publishClaim
	err := store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		value, n, t, sourceErr := s.candidateWithin(ctx, q, c)
		if sourceErr != nil && (value.ID == "" || sourceErr.Error() != "NODE_UNAVAILABLE" && sourceErr.Error() != "TASK_EVENT_SOURCE_CHANGED") {
			return sourceErr
		}
		now, err := nowWithin(ctx, q)
		if err != nil {
			return err
		}
		if value.State != "prepared" && value.State != "sent" || value.NotBefore.After(now) || value.ClaimExpiresAt != nil && value.ClaimExpiresAt.After(now) {
			return nil
		}
		if sourceErr != nil || n.Status != "active" || t.State != "pending" && t.State != "running" {
			err := deadWithin(ctx, q, value, "publish", "TASK_EVENT_SOURCE_OBSOLETE", value.OutcomeUnknown)
			if err == nil {
				out.handled = true
			}
			return err
		}
		expired := !now.Before(value.ExpiresAt)
		if expired && value.State == "prepared" {
			err := deadWithin(ctx, q, value, "expiry", "TRANSPORT_RETENTION_EXPIRED", false)
			if err == nil {
				out.handled = true
			}
			return err
		}
		claim := uuid.NewString()
		nextAttempt := value.PublishAttempts
		if nextAttempt < s.Configuration.MaximumPublishAttempts+1 {
			nextAttempt++
		}
		expires := now.Add(time.Duration(publishFenceStages*s.Configuration.RequestSeconds+s.Configuration.PullSeconds) * time.Second)
		count, err := q.Exec(ctx, statement(q, `UPDATE task_event_outbox SET state='sent',outcome_unknown=true,claim_id=$1,claim_expires_at=$2,publish_attempts=$11,broker_identity=$3,updated_at=$4 WHERE tenant_id=$5 AND workspace_id=$6 AND node_id=$7 AND id=$8 AND generation=$9 AND delivery_id=$10`,
			`UPDATE task_event_outbox SET state='sent',outcome_unknown=1,claim_id=?,claim_expires_at=?,publish_attempts=?,broker_identity=?,updated_at=? WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND generation=? AND delivery_id=?`, publishClaimArgs(q, value, claim, expires, s.Transport.Identity(), now, nextAttempt)...))
		if err != nil {
			return err
		}
		if count != 1 {
			return fail("TASK_EVENT_SOURCE_CHANGED")
		}
		value.PublishAttempts = nextAttempt
		value.OutcomeUnknown = true
		identity := s.Transport.Identity()
		value.BrokerIdentity = &identity
		out = publishClaim{value: value, claim: claim, expired: expired, exhausted: value.PublishAttempts > s.Configuration.MaximumPublishAttempts, found: true}
		return audit(ctx, q, value.Tenant, value.WorkspaceID, "", "system", "task_event.publish_claimed", value.ID, "Persisted fenced immutable transport delivery claim", uuid.NewString())
	})
	if err != nil {
		return publishClaim{}, safe(err)
	}
	return out, nil
}

func publishClaimArgs(q transaction.Executor, value storedDelivery, claim string, expires time.Time, identity string, now time.Time, attempt int) []any {
	if q.Dialect() == transaction.MySQL {
		return []any{claim, expires, attempt, identity, now, value.Tenant, value.WorkspaceID, value.NodeID, value.ID, value.Generation, value.DeliveryID}
	}
	return []any{claim, expires, identity, now, value.Tenant, value.WorkspaceID, value.NodeID, value.ID, value.Generation, value.DeliveryID, attempt}
}

// ProcessNext emits only an actual committed immutable delivery intent. It never
// invokes a node claim, executes a command or holds business locks during NATS IO.
func (s *Service) ProcessNext(ctx context.Context) (worked bool, err error) {
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
	c, err := s.nextCandidate(bounded, store)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, safe(err)
	}
	claim, err := s.claimPublish(bounded, store, c)
	if err != nil {
		return false, err
	}
	if !claim.found {
		return claim.handled, nil
	}
	// Readback is always first: a missing broker ACK may follow actual storage or
	// consumption. Absence alone never proves the event was not processed earlier.
	publication, found, lookupErr := s.Transport.Lookup(run, envelope(claim.value))
	if lookupErr != nil {
		return false, s.persistPublishFailure(run, store, c, claim, lookupErr)
	}
	if !found {
		if claim.expired || claim.exhausted {
			code := "TASK_BUS_PUBLISH_EXHAUSTED"
			kind := "publish"
			if claim.expired {
				code = "TRANSPORT_RETENTION_EXPIRED"
				kind = "expiry"
			}
			return true, s.finishDead(run, store, c, claim, kind, code, true)
		}
		// Same logical event/delivery bytes can be safely replayed even beyond the
		// broker's duplicate window only because durable SQL inbox proof is retained.
		publication, err = s.Transport.Publish(run, envelope(claim.value))
		if err != nil {
			return false, s.persistPublishFailure(run, store, c, claim, err)
		}
	}
	err = s.finishPublication(run, store, c, claim, publication)
	if err != nil {
		return false, err
	}
	return true, nil
}
func (s *Service) publishFence(ctx context.Context, q transaction.Executor, c candidate, claim publishClaim) (storedDelivery, error) {
	value, n, t, err := s.candidateWithin(ctx, q, c)
	if err != nil {
		return storedDelivery{}, err
	}
	if value.Generation != claim.value.Generation || value.DeliveryID != claim.value.DeliveryID {
		return storedDelivery{}, fail("TASK_EVENT_SOURCE_CHANGED")
	}
	if value.State == "received" || value.State == "obsolete" {
		return value, nil
	}
	if value.State != "sent" || value.ClaimID == nil || *value.ClaimID != claim.claim {
		return storedDelivery{}, fail("TASK_EVENT_SOURCE_CHANGED")
	}
	now, err := nowWithin(ctx, q)
	if err != nil {
		return storedDelivery{}, err
	}
	if value.ClaimExpiresAt == nil || !now.Before(*value.ClaimExpiresAt) {
		return storedDelivery{}, fail("TASK_EVENT_SOURCE_CHANGED")
	}
	if n.Status != "active" || t.State != "pending" && t.State != "running" {
		return storedDelivery{}, fail("TASK_EVENT_SOURCE_OBSOLETE")
	}
	return value, nil
}
func (s *Service) finishPublication(ctx context.Context, store transaction.Store, c candidate, claim publishClaim, p taskbus.Publication) error {
	if p.Identity != s.Transport.Identity() || p.Stream != s.Configuration.Stream || p.Sequence == 0 {
		return fail("TASK_BUS_READBACK_CONFLICT")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(s.Configuration.RequestSeconds)*time.Second)
	defer cancel()
	return safe(store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error {
		value, err := s.publishFence(ctx, q, c, claim)
		if err != nil {
			return err
		}
		if value.State == "received" || value.State == "obsolete" {
			return nil
		}
		now, err := nowWithin(ctx, q)
		if err != nil {
			return err
		}
		sequence := taskbus.SequenceText(p.Sequence)
		value.BrokerSequence = &sequence
		if !now.Before(value.ExpiresAt) {
			return deadWithin(ctx, q, value, "expiry", "TRANSPORT_RETENTION_EXPIRED", false)
		}
		count, err := q.Exec(ctx, statement(q, `UPDATE task_event_outbox SET state='published',outcome_unknown=false,broker_sequence=CAST($1::text AS numeric),claim_id=NULL,claim_expires_at=NULL,error_code=NULL,updated_at=$2 WHERE tenant_id=$3 AND workspace_id=$4 AND node_id=$5 AND id=$6 AND generation=$7 AND claim_id=$8`,
			`UPDATE task_event_outbox SET state='published',outcome_unknown=0,broker_sequence=CAST(? AS DECIMAL(20,0)),claim_id=NULL,claim_expires_at=NULL,error_code=NULL,updated_at=? WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND generation=? AND claim_id=?`, sequence, now, value.Tenant, value.WorkspaceID, value.NodeID, value.ID, value.Generation, claim.claim))
		if err != nil {
			return err
		}
		if count != 1 {
			return fail("TASK_EVENT_SOURCE_CHANGED")
		}
		return audit(ctx, q, value.Tenant, value.WorkspaceID, "", "system", "task_event.published", value.ID, "Confirmed actual broker publication identity and sequence", uuid.NewString())
	}))
}
func (s *Service) persistPublishFailure(ctx context.Context, store transaction.Store, c candidate, claim publishClaim, cause error) error {
	if cause != nil && (cause.Error() == "TASK_BUS_READBACK_CONFLICT" || cause.Error() == "TASK_BUS_IDENTITY_CHANGED" || cause.Error() == "TASK_BUS_PROFILE_REJECTED") {
		if err := s.finishDead(ctx, store, c, claim, "publish", cause.Error(), true); err != nil {
			return err
		}
		return fail(cause.Error())
	}
	if claim.expired || claim.value.PublishAttempts >= s.Configuration.MaximumPublishAttempts {
		kind := "publish"
		if claim.expired {
			kind = "expiry"
		}
		if err := s.finishDead(ctx, store, c, claim, kind, "TASK_BUS_PUBLISH_UNKNOWN", true); err != nil {
			return err
		}
		return fail("TASK_BUS_PUBLISH_UNKNOWN")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(s.Configuration.RequestSeconds)*time.Second)
	defer cancel()
	err := store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error {
		value, err := s.publishFence(ctx, q, c, claim)
		if err != nil {
			return err
		}
		if value.State == "received" || value.State == "obsolete" {
			return nil
		}
		now, err := nowWithin(ctx, q)
		if err != nil {
			return err
		}
		count, err := q.Exec(ctx, statement(q, `UPDATE task_event_outbox SET error_code='TASK_BUS_PUBLISH_UNKNOWN',not_before=$1,claim_id=NULL,claim_expires_at=NULL,updated_at=$2 WHERE tenant_id=$3 AND workspace_id=$4 AND node_id=$5 AND id=$6 AND generation=$7 AND claim_id=$8`,
			`UPDATE task_event_outbox SET error_code='TASK_BUS_PUBLISH_UNKNOWN',not_before=?,claim_id=NULL,claim_expires_at=NULL,updated_at=? WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND generation=? AND claim_id=?`, now.Add(time.Duration(s.Configuration.RetrySeconds)*time.Second), now, value.Tenant, value.WorkspaceID, value.NodeID, value.ID, value.Generation, claim.claim))
		if err != nil {
			return err
		}
		if count != 1 {
			return fail("TASK_EVENT_SOURCE_CHANGED")
		}
		return audit(ctx, q, value.Tenant, value.WorkspaceID, "", "system", "task_event.publish_unknown", value.ID, "Retained uncertain publication for exact readback", uuid.NewString())
	})
	if err != nil {
		return safe(err)
	}
	_ = cause // Raw provider errors never become stored reasons or response payloads.
	return fail("TASK_BUS_PUBLISH_UNKNOWN")
}
func (s *Service) finishDead(ctx context.Context, store transaction.Store, c candidate, claim publishClaim, kind, code string, unknown bool) error {
	bounded, cancel := context.WithTimeout(ctx, time.Duration(s.Configuration.RequestSeconds)*time.Second)
	defer cancel()
	return safe(store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error {
		value, err := s.publishFence(ctx, q, c, claim)
		if err != nil {
			return err
		}
		if value.State == "received" || value.State == "obsolete" {
			return nil
		}
		return deadWithin(ctx, q, value, kind, code, unknown)
	}))
}

func deadWithin(ctx context.Context, q transaction.Executor, value storedDelivery, kind, code string, unknown bool) error {
	raw, err := envelope(value).Canonical()
	if err != nil {
		return err
	}
	hash := digest(raw)
	var previous, priorHash, priorCode string
	err = q.QueryRow(ctx, statement(q, "SELECT id::text,envelope_sha256,error_code FROM task_event_dead_letters WHERE event_id=$1 AND generation=$2 FOR UPDATE", "SELECT id,envelope_sha256,error_code FROM task_event_dead_letters WHERE event_id=? AND generation=? FOR UPDATE", value.ID, value.Generation)).Scan(&previous, &priorHash, &priorCode)
	inserted := false
	if err == nil {
		if priorHash != hash {
			return fail("TASK_EVENT_SOURCE_CHANGED")
		}
		code = priorCode
		if value.State == "dead" {
			return nil
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		now, e := nowWithin(ctx, q)
		if e != nil {
			return e
		}
		if value.BrokerIdentity == nil {
			return fail("TASK_BUS_IDENTITY_CHANGED")
		}
		identity := *value.BrokerIdentity
		_, err = q.Exec(ctx, statement(q, `INSERT INTO task_event_dead_letters(id,event_id,tenant_id,workspace_id,node_id,task_id,generation,delivery_id,kind,outcome_unknown,broker_identity,broker_sequence,consumer_name,envelope_sha256,error_code,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,CAST($12::text AS numeric),$13,$14,$15,$16)`,
			`INSERT INTO task_event_dead_letters(id,event_id,tenant_id,workspace_id,node_id,task_id,generation,delivery_id,kind,outcome_unknown,broker_identity,broker_sequence,consumer_name,envelope_sha256,error_code,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,CAST(? AS DECIMAL(20,0)),?,?,?,?)`, uuid.NewString(), value.ID, value.Tenant, value.WorkspaceID, value.NodeID, value.TaskID, value.Generation, value.DeliveryID, kind, unknown, identity, nullable(value.BrokerSequence), value.ConsumerName, hash, code, now))
		if err != nil {
			return err
		}
		inserted = true
	} else {
		return err
	}
	count, err := q.Exec(ctx, statement(q, `UPDATE task_event_outbox SET state='dead',outcome_unknown=$1,error_code=$2,broker_sequence=CAST($3::text AS numeric),claim_id=NULL,claim_expires_at=NULL,updated_at=clock_timestamp() WHERE tenant_id=$4 AND workspace_id=$5 AND node_id=$6 AND id=$7 AND generation=$8 AND state NOT IN('received','obsolete')`,
		`UPDATE task_event_outbox SET state='dead',outcome_unknown=?,error_code=?,broker_sequence=CAST(? AS DECIMAL(20,0)),claim_id=NULL,claim_expires_at=NULL,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND generation=? AND state NOT IN('received','obsolete')`, unknown, code, nullable(value.BrokerSequence), value.Tenant, value.WorkspaceID, value.NodeID, value.ID, value.Generation))
	if err != nil {
		return err
	}
	if count != 1 {
		return fail("TASK_EVENT_SOURCE_CHANGED")
	}
	if inserted {
		return audit(ctx, q, value.Tenant, value.WorkspaceID, "", "system", "task_event.dead_lettered", value.ID, "Retained exact transport failure without physical task mutation", uuid.NewString())
	}
	return nil
}
