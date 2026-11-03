package deployments

import (
	"context"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func servingSHA(command wire.InferenceCommand) *string {
	if command.Derived == nil {
		return nil
	}
	value := command.Derived.ServingRecipeSHA256
	return &value
}
func (s *Service) initialCommand(ctx context.Context, version models.Version, n nodes.Node, gpu string) (wire.InferenceCommand, error) {
	c := wire.InferenceCommand{Action: "deploy", DeploymentID: uuid.NewString(), ModelID: version.ID, CatalogDigest: version.CatalogDigest, GPUUUID: gpu, Revision: 1}
	switch version.Kind {
	case "approved_base":
		return c, c.Validate(c.Operation())
	case wire.DerivedModelKind:
		if n.ExecutionProfile != wire.DockerModelLifecycleProfile || n.ProtocolVersion != wire.ModelLifecycleVersion {
			return c, fail("AGENT_PROFILE_REJECTED")
		}
		catalog, e := lifecycle.DecodeDerivedCatalog(version.Manifest)
		if e != nil {
			return c, e
		}
		sha, e := catalog.Digest()
		if e != nil || sha != version.CatalogDigest || catalog.ModelID != version.ID || catalog.Scope.TenantID != n.TenantID || catalog.Scope.WorkspaceID != n.WorkspaceID {
			return c, fail("MODEL_DERIVED_CATALOG_INVALID")
		}
		// Whole accepted-file health is checked outside all control DB authority locks.
		// The final owning callback checks current authority and exact immutable lineage again.
		if e = s.Models.VerifyDerivedFiles(ctx, catalog); e != nil {
			return c, e
		}
		c.Derived = &wire.DerivedInferenceIdentity{SchemaVersion: 1, Catalog: catalog, ServingRecipeSHA256: wire.DerivedServingRecipeDigest()}
		return c, c.Validate(c.Operation())
	default:
		return c, fail("MODEL_FILE_INVALID")
	}
}
func (s *Service) readyCommand(ctx context.Context, q transaction.Executor, n nodes.Node, c wire.InferenceCommand) error {
	if c.Derived == nil {
		return s.Models.ReadyWithin(ctx, q, n.TenantID, n.WorkspaceID, c.ModelID)
	}
	current, e := s.Models.DecodeDerivedVersionWithin(ctx, q, n.TenantID, n.WorkspaceID, c.ModelID)
	if e != nil {
		return e
	}
	sha, e := current.Digest()
	if e != nil || sha != c.CatalogDigest || c.Derived.ServingRecipeSHA256 != wire.DerivedServingRecipeDigest() {
		return fail("MODEL_DERIVED_CATALOG_INVALID")
	}
	return nil
}
func (s *Service) retainedCommand(ctx context.Context, q transaction.Executor, n nodes.Node, d Deployment, c wire.InferenceCommand) (wire.InferenceCommand, error) {
	if d.ModelKind == "approved_base" {
		if d.CatalogDigest != wire.ModelCatalogDigest() || d.ServingRecipeSHA256 != nil {
			return c, fail("INFERENCE_CONFIGURATION_INVALID")
		}
		return c, nil
	}
	if d.ModelKind != wire.DerivedModelKind || d.ServingRecipeSHA256 == nil || *d.ServingRecipeSHA256 != wire.DerivedServingRecipeDigest() {
		return c, fail("INFERENCE_CONFIGURATION_INVALID")
	}
	catalog, e := s.Models.RetainedDerivedVersionWithin(ctx, q, n.TenantID, n.WorkspaceID, d.ModelID)
	if e != nil {
		return c, e
	}
	c.Derived = &wire.DerivedInferenceIdentity{SchemaVersion: 1, Catalog: catalog, ServingRecipeSHA256: *d.ServingRecipeSHA256}
	return c, c.Validate(c.Operation())
}
func (s *Service) preflightCommand(ctx context.Context, q transaction.Executor, n nodes.Node, c wire.InferenceCommand, newLoad bool) error {
	if c.Derived != nil && (n.ExecutionProfile != wire.DockerModelLifecycleProfile || n.ProtocolVersion != wire.ModelLifecycleVersion) {
		return fail("AGENT_PROFILE_REJECTED")
	}
	if e := s.preflightCommandWithin(ctx, q, n, c); e != nil {
		return e
	}
	if c.Derived == nil {
		return nil
	}
	observation, e := s.HardwareWithin(ctx, q, n)
	if e != nil {
		return e
	}
	for _, gpu := range observation.GPUs {
		if gpu.UUID != c.GPUUUID {
			continue
		}
		recipe := wire.ApprovedDerivedServingRecipe()
		if gpu.ComputeCapability < recipe.MinimumComputeCapability {
			return fail("INFERENCE_GPU_UNSUPPORTED")
		}
		if newLoad && gpu.MemoryFreeMiB < recipe.MinimumGPUMemoryMiB {
			return fail("INFERENCE_GPU_MEMORY_LOW")
		}
		return nil
	}
	return fail("INFERENCE_GPU_UNAVAILABLE")
}
func matchesStoredServing(d Deployment, o wire.DeploymentObservation) bool {
	if o.CatalogDigest != d.CatalogDigest {
		return false
	}
	if d.ModelKind == "approved_base" {
		return o.ModelKind == "" && o.ServingRecipeSHA256 == "" && d.ServingRecipeSHA256 == nil
	}
	return d.ModelKind == wire.DerivedModelKind && o.ModelKind == wire.DerivedModelKind && d.ServingRecipeSHA256 != nil && *d.ServingRecipeSHA256 == wire.DerivedServingRecipeDigest() && o.ServingRecipeSHA256 == *d.ServingRecipeSHA256
}
