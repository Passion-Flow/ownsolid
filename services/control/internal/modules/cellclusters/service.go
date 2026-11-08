package cellclusters

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"slices"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

// maximumWorkersPerRequest bounds one create or add request; a cell grows in several.
const maximumWorkersPerRequest = 1000

// Service owns the cell clusters of this control plane's cell.
type Service struct {
	Identity   *identity.Service
	Workspaces *workspaces.Service
	Nodes      *nodes.Service
	config     config.KubernetesCellsConfiguration
	sealer     sealer
	artifacts  *artifactStore
	now        func() time.Time
}

// New returns the service, or nil when the configuration has no kubernetes_cells section:
// then the routes answer KUBERNETES_CELLS_UNCONFIGURED and Kubernetes tasks fail closed.
func New(s *identity.Service, w *workspaces.Service, n *nodes.Service) (*Service, error) {
	if s == nil || w == nil || n == nil {
		return nil, errors.New("KUBERNETES_CELLS_OWNER_REQUIRED")
	}
	if s.Config.KubernetesCells == nil {
		return nil, nil
	}
	c := s.Config.KubernetesCells.WithDefaults()
	if e := c.Validate(); e != nil {
		return nil, e
	}
	secret, e := config.Secret(c.JoinTokenKeyFile)
	if e != nil {
		return nil, errors.New("KUBERNETES_CELLS_SECRET_INVALID")
	}
	sealed, e := newSealer(secret)
	if e != nil {
		return nil, e
	}
	return &Service{Identity: s, Workspaces: w, Nodes: n, config: c, sealer: sealed, artifacts: newArtifactStore(c.ArtifactDirectory), now: time.Now}, nil
}

func fail(code string) error { return errors.New(code) }

func isAdmin(a identity.Auth) bool { return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin }

func canonical(value string) bool {
	parsed, e := uuid.Parse(value)
	return e == nil && parsed != uuid.Nil && parsed.String() == value
}

// publicCodes are the refusals this module returns as they are; anything else is storage.
var publicCodes = map[string]bool{
	"ACCESS_DENIED": true, "AUTH_SESSION_EXPIRED": true, "VALIDATION_FAILED": true, "RESOURCE_NOT_FOUND": true, "IDEMPOTENCY_CONFLICT": true,
	"AGENT_PROFILE_REJECTED": true, "AGENT_OPERATION_UNSUPPORTED": true, "NODE_BUSY": true, "NODE_UNAVAILABLE": true, "INVALID_CURSOR": true,
	"KUBERNETES_CELLS_UNCONFIGURED": true, "KUBERNETES_CELL_CLUSTER_EXISTS": true, "KUBERNETES_NODE_IN_CLUSTER": true,
	"KUBERNETES_NODE_ADDRESS_INVALID": true, "KUBERNETES_NODE_ADDRESS_CONFLICT": true, "KUBERNETES_MEMBER_STATE_CONFLICT": true,
	"KUBERNETES_MEMBER_CONFLICT": true, "KUBERNETES_JOIN_TOKEN_UNAVAILABLE": true, "KUBERNETES_ARTIFACT_UNAVAILABLE": true,
	"KUBERNETES_ARTIFACT_INVALID": true, "KUBERNETES_CLUSTER_STORAGE_UNSUPPORTED": true,
}

// publicError keeps vendor messages and SQL inside: only stable codes leave the module.
func publicError(e error) error {
	if e == nil {
		return nil
	}
	if kind, ok := transaction.CommitOutcome(e); ok {
		return fail(string(kind))
	}
	if transaction.IsCancellationOnly(e) {
		if errors.Is(e, context.Canceled) {
			return fail(string(transaction.Cancelled))
		}
		return fail(string(transaction.Timeout))
	}
	if publicCodes[e.Error()] {
		return e
	}
	var safe *transaction.Error
	if errors.As(e, &safe) && safe.Kind == transaction.UniqueConflict {
		return fail("KUBERNETES_NODE_IN_CLUSTER")
	}
	return fail("STORAGE_UNAVAILABLE")
}

