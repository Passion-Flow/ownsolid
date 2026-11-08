// Package distribution owns scoped model access authority and durable metadata-only request history.
package distribution

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"time"
)

type Service struct {
	DB          *pgxpool.Pool
	Identity    *identity.Service
	Workspaces  *workspaces.Service
	Nodes       *nodes.Service
	Deployments *deployments.Service
}

type RecoveryChallenge struct {
	ID              string             `json:"id"`
	RequestID       string             `json:"request_id"`
	WorkspaceID     string             `json:"workspace_id"`
	State           string             `json:"state"`
	CreatedAt       time.Time          `json:"created_at"`
	ExpiresAt       time.Time          `json:"expires_at"`
	FenceAcceptedAt *time.Time         `json:"fence_accepted_at"`
	ConfirmedAt     *time.Time         `json:"confirmed_at"`
	FenceReceipt    *wire.FenceReceipt `json:"fence_receipt"`
}
type Gateway struct {
	ID           string     `json:"id"`
	WorkspaceID  string     `json:"workspace_id"`
	NodeID       string     `json:"node_id"`
	Name         string     `json:"name"`
	ClientOrigin string     `json:"client_origin"`
	CreatedBy    string     `json:"created_by"`
	State        string     `json:"state"`
	CreatedAt    time.Time  `json:"created_at"`
	RevokedAt    *time.Time `json:"revoked_at"`
}
type ProvisionRequest struct {
	WorkspaceID  string `json:"workspace_id"`
	NodeID       string `json:"node_id"`
	Name         string `json:"name"`
	ClientOrigin string `json:"client_origin"`
	Reason       string `json:"reason"`
}
type Provisioned struct {
	Gateway         Gateway `json:"gateway"`
	Secret          *string `json:"secret"`
	SecretAvailable bool    `json:"secret_available"`
	Created         bool    `json:"-"`
}
type Alias struct {
	HeldRequests       int64      `json:"held_requests"`
	ClientOrigin       string     `json:"client_origin"`
	ID                 string     `json:"id"`
	WorkspaceID        string     `json:"workspace_id"`
	GatewayID          string     `json:"gateway_id"`
	NodeID             string     `json:"node_id"`
	Name               string     `json:"name"`
	DeploymentID       string     `json:"deployment_id"`
	DeploymentRevision int64      `json:"deployment_revision"`
	Revision           int64      `json:"revision"`
	DisabledAt         *time.Time `json:"disabled_at"`
	CreatedAt          time.Time  `json:"created_at"`
	Available          bool       `json:"available"`
	UnavailableCode    *string    `json:"unavailable_code"`
}
type PublishRequest struct {
	WorkspaceID        string `json:"workspace_id"`
	GatewayID          string `json:"gateway_id"`
	Name               string `json:"name"`
	DeploymentID       string `json:"deployment_id"`
	DeploymentRevision int64  `json:"deployment_revision"`
	Reason             string `json:"reason"`
}

type APIKey struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	AliasID     string     `json:"alias_id"`
	AliasName   string     `json:"alias_name"`
	Label       string     `json:"label"`
	Prefix      string     `json:"prefix"`
	CreatedBy   string     `json:"created_by"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	// RotatedFrom is the key this one replaced; ReplacedBy is the key that replaced this one.
	// Only PostgreSQL records rotation, so other chains read both as null.
	RotatedFrom *string `json:"rotated_from"`
	ReplacedBy  *string `json:"replaced_by"`
}
type KeyRequest struct {
	WorkspaceID string `json:"workspace_id"`
	AliasID     string `json:"alias_id"`
	Label       string `json:"label"`
	TTLSeconds  int    `json:"ttl_seconds"`
	Reason      string `json:"reason"`
}
// RotateRequest replaces a key's secret and stops the old key after an overlap.
type RotateRequest struct {
	OverlapSeconds int    `json:"overlap_seconds"`
	Reason         string `json:"reason"`
}
type MintedKey struct {
	Key             APIKey  `json:"key"`
	Secret          *string `json:"secret"`
	SecretAvailable bool    `json:"secret_available"`
	Created         bool    `json:"-"`
}
