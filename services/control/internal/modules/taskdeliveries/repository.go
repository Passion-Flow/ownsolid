package taskdeliveries

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

func fail(code string) error { return errors.New(code) }
func vendorStatement(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, args...)
	default:
		return transaction.Statement{}
	}
}
func statement(q transaction.Executor, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, args...)
	}
	return transaction.SQL(transaction.PostgreSQL, pg, args...)
}
func safe(err error) error {
	if err == nil {
		return nil
	}
	if kind, ok := transaction.CommitOutcome(err); ok {
		return fail(string(kind))
	}
	if transaction.IsCancellationOnly(err) {
		if errors.Is(err, context.Canceled) {
			return fail(string(transaction.Cancelled))
		}
		return fail(string(transaction.Timeout))
	}
	switch err.Error() {
	case "ACCESS_DENIED", "AUTH_SESSION_EXPIRED", "RESOURCE_NOT_FOUND", "VALIDATION_FAILED", "INVALID_CURSOR", "IDEMPOTENCY_CONFLICT", "NODE_UNAVAILABLE", "NODE_STORAGE_MISMATCH",
		"TASK_EVENT_INVALID", "TASK_EVENT_SOURCE_CHANGED", "TASK_EVENT_SOURCE_OBSOLETE", "TASK_EVENT_RECEIPT_CONFLICT", "TASK_BUS_MESSAGE_INVALID", "TASK_BUS_PUBLISH_EXHAUSTED", "TASK_BUS_CONSUME_EXHAUSTED", "TRANSPORT_RETENTION_EXPIRED", "TASK_EVENT_EXPIRED", "TASK_EVENT_RETRY_REFUSED", "TASK_EVENT_LIMIT_EXCEEDED", "TASK_BUS_SCHEMA_REJECTED", "TASK_BUS_WORKER_ALREADY_RUNNING", "TASK_BUS_RECOVERY_EXHAUSTED", "TASK_BUS_NOT_ADMITTED", "TASK_BUS_COMPOSITION_INVALID", "TASK_BUS_IDENTITY_CHANGED", "TASK_BUS_PROFILE_REJECTED", "TASK_BUS_CLOSED", "TASK_BUS_PUBLISH_UNKNOWN", "TASK_BUS_ACK_UNKNOWN", "TASK_BUS_READBACK_CONFLICT", "TASK_BUS_UNAVAILABLE", "TASK_BUS_TIMEOUT", "TASK_BUS_CANCELLED",
		"STORAGE_UNAVAILABLE", "STORAGE_DEADLOCK", "STORAGE_LOCK_TIMEOUT", "STORAGE_CANCELLED", "STORAGE_TIMEOUT", "STORAGE_COMMIT_OUTCOME_UNKNOWN", "STORAGE_COMMIT_ROLLED_BACK":
		return fail(err.Error())
	default:
		return fail("STORAGE_UNAVAILABLE")
	}
}