// within runs fn in one bounded PostgreSQL transaction. Cell clusters are stored only on
// PostgreSQL (ADR 0051).
func (s *Service) within(ctx context.Context, seconds int, fn func(context.Context, transaction.Executor) error) error {
	if s == nil {
		return fail("KUBERNETES_CELLS_UNCONFIGURED")
	}
	if ctx == nil {
		return fail("VALIDATION_FAILED")
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	if store.Dialect() != transaction.PostgreSQL {
		return fail("KUBERNETES_CLUSTER_STORAGE_UNSUPPORTED")
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(seconds))
	defer cancel()
	return publicError(store.Within(bounded, false, fn))
}

// adminWithin is within for an administrator's request, rechecked in the transaction.
func (s *Service) adminWithin(ctx context.Context, a identity.Auth, fn func(context.Context, transaction.Executor) error) error {
	if !isAdmin(a) {
		return fail("ACCESS_DENIED")
	}
	if s == nil {
		return fail("KUBERNETES_CELLS_UNCONFIGURED")
	}
	return s.within(ctx, s.Identity.Config.Policy().HTTP.RequestSeconds, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		return fn(ctx, q)
	})
}

func pg(text string, args ...any) transaction.Statement {
	return transaction.SQL(transaction.PostgreSQL, text, args...)
}

// installationTenant reads the one tenant of this installation.
func installationTenant(ctx context.Context, q transaction.Executor) (string, error) {
	var tenant string
	e := q.QueryRow(ctx, pg(`SELECT i.tenant_id::text FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' FOR SHARE OF i,t`)).Scan(&tenant)
	if e != nil {
		return "", fail("RESOURCE_NOT_FOUND")
	}
	return tenant, nil
}

// candidate is a node an administrator wants in a cluster.
type candidate struct {
	id, workspace, name, status, profile, version, address string
	capabilities                                           []string
}

