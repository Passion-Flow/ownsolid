package workflows

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"sync"
	"sync/atomic"
	"time"
)

type Service struct {
	maintenanceMu      sync.Mutex
	maintenanceAfter   string
	shutdownDone       chan struct{}
	lifecycleContext   context.Context
	lifecycleCancel    context.CancelCauseFunc
	started            bool
	closing            bool
	maintenanceError   string
	workers            sync.WaitGroup
	running            map[string]context.CancelCauseFunc
	DB                 *pgxpool.Pool
	Identity           *identity.Service
	Workspaces         *workspaces.Service
	Knowledge          *knowledge.Service
	Plugins            *plugins.Service
	Distribution       *distribution.Service
	lifecycleMu        sync.Mutex
	configurationState atomic.Pointer[configurationReadiness]
	processOwner       string
	active             chan struct{}
}
type Workflow struct {
	ID               string          `json:"id"`
	WorkspaceID      string          `json:"workspace_id"`
	Name             string          `json:"name"`
	Enabled          bool            `json:"enabled"`
	Revision         int64           `json:"revision"`
	DraftGraph       json.RawMessage `json:"draft_graph,omitempty"`
	DraftSHA256      string          `json:"draft_sha256,omitempty"`
	CurrentVersionID *string         `json:"current_version_id"`
	CreatedBy        string          `json:"created_by"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	CanEdit          bool            `json:"can_edit"`
}
type workflowRecord struct {
	Workflow
	authority string
	graph     []byte
	digest    string
}
type CreateRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Reason      string `json:"reason"`
}
type UpdateRequest struct {
	Name             string `json:"name"`
	Graph            Graph  `json:"graph"`
	Enabled          bool   `json:"enabled"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}
type RevisionReason struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
