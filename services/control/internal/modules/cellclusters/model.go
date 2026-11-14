// Package cellclusters installs and manages one Kubernetes cluster per cell on nodes of the
// kubernetes_node profile (ARC-104, ADR 0051). Control decides what each node does and
// writes node tasks; the agent on each node installs RKE2 and reports back.
package cellclusters

import "time"

// Member states. A member waits as pending until control writes its join task, is joining
// while that task runs, and then is ready, not_ready or failed.
const (
	MemberPending  = "pending"
	MemberJoining  = "joining"
	MemberReady    = "ready"
	MemberNotReady = "not_ready"
	MemberFailed   = "failed"
)

// Cluster states, derived from the members.
const (
	ClusterInstalling = "installing"
	ClusterReady      = "ready"
	ClusterDegraded   = "degraded"
	ClusterFailed     = "failed"
)

// MemberCounts counts a cluster's members by state.
type MemberCounts struct {
	Pending  int `json:"pending"`
	Joining  int `json:"joining"`
	Ready    int `json:"ready"`
	NotReady int `json:"not_ready"`
	Failed   int `json:"failed"`
}

// Total is the number of members.
func (c MemberCounts) Total() int { return c.Pending + c.Joining + c.Ready + c.NotReady + c.Failed }

// State derives the cluster's state. A failed server stops the whole cluster, since no
// worker can join without it; otherwise the cluster installs while any member waits or
// joins, is ready when every member is, and is degraded when some are not.
func (c MemberCounts) State(serverState string) string {
	switch {
	case serverState == MemberFailed:
		return ClusterFailed
	case c.Pending+c.Joining > 0:
		return ClusterInstalling
	case c.Ready == c.Total():
		return ClusterReady
	default:
		return ClusterDegraded
	}
}

// Cluster is a cell's Kubernetes cluster as an administrator reads it.
type Cluster struct {
	ID          string `json:"id"`
	SiteID      string `json:"site_id"`
	CellID      string `json:"cell_id"`
	Name        string `json:"name"`
	Version     string `json:"distribution_version"`
	State       string `json:"state"`
	ServerNode  string `json:"server_node_id"`
	ServerState string `json:"server_state"`
	// CASHA256 is the hash of the server CA that workers pin. It is public: it identifies
	// the cluster and grants nothing.
	CASHA256  *string      `json:"ca_sha256"`
	Members   MemberCounts `json:"members"`
	CreatedBy string       `json:"created_by"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// Member is one node of a cluster.
type Member struct {
	NodeID       string     `json:"node_id"`
	NodeName     string     `json:"node_name"`
	WorkspaceID  string     `json:"workspace_id"`
	Role         string     `json:"role"`
	Address      string     `json:"node_address"`
	State        string     `json:"state"`
	ErrorCode    *string    `json:"error_code"`
	TaskID       *string    `json:"task_id"`
	JoinAttempts int        `json:"join_attempts"`
	ObservedAt   *time.Time `json:"observed_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// Page is one page of a list.
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// CreateInput is what an administrator chooses when creating a cell's cluster.
type CreateInput struct {
	Name          string   `json:"name"`
	ServerNodeID  string   `json:"server_node_id"`
	WorkerNodeIDs []string `json:"worker_node_ids"`
	Reason        string   `json:"reason"`
}

// AddWorkersInput adds worker nodes to a cluster.
type AddWorkersInput struct {
	WorkerNodeIDs []string `json:"worker_node_ids"`
	Reason        string   `json:"reason"`
}

// ReasonInput carries the reason an administrator gives for an action.
type ReasonInput struct {
	Reason string `json:"reason"`
}
