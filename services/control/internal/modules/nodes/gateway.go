package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

// GatewayOwnerWithin keeps the model gateways of nodes with their owner, inside node
// authority transactions (GW-114, ADR 0052).
type GatewayOwnerWithin interface {
	// ActivateGatewayWithin binds the credential and key the agent generated for the gateway
	// its gateway.start task starts, and returns the gateway's serving certificate.
	ActivateGatewayWithin(ctx context.Context, q transaction.Executor, n Node, t Task, command wire.GatewayCommand, activation wire.GatewayActivation, request string) (wire.GatewayActivated, error)
	// RecordGatewayResultWithin records what the agent did with the gateway.
	RecordGatewayResultWithin(context.Context, transaction.Executor, Node, Task, wire.GatewayCommand, wire.Result) error
}

// CreateGatewayTaskWithin queues a gateway task on a serving node inside its owner's
// transaction. The node must be idle, as for every task an administrator asks for.
func (s *Service) CreateGatewayTaskWithin(ctx context.Context, q transaction.Executor, a identity.Auth, id string, command wire.GatewayCommand, reason, request string) (Task, error) {
	operation := wire.GatewayOperation(command.Action)
	if command.Validate(operation) != nil || !nodeID(id) || !nodeID(request) || !workspaces.ValidReason(reason) {
		return Task{}, fail("VALIDATION_FAILED")
	}
	if e := s.taskExecutor(ctx, q); e != nil {
		return Task{}, e
	}
	if q.Dialect() != transaction.PostgreSQL {
		return Task{}, fail("AGENT_PROFILE_REJECTED")
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
	if n.ExecutionProfile != wire.DockerInferenceProfile || !nodeOperationAllowed(n, operation) {
		return Task{}, fail("AGENT_PROFILE_REJECTED")
	}
	if e = s.taskIdleWithin(ctx, q, n, request); e != nil {
		return Task{}, e
	}
	payload, e := json.Marshal(command)
	if e != nil {
		return Task{}, fail("VALIDATION_FAILED")
	}
	task, e := s.insertTaskWithin(ctx, q, a.Me.User.ID, n, operation, uuid.NewString(), payload, digest("gateway-command", string(payload)), s.Identity.Config.Policy().Nodes.TaskSeconds, nil)
	if e != nil {
		return Task{}, e
	}
	if e = s.auditTargetWithin(ctx, q, a.Me.User.ID, "admin", n, "gateway."+command.Action+"_requested", "gateway_instance", command.GatewayID, reason, request); e != nil {
		return Task{}, e
	}
	return task, nil
}

// ReadGatewayCommandWithin reads the command a gateway task carries.
func (s *Service) ReadGatewayCommandWithin(ctx context.Context, q transaction.Executor, n Node, t Task) (wire.GatewayCommand, error) {
	if e := s.taskExecutor(ctx, q); e != nil {
		return wire.GatewayCommand{}, e
	}
	if n.ExecutionProfile != wire.DockerInferenceProfile || !wire.IsGatewayOperation(t.Operation) || !nodeOperationAllowed(n, t.Operation) || t.TenantID != n.TenantID || t.WorkspaceID != n.WorkspaceID || t.NodeID != n.ID {
		return wire.GatewayCommand{}, fail("AGENT_PROFILE_REJECTED")
	}
	var payload []byte
	if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT payload FROM node_tasks WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND node_id=$4", t.ID, n.TenantID, n.WorkspaceID, n.ID)).Scan(&payload); e != nil {
		return wire.GatewayCommand{}, e
	}
	var out wire.GatewayCommand
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || out.Validate(t.Operation) != nil {
		return wire.GatewayCommand{}, fail("AGENT_OPERATION_UNSUPPORTED")
	}
	return out, nil
}

// ActivateGateway runs a gateway activation under the lease of its gateway.start task.
func (s *Service) ActivateGateway(ctx context.Context, id, credential, taskID string, activation wire.GatewayActivation, request string) (wire.GatewayActivated, error) {
	if !nodeID(request) {
		return wire.GatewayActivated{}, fail("VALIDATION_FAILED")
	}
	var out wire.GatewayActivated
	e := s.WithLeaseExecutor(ctx, id, credential, taskID, activation.LeaseToken, func(ctx context.Context, q transaction.Executor, n Node, t Task) error {
		if s.Gateways == nil || t.Operation != wire.GatewayOperation("start") {
			return fail("AGENT_OPERATION_UNSUPPORTED")
		}
		command, e := s.ReadGatewayCommandWithin(ctx, q, n, t)
		if e != nil {
			return e
		}
		out, e = s.Gateways.ActivateGatewayWithin(ctx, q, n, t, command, activation, request)
		return e
	})
	if e != nil {
		return wire.GatewayActivated{}, e
	}
	return out, nil
}
