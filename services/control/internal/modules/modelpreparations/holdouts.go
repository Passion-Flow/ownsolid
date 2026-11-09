package modelpreparations

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations/snapshot"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	"github.com/google/uuid"
)

// LabelCounts is the frozen holdout label distribution.
type LabelCounts = snapshot.LabelCounts

// Holdout is the version 1 snapshot of the preparation evaluation input.
type Holdout = snapshot.Holdout

type HoldoutRequest struct {
	WorkspaceID            string `json:"workspace_id"`
	DatasetID              string `json:"dataset_id"`
	DatasetVersionID       string `json:"dataset_version_id"`
	ExpectedManifestSHA256 string `json:"expected_manifest_sha256"`
	Name                   string `json:"name"`
	Reason                 string `json:"reason"`
}
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

const holdoutFields = `id::text,workspace_id::text,dataset_id::text,dataset_version_id::text,name,manifest_sha256,effective_source_sha256,row_mapping_sha256,source_bytes,row_count,label_counts,recipe_id,recipe_sha256,instruction_sha256,fingerprint_algorithm,created_by::text,audience,reason,created_at`

func scanHoldout(row transaction.Row) (Holdout, error) {
	row = preparationIDRow(row, 0, 1, 2, 3, 15)
	var value Holdout
	var labels []byte
	e := row.Scan(&value.ID, &value.WorkspaceID, &value.DatasetID, &value.DatasetVersionID, &value.Name, &value.ManifestSHA256, &value.EffectiveSourceSHA256, &value.RowMappingSHA256, &value.SourceBytes, &value.RowCount, &labels, &value.RecipeID, &value.RecipeSHA256, &value.InstructionSHA256, &value.FingerprintAlgorithm, &value.CreatedBy, &value.Audience, &value.Reason, &value.CreatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return value, fail("MODEL_HOLDOUT_NOT_FOUND")
	}
	if e != nil {
		return Holdout{}, e
	}
	if wire.DecodeStrict(labels, &value.Labels) != nil || value.RowCount < wire.MinimumRows || value.RowCount > wire.MaximumRows || !value.Labels.ValidForRows(value.RowCount) || value.SourceBytes < 1 || value.SourceBytes > datawire.MaximumBytesCeiling || !datawire.ValidDigest(value.ManifestSHA256) || !datawire.ValidDigest(value.EffectiveSourceSHA256) || !datawire.ValidDigest(value.RowMappingSHA256) || !datawire.ValidDigest(value.RecipeSHA256) || !datawire.ValidDigest(value.InstructionSHA256) {
		return Holdout{}, fail("MODEL_HOLDOUT_DATA_INVALID")
	}
	value.CreatedAt = value.CreatedAt.UTC()
	return value, nil
}
func (s *Service) ReserveHoldout(ctx context.Context, a identity.Auth, input HoldoutRequest, key, request string) (Holdout, error) {
	var out Holdout
	if !datawire.ValidID(key) || !datawire.ValidID(input.WorkspaceID) || !datawire.ValidID(input.DatasetID) || !datawire.ValidID(input.DatasetVersionID) || !datawire.ValidDigest(input.ExpectedManifestSHA256) || !text(input.Name, 100) || !text(input.Reason, 500) {
		return out, fail("VALIDATION_FAILED")
	}
	wanted := fingerprint(a, input)
	replayed := false
	tenant := ""
	e := s.preparationActorTransaction(ctx, a, input.WorkspaceID, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		tenant = w.TenantID
		var e error
		out, replayed, e = s.replayHoldoutWithin(ctx, tx, a, w, key, wanted)
		return e
	})
	if e != nil {
		return Holdout{}, e
	}
	if replayed {
		return out, nil
	}
	if s.Datasets == nil {
		return out, fail("MODEL_HOLDOUT_UNAVAILABLE")
	}
	selected, references, e := s.analyzeHoldout(ctx, a, tenant, input)
	if e != nil {
		return out, sourceError(e)
	}
	e = s.preparationActorTransaction(ctx, a, input.WorkspaceID, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, replayed, e = s.replayHoldoutWithin(ctx, tx, a, w, key, wanted)
		if e != nil || replayed {
			return e
		}
		current, e := s.Datasets.PublishedSourceWithin(ctx, tx, w.TenantID, w.ID, input.DatasetID, input.DatasetVersionID, false)
		if e != nil {
			return e
		}
		if !sameSource(selected, current) || current.ManifestSHA256 != input.ExpectedManifestSHA256 {
			return fail("MODEL_HOLDOUT_SOURCE_CHANGED")
		}
		now, e := s.trainingReferencesWithin(ctx, tx, w.TenantID, w.ID)
		if e != nil {
			return e
		}
		if !sameReferences(now, references) {
			return fail("MODEL_HOLDOUT_CONCURRENT_TRAINING_RETRY")
		}
		var count int
		if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT COUNT(*) FROM "MODEL_HOLDOUTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=?`), `SELECT count(*) FROM model_holdouts WHERE tenant_id=$1 AND workspace_id=$2`, `SELECT count(*) FROM model_holdouts WHERE tenant_id=? AND workspace_id=?`, w.TenantID, w.ID)).Scan(&count); e != nil {
			return e
		}
		if count >= s.policy().MaxHoldoutsPerWorkspace {
			return fail("MODEL_HOLDOUT_LIMIT_REACHED")
		}
		var exists int
		if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT COUNT(*) FROM "MODEL_HOLDOUTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "DATASET_VERSION_ID"=? AND "RECIPE_ID"=?`), `SELECT COUNT(*) FROM model_holdouts WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_version_id=$3 AND recipe_id=$4`, `SELECT COUNT(*) FROM model_holdouts WHERE tenant_id=? AND workspace_id=? AND dataset_version_id=? AND recipe_id=?`, w.TenantID, w.ID, input.DatasetVersionID, wire.RecipeID)).Scan(&exists); e != nil {
			return e
		}
		if exists > 0 {
			return fail("MODEL_HOLDOUT_EXISTS")
		}
		id := uuid.NewString()
		labels, _ := json.Marshal(selected.labels)
		n, e := tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`INSERT INTO "MODEL_HOLDOUTS"("ID","TENANT_ID","WORKSPACE_ID","DATASET_ID","DATASET_VERSION_ID","NAME","MANIFEST_SHA256","EFFECTIVE_SOURCE_SHA256","ROW_MAPPING_SHA256","SOURCE_BYTES","ROW_COUNT","LABEL_COUNTS","RECIPE_ID","RECIPE_SHA256","INSTRUCTION_SHA256","FINGERPRINT_ALGORITHM","CREATED_BY","AUDIENCE","REASON","CREATION_KEY","CREATION_FINGERPRINT") VALUES(?,?,?,?,?,?,?,?,?,?,?,CAST(? AS CLOB),?,?,?,?,?,?,?,?,?)`, 11), `INSERT INTO model_holdouts(id,tenant_id,workspace_id,dataset_id,dataset_version_id,name,manifest_sha256,effective_source_sha256,row_mapping_sha256,source_bytes,row_count,label_counts,recipe_id,recipe_sha256,instruction_sha256,fingerprint_algorithm,created_by,audience,reason,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`, `INSERT INTO model_holdouts(id,tenant_id,workspace_id,dataset_id,dataset_version_id,name,manifest_sha256,effective_source_sha256,row_mapping_sha256,source_bytes,row_count,label_counts,recipe_id,recipe_sha256,instruction_sha256,fingerprint_algorithm,created_by,audience,reason,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?,?,CAST(? AS JSON),?,?,?,?,?,?,?,?,?)`, id, w.TenantID, w.ID, input.DatasetID, input.DatasetVersionID, input.Name, current.ManifestSHA256, current.EffectiveSourceSHA256, current.RowMappingSHA256, selected.size, selected.rowCount, labels, wire.RecipeID, wire.RecipeDigest(), wire.InstructionDigest(), wire.PromptFingerprintAlgorithm, a.Me.User.ID, a.Me.Audience, input.Reason, key, wanted))
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("STORAGE_UNAVAILABLE")
		}
		for _, group := range []struct {
			values     map[string]bool
			pg, my, dm string
		}{{selected.groups, `INSERT INTO model_holdout_sources(tenant_id,workspace_id,holdout_id,source_sha256) VALUES($1,$2,$3,$4)`, `INSERT INTO model_holdout_sources(tenant_id,workspace_id,holdout_id,source_sha256) VALUES(?,?,?,?)`, `INSERT INTO "MODEL_HOLDOUT_SOURCES"("TENANT_ID","WORKSPACE_ID","HOLDOUT_ID","SOURCE_SHA256") VALUES(?,?,?,?)`}, {selected.prompts, `INSERT INTO model_holdout_prompts(tenant_id,workspace_id,holdout_id,prompt_sha256) VALUES($1,$2,$3,$4)`, `INSERT INTO model_holdout_prompts(tenant_id,workspace_id,holdout_id,prompt_sha256) VALUES(?,?,?,?)`, `INSERT INTO "MODEL_HOLDOUT_PROMPTS"("TENANT_ID","WORKSPACE_ID","HOLDOUT_ID","PROMPT_SHA256") VALUES(?,?,?,?)`}} {
			for _, digest := range sortedHashes(group.values) {
				raw, e := hex.DecodeString(digest)
				if e != nil {
					return fail("MODEL_HOLDOUT_DATA_INVALID")
				}
				n, e := tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(group.dm), group.pg, group.my, w.TenantID, w.ID, id, raw))
				if e != nil {
					return e
				}
				if n != 1 {
					return fail("STORAGE_UNAVAILABLE")
				}
			}
		}
		if e = holdoutAuditWithin(ctx, tx, a, w, id, input.Reason, request); e != nil {
			return e
		}
		out, e = holdoutWithin(ctx, tx, w, id)
		return e
	})
	if e != nil {
		return Holdout{}, e
	}
	return out, nil
}
func (s *Service) analyzeHoldout(ctx context.Context, a identity.Auth, tenant string, input HoldoutRequest) (sourceSnapshot, []trainingReference, error) {
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().ScanSeconds))
	defer cancel()
	release, e := s.acquireScan(ctx)
	if e != nil {
		return sourceSnapshot{}, nil, e
	}
	defer release()
	var references []trainingReference
	e = s.preparationActorTransaction(ctx, a, input.WorkspaceID, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if w.TenantID != tenant {
			return fail("ACCESS_DENIED")
		}
		var e error
		references, e = s.trainingReferencesWithin(ctx, q, w.TenantID, w.ID)
		return e
	})
	if e != nil {
		return sourceSnapshot{}, nil, e
	}
	budget := &analysisBudget{}
	selected, e := s.scanSource(ctx, a, input.WorkspaceID, input.DatasetID, input.DatasetVersionID, false, true, budget)
	if e != nil {
		return sourceSnapshot{}, nil, e
	}
	if selected.version.ManifestSHA256 != input.ExpectedManifestSHA256 {
		return sourceSnapshot{}, nil, fail("MODEL_HOLDOUT_SOURCE_CHANGED")
	}
	if len(references)+1 > s.policy().ScanMaximumVersions {
		return sourceSnapshot{}, nil, fail("MODEL_HOLDOUT_ANALYSIS_LIMIT")
	}
	for _, reference := range references {
		if reference.VersionID == selected.version.ID {
			return sourceSnapshot{}, nil, fail("MODEL_HOLDOUT_OVERLAP")
		}
		previous, e := s.scanSource(ctx, a, input.WorkspaceID, reference.DatasetID, reference.VersionID, true, false, budget)
		if e != nil {
			return sourceSnapshot{}, nil, e
		}
		if previous.version.ManifestSHA256 != reference.ManifestSHA256 {
			return sourceSnapshot{}, nil, fail("MODEL_HOLDOUT_SOURCE_CHANGED")
		}
		if overlaps(selected.groups, previous.groups) || overlaps(selected.prompts, previous.prompts) {
			return sourceSnapshot{}, nil, fail("MODEL_HOLDOUT_OVERLAP")
		}
	}
	return selected, references, nil
}
func (s *Service) GetHoldout(ctx context.Context, a identity.Auth, workspace, id string) (Holdout, error) {
	var out Holdout
	e := s.preparationActorTransaction(ctx, a, workspace, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, e = holdoutWithin(ctx, q, w, id)
		return e
	})
	if e != nil {
		return Holdout{}, e
	}
	return out, nil
}
func (s *Service) ListHoldouts(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[Holdout], error) {
	empty := Page[Holdout]{Items: []Holdout{}}
	if ctx == nil || a.Me.Audience != "portal" && !admin(a) {
		return empty, fail("ACCESS_DENIED")
	}
	if _, e := s.reviewRepository(); e != nil {
		return empty, preparationRepositoryError(e)
	}
	query := url.Values{}
	for n, v := range values {
		query[n] = append([]string{}, v...)
	}
	if _, ok := query["limit"]; !ok {
		query.Set("limit", strconv.Itoa(s.Identity.Config.Policy().UI.PageSize))
	}
	limit, cursor, scope, e := pagination.Parse(query, "model:holdouts:"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace)
	if e != nil {
		return empty, e
	}
	var stamp, id any
	if cursor.ID != "" {
		stamp, id = cursor.Time.UTC(), cursor.ID
	}
	page := Page[Holdout]{Items: []Holdout{}}
	e = s.preparationActorTransaction(ctx, a, workspace, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if e := preparationSelectedCursor(q, cursor); e != nil {
			return e
		}
		rows, e := preparationQuery(ctx, q, preparationSelectedSQL(q, preparationNativeHoldoutPage(cursor.ID != ""), "SELECT "+holdoutFields+" FROM model_holdouts WHERE tenant_id=$1 AND workspace_id=$2 AND ($3::timestamptz IS NULL OR (created_at,id)<($4::timestamptz,$5::uuid)) ORDER BY created_at DESC,id DESC LIMIT $6", "SELECT "+mysqlHoldoutFields+" FROM model_holdouts WHERE tenant_id=? AND workspace_id=? AND (? IS NULL OR (created_at,id)<(?,?)) ORDER BY created_at DESC,id DESC LIMIT ?", w.TenantID, w.ID, stamp, stamp, id, limit+1))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			h, e := scanHoldout(rows)
			if e != nil {
				return e
			}
			page.Items = append(page.Items, h)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		if len(page.Items) > limit {
			page.Items = page.Items[:limit]
			last := page.Items[len(page.Items)-1]
			page.NextCursor = pagination.Encode(last.CreatedAt, last.ID, scope)
		}
		return nil
	})
	if e != nil {
		return empty, e
	}
	return page, nil
}
