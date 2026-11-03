package http

import (
	"context"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// composeTraining extends the cumulative graph without constructing M10 owners.
func (h *Handler) composeTraining() {
	s := &training.Service{DB: h.Service.DB, Identity: h.Service, Workspaces: h.Workspaces.Service, Nodes: h.Nodes.Service, Models: h.Models.Service, Datasets: h.Datasets.Service, Inference: h.Deployments.Service}
	h.Training = s
	h.Nodes.Service.Training = s
	h.Nodes.TrainingInputFile = s.InputFile
	h.Nodes.TrainingEvents = s.RecordEvents
	h.Nodes.BeginTrainingUpload = s.BeginUpload
	h.Nodes.TrainingChunk = s.UploadChunk
	h.Nodes.FinishTrainingUpload = s.FinishUpload
	h.Nodes.TrainingStop = s.RecordStop
	prior := h.Models.Service.Referenced
	h.Models.Service.Referenced = func(ctx context.Context, q transaction.Executor, tenant, workspace, model string) (bool, error) {
		found, e := prior(ctx, q, tenant, workspace, model)
		if e != nil || found {
			return found, e
		}
		return s.Referenced(ctx, q, tenant, workspace, model)
	}
}

// Explicit leaves prevent future descendants reaching an absent owner.
func trainingHumanOwns(path string) bool {
	var p []string
	if strings.HasPrefix(path, "/api/v1/admin/training/") {
		p = strings.Split(strings.TrimPrefix(path, "/api/v1/admin/training/"), "/")
	} else if strings.HasPrefix(path, "/api/v1/workspaces/") {
		v := strings.Split(strings.TrimPrefix(path, "/api/v1/workspaces/"), "/")
		if len(v) < 3 || !wire.ValidID(v[0]) || v[1] != "training" {
			return false
		}
		p = v[2:]
	} else {
		return false
	}
	if len(p) == 1 {
		return p[0] == "capabilities" || p[0] == "jobs"
	}
	if len(p) == 2 && p[0] == "nodes" {
		return wire.ValidID(p[1])
	}
	if len(p) < 2 || p[0] != "jobs" || !wire.ValidID(p[1]) {
		return false
	}
	if len(p) == 2 {
		return true
	}
	if len(p) == 3 {
		return p[2] == "cancellations" || p[2] == "recoveries" || p[2] == "metrics" || p[2] == "attempts" || p[2] == "checkpoints"
	}
	return len(p) == 4 && p[2] == "files" && (p[3] == "manifest.json" || p[3] == "adapter_model.safetensors" || p[3] == "adapter_config.json" || p[3] == "README.md")
}
