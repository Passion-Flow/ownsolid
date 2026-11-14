// Package plugins owns signed builtin tool installation and current grants.
package plugins

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

const (
	ImportedState          = "imported"
	InstalledState         = "installed"
	UninstalledState       = "uninstalled"
	MaximumRevision  int64 = 9007199254740991
)

type Service struct {
	DB               *pgxpool.Pool
	Identity         *identity.Service
	Workspaces       *workspaces.Service
	Models           *models.Service
	lifecycleMu      sync.Mutex
	lifecycleContext context.Context
	closing          bool
	stop             context.CancelFunc
	joined           chan struct{}
	maintenanceError bool
}
type Version struct {
	ID              string     `json:"id"`
	PluginID        string     `json:"plugin_id"`
	ReleaseVersion  string     `json:"version"`
	ABI             string     `json:"abi"`
	Executor        string     `json:"executor"`
	Capabilities    []string   `json:"capabilities"`
	License         string     `json:"license"`
	CapsuleSHA256   string     `json:"capsule_sha256"`
	ManifestSHA256  string     `json:"manifest_sha256"`
	KeyID           string     `json:"key_id"`
	State           string     `json:"state"`
	Revision        int64      `json:"revision"`
	Trusted         bool       `json:"trusted"`
	Available       bool       `json:"available"`
	UnavailableCode *string    `json:"unavailable_code"`
	ImportedBy      string     `json:"imported_by"`
	CreatedAt       time.Time  `json:"created_at"`
	InstalledAt     *time.Time `json:"installed_at"`
	UninstalledAt   *time.Time `json:"uninstalled_at"`
}
type versionRecord struct {
	Version
	tenant            string
	capsule, manifest []byte
}
type Grant struct {
	ID              string    `json:"id"`
	WorkspaceID     string    `json:"workspace_id"`
	PluginVersionID string    `json:"plugin_version_id"`
	Enabled         bool      `json:"enabled"`
	Revision        int64     `json:"revision"`
	GrantedBy       string    `json:"granted_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Version         Version   `json:"version"`
}
type grantRecord struct {
	Grant
	authority string
}
type ImportRequest struct {
	Capsule string `json:"capsule"`
	SHA256  string `json:"sha256"`
	Reason  string `json:"reason"`
}
type RevisionReason struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}
type GrantRequest struct {
	PluginVersionID string `json:"plugin_version_id"`
	Reason          string `json:"reason"`
}
type GrantUpdate struct {
	Enabled          bool   `json:"enabled"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}
type UninstallRequest struct {
	ExpectedRevision           int64  `json:"expected_revision"`
	ReferencesSHA256           string `json:"references_sha256"`
	ConfirmReferencedUninstall bool   `json:"confirm_referenced_uninstall"`
	Reason                     string `json:"reason"`
}
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
type Reference struct {
	Kind        string `json:"kind"`
	WorkspaceID string `json:"workspace_id"`
	WorkflowID  string `json:"workflow_id"`
	EntityID    string `json:"entity_id"`
	GraphSHA256 string `json:"graph_sha256"`
}
type References struct {
	Items  []Reference `json:"items"`
	SHA256 string      `json:"sha256"`
	Count  int         `json:"count"`
}
type ModelReference struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	ModelKey      string `json:"model_key"`
	CatalogDigest string `json:"catalog_digest"`
}
type ModelList struct {
	Items []ModelReference `json:"items"`
}
type storedProof struct {
	SchemaVersion   int              `json:"schema_version"`
	WorkspaceID     string           `json:"workspace_id"`
	VersionID       string           `json:"plugin_version_id"`
	CapsuleSHA256   string           `json:"capsule_sha256"`
	GrantID         string           `json:"grant_id"`
	GrantAuthority  string           `json:"grant_authority"`
	ModelReferences []ModelReference `json:"model_references"`
}

// CompletedModelList is a Go-only capability; no HTTP route accepts its proof.
type CompletedModelList struct {
	service                                 *Service
	actor, audience, session, run, question string
	tokenFingerprint                        [32]byte
	proof                                   storedProof
	result                                  ModelList
}

func (c *CompletedModelList) Result() ModelList {
	return ModelList{Items: append([]ModelReference{}, c.result.Items...)}
}
func (c *CompletedModelList) StoredProof() json.RawMessage {
	raw, _ := json.Marshal(c.proof)
	return raw
}
