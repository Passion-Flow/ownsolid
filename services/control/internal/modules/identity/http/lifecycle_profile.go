package http

import (
	"context"
	"errors"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// composeLifecycle extends the existing cumulative stores and authority graph.
// Preparation never becomes a separate model-registration or training authority.
func (h *Handler) composeLifecycle() {
	s, e := modelpreparations.New(h.Service.DB, h.Service, h.Workspaces.Service, h.Datasets.Service)
	h.initializationError = errors.Join(h.initializationError, e)
	if e != nil {
		return
	}
	s.Training, s.Nodes, s.Models, s.Inference = h.Training, h.Nodes.Service, h.Models.Service, h.Deployments.Service
	h.ModelPreparations, h.Training.Holdouts, h.Nodes.Service.Preparations = s, s, s
	h.Nodes.PreparationInputFile, h.Nodes.PreparationEvents = s.InputFile, s.RecordEvents
	h.Nodes.BeginPreparationUpload, h.Nodes.PreparationChunk = s.BeginUpload, s.UploadChunk
	h.Nodes.FinishPreparationUpload, h.Nodes.PreparationStop = s.FinishUpload, s.RecordStop
	prior := h.Models.Service.Referenced
	h.Models.Service.Referenced = func(ctx context.Context, q transaction.Executor, tenant, workspace, model string) (bool, error) {
		found, e := prior(ctx, q, tenant, workspace, model)
		if e != nil || found {
			return found, e
		}
		return s.Referenced(ctx, q, tenant, workspace, model)
	}
	h.Nodes.ModelFileWithin = func(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, input wire.ModelFileRequest) (httpserver.Download, error) {
		c, e := h.Nodes.Service.ReadInferenceCommandWithin(ctx, q, n, t)
		if e != nil {
			return httpserver.Download{}, e
		}
		if c.Action != "deploy" || c.RegistryPinID != "" {
			return httpserver.Download{}, errors.New("AGENT_OPERATION_UNSUPPORTED")
		}
		if c.Derived != nil {
			return h.Models.Service.OpenDerivedTransferWithin(ctx, q, n.TenantID, n.WorkspaceID, c, input)
		}
		return h.Models.Service.OpenTransferWithin(ctx, q, n.TenantID, n.WorkspaceID, c.ModelID, input)
	}
}

// lifecycleHumanOwns is a closed list of M10 leaves. Future descendants remain absent.
func lifecycleHumanOwns(path string) bool {
	if strings.HasPrefix(path, "/api/v1/admin/distribution/workspaces/") {
		p := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/distribution/workspaces/"), "/")
		return len(p) == 4 && datawire.ValidID(p[0]) && p[1] == "aliases" && datawire.ValidID(p[2]) && (p[3] == "cutovers" || p[3] == "targets")
	}
	var p []string
	if strings.HasPrefix(path, "/api/v1/admin/") {
		p = strings.Split(strings.TrimPrefix(path, "/api/v1/admin/"), "/")
	} else if strings.HasPrefix(path, "/api/v1/workspaces/") {
		v := strings.Split(strings.TrimPrefix(path, "/api/v1/workspaces/"), "/")
		if len(v) < 2 || !datawire.ValidID(v[0]) {
			return false
		}
		p = v[1:]
	} else {
		return false
	}
	if p[0] != "model-holdouts" && p[0] != "model-preparations" {
		return false
	}
	if len(p) == 1 {
		return true
	}
	if len(p) == 2 {
		return p[1] == "capabilities" || datawire.ValidID(p[1])
	}
	if p[0] != "model-preparations" {
		return false
	}
	if len(p) == 3 && p[1] == "nodes" {
		return datawire.ValidID(p[2])
	}
	if !datawire.ValidID(p[1]) {
		return false
	}
	if len(p) == 3 {
		return p[2] == "cancellations" || p[2] == "recoveries" || p[2] == "report" || p[2] == "attempts" || p[2] == "reviews"
	}
	return len(p) == 5 && p[2] == "attempts" && datawire.ValidID(p[3]) && p[4] == "events"
}
