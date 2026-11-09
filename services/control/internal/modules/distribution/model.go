// Package distribution owns scoped model access authority and durable metadata-only request history.
package distribution

import (
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Service struct {
	DB          *pgxpool.Pool
	Identity    *identity.Service
	Workspaces  *workspaces.Service
	Nodes       *nodes.Service
	Deployments *deployments.Service
	// Certificates signs the serving certificates of gateways agents run (ADR 0052). Only
	// the agent path, which activates them, holds it.
	Certificates *agentidentity.Authority
	// ProjectGatewayReceipts enables transactional metering after the PostgreSQL ledger is installed.
	// Set it only at composition time.
	ProjectGatewayReceipts bool
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
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	NodeID      string `json:"node_id"`
	Name        string `json:"name"`
	// ClientOrigin is where callers reach the gateway. A gateway the agent runs has none
	// until the agent activates it with the origin its node declares.
	ClientOrigin *string    `json:"client_origin"`
	CreatedBy    string     `json:"created_by"`
	State        string     `json:"state"`
	CreatedAt    time.Time  `json:"created_at"`
	RevokedAt    *time.Time `json:"revoked_at"`
	// Delivery says who runs the gateway: "agent" when the node's agent runs it (ADR 0052),
	// "manual" when an administrator placed the credential shown at registration.
	Delivery string `json:"delivery"`
	// DeliveryState is how far the agent got: starting, running, failed, stopping or stopped.
	// A manual gateway has none.
	DeliveryState *string `json:"delivery_state"`
	// DeliveryErrorCode says why the agent's last task on the gateway failed.
	DeliveryErrorCode *string `json:"delivery_error_code"`
	// CertificateNotAfter is when the serving certificate issued at activation expires.
	CertificateNotAfter *time.Time `json:"certificate_not_after"`
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
	// Description and its revision appear together after the description migration.
	Description         *string `json:"description,omitempty"`
	DescriptionRevision *int64  `json:"description_revision,omitempty"`
}
type PublishRequest struct {
	WorkspaceID        string `json:"workspace_id"`
	GatewayID          string `json:"gateway_id"`
	Name               string `json:"name"`
	DeploymentID       string `json:"deployment_id"`
	DeploymentRevision int64  `json:"deployment_revision"`
	Reason             string `json:"reason"`
	// Omit empty text to retain the fingerprint of publications made before descriptions.
	Description string `json:"description,omitempty"`
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
