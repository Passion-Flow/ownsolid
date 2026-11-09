package distribution

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/google/uuid"
)

type CutoverRequest struct {
	Action             string  `json:"action"`
	ExpectedRevision   int64   `json:"expected_revision"`
	DeploymentID       *string `json:"deployment_id"`
	DeploymentRevision *int64  `json:"deployment_revision"`
	SourceRevision     *int64  `json:"source_revision"`
	Reason             string  `json:"reason"`
}
type CutoverResult struct {
	Alias           Alias `json:"alias"`
	AppliedRevision int64 `json:"applied_revision"`
	Replayed        bool  `json:"replayed"`
}
type AliasTarget struct {
	SourceRevision      *int64    `json:"source_revision"`
	ID                  string    `json:"id"`
	AliasID             string    `json:"alias_id"`
	Revision            int64     `json:"revision"`
	Action              string    `json:"action"`
	DeploymentID        string    `json:"deployment_id"`
	DeploymentRevision  int64     `json:"deployment_revision"`
	ModelID             string    `json:"model_id"`
	ModelKind           string    `json:"model_kind"`
	CatalogDigest       string    `json:"catalog_digest"`
	ServingRecipeSHA256 *string   `json:"serving_recipe_sha256"`
	CreatedAt           time.Time `json:"created_at"`
}

const targetFields = `id::text,alias_id::text,revision,action,deployment_id::text,deployment_revision,model_id::text,model_kind,catalog_digest,serving_recipe_sha256,created_at,source_revision`

