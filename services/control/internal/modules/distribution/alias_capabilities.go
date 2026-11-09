package distribution

import (
	"context"
	"database/sql"
	"errors"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// AliasCapabilityMetadata is a server-side projection of a verified target.
// The tokenizer can impose a lower limit than ContextTokenCeiling.
type AliasCapabilityMetadata struct {
	AliasID                string
	AliasRevision          int64
	DeploymentID           string
	DeploymentRevision     int64
	VerifiedTextGeneration bool
	ContextTokenCeiling    int
}

// AliasCapabilities returns evidence for the same alias visible to Admin or Portal.
// A nil result means no verified capability metadata for that visible alias.
func (s *Service) AliasCapabilities(ctx context.Context, a identity.Auth, workspace, id string) (*AliasCapabilityMetadata, error) {
	if !wire.ValidID(id) {
		return nil, fail("VALIDATION_FAILED")
	}
	var out *AliasCapabilityMetadata
	err := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		alias, err := visibleAliasWithin(ctx, tx, a, w, id)
		if err != nil || tx.Dialect() != transaction.PostgreSQL || w.Status != "active" {
			return err
		}
		if err := s.aliasReadiness(ctx, tx, w, &alias); err != nil || !alias.Available {
			return err
		}
		var kind, catalog string
		var recipe *string
		err = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `
			SELECT h.model_kind,h.catalog_digest,h.serving_recipe_sha256
			FROM model_aliases a
			JOIN model_alias_target_versions h ON h.tenant_id=a.tenant_id AND h.workspace_id=a.workspace_id
				AND h.site_id=a.site_id AND h.cell_id=a.cell_id AND h.alias_id=a.id AND h.revision=a.revision
				AND h.deployment_id=a.deployment_id AND h.deployment_revision=a.deployment_revision
			JOIN deployments d ON d.tenant_id=h.tenant_id AND d.workspace_id=h.workspace_id
				AND d.site_id=h.site_id AND d.cell_id=h.cell_id AND d.node_id=a.node_id
				AND d.id=h.deployment_id AND d.revision=h.deployment_revision AND d.model_id=h.model_id
				AND d.model_kind=h.model_kind AND d.catalog_digest=h.catalog_digest
				AND d.serving_recipe_sha256 IS NOT DISTINCT FROM h.serving_recipe_sha256
			JOIN model_versions m ON m.tenant_id=d.tenant_id AND m.workspace_id=d.workspace_id
				AND m.site_id=d.site_id AND m.cell_id=d.cell_id AND m.id=d.model_id
				AND m.kind=d.model_kind AND m.catalog_digest=d.catalog_digest AND m.state='ready'
			WHERE a.tenant_id=$1 AND a.workspace_id=$2 AND a.id=$3 AND a.revision=$4
				AND a.site_id=local_site_id() AND a.cell_id=local_cell_id()`,
			w.TenantID, w.ID, id, alias.Revision)).Scan(&kind, &catalog, &recipe)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ceiling := aliasContextCeiling(kind, catalog, recipe, s.Identity.Config.Policy().Distribution.ContextTokens)
		if ceiling > 0 {
			out = &AliasCapabilityMetadata{
				AliasID: alias.ID, AliasRevision: alias.Revision, DeploymentID: alias.DeploymentID,
				DeploymentRevision: alias.DeploymentRevision, VerifiedTextGeneration: true, ContextTokenCeiling: ceiling,
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func aliasContextCeiling(kind, catalog string, recipe *string, policy int) int {
	switch kind {
	case "approved_base":
		if recipe == nil && catalog == agentwire.ModelCatalogDigest() {
			return min(agentwire.ApprovedModel().MaxContext, agentwire.ApprovedInferenceRecipe().MaximumContext, policy)
		}
	case agentwire.DerivedModelKind:
		if recipe != nil && *recipe == agentwire.DerivedServingRecipeDigest() {
			// Readiness has verified the exact derived catalogue, whose validator requires this limit.
			return min(int(lifecycle.MaximumContextTokens), agentwire.ApprovedDerivedServingRecipe().MaximumContext, policy)
		}
	}
	return 0
}
