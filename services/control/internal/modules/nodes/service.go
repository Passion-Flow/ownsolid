package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/liveness"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	// Certificates signs node certificates (ADR 0050). Serving agents on PostgreSQL always
	// composes it on control; other engines and test compositions leave it nil and issue none.
	Certificates CertificateAuthority
	TaskEvents   TaskEventRecorder
	Preparations PreparationAuthority
	DB           *pgxpool.Pool
	Identity     *identity.Service
	Workspaces   *workspaces.Service
	Components   ComponentObserver
	Inference    InferenceObserver
	Training     TrainingAuthority
	// Kubernetes owns cell clusters (ADR 0051); nil where none are configured, and then
	// Kubernetes tasks fail closed.
	Kubernetes KubernetesAuthority
	// Gateways records what agents do with the model gateways of their nodes (GW-114).
	// Without it, gateway tasks are refused.
	Gateways GatewayOwnerWithin
	// wake lets claims held open for new work (ClaimWaiting) see tasks as they are written.
	wake wakeHub
	// delivering says the outbox reader runs, so wakes arrive after commit (ADR 0028).
	delivering atomic.Bool
	// wakeCounters report that reader to the metrics store.
	wakeCounters wakeCounters
	// views answers repeated heartbeats from memory while that reader runs (ADR 0047).
	views viewCache
	// prune remembers where the walk of finished tasks stands (ADR 0029).
	prune taskPruneCursor
	// resync bounds full resyncs granted by this process (ADR 0011).
	resync resyncBucket
	// pacing caches what heartbeat replies tell agents (ADR 0013).
	pacing pacingCache
	// Shed is this process's admission controller; nil where no agent traffic is served.
	Shed ShedSwitch
	// localShed holds a level set on the host until the database has stored it (ADR 0043).
	localShed localShed
	// sightings holds liveness in memory once the worker runs (ADR 0022). Until then every
	// heartbeat writes synchronously, because nothing would persist what the table holds.
	sightings atomic.Pointer[liveness.Table[heartbeatReport]]
	// livenessCounters counts what the liveness path did, for the metrics store.
	livenessCounters livenessCounters
	// LivenessReplica shares sightings with other gateways; nil with a single gateway.
	LivenessReplica liveness.Replica
}

// TaskEventRecorder borrows the actual node-task transaction. It must not commit
// independently or contact the broker; a failure rolls back the task owner.
type TaskEventRecorder interface {
	RecordTaskQueuedWithin(context.Context, transaction.Executor, Node, Task) error
}

// ComponentObserver owns component data while sharing the node authority transaction.
type ComponentObserver interface {
	Observe(context.Context, pgx.Tx, Node, wire.ComponentObservation, string) error
}

// ComponentObserverWithin and ComponentResultObserverWithin must be supplied by
// the actual selected component owner; missing MySQL implementations fail closed.
type ComponentObserverWithin interface {
	ObserveWithin(context.Context, transaction.Executor, Node, wire.ComponentObservation, string) error
}
type ComponentResultObserverWithin interface {
	RecordResultWithin(context.Context, transaction.Executor, Node, Task, wire.Result) error
}

func fail(code string) error              { return errors.New(code) }
func isAdmin(a identity.Auth) bool        { return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin }
func digest(purpose, token string) []byte { return security.Digest(purpose + ":v1:" + token) }
func mapped(e error) error {
	var p *pgconn.PgError
	if errors.As(e, &p) && p.ConstraintName == "nodes_live_name_idx" {
		return fail("NODE_NAME_EXISTS")
	}
	return e
}

func scanNode(row pgx.Row) (Node, error) {
	return scanNodeRow(row, nil, nil)
}

// scanNodeRow can retain the original facts JSON or a credential digest selected with the locked node.
func scanNodeRow(row pgx.Row, storedFacts *json.RawMessage, credentialHash *[]byte) (Node, error) {
	var n Node
	var facts, capabilities []byte
	dest := []any{&n.ID, &n.TenantID, &n.WorkspaceID, &n.Name, &n.Status, &n.Version, &n.ProtocolVersion, &facts, &n.LastHeartbeatAt, &n.CreatedAt, &n.UpdatedAt, &n.Connectivity, &n.ExecutionProfile, &capabilities, &n.SiteID, &n.CellID, &n.ConnectivityChangedAt}
	if credentialHash != nil {
		dest = append(dest, credentialHash)
	}
	e := row.Scan(dest...)
	if errors.Is(e, pgx.ErrNoRows) {
		return n, fail("RESOURCE_NOT_FOUND")
	}
	if e == nil && (json.Unmarshal(facts, &n.Facts) != nil || json.Unmarshal(capabilities, &n.Capabilities) != nil) {
		return n, fail("STORAGE_UNAVAILABLE")
	}
	if e == nil {
		if storedFacts != nil {
			*storedFacts = facts
		}
		n.LastHeartbeatAt, n.CreatedAt, n.UpdatedAt = n.LastHeartbeatAt.UTC(), n.CreatedAt.UTC(), n.UpdatedAt.UTC()
		if n.ConnectivityChangedAt != nil {
			changed := n.ConnectivityChangedAt.UTC()
			n.ConnectivityChangedAt = &changed
		}
	}
	return n, e
}
func (s *Service) audit(ctx context.Context, tx pgx.Tx, actor, audience, tenant, workspace, action, target, id, reason, request string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),NULLIF($2,'')::uuid,$3,$4,$5,$6,'success',$7,$8,NULLIF($9,''),$10)`, uuid.NewString(), actor, tenant, workspace, audience, action, target, id, reason, request)
	return e
}
func (s *Service) adminTransaction(ctx context.Context, a identity.Auth, workspace string, fn func(pgx.Tx, workspaces.Workspace) error) error {
	if !isAdmin(a) {
		return fail("ACCESS_DENIED")
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	defer tx.Rollback(ctx)
	if e = s.Identity.Recheck(ctx, tx, a); e != nil {
		return e
	}
	w, e := s.Workspaces.RecheckScope(ctx, tx, a, workspace)
	if e != nil {
		return e
	}
	if e = fn(tx, w); e != nil {
		return mapped(e)
	}
	if tx.Commit(ctx) != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return nil
}

const joinFields = `j.id::text,j.tenant_id::text,j.workspace_id::text,j.node_name,j.created_by::text,j.node_id::text,j.created_at,j.expires_at,CASE WHEN j.revoked_at IS NOT NULL THEN 'revoked' WHEN j.consumed_at IS NOT NULL THEN 'consumed' WHEN j.expires_at<=clock_timestamp() THEN 'expired' ELSE 'pending' END,j.execution_profile`

func scanJoin(row pgx.Row) (JoinToken, error) {
	var v JoinToken
	e := row.Scan(&v.ID, &v.TenantID, &v.WorkspaceID, &v.NodeName, &v.CreatedBy, &v.NodeID, &v.CreatedAt, &v.ExpiresAt, &v.Status, &v.ExecutionProfile)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, fail("RESOURCE_NOT_FOUND")
	}
	if e == nil {
		v.CreatedAt, v.ExpiresAt = v.CreatedAt.UTC(), v.ExpiresAt.UTC()
	}
	return v, e
}
