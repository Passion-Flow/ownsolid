package taskdeliveries

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

// ConsumeNext processes only an immutable delivery event. It never calls the
// machine claim/result API, starts a process or changes a physical node lease.
func (s *Service) ConsumeNext(ctx context.Context) (worked bool, err error) {
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
	message, err := s.Transport.Next(run)
	if err != nil {
		return false, safe(err)
	}
	if message == nil {
		return false, nil
	}
	store, err := s.repository()
	if err != nil {
		return false, err
	}
	envelope, decodeErr := taskbus.DecodeEnvelope(message.Body)
	if decodeErr != nil || envelope.Event.NamespaceID != s.Configuration.NamespaceID || envelope.Event.TenantID != s.lifecycle.tenant || !envelope.MatchesSubject(message.Subject) {
		err = s.quarantine(run, store, message, "TASK_EVENT_ENVELOPE_REJECTED")
	} else {
		err = s.consumeEnvelope(run, store, message, envelope)
	}
	if err != nil {
		// This is a delayed negative acknowledgment, never a positive processing ACK.
		// Unknown SQL commit is resolved by durable receipt/DLQ readback on redelivery.
		_ = message.Delay(run)
		return false, safe(err)
	}
	if err = message.Confirm(run); err != nil {
		return false, safe(err)
	}
	return true, nil
}
func (s *Service) consumeEnvelope(ctx context.Context, store transaction.Store, m *taskbus.Message, e taskbus.Envelope) error {
	bounded, cancel := context.WithTimeout(ctx, time.Duration(s.Configuration.RequestSeconds)*time.Second)
	defer cancel()
	return safe(store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error {
		c := candidate{tenant: e.Event.TenantID, workspace: e.Event.WorkspaceID, node: e.Event.NodeID, task: e.Event.TaskID, event: e.Event.ID}
		value, n, t, sourceErr := s.candidateWithin(ctx, q, c)
		if sourceErr != nil && value.ID == "" {
			if errors.Is(sourceErr, sql.ErrNoRows) || sourceErr.Error() == "RESOURCE_NOT_FOUND" {
				return s.quarantineWithin(ctx, q, m, "TASK_EVENT_UNCOMMITTED_SOURCE")
			}
			return sourceErr
		}
		actualHash, err := e.Event.Digest()
		if err != nil {
			return err
		}
		if value.LogicalSHA256 != actualHash || value.Event != e.Event {
			return s.quarantineWithin(ctx, q, m, "TASK_EVENT_ENVELOPE_REJECTED")
		}
		if m.Identity != s.Transport.Identity() || m.Stream != s.Configuration.Stream || m.Consumer != s.Configuration.Consumer {
			return fail("TASK_BUS_IDENTITY_CHANGED")
		}
		// A prior logical receipt remains proof across all transport generations and
		// ACK loss. Verify it without another receipt/audit/business side effect.
		var priorHash, priorConsumer string
		err = q.QueryRow(ctx, statement(q, "SELECT logical_sha256,consumer_name FROM task_event_receipts WHERE event_id=$1 FOR UPDATE", "SELECT logical_sha256,consumer_name FROM task_event_receipts WHERE event_id=? FOR UPDATE", value.ID)).Scan(&priorHash, &priorConsumer)
		if err == nil {
			if priorHash != value.LogicalSHA256 || priorConsumer != s.Configuration.Consumer {
				return fail("TASK_EVENT_RECEIPT_CONFLICT")
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if value.DeliveryID != e.DeliveryID {
			// Do not install a logical inbox receipt for an obsolete generation and
			// thereby prevent a valid new generation from being consumed. Existing exact
			// old DLQ evidence is sufficient; otherwise retain an unscoped quarantine.
			var retainedHash string
			err = q.QueryRow(ctx, statement(q, "SELECT envelope_sha256 FROM task_event_dead_letters WHERE event_id=$1 AND envelope_sha256=$2 FOR UPDATE", "SELECT envelope_sha256 FROM task_event_dead_letters WHERE event_id=? AND envelope_sha256=? FOR UPDATE", value.ID, digest(m.Body))).Scan(&retainedHash)
			if err == nil {
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			return s.quarantineWithin(ctx, q, m, "TASK_EVENT_STALE_DELIVERY")
		}
		if value.State == "prepared" || value.BrokerIdentity == nil || *value.BrokerIdentity != m.Identity {
			return s.quarantineWithin(ctx, q, m, "TASK_EVENT_UNCOMMITTED_SOURCE")
		}
		sequence := taskbus.SequenceText(m.Sequence)
		value.BrokerSequence = &sequence
		if err = s.recordConsumeCount(ctx, q, value, m.Deliveries); err != nil {
			return err
		}
		if sourceErr != nil {
			if sourceErr.Error() != "NODE_UNAVAILABLE" && sourceErr.Error() != "TASK_EVENT_SOURCE_CHANGED" {
				return sourceErr
			}
			return deadWithin(ctx, q, value, "consume", "TASK_EVENT_SOURCE_OBSOLETE", false)
		}
		now, err := nowWithin(ctx, q)
		if err != nil {
			return err
		}
		if !now.Before(value.ExpiresAt) {
			return deadWithin(ctx, q, value, "expiry", "TRANSPORT_RETENTION_EXPIRED", false)
		}
		// The configured threshold bounds allowed processing attempts; the following
		// able redelivery can still commit a SQL DLQ when all previous attempts crashed
		// or SQL was unavailable. Native unlimited is not unlimited application work.
		if m.Deliveries > uint64(s.Configuration.MaximumDeliveries) {
			return deadWithin(ctx, q, value, "consume", "TASK_BUS_CONSUME_EXHAUSTED", false)
		}
		outcome := "received"
		if n.Status != "active" || t.State != "pending" && t.State != "running" {
			outcome = "obsolete"
		}
		count, err := q.Exec(ctx, statement(q, `INSERT INTO task_event_receipts(id,event_id,tenant_id,workspace_id,node_id,task_id,logical_sha256,delivery_id,generation,consumer_name,broker_identity,broker_sequence,consumer_sequence,outcome,task_state,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,CAST($12::text AS numeric),CAST($13::text AS numeric),$14,$15,$16)`,
			`INSERT INTO task_event_receipts(id,event_id,tenant_id,workspace_id,node_id,task_id,logical_sha256,delivery_id,generation,consumer_name,broker_identity,broker_sequence,consumer_sequence,outcome,task_state,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,CAST(? AS DECIMAL(20,0)),CAST(? AS DECIMAL(20,0)),?,?,?)`, uuid.NewString(), value.ID, value.Tenant, value.WorkspaceID, value.NodeID, value.TaskID, value.LogicalSHA256, value.DeliveryID, value.Generation, s.Configuration.Consumer, m.Identity, sequence, taskbus.SequenceText(m.ConsumerSequence), outcome, t.State, now))
		if err != nil {
			return err
		}
		if count != 1 {
			return fail("STORAGE_UNAVAILABLE")
		}
		count, err = q.Exec(ctx, statement(q, `UPDATE task_event_outbox SET state=$1,outcome_unknown=false,broker_sequence=CAST($2::text AS numeric),claim_id=NULL,claim_expires_at=NULL,error_code=NULL,updated_at=$3 WHERE tenant_id=$4 AND workspace_id=$5 AND node_id=$6 AND id=$7 AND generation=$8 AND delivery_id=$9`,
			`UPDATE task_event_outbox SET state=?,outcome_unknown=0,broker_sequence=CAST(? AS DECIMAL(20,0)),claim_id=NULL,claim_expires_at=NULL,error_code=NULL,updated_at=? WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND generation=? AND delivery_id=?`, outcome, sequence, now, value.Tenant, value.WorkspaceID, value.NodeID, value.ID, value.Generation, value.DeliveryID))
		if err != nil {
			return err
		}
		if count != 1 {
			return fail("TASK_EVENT_SOURCE_CHANGED")
		}
		return audit(ctx, q, value.Tenant, value.WorkspaceID, "", "system", "task_event."+outcome, value.ID, "Confirmed one immutable logical task event receipt", uuid.NewString())
	}))
}
func (s *Service) recordConsumeCount(ctx context.Context, q transaction.Executor, value storedDelivery, observed uint64) error {
	maximum := uint64(s.Configuration.MaximumDeliveries + 1)
	if observed > maximum {
		observed = maximum
	}
	_, err := q.Exec(ctx, statement(q, `UPDATE task_event_outbox SET consume_attempts=GREATEST(consume_attempts,$1) WHERE tenant_id=$2 AND workspace_id=$3 AND node_id=$4 AND id=$5 AND generation=$6 AND delivery_id=$7`,
		`UPDATE task_event_outbox SET consume_attempts=GREATEST(consume_attempts,?) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND generation=? AND delivery_id=?`, int64(observed), value.Tenant, value.WorkspaceID, value.NodeID, value.ID, value.Generation, value.DeliveryID))
	return err
}
func (s *Service) quarantine(ctx context.Context, store transaction.Store, m *taskbus.Message, code string) error {
	bounded, cancel := context.WithTimeout(ctx, time.Duration(s.Configuration.RequestSeconds)*time.Second)
	defer cancel()
	return safe(store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error { return s.quarantineWithin(ctx, q, m, code) }))
}
func (s *Service) quarantineWithin(ctx context.Context, q transaction.Executor, m *taskbus.Message, code string) error {
	if m == nil || m.Identity != s.Transport.Identity() || m.Stream != s.Configuration.Stream || m.Consumer != s.Configuration.Consumer || m.Sequence == 0 || len(m.Body) > taskbus.MaximumTransportBytes {
		return fail("TASK_BUS_MESSAGE_INVALID")
	}
	var tenant string
	err := q.QueryRow(ctx, statement(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete FOR SHARE", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1 FOR SHARE")).Scan(&tenant)
	if err != nil {
		return err
	}
	if tenant != s.lifecycle.tenant {
		return fail("TASK_BUS_IDENTITY_CHANGED")
	}
	hash := digest(m.Body)
	sequence := taskbus.SequenceText(m.Sequence)
	var priorHash string
	err = q.QueryRow(ctx, statement(q, "SELECT envelope_sha256 FROM task_event_dead_letters WHERE broker_identity=$1 AND broker_sequence=CAST($2::text AS numeric) AND consumer_name=$3 FOR UPDATE", "SELECT envelope_sha256 FROM task_event_dead_letters WHERE broker_identity=? AND broker_sequence=CAST(? AS DECIMAL(20,0)) AND consumer_name=? FOR UPDATE", m.Identity, sequence, m.Consumer)).Scan(&priorHash)
	if err == nil {
		if hash != priorHash {
			return fail("TASK_BUS_READBACK_CONFLICT")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now, err := nowWithin(ctx, q)
	if err != nil {
		return err
	}
	id := uuid.NewString()
	count, err := q.Exec(ctx, statement(q, `INSERT INTO task_event_dead_letters(id,tenant_id,kind,outcome_unknown,broker_identity,broker_sequence,consumer_name,envelope_sha256,error_code,created_at) VALUES($1,$2,'poison',false,$3,CAST($4::text AS numeric),$5,$6,$7,$8)`,
		`INSERT INTO task_event_dead_letters(id,tenant_id,kind,outcome_unknown,broker_identity,broker_sequence,consumer_name,envelope_sha256,error_code,created_at) VALUES(?,?,'poison',0,?,CAST(? AS DECIMAL(20,0)),?,?,?,?)`, id, tenant, m.Identity, sequence, m.Consumer, hash, code, now))
	if err != nil {
		return err
	}
	if count != 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	_, err = q.Exec(ctx, statement(q, `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,'system','task_event.poison_quarantined','success','task_event_dead_letter',$3,'Retained exact broker coordinate and hash without message body',$4)`,
		`INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,'system','task_event.poison_quarantined','success','task_event_dead_letter',?,'Retained exact broker coordinate and hash without message body',?)`, uuid.NewString(), tenant, id, uuid.NewString()))
	return err
}
