package deployments

import (
	"context"
	"reflect"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// RegistryDeployment is derived only from the current persisted deployment and
// its exact current node task. It is not an HTTP request or a replacement recipe.
type RegistryDeployment struct {
	TenantID, WorkspaceID, NodeID, DeploymentID, TaskID, ModelID string
	Revision                                                     int64
	ImageManifestDigest, ImageConfigDigest                       string
}

func (s *Service) RegistryReferenceWithin(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, c wire.InferenceCommand, record func(context.Context, transaction.Executor, RegistryDeployment) error) (RegistryDeployment, error) {
	if e := s.runtimeExecutor(ctx, q); e != nil {
		return RegistryDeployment{}, e
	}
	if record == nil || t.NodeID != n.ID || t.TenantID != n.TenantID || t.WorkspaceID != n.WorkspaceID || c.Action != "deploy" || c.Validate(t.Operation) != nil {
		return RegistryDeployment{}, fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	d, e := s.deploymentWithin(ctx, q, n.TenantID, n.WorkspaceID, c.DeploymentID, true)
	if e != nil {
		return RegistryDeployment{}, e
	}
	if d.NodeID != n.ID || d.ModelID != c.ModelID || d.Revision != c.Revision || d.RemovedAt != nil || d.DesiredState != "running" || d.ResourceState != "held" || d.LastTaskID == nil || *d.LastTaskID != t.ID {
		return RegistryDeployment{}, fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	retained, e := s.retainedCommand(ctx, q, n, d, c)
	if e != nil {
		return RegistryDeployment{}, e
	}
	if retained.Validate(t.Operation) != nil || !reflect.DeepEqual(retained, c) {
		return RegistryDeployment{}, fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	recipe := wire.ApprovedInferenceRecipe()
	out := RegistryDeployment{n.TenantID, n.WorkspaceID, n.ID, d.ID, t.ID, d.ModelID, d.Revision, recipe.ImageManifestDigest, recipe.ImageConfigDigest}
	if e = record(ctx, q, out); e != nil {
		return RegistryDeployment{}, e
	}
	return out, nil
}

// RegistryRemovalWithin accepts only the actual durable physical removal facts,
// never a stale lease, heartbeat timeout or client-supplied acknowledgement.
func (s *Service) RegistryRemovalWithin(ctx context.Context, q transaction.Executor, tenant, workspace, deployment string, record func(context.Context, transaction.Executor) error) error {
	if e := s.runtimeExecutor(ctx, q); e != nil {
		return e
	}
	d, e := s.deploymentWithin(ctx, q, tenant, workspace, deployment, true)
	if e != nil {
		return e
	}
	if record == nil || d.RemovedAt == nil || d.DesiredState != "removed" || d.ResourceState != "released" || d.ObservedState != "not_installed" {
		return fail("INFERENCE_RECOVERY_REQUIRED")
	}
	return record(ctx, q)
}

func registryFingerprint(actor string, input CreateRequest, binding string) []byte {
	if binding == "" {
		return fingerprint(actor, input)
	}
	return fingerprint(actor, struct {
		Request     CreateRequest
		RegistryPin string
	}{input, binding})
}

// CreateRegistryPinned preserves the real creator's single transaction. A replay
// invokes the owner callback in verification-only mode and cannot repair a graph.
func (s *Service) CreateRegistryPinned(ctx context.Context, a identity.Auth, input CreateRequest, key, request, pin string, admit func(context.Context, transaction.Executor) error, record func(context.Context, transaction.Executor, nodes.Node, nodes.Task, wire.InferenceCommand, bool) error) (Deployment, error) {
	if !id(pin) || record == nil || admit == nil {
		return Deployment{}, fail("VALIDATION_FAILED")
	}
	var ownerError error
	out, e := s.createBoundSelected(ctx, a, input, key, request, pin, func(ctx context.Context, q transaction.Executor) error { ownerError = admit(ctx, q); return ownerError }, func(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, c wire.InferenceCommand, replayed bool) error {
		ownerError = record(ctx, q, n, t, c, replayed)
		return ownerError
	})
	if e != nil {
		if kind, ok := transaction.CommitOutcome(e); ok {
			return Deployment{}, fail(string(kind))
		}
		if ownerError != nil {
			return Deployment{}, ownerError
		}
		return Deployment{}, e
	}
	return out, nil
}