func scanAliasTarget(row transaction.Row) (AliasTarget, error) {
	var out AliasTarget
	e := row.Scan(&out.ID, &out.AliasID, &out.Revision, &out.Action, &out.DeploymentID, &out.DeploymentRevision, &out.ModelID, &out.ModelKind, &out.CatalogDigest, &out.ServingRecipeSHA256, &out.CreatedAt, &out.SourceRevision)
	if errors.Is(e, sql.ErrNoRows) {
		return AliasTarget{}, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return AliasTarget{}, e
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return out, nil
}
func (s *Service) recordAliasTarget(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, id string, revision int64, action string, source *int64) error {
	result, e := tx.Exec(ctx, distributionNativeStatement(tx, `INSERT INTO MODEL_ALIAS_TARGET_VERSIONS(ID,TENANT_ID,WORKSPACE_ID,ALIAS_ID,REVISION,ACTION,DEPLOYMENT_ID,DEPLOYMENT_REVISION,MODEL_ID,MODEL_KIND,CATALOG_DIGEST,SERVING_RECIPE_SHA256,SOURCE_REVISION) SELECT ?,A.TENANT_ID,A.WORKSPACE_ID,A.ID,A.REVISION,?,D.ID,A.DEPLOYMENT_REVISION,D.MODEL_ID,D.MODEL_KIND,D.CATALOG_DIGEST,D.SERVING_RECIPE_SHA256,? FROM MODEL_ALIASES A JOIN DEPLOYMENTS D ON D.TENANT_ID=A.TENANT_ID AND D.WORKSPACE_ID=A.WORKSPACE_ID AND D.NODE_ID=A.NODE_ID AND D.ID=A.DEPLOYMENT_ID WHERE A.TENANT_ID=? AND A.WORKSPACE_ID=? AND A.ID=? AND A.REVISION=?`, []any{uuid.NewString(), action, source, w.TenantID, w.ID, id, revision}, `INSERT INTO model_alias_target_versions(id,tenant_id,workspace_id,alias_id,revision,action,deployment_id,deployment_revision,model_id,model_kind,catalog_digest,serving_recipe_sha256,source_revision)
 SELECT $1,a.tenant_id,a.workspace_id,a.id,a.revision,$6,d.id,a.deployment_revision,d.model_id,d.model_kind,d.catalog_digest,d.serving_recipe_sha256,$7
 FROM model_aliases a JOIN deployments d ON d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.node_id=a.node_id AND d.id=a.deployment_id
 WHERE a.tenant_id=$2 AND a.workspace_id=$3 AND a.id=$4 AND a.revision=$5`, `INSERT INTO model_alias_target_versions(id,tenant_id,workspace_id,alias_id,revision,action,deployment_id,deployment_revision,model_id,model_kind,catalog_digest,serving_recipe_sha256,source_revision)
 SELECT ?,a.tenant_id,a.workspace_id,a.id,a.revision,?,d.id,a.deployment_revision,d.model_id,d.model_kind,d.catalog_digest,d.serving_recipe_sha256,?
 FROM model_aliases a JOIN deployments d ON d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.node_id=a.node_id AND d.id=a.deployment_id
 WHERE a.tenant_id=? AND a.workspace_id=? AND a.id=? AND a.revision=?`, []any{uuid.NewString(), w.TenantID, w.ID, id, revision, action, source}, []any{uuid.NewString(), action, source, w.TenantID, w.ID, id, revision}))
	if e != nil {
		return e
	}
	if result != 1 {
		return fail("MODEL_ALIAS_UNAVAILABLE")
	}
	return nil
}
func validCutover(in CutoverRequest) bool {
	if in.ExpectedRevision < 1 || in.ExpectedRevision >= maximumRevision || !reason(in.Reason) {
		return false
	}
	switch in.Action {
	case "pause", "resume":
		return in.DeploymentID == nil && in.DeploymentRevision == nil && in.SourceRevision == nil
	case "retarget", "rollback":
		if in.DeploymentID == nil || !wire.ValidID(*in.DeploymentID) || in.DeploymentRevision == nil || *in.DeploymentRevision < 1 || *in.DeploymentRevision >= maximumRevision {
			return false
		}
		if in.Action == "retarget" {
			return in.SourceRevision == nil
		}
		return in.SourceRevision != nil && *in.SourceRevision >= 1 && *in.SourceRevision < in.ExpectedRevision
	}
	return false
}

func (s *Service) Cutover(ctx context.Context, a identity.Auth, workspace, id string, in CutoverRequest, key, request string) (CutoverResult, error) {
	var out CutoverResult
	if !admin(a) {
		return out, fail("ACCESS_DENIED")
	}
	if !wire.ValidID(id) || !wire.ValidID(key) || !validCutover(in) {
		return out, fail("VALIDATION_FAILED")
	}
	e := s.transaction(ctx, a, workspace, in.Action != "pause", nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		expected := fingerprint(a.Me.User.ID, struct {
			Alias string
			Input CutoverRequest
		}{id, in})
		var saved []byte
		var retainedAlias string
		e := tx.QueryRow(ctx, distributionStatement(tx, "SELECT alias_id::text,result_revision,fingerprint FROM model_alias_cutover_intents WHERE tenant_id=$1 AND workspace_id=$2 AND actor_user_id=$3 AND idempotency_key=$4", `SELECT alias_id,result_revision,fingerprint FROM model_alias_cutover_intents WHERE tenant_id=? AND workspace_id=? AND actor_user_id=? AND idempotency_key=?`, []any{w.TenantID, w.ID, a.Me.User.ID, key}, []any{w.TenantID, w.ID, a.Me.User.ID, key})).Scan(&retainedAlias, &out.AppliedRevision, &saved)
		if e == nil {
			if retainedAlias != id || !same(saved, expected) {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out.Replayed = true
			out.Alias, e = scanAlias(tx.QueryRow(ctx, distributionStatement(tx, "SELECT "+aliasFields+" FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", `SELECT `+mysqlAliasFields+` FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND id=?`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})))
			if e != nil {
				return e
			}
			return s.publicAliasAvailability(ctx, tx, w, &out.Alias)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		plan, e := s.planCutover(ctx, tx, w, id, in)
		if e != nil {
			return e
		}
		action := map[string]string{"pause": "paused", "resume": "resumed", "retarget": "retargeted", "rollback": "rolled_back"}[in.Action]
		switch in.Action {
		case "pause":
			_, e = tx.Exec(ctx, distributionStatement(tx, "UPDATE model_aliases SET disabled_at=clock_timestamp(),revision=revision+1 WHERE id=$1", `UPDATE model_aliases SET disabled_at=UTC_TIMESTAMP(6),revision=revision+1 WHERE id=?`, []any{id}, []any{id}))
		case "resume":
			_, e = tx.Exec(ctx, distributionStatement(tx, "UPDATE model_aliases SET disabled_at=NULL,revision=revision+1 WHERE id=$1", `UPDATE model_aliases SET disabled_at=NULL,revision=revision+1 WHERE id=?`, []any{id}, []any{id}))
		default:
			_, e = tx.Exec(ctx, distributionStatement(tx, "UPDATE model_aliases SET deployment_id=$2,deployment_revision=$3,revision=revision+1 WHERE id=$1", `UPDATE model_aliases SET deployment_id=?,deployment_revision=?,revision=revision+1 WHERE id=?`, []any{id, plan.After.DeploymentID, plan.After.DeploymentRevision}, []any{plan.After.DeploymentID, plan.After.DeploymentRevision, id}))
		}
		if e != nil {
			return e
		}
		out.AppliedRevision = plan.After.Revision
		if _, e = tx.Exec(ctx, distributionStatement(tx, "INSERT INTO model_alias_history(tenant_id,workspace_id,alias_id,revision,action,actor_user_id,reason) VALUES($1,$2,$3,$4,$5,$6,$7)", `INSERT INTO model_alias_history(tenant_id,workspace_id,alias_id,revision,action,actor_user_id,reason) VALUES(?,?,?,?,?,?,?)`, []any{w.TenantID, w.ID, id, out.AppliedRevision, action, a.Me.User.ID, in.Reason}, []any{w.TenantID, w.ID, id, out.AppliedRevision, action, a.Me.User.ID, in.Reason})); e != nil {
			return e
		}
		if e = s.recordAliasTarget(ctx, tx, w, id, out.AppliedRevision, action, in.SourceRevision); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, distributionStatement(tx, "INSERT INTO model_alias_cutover_intents(tenant_id,workspace_id,actor_user_id,idempotency_key,alias_id,result_revision,fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7)", `INSERT INTO model_alias_cutover_intents(tenant_id,workspace_id,actor_user_id,idempotency_key,alias_id,result_revision,fingerprint) VALUES(?,?,?,?,?,?,?)`, []any{w.TenantID, w.ID, a.Me.User.ID, key, id, out.AppliedRevision, expected}, []any{w.TenantID, w.ID, a.Me.User.ID, key, id, out.AppliedRevision, expected})); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "model.alias_"+action, "model_alias", id, in.Reason, request); e != nil {
			return e
		}
		out.Alias, e = scanAlias(tx.QueryRow(ctx, distributionStatement(tx, "SELECT "+aliasFields+" FROM model_aliases WHERE id=$1", `SELECT `+mysqlAliasFields+` FROM model_aliases WHERE id=?`, []any{id}, []any{id})))
		if e != nil {
			return e
		}
		return s.publicAliasAvailability(ctx, tx, w, &out.Alias)
	})
	if e != nil {
		return CutoverResult{}, e
	}
	return out, nil
}
