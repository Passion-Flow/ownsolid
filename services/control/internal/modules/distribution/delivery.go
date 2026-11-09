package distribution

import (
	"context"
	"database/sql"
	"errors"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

// Gateways the node's agent runs (GW-114, ADR 0052). Registering one queues a gateway.start
// task: the agent generates the credential and key on the node, activates them under the
// task's lease, and control keeps only the credential's digest and signs a serving
// certificate for the origin the node declares. No person sees the credential.

// agentDelivers reports whether the node's agent runs the gateway: the installation has a
// gateway CA, the database records delivery, and the node enrolled with gateway tasks.
func (s *Service) agentDelivers(ctx context.Context, q transaction.Executor, w workspaces.Workspace, node string) (bool, error) {
	if q.Dialect() != transaction.PostgreSQL || !s.Identity.Config.AgentsRunGateways() || s.Nodes == nil {
		return false, nil
	}
	var capable bool
	e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT capabilities ? $4 FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", w.TenantID, w.ID, node, agentwire.GatewayOperation("start"))).Scan(&capable)
	return capable, e
}

// provisionDelivered registers a gateway the node's agent will run and queues its start.
func (s *Service) provisionDelivered(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, input ProvisionRequest, key string, fingerprint []byte, controlOrigin, request string) (Gateway, error) {
	id := uuid.NewString()
	task, e := s.Nodes.CreateGatewayTaskWithin(ctx, q, a, input.NodeID, agentwire.GatewayCommand{Action: "start", GatewayID: id, ControlOrigin: controlOrigin}, input.Reason, request)
	if e != nil {
		return Gateway{}, e
	}
	if _, e = distributionExecOne(ctx, q, transaction.SQL(transaction.PostgreSQL, "INSERT INTO gateway_instances(id,tenant_id,workspace_id,node_id,name,created_by,requested_epoch,idempotency_key,fingerprint,control_origin_sha256,delivery,delivery_state,delivery_task_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'agent','starting',$11)", id, w.TenantID, w.ID, input.NodeID, input.Name, a.Me.User.ID, a.Epoch, key, fingerprint, originDigest(controlOrigin), task.ID)); e != nil {
		return Gateway{}, e
	}
	if e = s.audit(ctx, q, a, w, "gateway.instance_created", "gateway_instance", id, input.Reason, request); e != nil {
		return Gateway{}, e
	}
	return scanGateway(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+gatewayFields+" FROM gateway_instances WHERE id=$1", id)))
}

// stopDelivered asks the node's agent to stop a revoked gateway. A node that is busy, gone or
// disabled cannot take the task now: the gateway keeps its last state, refuses every call
// because control refuses its credential, and revoking it again asks again.
func (s *Service) stopDelivered(ctx context.Context, q transaction.Executor, a identity.Auth, g Gateway, explanation, request string) error {
	if g.Delivery != "agent" || g.DeliveryState == nil || (*g.DeliveryState != "running" && *g.DeliveryState != "failed") || s.Nodes == nil {
		return nil
	}
	task, e := s.Nodes.CreateGatewayTaskWithin(ctx, q, a, g.NodeID, agentwire.GatewayCommand{Action: "stop", GatewayID: g.ID}, explanation, request)
	if e != nil {
		switch e.Error() {
		case "NODE_BUSY", "ACCESS_DENIED", "AGENT_PROFILE_REJECTED", "RESOURCE_NOT_FOUND":
			return nil
		}
		return e
	}
	_, e = distributionExecOne(ctx, q, transaction.SQL(transaction.PostgreSQL, "UPDATE gateway_instances SET delivery_state='stopping',delivery_error_code=NULL,delivery_task_id=$2 WHERE id=$1 AND delivery='agent'", g.ID, task.ID))
	return e
}

