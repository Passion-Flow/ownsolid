package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// KubernetesAuthority owns cell clusters while sharing the node authority transaction
// (ADR 0051). The cellclusters module implements it; nodes only carries its tasks.
type KubernetesAuthority interface {
	// KubernetesLeaseWithin completes the stored command of a task the node is claiming,
	// adding what is kept out of the task row: a worker's join token. It refuses a task its
	// cluster no longer wants.
	KubernetesLeaseWithin(ctx context.Context, q transaction.Executor, n Node, t Task, stored wire.KubernetesCommand) (wire.KubernetesCommand, error)
	// RecordKubernetesResultWithin stores a validated result for the cluster to act on.
	RecordKubernetesResultWithin(ctx context.Context, q transaction.Executor, n Node, t Task, stored wire.KubernetesCommand, result wire.Result, request string) error
}

// storedJoinTokenShape stands in for a worker's join token when a stored command is
// checked: the task row never holds the token, which the cluster adds at claim.
const storedJoinTokenShape = "K10" + "0000000000000000000000000000000000000000000000000000000000000000" + "::000000.0000000000000000"

// validStoredKubernetesCommand checks a command as a task row stores it.
func validStoredKubernetesCommand(operation string, c wire.KubernetesCommand) bool {
	if c.JoinToken != "" {
		return false
	}
	if operation == wire.KubernetesJoinOperation && c.Role == wire.KubernetesWorkerRole {
		c.JoinToken = storedJoinTokenShape
	}
	return c.Validate(operation) == nil
}

// kubernetesNode reports whether the node takes Kubernetes tasks of this operation.
func kubernetesNode(n Node, operation string) bool {
	return n.ExecutionProfile == wire.KubernetesNodeProfile && n.ProtocolVersion == wire.KubernetesVersion && wire.IsKubernetesOperation(operation) && nodeOperationAllowed(n, operation)
}

// CreateKubernetesTaskWithin writes a Kubernetes task inside the caller's transaction,
// which must be PostgreSQL's. The node is locked first, so tasks of one node are written
// one at a time. requestedBy is the user whose request the task carries out. It refuses
// NODE_UNAVAILABLE for a node that is not active or has gone offline, and NODE_BUSY while
// the node has another task.
func (s *Service) CreateKubernetesTaskWithin(ctx context.Context, q transaction.Executor, requestedBy, tenant, workspace, node, operation string, command wire.KubernetesCommand, seconds int, request string) (Task, error) {
	if !nodeID(requestedBy) || !nodeID(tenant) || !nodeID(workspace) || !nodeID(node) || !nodeID(request) || command.NodeName != node || !validStoredKubernetesCommand(operation, command) || seconds < 1 {
		return Task{}, fail("VALIDATION_FAILED")
	}
	if e := s.taskExecutor(ctx, q); e != nil {
		return Task{}, e
	}
	if q.Dialect() != transaction.PostgreSQL {
		return Task{}, fail("AGENT_PROFILE_REJECTED")
	}
	n, e := s.lockedUpdateNode(ctx, q, tenant, workspace, node)
	if e != nil {
		return Task{}, e
	}
	if !kubernetesNode(n, operation) {
		return Task{}, fail("AGENT_PROFILE_REJECTED")
	}
	if n.Status != "active" || n.Connectivity == "offline" {
		return Task{}, fail("NODE_UNAVAILABLE")
	}
	if e = s.taskIdleWithin(ctx, q, n, request); e != nil {
		return Task{}, e
	}
	payload, e := json.Marshal(command)
	if e != nil {
		return Task{}, fail("VALIDATION_FAILED")
	}
	// The cluster member, not an idempotency key, deduplicates these tasks.
	out, e := s.insertTaskWithin(ctx, q, requestedBy, n, operation, uuid.NewString(), payload, nil, seconds, nil)
	if e != nil {
		return Task{}, e
	}
	if e = s.taskAuditWithin(ctx, q, "", "system", n, "node.task_requested", out.ID, "", request); e != nil {
		return Task{}, e
	}
	return out, nil
}

// ReadKubernetesCommandWithin reads the command a Kubernetes task row stores, which never
// holds a join token.
func (s *Service) ReadKubernetesCommandWithin(ctx context.Context, q transaction.Executor, n Node, t Task) (wire.KubernetesCommand, error) {
	if s.Kubernetes == nil || q.Dialect() != transaction.PostgreSQL {
		return wire.KubernetesCommand{}, fail("AGENT_PROFILE_REJECTED")
	}
	if !kubernetesNode(n, t.Operation) || t.TenantID != n.TenantID || t.WorkspaceID != n.WorkspaceID || t.NodeID != n.ID {
		return wire.KubernetesCommand{}, fail("AGENT_PROFILE_REJECTED")
	}
	var payload []byte
	if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT payload FROM node_tasks WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND node_id=$4", t.ID, n.TenantID, n.WorkspaceID, n.ID)).Scan(&payload); e != nil {
		return wire.KubernetesCommand{}, e
	}
	var out wire.KubernetesCommand
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || out.NodeName != n.ID || !validStoredKubernetesCommand(t.Operation, out) {
		return wire.KubernetesCommand{}, fail("AGENT_OPERATION_UNSUPPORTED")
	}
	return out, nil
}

// kubernetesLeaseWithin is the command a claimed Kubernetes task's lease carries.
func (s *Service) kubernetesLeaseWithin(ctx context.Context, q transaction.Executor, n Node, t Task) (wire.KubernetesCommand, error) {
	stored, e := s.ReadKubernetesCommandWithin(ctx, q, n, t)
	if e != nil {
		return wire.KubernetesCommand{}, e
	}
	out, e := s.Kubernetes.KubernetesLeaseWithin(ctx, q, n, t, stored)
	if e != nil {
		return wire.KubernetesCommand{}, e
	}
	if e = out.Validate(t.Operation); e != nil || out.ClusterID != stored.ClusterID || out.Role != stored.Role || out.NodeAddress != stored.NodeAddress || out.ServerAddress != stored.ServerAddress {
		return wire.KubernetesCommand{}, fail("AGENT_OPERATION_UNSUPPORTED")
	}
	return out, nil
}