// validWorkers checks a list of worker IDs: canonical, unique and bounded.
func validWorkers(ids []string, server string) bool {
	if len(ids) > maximumWorkersPerRequest {
		return false
	}
	seen := map[string]bool{server: true}
	for _, id := range ids {
		if !canonical(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// candidatesWithin locks the named nodes and checks each can join a cluster: active, of
// the Kubernetes profile, with the address it enrolled from, and in no cluster yet. The
// administrator's scope over each node's Workspace is rechecked first, in a fixed order,
// before any node is locked.
func (s *Service) candidatesWithin(ctx context.Context, q transaction.Executor, a identity.Auth, ids []string) (string, map[string]candidate, error) {
	rows, e := q.Query(ctx, pg(`SELECT DISTINCT workspace_id::text FROM managed_nodes WHERE id=ANY($1::uuid[]) ORDER BY 1`, ids))
	if e != nil {
		return "", nil, e
	}
	var spaces []string
	for rows.Next() {
		var w string
		if e = rows.Scan(&w); e != nil {
			rows.Close()
			return "", nil, e
		}
		spaces = append(spaces, w)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return "", nil, e
	}
	for _, w := range spaces {
		space, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, w)
		if e != nil {
			return "", nil, e
		}
		if space.Status != "active" {
			return "", nil, fail("ACCESS_DENIED")
		}
	}
	tenant, e := installationTenant(ctx, q)
	if e != nil {
		return "", nil, e
	}
	rows, e = q.Query(ctx, pg(`SELECT n.id::text,n.workspace_id::text,n.name,n.status,n.execution_profile,n.protocol_version,COALESCE(host(n.enrolled_address),''),n.capabilities,EXISTS(SELECT 1 FROM cell_cluster_members m WHERE m.node_id=n.id)
		FROM managed_nodes n WHERE n.tenant_id=$1 AND n.id=ANY($2::uuid[]) AND n.cell_id=local_cell_id() ORDER BY n.id FOR UPDATE OF n`, tenant, ids))
	if e != nil {
		return "", nil, e
	}
	found := map[string]candidate{}
	member := false
	for rows.Next() {
		var c candidate
		var raw []byte
		var in bool
		if e = rows.Scan(&c.id, &c.workspace, &c.name, &c.status, &c.profile, &c.version, &c.address, &raw, &in); e != nil {
			rows.Close()
			return "", nil, e
		}
		if json.Unmarshal(raw, &c.capabilities) != nil {
			rows.Close()
			return "", nil, fail("STORAGE_UNAVAILABLE")
		}
		member = member || in
		found[c.id] = c
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return "", nil, e
	}
	if len(found) != len(ids) {
		return "", nil, fail("RESOURCE_NOT_FOUND")
	}
	addresses := map[string]bool{}
	for _, c := range found {
		if c.status != "active" {
			return "", nil, fail("NODE_UNAVAILABLE")
		}
		if c.profile != wire.KubernetesNodeProfile || c.version != wire.KubernetesVersion || !slices.Equal(c.capabilities, wire.Capabilities(wire.KubernetesNodeProfile)) {
			return "", nil, fail("AGENT_PROFILE_REJECTED")
		}
		if !wire.ValidKubernetesNodeAddress(c.address) {
			return "", nil, fail("KUBERNETES_NODE_ADDRESS_INVALID")
		}
		if addresses[c.address] {
			return "", nil, fail("KUBERNETES_NODE_ADDRESS_CONFLICT")
		}
		addresses[c.address] = true
	}
	if member {
		return "", nil, fail("KUBERNETES_NODE_IN_CLUSTER")
	}
	return tenant, found, nil
}

// fingerprint binds an idempotency key to what was asked.
func fingerprint(v any) []byte {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return sum[:]
}

// Create creates the local cell's cluster from a server node and worker nodes, and writes
// the server's join task when the server is idle. A repeated request with the same key
// returns the cluster it created.
func (s *Service) Create(ctx context.Context, a identity.Auth, input CreateInput, key, request string) (Cluster, error) {
	if s == nil {
		return Cluster{}, fail("KUBERNETES_CELLS_UNCONFIGURED")
	}
	name, e := security.Name(input.Name)
	if e != nil || !canonical(input.ServerNodeID) || !validWorkers(input.WorkerNodeIDs, input.ServerNodeID) || !workspaces.ValidReason(input.Reason) || !canonical(key) || !canonical(request) {
		return Cluster{}, fail("VALIDATION_FAILED")
	}
	input.Name = name
	asked := fingerprint(input)
	var id string
	e = s.adminWithin(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		tenant, e := installationTenant(ctx, q)
		if e != nil {
			return e
		}
		var previous []byte
		e = q.QueryRow(ctx, pg(`SELECT id::text,request_fingerprint FROM cell_clusters WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, key)).Scan(&id, &previous)
		if e == nil {
			if subtle.ConstantTimeCompare(previous, asked) != 1 {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			return nil
		}
		// The cell row serializes creates, so two at once cannot both find the cell empty.
		var cell string
		if e = q.QueryRow(ctx, pg(`SELECT id FROM cells WHERE id=local_cell_id() FOR UPDATE`)).Scan(&cell); e != nil {
			return e
		}
		var exists bool
		if e = q.QueryRow(ctx, pg(`SELECT EXISTS(SELECT 1 FROM cell_clusters WHERE cell_id=$1)`, cell)).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return fail("KUBERNETES_CELL_CLUSTER_EXISTS")
		}
		ids := append([]string{input.ServerNodeID}, input.WorkerNodeIDs...)
		_, found, e := s.candidatesWithin(ctx, q, a, ids)
		if e != nil {
			return e
		}
		id = uuid.NewString()
		if _, e = q.Exec(ctx, pg(`INSERT INTO cell_clusters(id,tenant_id,name,distribution_version,created_by,idempotency_key,request_fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			id, tenant, name, wire.ApprovedKubernetesRelease().Version, a.Me.User.ID, key, asked)); e != nil {
			return e
		}
		if e = s.insertMembersWithin(ctx, q, a, tenant, id, found, input.ServerNodeID); e != nil {
			return e
		}
		if e = s.auditWithin(ctx, q, a.Me.User.ID, "admin", tenant, "", "cell_cluster.created", "cell_cluster", id, input.Reason, request); e != nil {
			return e
		}
		return s.advanceWithin(ctx, q, tenant, id, request)
	})
	if e != nil {
		return Cluster{}, e
	}
	return s.Get(ctx, a, id)
}

// insertMembersWithin adds the found nodes as pending members; server names the server.
func (s *Service) insertMembersWithin(ctx context.Context, q transaction.Executor, a identity.Auth, tenant, cluster string, found map[string]candidate, server string) error {
	ids := make([]string, 0, len(found))
	for id := range found {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		c := found[id]
		role := wire.KubernetesWorkerRole
		if id == server {
			role = wire.KubernetesServerRole
		}
		if _, e := q.Exec(ctx, pg(`INSERT INTO cell_cluster_members(cluster_id,tenant_id,workspace_id,node_id,role,node_address,state,requested_by) VALUES($1,$2,$3,$4,$5,$6::inet,'pending',$7)`,
			cluster, tenant, c.workspace, c.id, role, c.address, a.Me.User.ID)); e != nil {
			return e
		}
	}
	return nil
}

// AddWorkers adds worker nodes to a cluster. Nodes already in this cluster are left as they
// are, so a repeated request changes nothing.
func (s *Service) AddWorkers(ctx context.Context, a identity.Auth, cluster string, input AddWorkersInput, request string) (Cluster, error) {
	if s == nil {
		return Cluster{}, fail("KUBERNETES_CELLS_UNCONFIGURED")
	}
	if !canonical(cluster) || len(input.WorkerNodeIDs) == 0 || !validWorkers(input.WorkerNodeIDs, cluster) || !workspaces.ValidReason(input.Reason) || !canonical(request) {
		return Cluster{}, fail("VALIDATION_FAILED")
	}
	e := s.adminWithin(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		tenant, e := s.lockClusterWithin(ctx, q, cluster)
		if e != nil {
			return e
		}
		rows, e := q.Query(ctx, pg(`SELECT node_id::text FROM cell_cluster_members WHERE cluster_id=$1 AND node_id=ANY($2::uuid[])`, cluster, input.WorkerNodeIDs))
		if e != nil {
			return e
		}
		already := map[string]bool{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			already[id] = true
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		added := []string{}
		for _, id := range input.WorkerNodeIDs {
			if !already[id] {
				added = append(added, id)
			}
		}
		if len(added) == 0 {
			return nil
		}
		_, found, e := s.candidatesWithin(ctx, q, a, added)
		if e != nil {
			return e
		}
		// A worker's address must differ from every member's, not only the new ones'.
		var clash bool
		addresses := make([]string, 0, len(found))
		for _, c := range found {
			addresses = append(addresses, c.address)
		}
		if e = q.QueryRow(ctx, pg(`SELECT EXISTS(SELECT 1 FROM cell_cluster_members WHERE cluster_id=$1 AND host(node_address)=ANY($2::text[]))`, cluster, addresses)).Scan(&clash); e != nil {
			return e
		}
		if clash {
			return fail("KUBERNETES_NODE_ADDRESS_CONFLICT")
		}
		if e = s.insertMembersWithin(ctx, q, a, tenant, cluster, found, ""); e != nil {
			return e
		}
		if e = s.auditWithin(ctx, q, a.Me.User.ID, "admin", tenant, "", "cell_cluster.workers_added", "cell_cluster", cluster, input.Reason, request); e != nil {
			return e
		}
		return s.advanceWithin(ctx, q, tenant, cluster, request)
	})
	if e != nil {
		return Cluster{}, e
	}
	return s.Get(ctx, a, cluster)
}

// Retry returns a failed member to pending so control joins it again.
func (s *Service) Retry(ctx context.Context, a identity.Auth, cluster, node string, input ReasonInput, request string) (Member, error) {
	if s == nil {
		return Member{}, fail("KUBERNETES_CELLS_UNCONFIGURED")
	}
	if !canonical(cluster) || !canonical(node) || !workspaces.ValidReason(input.Reason) || !canonical(request) {
		return Member{}, fail("VALIDATION_FAILED")
	}
	var out Member
	e := s.adminWithin(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		tenant, e := s.lockClusterWithin(ctx, q, cluster)
		if e != nil {
			return e
		}
		var workspace, state string
		e = q.QueryRow(ctx, pg(`SELECT workspace_id::text,state FROM cell_cluster_members WHERE cluster_id=$1 AND node_id=$2 FOR UPDATE`, cluster, node)).Scan(&workspace, &state)
		if e != nil {
			return fail("RESOURCE_NOT_FOUND")
		}
		if space, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace); e != nil || space.Status != "active" {
			return fail("ACCESS_DENIED")
		}
		if state != MemberFailed {
			return fail("KUBERNETES_MEMBER_STATE_CONFLICT")
		}
		if _, e = q.Exec(ctx, pg(`UPDATE cell_cluster_members SET state='pending',error_code=NULL,task_id=NULL,requested_by=$3,updated_at=clock_timestamp() WHERE cluster_id=$1 AND node_id=$2`, cluster, node, a.Me.User.ID)); e != nil {
			return e
		}
		if e = s.auditWithin(ctx, q, a.Me.User.ID, "admin", tenant, workspace, "cell_cluster.member_retried", "node", node, input.Reason, request); e != nil {
			return e
		}
		if e = s.advanceWithin(ctx, q, tenant, cluster, request); e != nil {
			return e
		}
		out, e = memberWithin(ctx, q, cluster, node)
		return e
	})
	if e != nil {
		return Member{}, e
	}
	return out, nil
}

