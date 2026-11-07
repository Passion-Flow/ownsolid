package nodes

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// InferenceObserver keeps resource ownership with deployments inside node authority transactions.
type InferenceObserver interface {
	ObserveInference(context.Context, pgx.Tx, Node, wire.InferenceObservation) error
	FenceInferenceNode(context.Context, transaction.Executor, Node) error
	RecordInferenceResult(context.Context, pgx.Tx, Node, Task, wire.Result) error
}

// InferenceObserverWithin is implemented by the actual selected deployment owner.
type InferenceObserverWithin interface {
	ObserveInferenceWithin(context.Context, transaction.Executor, Node, wire.InferenceObservation) error
	RecordInferenceResultWithin(context.Context, transaction.Executor, Node, Task, wire.Result) error
}

// CreateInferenceTaskBound serializes node work and commits resource intent and audit atomically.
func (s *Service) CreateInferenceTaskBound(ctx context.Context, a identity.Auth, id, operation, key, reason, request string, fingerprint []byte, build func(pgx.Tx, Node) (wire.InferenceCommand, error), record func(pgx.Tx, Node, Task, wire.InferenceCommand) error) (Task, error) {
	if build == nil || record == nil {
		return Task{}, fail("VALIDATION_FAILED")
	}
	var out Task
	e := s.postgresTaskTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q transaction.Executor) error {
		var e error
		out, e = s.CreateInferenceTaskBoundWithin(ctx, q, a, id, operation, key, reason, request, fingerprint,
			func(_ context.Context, _ transaction.Executor, n Node) (wire.InferenceCommand, error) {
				return build(tx, n)
			},
			func(_ context.Context, _ transaction.Executor, n Node, t Task, c wire.InferenceCommand) error {
				return record(tx, n, t, c)
			})
		return e
	})
	if e != nil {
		return Task{}, e
	}
	return out, nil
}

// Within borrows the real owner transaction; no commit, callback emulation or separate pool.
func (s *Service) CreateInferenceTaskBoundWithin(ctx context.Context, q transaction.Executor, a identity.Auth, id, operation, key, reason, request string, fingerprint []byte, build func(context.Context, transaction.Executor, Node) (wire.InferenceCommand, error), record func(context.Context, transaction.Executor, Node, Task, wire.InferenceCommand) error) (Task, error) {
	if !wire.IsInferenceOperation(operation) || len(fingerprint) != 32 || !nodeID(key) || !nodeID(request) || !workspaces.ValidReason(reason) || build == nil || record == nil {
		return Task{}, fail("VALIDATION_FAILED")
	}
	if e := s.taskExecutor(ctx, q); e != nil {
		return Task{}, e
	}
	if !isAdmin(a) {
		return Task{}, fail("ACCESS_DENIED")
	}
	if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
		return Task{}, e
	}
	n, e := s.taskNodeWithin(ctx, q, a, id)
	if e != nil {
		return Task{}, e
	}
	if !wire.IsInferenceProfile(n.ExecutionProfile) || !nodeOperationAllowed(n, operation) {
		return Task{}, fail("AGENT_PROFILE_REJECTED")
	}
	previous, e := s.taskReplayWithin(ctx, q, n, key)
	if e == nil {
		var original []byte
		if e = q.QueryRow(ctx, nodeSQL(q, "SELECT request_fingerprint FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4", "SELECT request_fingerprint FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?", `SELECT request_fingerprint FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, n.TenantID, n.WorkspaceID, n.ID, previous.ID)).Scan(&original); e != nil {
			return Task{}, e
		}
		if previous.Operation != operation || subtle.ConstantTimeCompare(original, fingerprint) != 1 {
			return Task{}, fail("IDEMPOTENCY_CONFLICT")
		}
		return previous, nil
	}
	if e.Error() != "RESOURCE_NOT_FOUND" {
		return Task{}, e
	}
	if e = s.taskIdleWithin(ctx, q, n, request); e != nil {
		return Task{}, e
	}
	command, e := build(ctx, q, n)
	if e != nil {
		return Task{}, e
	}
	if e = validateNodeInferenceCommand(n, command, operation); e != nil {
		return Task{}, e
	}
	seconds := s.Identity.Config.Policy().Nodes.TaskSeconds
	if command.Action == "deploy" {
		seconds = s.Identity.Config.Policy().Nodes.InferenceDeploySeconds
	}
	payload, e := json.Marshal(command)
	if e != nil {
		return Task{}, fail("VALIDATION_FAILED")
	}
	var epoch *int64
	if operation == wire.DerivedInferenceOperation {
		value := a.Epoch
		epoch = &value
	}
	out, e := s.insertTaskWithin(ctx, q, a, n, operation, key, payload, fingerprint, seconds, epoch)
	if e != nil {
		return Task{}, e
	}
	if e = record(ctx, q, n, out, command); e != nil {
		return Task{}, e
	}
	// What a person asked of a deployment belongs to its history; a read of the node's GPUs has
	// no deployment, so it stays on its task.
	targetType, target := "node_task", out.ID
	if command.DeploymentID != "" {
		targetType, target = "deployment", command.DeploymentID
	}
	if e = s.auditTargetWithin(ctx, q, a.Me.User.ID, "admin", n, "inference."+command.Action+"_requested", targetType, target, reason, request); e != nil {
		return Task{}, e
	}
	return out, nil
}

func canonicalID(value string) bool {
	parsed, e := uuid.Parse(value)
	return e == nil && parsed.String() == value
}

func (s *Service) ReadInferenceCommand(ctx context.Context, tx pgx.Tx, n Node, t Task) (wire.InferenceCommand, error) {
	if tx == nil {
		return wire.InferenceCommand{}, fail("STORAGE_UNAVAILABLE")
	}
	return s.ReadInferenceCommandWithin(ctx, transaction.PostgreSQLTransaction(tx), n, t)
}
func (s *Service) ReadInferenceCommandWithin(ctx context.Context, q transaction.Executor, n Node, t Task) (wire.InferenceCommand, error) {
	if e := s.taskExecutor(ctx, q); e != nil {
		return wire.InferenceCommand{}, e
	}
	if !wire.IsInferenceProfile(n.ExecutionProfile) || !nodeOperationAllowed(n, t.Operation) || t.TenantID != n.TenantID || t.WorkspaceID != n.WorkspaceID || t.NodeID != n.ID {
		return wire.InferenceCommand{}, fail("AGENT_PROFILE_REJECTED")
	}
	var payload []byte
	if e := q.QueryRow(ctx, nodeSQL(q, "SELECT payload FROM node_tasks WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND node_id=$4", "SELECT payload FROM node_tasks WHERE id=? AND tenant_id=? AND workspace_id=? AND node_id=?", `SELECT payload FROM node_tasks WHERE id=? AND tenant_id=? AND workspace_id=? AND node_id=?`, t.ID, n.TenantID, n.WorkspaceID, n.ID)).Scan(&payload); e != nil {
		return wire.InferenceCommand{}, e
	}
	var out wire.InferenceCommand
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || validateNodeInferenceCommand(n, out, t.Operation) != nil {
		return wire.InferenceCommand{}, fail("AGENT_OPERATION_UNSUPPORTED")
	}
	if e := s.checkDerivedTaskAuthorityWithin(ctx, q, n, t); e != nil {
		return wire.InferenceCommand{}, e
	}
	return out, nil
}
