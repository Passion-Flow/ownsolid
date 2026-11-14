package distribution

import (
	"context"
	"reflect"

	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// CutoverState identifies the alias revision, serving target and pause state.
type CutoverState struct {
	Revision           int64  `json:"revision"`
	DeploymentID       string `json:"deployment_id"`
	DeploymentRevision int64  `json:"deployment_revision"`
	Paused             bool   `json:"paused"`
}

// CutoverPreview describes the checked state before and after a proposed cutover.
type CutoverPreview struct {
	Before CutoverState `json:"before"`
	After  CutoverState `json:"after"`
}

// PreviewCutover validates a proposed cutover against current authority without
// applying it. A later Cutover rechecks the expected revision and target.
func (s *Service) PreviewCutover(ctx context.Context, a identity.Auth, workspace, id string, in CutoverRequest) (CutoverPreview, error) {
	if !admin(a) {
		return CutoverPreview{}, fail("ACCESS_DENIED")
	}
	if !wire.ValidID(id) || !validCutover(in) {
		return CutoverPreview{}, fail("VALIDATION_FAILED")
	}
	var out CutoverPreview
	err := s.transaction(ctx, a, workspace, in.Action != "pause", nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, e = s.planCutover(ctx, tx, w, id, in)
		return e
	})
	if err != nil {
		return CutoverPreview{}, err
	}
	return out, nil
}

// planCutover resolves the live change under the apply lock order before any write.
func (s *Service) planCutover(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, id string, in CutoverRequest) (CutoverPreview, error) {
	if e := s.gatewayComponents(); e != nil {
		return CutoverPreview{}, e
	}
	// The Workspace lock serializes all admission/report/alias mutation; lock remaining owners in node/gateway/alias order.
	current, e := scanAlias(tx.QueryRow(ctx, distributionStatement(tx, "SELECT "+aliasFields+" FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", `SELECT `+mysqlAliasFields+` FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND id=?`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})))
	if e != nil {
		return CutoverPreview{}, e
	}
	var node string
	if e = tx.QueryRow(ctx, distributionStatement(tx, "SELECT id::text FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", `SELECT id FROM managed_nodes WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, current.NodeID}, []any{w.TenantID, w.ID, current.NodeID})).Scan(&node); e != nil {
		return CutoverPreview{}, e
	}
	gateway, e := scanGateway(tx.QueryRow(ctx, distributionStatement(tx, "SELECT "+gatewayFields+" FROM gateway_instances WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND node_id=$4 FOR UPDATE", `SELECT `+mysqlGatewayFields+` FROM gateway_instances WHERE tenant_id=? AND workspace_id=? AND id=? AND node_id=? FOR UPDATE`, []any{w.TenantID, w.ID, current.GatewayID, node}, []any{w.TenantID, w.ID, current.GatewayID, node})))
	if e != nil {
		return CutoverPreview{}, e
	}
	current, e = scanAlias(tx.QueryRow(ctx, distributionStatement(tx, "SELECT "+aliasFields+" FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", `SELECT `+mysqlAliasFields+` FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})))
	if e != nil {
		return CutoverPreview{}, e
	}
	if current.Revision != in.ExpectedRevision {
		return CutoverPreview{}, fail("VERSION_CONFLICT")
	}
	before := CutoverState{
		Revision:           current.Revision,
		DeploymentID:       current.DeploymentID,
		DeploymentRevision: current.DeploymentRevision,
		Paused:             current.DisabledAt != nil,
	}
	out := CutoverPreview{Before: before, After: before}
	out.After.Revision++
	if in.Action == "pause" {
		if before.Paused {
			return CutoverPreview{}, fail("ALIAS_ALREADY_PAUSED")
		}
		out.After.Paused = true
		return out, nil
	}
	if !before.Paused {
		if in.Action == "resume" {
			return CutoverPreview{}, fail("ALIAS_NOT_PAUSED")
		}
		return CutoverPreview{}, fail("ALIAS_PAUSE_REQUIRED")
	}
	var held bool
	if e = tx.QueryRow(ctx, distributionStatement(tx, "SELECT EXISTS(SELECT 1 FROM gateway_requests WHERE tenant_id=$1 AND workspace_id=$2 AND alias_id=$3 AND capacity_held)", `SELECT EXISTS(SELECT 1 FROM gateway_requests WHERE tenant_id=? AND workspace_id=? AND alias_id=? AND capacity_held)`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})).Scan(&held); e != nil {
		return CutoverPreview{}, e
	}
	if held {
		return CutoverPreview{}, fail("ALIAS_REQUESTS_UNDRAINED")
	}
	if gateway.State != "active" {
		return CutoverPreview{}, fail("GATEWAY_INSTANCE_REVOKED")
	}
	deployment, revision := current.DeploymentID, current.DeploymentRevision
	if in.Action != "resume" {
		deployment, revision = *in.DeploymentID, *in.DeploymentRevision
	}
	target, e := s.Deployments.ReadyGatewayTargetWithin(ctx, tx, w.TenantID, w.ID, node, deployment, revision)
	if e != nil {
		return CutoverPreview{}, e
	}
	if in.Action == "rollback" {
		source, e := scanAliasTarget(tx.QueryRow(ctx, distributionStatement(tx, "SELECT "+targetFields+" FROM model_alias_target_versions WHERE tenant_id=$1 AND workspace_id=$2 AND alias_id=$3 AND revision=$4", `SELECT `+mysqlTargetFields+` FROM model_alias_target_versions WHERE tenant_id=? AND workspace_id=? AND alias_id=? AND revision=?`, []any{w.TenantID, w.ID, id, *in.SourceRevision}, []any{w.TenantID, w.ID, id, *in.SourceRevision})))
		if e != nil {
			return CutoverPreview{}, fail("ALIAS_ROLLBACK_TARGET_INVALID")
		}
		var kind, digest string
		var recipe *string
		if e = tx.QueryRow(ctx, distributionStatement(tx, "SELECT model_kind,catalog_digest,serving_recipe_sha256 FROM deployments WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", `SELECT model_kind,catalog_digest,serving_recipe_sha256 FROM deployments WHERE tenant_id=? AND workspace_id=? AND id=?`, []any{w.TenantID, w.ID, target.ID}, []any{w.TenantID, w.ID, target.ID})).Scan(&kind, &digest, &recipe); e != nil {
			return CutoverPreview{}, e
		}
		if source.ModelID != target.ModelID || source.ModelKind != kind || source.CatalogDigest != digest || !reflect.DeepEqual(source.ServingRecipeSHA256, recipe) {
			return CutoverPreview{}, fail("ALIAS_ROLLBACK_TARGET_INVALID")
		}
	}
	if in.Action == "resume" {
		var count int
		if e = tx.QueryRow(ctx, distributionStatement(tx, "SELECT count(*) FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND disabled_at IS NULL", `SELECT count(*) FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND disabled_at IS NULL`, []any{w.TenantID, w.ID}, []any{w.TenantID, w.ID})).Scan(&count); e != nil {
			return CutoverPreview{}, e
		}
		if count >= s.Identity.Config.Policy().Distribution.AliasMaxActive {
			return CutoverPreview{}, fail("RESOURCE_LIMIT_REACHED")
		}
		out.After.Paused = false
	}
	out.After.DeploymentID = target.ID
	out.After.DeploymentRevision = target.Revision
	return out, nil
}