// Observe asks every settled member whose node is idle to report its readiness now.
func (s *Service) Observe(ctx context.Context, a identity.Auth, cluster string, input ReasonInput, request string) (Cluster, error) {
	if s == nil {
		return Cluster{}, fail("KUBERNETES_CELLS_UNCONFIGURED")
	}
	if !canonical(cluster) || !workspaces.ValidReason(input.Reason) || !canonical(request) {
		return Cluster{}, fail("VALIDATION_FAILED")
	}
	e := s.adminWithin(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		tenant, e := s.lockClusterWithin(ctx, q, cluster)
		if e != nil {
			return e
		}
		if e = s.auditWithin(ctx, q, a.Me.User.ID, "admin", tenant, "", "cell_cluster.observation_requested", "cell_cluster", cluster, input.Reason, request); e != nil {
			return e
		}
		return s.observeWithin(ctx, q, a.Me.User.ID, cluster, request)
	})
	if e != nil {
		return Cluster{}, e
	}
	return s.Get(ctx, a, cluster)
}

// lockClusterWithin locks a cluster of this cell and returns its tenant.
func (s *Service) lockClusterWithin(ctx context.Context, q transaction.Executor, cluster string) (string, error) {
	var tenant string
	if e := q.QueryRow(ctx, pg(`SELECT tenant_id::text FROM cell_clusters WHERE id=$1 AND cell_id=local_cell_id() FOR UPDATE`, cluster)).Scan(&tenant); e != nil {
		return "", fail("RESOURCE_NOT_FOUND")
	}
	return tenant, nil
}

// auditWithin records one successful action. workspace is empty for a cluster-wide one.
func (s *Service) auditWithin(ctx context.Context, q transaction.Executor, actor, audience, tenant, workspace, action, targetType, target, reason, request string) error {
	_, e := q.Exec(ctx, pg(`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),NULLIF($2,'')::uuid,$3,NULLIF($4,'')::uuid,$5,$6,'success',$7,$8,NULLIF($9,''),$10)`,
		uuid.NewString(), actor, tenant, workspace, audience, action, targetType, target, reason, request))
	return e
}