// Stored codes are a fixed transport contract, never raw SDK/SQL diagnostics.
func storedErrorCode(code string) bool {
	switch code {
	case "TASK_BUS_READBACK_CONFLICT", "TASK_BUS_IDENTITY_CHANGED", "TASK_BUS_PROFILE_REJECTED", "TASK_BUS_PUBLISH_UNKNOWN", "TASK_EVENT_SOURCE_OBSOLETE", "TASK_EVENT_SOURCE_CHANGED", "TASK_BUS_PUBLISH_EXHAUSTED", "TASK_BUS_CONSUME_EXHAUSTED", "TRANSPORT_RETENTION_EXPIRED", "TASK_EVENT_ENVELOPE_REJECTED", "TASK_EVENT_UNCOMMITTED_SOURCE", "TASK_EVENT_STALE_DELIVERY", "TASK_BUS_RESTORE_FENCED":
		return true
	}
	return false
}
func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil || s.Workspaces == nil || s.Workspaces.Identity != s.Identity || s.Nodes == nil || s.Nodes.Identity != s.Identity || s.Nodes.Workspaces != s.Workspaces || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB || s.Nodes.DB != nil && s.Nodes.DB != s.Identity.DB || !s.Configuration.Enabled || s.Configuration.Validate() != nil || s.Transport == nil || !s.Transport.MatchesConfiguration(s.Configuration) {
		return nil, fail("TASK_BUS_COMPOSITION_INVALID")
	}
	return s.Identity.Repository()
}
func digest(raw []byte) string   { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func admin(a identity.Auth) bool { return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin }
func nowWithin(ctx context.Context, q transaction.Executor) (time.Time, error) {
	var now time.Time
	err := q.QueryRow(ctx, statement(q, "SELECT clock_timestamp()", "SELECT UTC_TIMESTAMP(6)")).Scan(&now)
	if err != nil {
		return time.Time{}, err
	}
	return now.UTC().Truncate(time.Microsecond), nil
}
func audit(ctx context.Context, q transaction.Executor, tenant, workspace, actor, audience, action, target, reason, request string) error {
	_, err := q.Exec(ctx, statement(q, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),NULLIF($2,'')::uuid,$3,NULLIF($4,'')::uuid,$5,$6,'success','task_event',$7,$8,$9)`,
		`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),NULLIF(?,''),?,NULLIF(?,''),?,?,'success','task_event',?,?,?)`, uuid.NewString(), actor, tenant, workspace, audience, action, target, reason, request))
	return err
}

const pgFields = `id::text,tenant_id::text,workspace_id::text,node_id::text,task_id::text,namespace_id::text,event_kind,operation,event_bytes,logical_sha256,delivery_id::text,generation,state,outcome_unknown,publish_attempts,consume_attempts,claim_id::text,claim_expires_at,broker_identity,broker_sequence::text,consumer_name,error_code,not_before,expires_at,created_at,updated_at`
const myFields = `id,tenant_id,workspace_id,node_id,task_id,namespace_id,event_kind,operation,event_bytes,logical_sha256,delivery_id,generation,state,outcome_unknown,publish_attempts,consume_attempts,claim_id,claim_expires_at,broker_identity,CAST(broker_sequence AS CHAR),consumer_name,error_code,not_before,expires_at,created_at,updated_at`

func scanDelivery(row transaction.Row) (storedDelivery, error) {
	var value storedDelivery
	var raw []byte
	var kind, operation string
	err := row.Scan(&value.ID, &value.Tenant, &value.WorkspaceID, &value.NodeID, &value.TaskID, &value.Namespace, &kind, &operation, &raw, &value.LogicalSHA256, &value.DeliveryID, &value.Generation, &value.State, &value.OutcomeUnknown, &value.PublishAttempts, &value.ConsumeAttempts, &value.ClaimID, &value.ClaimExpiresAt, &value.BrokerIdentity, &value.BrokerSequence, &value.ConsumerName, &value.ErrorCode, &value.NotBefore, &value.ExpiresAt, &value.CreatedAt, &value.UpdatedAt)
	if err != nil {
		return storedDelivery{}, err
	}
	if value.BrokerIdentity == nil || len(*value.BrokerIdentity) != 64 || digestHex(*value.BrokerIdentity) == false || !taskbus.ValidResourceName(value.ConsumerName) || (value.ClaimID == nil) != (value.ClaimExpiresAt == nil) || value.ClaimID != nil && !taskbus.ValidID(*value.ClaimID) || value.OutcomeUnknown && value.State != "sent" && value.State != "dead" {
		return storedDelivery{}, fail("TASK_EVENT_INVALID")
	}
	if value.BrokerSequence != nil {
		seq, e := strconv.ParseUint(*value.BrokerSequence, 10, 64)
		if e != nil || seq == 0 || strconv.FormatUint(seq, 10) != *value.BrokerSequence {
			return storedDelivery{}, fail("TASK_EVENT_INVALID")
		}
	} else if value.State == "published" || value.State == "received" || value.State == "obsolete" {
		return storedDelivery{}, fail("TASK_EVENT_INVALID")
	}
	if value.ErrorCode != nil && !storedErrorCode(*value.ErrorCode) {
		return storedDelivery{}, fail("TASK_EVENT_INVALID")
	}
	if len(raw) > taskbus.MaximumEnvelopeBytes || json.Unmarshal(raw, &value.Event) != nil {
		return storedDelivery{}, fail("TASK_EVENT_INVALID")
	}
	canonical, err := value.Event.Canonical()
	if err != nil || !bytes.Equal(raw, canonical) || value.Event.Kind != kind || value.Event.Operation != operation || digest(raw) != value.LogicalSHA256 || value.Event.ID != value.ID || value.Event.NamespaceID != value.Namespace || value.Event.TenantID != value.Tenant || value.Event.WorkspaceID != value.WorkspaceID || value.Event.NodeID != value.NodeID || value.Event.TaskID != value.TaskID || !taskbus.ValidID(value.DeliveryID) || value.Generation < 1 || value.PublishAttempts < 0 || value.ConsumeAttempts < 0 {
		return storedDelivery{}, fail("TASK_EVENT_INVALID")
	}
	switch value.State {
	case "prepared", "sent", "published", "received", "obsolete", "dead":
	default:
		return storedDelivery{}, fail("TASK_EVENT_INVALID")
	}
	value.CreatedAt = value.CreatedAt.UTC()
	value.UpdatedAt = value.UpdatedAt.UTC()
	value.ExpiresAt = value.ExpiresAt.UTC()
	value.NotBefore = value.NotBefore.UTC()
	if !value.CreatedAt.Equal(value.Event.CreatedAt) || !value.ExpiresAt.After(value.CreatedAt) || value.UpdatedAt.Before(value.CreatedAt) {
		return storedDelivery{}, fail("TASK_EVENT_INVALID")
	}
	value.Operation = value.Event.Operation
	return value, nil
}
func deliveryWithin(ctx context.Context, q transaction.Executor, tenant, workspace, node, event string, lock bool) (storedDelivery, error) {
	pg, my := "SELECT "+pgFields+" FROM task_event_outbox WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4", "SELECT "+myFields+" FROM task_event_outbox WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?"
	if lock {
		pg += " FOR UPDATE"
		my += " FOR UPDATE"
	}
	return scanDelivery(q.QueryRow(ctx, statement(q, pg, my, tenant, workspace, node, event)))
}
func (s *Service) sourceWithin(ctx context.Context, q transaction.Executor, value storedDelivery) (nodes.Task, error) {
	_, task, err := s.Nodes.TaskEventSourceWithin(ctx, q, value.Tenant, value.WorkspaceID, value.NodeID, value.TaskID)
	if err != nil {
		return nodes.Task{}, err
	}
	if value.Namespace != s.Configuration.NamespaceID || value.ConsumerName != s.Configuration.Consumer || task.Operation != value.Event.Operation || !task.CreatedAt.Equal(value.Event.TaskCreatedAt) {
		return nodes.Task{}, fail("TASK_EVENT_SOURCE_CHANGED")
	}
	return task, nil
}

func digestHex(value string) bool {
	_, e := hex.DecodeString(value)
	return e == nil && len(value) == 64 && value == string(bytes.ToLower([]byte(value)))
}
