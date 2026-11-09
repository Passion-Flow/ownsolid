package deployments

import (
	"context"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
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
		if !derivedProfile(n) {
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
	derived := c.Derived != nil
	if derived && !derivedProfile(n) {
		return fail("AGENT_PROFILE_REJECTED")
	}
	if e := admitNode(n); e != nil {
		return e
	}
	o, e := s.HardwareWithin(ctx, q, n)
	if e != nil {
		return hardwareRefusal(e)
	}
	if e := admitObservation(o, c); e != nil {
		return e
	}
	return admitGPU(o, c.GPUUUID, derived, newLoad, s.gpuObservationWindow())
}

// derivedProfile reports whether a node's agent can serve a model made here.
func derivedProfile(n nodes.Node) bool {
	return n.ExecutionProfile == wire.DockerModelLifecycleProfile && n.ProtocolVersion == wire.ModelLifecycleVersion
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
