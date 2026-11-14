package cellclusters

import (
	"context"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

// The service is the node module's Kubernetes authority.
var _ nodes.KubernetesAuthority = (*Service)(nil)

// taskOwnerWithin checks that the task is still the one its cluster is waiting for: the
// member's join or observe task, or the cluster's token task. The cluster row is locked
// when lock is set, which recording a result needs and a lease does not.
func taskOwnerWithin(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, stored wire.KubernetesCommand, lock bool) (clusterRow, *openMember, error) {
	query := `SELECT tenant_id::text FROM cell_clusters WHERE id=$1 AND tenant_id=$2 AND cell_id=local_cell_id()`
	if lock {
		query += ` FOR UPDATE`
	}
	var tenant string
	if e := q.QueryRow(ctx, pg(query, stored.ClusterID, n.TenantID)).Scan(&tenant); e != nil {
		return clusterRow{}, nil, fail("KUBERNETES_MEMBER_CONFLICT")
	}
	c, e := readClusterWithin(ctx, q, tenant, stored.ClusterID)
	if e != nil {
		return clusterRow{}, nil, e
	}
	rows, e := q.Query(ctx, pg(`SELECT node_id::text,workspace_id::text,role,host(node_address),state,requested_by::text,task_id::text FROM cell_cluster_members WHERE cluster_id=$1 AND node_id=$2 AND workspace_id=$3`, c.id, n.ID, n.WorkspaceID))
	if e != nil {
		return clusterRow{}, nil, e
	}
	found, e := scanOpenMembers(rows)
	if e != nil {
		return clusterRow{}, nil, e
	}
	if len(found) != 1 || c.version != stored.Version {
		return clusterRow{}, nil, fail("KUBERNETES_MEMBER_CONFLICT")
	}
	m := &found[0]
	if m.role != stored.Role || m.address != stored.NodeAddress {
		return clusterRow{}, nil, fail("KUBERNETES_MEMBER_CONFLICT")
	}
	owned := m.task != nil && *m.task == t.ID
	if t.Operation == wire.KubernetesTokenOperation {
		owned = m.role == wire.KubernetesServerRole && c.tokenTask != nil && *c.tokenTask == t.ID
	}
	if !owned {
		return clusterRow{}, nil, fail("KUBERNETES_MEMBER_CONFLICT")
	}
	return c, m, nil
}

// KubernetesLeaseWithin completes a claimed task's command. A worker's join gets the
// cluster's current join token, which is never stored in the task row.
func (s *Service) KubernetesLeaseWithin(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, stored wire.KubernetesCommand) (wire.KubernetesCommand, error) {
	c, m, e := taskOwnerWithin(ctx, q, n, t, stored, false)
	if e != nil {
		return wire.KubernetesCommand{}, e
	}
	if t.Operation != wire.KubernetesJoinOperation || m.role != wire.KubernetesWorkerRole {
		return stored, nil
	}
	var sealed []byte
	if e = q.QueryRow(ctx, pg(`SELECT join_token_sealed FROM cell_clusters WHERE id=$1 AND join_token_expires_at>clock_timestamp()`, c.id)).Scan(&sealed); e != nil {
		return wire.KubernetesCommand{}, fail("KUBERNETES_JOIN_TOKEN_UNAVAILABLE")
	}
	token, e := s.sealer.open(c.id, sealed)
	if e != nil {
		return wire.KubernetesCommand{}, e
	}
	if hash, ok := wire.KubernetesTokenCAHash(token); !ok || c.ca == nil || hash != *c.ca {
		return wire.KubernetesCommand{}, fail("KUBERNETES_JOIN_TOKEN_UNAVAILABLE")
	}
	stored.JoinToken = token
	return stored, nil
}

// RecordKubernetesResultWithin stores what a node reported for its cluster task. Moving the
// cluster on to its next tasks is left to the reconciler, which locks the cluster before any
// node, so a result never waits on another node's lock.
func (s *Service) RecordKubernetesResultWithin(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, stored wire.KubernetesCommand, result wire.Result, request string) error {
	c, m, e := taskOwnerWithin(ctx, q, n, t, stored, true)
	if e != nil {
		return e
	}
	o := result.Kubernetes
	switch t.Operation {
	case wire.KubernetesTokenOperation:
		if result.Outcome != "success" {
			return s.tokenFailedWithin(ctx, q, c, o.ErrorCode, request)
		}
		if c.ca == nil || o.CASHA256 != *c.ca {
			code := "KUBERNETES_CA_CHANGED"
			return s.tokenFailedWithin(ctx, q, c, code, request)
		}
		sealed, e := s.sealer.seal(c.id, o.JoinToken)
		if e != nil {
			return e
		}
		_, e = q.Exec(ctx, pg(`UPDATE cell_clusters SET join_token_sealed=$2,join_token_expires_at=$3,token_task_id=NULL,updated_at=clock_timestamp() WHERE id=$1`, c.id, sealed, o.JoinTokenExpiresAt))
		return e
	case wire.KubernetesJoinOperation:
		if result.Outcome != "success" {
			return s.settleWithin(ctx, q, c.tenant, c.id, *m, MemberFailed, &o.ErrorCode, request)
		}
		if m.role == wire.KubernetesServerRole {
			// The CA identifies the cluster. A server that comes back with another one was
			// installed anew, and the workers' tokens would no longer match it.
			if c.ca != nil && *c.ca != o.CASHA256 {
				code := "KUBERNETES_CA_CHANGED"
				return s.settleWithin(ctx, q, c.tenant, c.id, *m, MemberFailed, &code, request)
			}
			if _, e = q.Exec(ctx, pg(`UPDATE cell_clusters SET ca_sha256=$2,updated_at=clock_timestamp() WHERE id=$1`, c.id, o.CASHA256)); e != nil {
				return e
			}
		}
		return s.settleWithin(ctx, q, c.tenant, c.id, *m, MemberReady, nil, request)
	default:
		state := MemberReady
		var code *string
		switch {
		case result.Outcome != "success":
			// An observation that could not be made leaves the member as it was.
			state = m.state
			if state == MemberJoining || state == MemberPending {
				state = MemberNotReady
			}
		case o.State == wire.KubernetesNotReady:
			state = MemberNotReady
		case o.State == wire.KubernetesNotInstalled:
			state, code = MemberFailed, ptr("KUBERNETES_NOT_INSTALLED")
		}
		return s.settleWithin(ctx, q, c.tenant, c.id, *m, state, code, request)
	}
}

func ptr(v string) *string { return &v }

// ArtifactWithin serves a range of an RKE2 file to a node whose join task is running.
func (s *Service) ArtifactWithin(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, input wire.KubernetesArtifactRequest) (httpserver.Download, error) {
	if s == nil {
		return httpserver.Download{}, fail("KUBERNETES_CELLS_UNCONFIGURED")
	}
	if t.Operation != wire.KubernetesJoinOperation {
		return httpserver.Download{}, fail("AGENT_OPERATION_UNSUPPORTED")
	}
	stored, e := s.Nodes.ReadKubernetesCommandWithin(ctx, q, n, t)
	if e != nil {
		return httpserver.Download{}, e
	}
	if _, _, e = taskOwnerWithin(ctx, q, n, t, stored, false); e != nil {
		return httpserver.Download{}, e
	}
	return s.artifacts.Range(ctx, input)
}