// ActivateGatewayWithin binds the credential and key the agent generated for the gateway its
// gateway.start task starts. Control keeps the credential's digest and the declared origin,
// and signs a serving certificate naming only that origin's host.
func (s *Service) ActivateGatewayWithin(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, command agentwire.GatewayCommand, activation agentwire.GatewayActivation, request string) (agentwire.GatewayActivated, error) {
	if q.Dialect() != transaction.PostgreSQL || s.Certificates == nil {
		return agentwire.GatewayActivated{}, fail("GATEWAY_AUTHORITY_UNAVAILABLE")
	}
	if activation.Validate(command.GatewayID) != nil {
		return agentwire.GatewayActivated{}, fail("VALIDATION_FAILED")
	}
	origin, e := agentwire.ParseGatewayOrigin(activation.Origin)
	if e != nil {
		return agentwire.GatewayActivated{}, fail("VALIDATION_FAILED")
	}
	csr, e := agentwire.ParseCertificateRequest(activation.CertificateRequest)
	if e != nil {
		return agentwire.GatewayActivated{}, fail("VALIDATION_FAILED")
	}
	_, token, e := parseCredential(gatewayCredentialPrefix, activation.Credential)
	if e != nil {
		return agentwire.GatewayActivated{}, fail("VALIDATION_FAILED")
	}
	var delivery, deliveryTask string
	var active bool
	e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT delivery,COALESCE(delivery_task_id::text,''),revoked_at IS NULL AND authority_invalidated_at IS NULL FROM gateway_instances WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 FOR UPDATE", n.TenantID, n.WorkspaceID, n.ID, command.GatewayID)).Scan(&delivery, &deliveryTask, &active)
	if errors.Is(e, sql.ErrNoRows) {
		return agentwire.GatewayActivated{}, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return agentwire.GatewayActivated{}, e
	}
	if delivery != "agent" || deliveryTask != t.ID {
		return agentwire.GatewayActivated{}, fail("AGENT_OPERATION_UNSUPPORTED")
	}
	if !active {
		return agentwire.GatewayActivated{}, fail("GATEWAY_INSTANCE_INACTIVE")
	}
	bundle, e := publicGatewayCA(s.Certificates.Bundle())
	if e != nil {
		return agentwire.GatewayActivated{}, e
	}
	certificate, notAfter, e := s.Certificates.IssueServing(csr, command.GatewayID, origin)
	if e != nil {
		return agentwire.GatewayActivated{}, e
	}
	// The certificate keeps whole seconds; so does what control reports of it.
	notAfter = notAfter.Truncate(time.Second).UTC()
	if _, e = distributionExecOne(ctx, q, transaction.SQL(transaction.PostgreSQL, "UPDATE gateway_instances SET credential_hash=$2,client_origin=$3,certificate_not_after=$4 WHERE id=$1", command.GatewayID, s.digest("gateway-credential", command.GatewayID, token), origin.String(), notAfter)); e != nil {
		return agentwire.GatewayActivated{}, e
	}
	if _, e = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),NULL,$2,$3,'system','gateway.instance_activated','success','gateway_instance',$4,NULL,$5)`, uuid.NewString(), n.TenantID, n.WorkspaceID, command.GatewayID, request)); e != nil {
		return agentwire.GatewayActivated{}, e
	}
	return agentwire.GatewayActivated{Certificate: certificate, CertificateAuthority: string(bundle), NotAfter: notAfter}, nil
}

// RecordGatewayResultWithin records what the agent did with a gateway. A result of a task the
// gateway no longer waits for changes nothing.
func (s *Service) RecordGatewayResultWithin(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, command agentwire.GatewayCommand, r agentwire.Result) error {
	if q.Dialect() != transaction.PostgreSQL || r.ValidateGateway(command) != nil {
		return fail("VALIDATION_FAILED")
	}
	state := r.Gateway.State
	if command.Action == "start" && r.Outcome == "failure" {
		state = "failed"
	}
	_, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, "UPDATE gateway_instances SET delivery_state=$5,delivery_error_code=NULLIF($6,'') WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 AND delivery='agent' AND delivery_task_id=$7", n.TenantID, n.WorkspaceID, n.ID, command.GatewayID, state, r.ErrorCode, t.ID))
	return e
}
