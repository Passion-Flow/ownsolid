package nodes

import (
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"time"
)

type Node struct {
	ID               string     `json:"id"`
	TenantID         string     `json:"tenant_id"`
	WorkspaceID      string     `json:"workspace_id"`
	Name             string     `json:"name"`
	Status           string     `json:"status"`
	Connectivity     string     `json:"connectivity"`
	Version          int64      `json:"version"`
	ProtocolVersion  string     `json:"protocol_version"`
	ExecutionProfile string     `json:"execution_profile"`
	Capabilities     []string   `json:"capabilities"`
	Facts            wire.Facts `json:"facts"`
	LastHeartbeatAt  time.Time  `json:"last_heartbeat_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
type JoinToken struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id"`
	WorkspaceID      string    `json:"workspace_id"`
	NodeName         string    `json:"node_name"`
	ExecutionProfile string    `json:"execution_profile"`
	Status           string    `json:"status"`
	CreatedBy        string    `json:"created_by"`
	NodeID           *string   `json:"node_id"`
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at"`
}
type MintedToken struct {
	JoinToken
	Token string `json:"token"`
}
type Task struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	WorkspaceID string     `json:"workspace_id"`
	NodeID      string     `json:"node_id"`
	Operation   string     `json:"operation"`
	State       string     `json:"state"`
	Attempt     int        `json:"attempt"`
	ErrorCode   *string    `json:"error_code"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeadlineAt  time.Time  `json:"deadline_at"`
	CompletedAt *time.Time `json:"completed_at"`
}
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
