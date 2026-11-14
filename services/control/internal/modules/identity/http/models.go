package http

import (
	"context"
	"errors"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	deploymenthttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	modelhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/models/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

// This graph contains only M01-M06 owners. Model retirement borrows the actual
// deployment owner; no absent training/distribution table is queried.
func (h *Handler) composeModels() {
	m := &models.Service{DB: h.Service.DB, Identity: h.Service, Workspaces: h.Workspaces.Service, RootDirectory: h.Service.Config.Models.ArtifactRoot}
	d := &deployments.Service{Nodes: h.Nodes.Service, Models: m}
	m.Referenced = d.HasModelReferences
	h.Models = &modelhttp.Handler{Service: m}
	h.Deployments = &deploymenthttp.Handler{Service: d}
	h.Nodes.Service.Inference = d
	h.Nodes.ModelManifestWithin = func(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task) (wire.ModelCatalog, error) {
		c, e := h.Nodes.Service.ReadInferenceCommandWithin(ctx, q, n, t)
		if e != nil {
			return wire.ModelCatalog{}, e
		}
		if c.Action != "deploy" || c.Derived != nil || c.RegistryPinID != "" {
			return wire.ModelCatalog{}, errors.New("AGENT_OPERATION_UNSUPPORTED")
		}
		if e = m.ReadyWithin(ctx, q, n.TenantID, n.WorkspaceID, c.ModelID); e != nil {
			return wire.ModelCatalog{}, e
		}
		return wire.ApprovedModel(), nil
	}
	h.Nodes.ModelFileWithin = func(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, input wire.ModelFileRequest) (httpserver.Download, error) {
		c, e := h.Nodes.Service.ReadInferenceCommandWithin(ctx, q, n, t)
		if e != nil {
			return httpserver.Download{}, e
		}
		if c.Action != "deploy" || c.Derived != nil || c.RegistryPinID != "" {
			return httpserver.Download{}, errors.New("AGENT_OPERATION_UNSUPPORTED")
		}
		return m.OpenTransferWithin(ctx, q, n.TenantID, n.WorkspaceID, c.ModelID, input)
	}
}

func modelsHumanOwns(path string) bool {
	if strings.HasPrefix(path, "/api/v1/workspaces/") {
		p := strings.Split(strings.TrimPrefix(path, "/api/v1/workspaces/"), "/")
		return len(p) >= 2 && len(p) <= 3 && (p[1] == "models" || p[1] == "deployments")
	}
	if strings.HasPrefix(path, "/api/v1/admin/nodes/") {
		p := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/nodes/"), "/")
		return len(p) == 2 && p[1] == "inference" || len(p) == 3 && p[1] == "inference" && p[2] == "observations"
	}
	if !strings.HasPrefix(path, "/api/v1/admin/") {
		return false
	}
	p := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/"), "/")
	if len(p) == 0 {
		return false
	}
	switch p[0] {
	case "models":
		return len(p) == 1 || len(p) == 2 || len(p) == 3 && p[2] == "retire"
	case "model-imports":
		return len(p) == 1 || len(p) == 2 || len(p) == 3 && (p[2] == "chunks" || p[2] == "complete" || p[2] == "cancel")
	case "deployments":
		return len(p) == 1 || len(p) == 2 || len(p) == 3 && p[2] == "actions"
	}
	return false
}
