package models

import (
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations/snapshot"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Fixed v1 wire ceilings cover bounded schema fields and the complete data envelope.
const (
	HistoryMaximumLimit       = 100
	HistoryMaximumItemBytes   = 4 << 10
	HistoryMaximumPageBytes   = 512 << 10
	HistoryMaximumDetailBytes = 128 << 10
)

// HistoryPolicy reports the installed page default and immutable v1 wire ceilings.
type HistoryPolicy struct {
	DefaultLimit       int `json:"default_limit"`
	MaximumLimit       int `json:"maximum_limit"`
	MaximumCursorBytes int `json:"maximum_cursor_bytes"`
	MaximumPageBytes   int `json:"maximum_page_bytes"`
	MaximumDetailBytes int `json:"maximum_detail_bytes"`
}

// VersionHistoryPolicy reuses the owning installation's configured UI page size.
func (s *Service) VersionHistoryPolicy() HistoryPolicy {
	if s == nil || s.Identity == nil {
		return HistoryPolicy{}
	}
	return HistoryPolicy{s.Identity.Config.Policy().UI.PageSize, HistoryMaximumLimit, HistoryMaximumCursorBytes, HistoryMaximumPageBytes, HistoryMaximumDetailBytes}
}

// HistoryQuery preserves omitted values separately from explicit invalid empty inputs.
type HistoryQuery struct {
	Limit  *int
	Cursor *string
	State  *string
}

// HistoricalMetadata is retained metadata, never an execution admission result.
type HistoricalMetadata struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	Name          string    `json:"name"`
	ModelKey      string    `json:"model_key"`
	Kind          string    `json:"kind"`
	CatalogDigest string    `json:"catalog_digest"`
	State         string    `json:"state"`
	CreatedAt     time.Time `json:"created_at"`
}

// VersionHistory is one currently authorized page; NextCursor is always encoded.
type VersionHistory struct {
	WorkspaceID     string               `json:"workspace_id"`
	FamilyID        string               `json:"family_id"`
	AnchorVersionID string               `json:"anchor_version_id"`
	Items           []HistoricalMetadata `json:"items"`
	NextCursor      *string              `json:"next_cursor"`
}

// HistoricalDetail reuses the immutable manifest and existing retained lineage.
type HistoricalDetail struct {
	HistoricalMetadata
	FamilyID        string                   `json:"family_id"`
	AnchorVersionID string                   `json:"anchor_version_id"`
	Manifest        json.RawMessage          `json:"manifest"`
	DatasetLineage  *snapshot.DatasetLineage `json:"dataset_lineage,omitempty"`
}

func historicalMetadata(value Version) (HistoricalMetadata, error) {
	state := value.State
	if state == "deleted" {
		state = "retired"
	}
	if !nonzeroModelID(value.ID) || !nonzeroModelID(value.WorkspaceID) ||
		!utf8.ValidString(value.Name) || utf8.RuneCountInString(value.Name) < 1 || utf8.RuneCountInString(value.Name) > 100 ||
		value.ModelKey != "qwen3-0.6b" || state != "ready" && state != "retired" || !historyStampValid(value.CreatedAt) {
		return HistoricalMetadata{}, fail("MODEL_HISTORY_INVALID")
	}
	out := HistoricalMetadata{value.ID, value.WorkspaceID, value.Name, value.ModelKey, value.Kind, value.CatalogDigest, state, value.CreatedAt}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > HistoryMaximumItemBytes {
		return HistoricalMetadata{}, fail("MODEL_HISTORY_INVALID")
	}
	return out, nil
}

func historyEnvelopeBound(value any, maximum int) error {
	raw, err := json.Marshal(struct {
		Data any `json:"data"`
	}{value})
	if err != nil || len(raw) > maximum {
		return fail("MODEL_HISTORY_INVALID")
	}
	return nil
}

func (s *Service) retainedGraphWithin(ctx context.Context, q transaction.Executor, tenant, workspace string, value *Version) error {
	if _, err := historicalMetadata(*value); err != nil {
		return err
	}
	if value.Kind != "merged_lora" {
		return nil
	}
	catalog, err := s.RetainedDerivedVersionWithin(ctx, q, tenant, workspace, value.ID)
	if err != nil {
		if err.Error() == "RESOURCE_NOT_FOUND" {
			return fail("MODEL_DERIVED_CATALOG_INVALID")
		}
		return err
	}
	value.DatasetLineage, err = derivedLineageWithin(ctx, q, catalog)
	return err
}

func (s *Service) retainedVersionWithin(ctx context.Context, q transaction.Executor, tenant, workspace, version string) (Version, error) {
	out, err := scanVersion(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT `+versionFields+` FROM model_versions WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND state IN ('ready','deleted') FOR SHARE`, tenant, workspace, version)), tenant)
	if err == nil {
		err = s.retainedGraphWithin(ctx, q, tenant, workspace, &out)
	}
	if err != nil {
		return Version{}, err
	}
	return out, nil
}

type historyAnchor struct {
	workspace workspaces.Workspace
	version   Version
	family    string
}

func (s *Service) historyAnchorWithin(ctx context.Context, q transaction.Executor, a identity.Auth, workspace, anchor string) (historyAnchor, error) {
	w, err := s.modelScope(ctx, q, a, workspace)
	if err != nil {
		return historyAnchor{}, err
	}
	if w.Status != "active" {
		return historyAnchor{}, fail("ACCESS_DENIED")
	}
	if err = historyAvailable(ctx, q); err != nil {
		return historyAnchor{}, err
	}
	selected, err := s.retainedVersionWithin(ctx, q, w.TenantID, w.ID, anchor)
	if err != nil {
		return historyAnchor{}, err
	}
	family, err := versionFamilyWithin(ctx, q, w.TenantID, w.ID, anchor)
	if err != nil {
		return historyAnchor{}, err
	}
	return historyAnchor{workspace: w, version: selected, family: family}, nil
}

// History resolves the family from an exact anchor under current Admin authority.
// It returns a keyset page without scanning physical files or granting execution.
func (s *Service) History(ctx context.Context, a identity.Auth, workspace, anchor string, input HistoryQuery) (VersionHistory, error) {
	var out VersionHistory
	if !admin(a) {
		return out, fail("ACCESS_DENIED")
	}
	if !nonzeroModelID(workspace) || !nonzeroModelID(anchor) {
		return out, fail("RESOURCE_NOT_FOUND")
	}
	err := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		frame, err := s.historyAnchorWithin(ctx, q, a, workspace, anchor)
		if err != nil {
			return err
		}
		w, family := frame.workspace, frame.family
		policy := s.VersionHistoryPolicy()
		limit, state := policy.DefaultLimit, ""
		if limit < 1 || limit > policy.MaximumLimit || !historyKeyValid(s.Identity.HMACKey) {
			return fail("MODEL_HISTORY_UNAVAILABLE")
		}
		if input.Limit != nil {
			limit = *input.Limit
			if limit < 1 || limit > policy.MaximumLimit {
				return fail("VALIDATION_FAILED")
			}
		}
		if input.State != nil {
			state = *input.State
			if state != "ready" && state != "retired" {
				return fail("VALIDATION_FAILED")
			}
		}
		scope := historyScope(a, w.TenantID, w.ID, family, anchor, state)
		var stamp, after any
		if input.Cursor != nil {
			cursor, err := decodeHistoryCursor(*input.Cursor, scope, s.Identity.HMACKey)
			if err != nil {
				return err
			}
			stamp, after = cursor.Time, cursor.ID
		}
		rows, err := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `
SELECT v.id::text,v.workspace_id::text,v.name,v.model_key,v.catalog_digest,v.manifest,v.state,v.created_at,v.kind
FROM model_family_members m JOIN model_versions v ON (v.tenant_id,v.workspace_id,v.id)=(m.tenant_id,m.workspace_id,m.version_id)
WHERE m.tenant_id=$1 AND m.workspace_id=$2 AND m.family_id=$3 AND v.state IN ('ready','deleted')
 AND ($4='' OR v.state=CASE WHEN $4='retired' THEN 'deleted' ELSE $4 END)
 AND ($5::timestamptz IS NULL OR (v.created_at,v.id)<($5,$6::uuid))
ORDER BY v.created_at DESC,v.id DESC LIMIT $7 FOR SHARE OF v`, w.TenantID, w.ID, family, state, stamp, after, limit+1))
		if err != nil {
			return err
		}
		values := make([]Version, 0, limit+1)
		for rows.Next() {
			value, err := scanVersion(rows, w.TenantID)
			if err != nil {
				rows.Close()
				return err
			}
			values = append(values, value)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		out = VersionHistory{WorkspaceID: w.ID, FamilyID: family, AnchorVersionID: anchor, Items: []HistoricalMetadata{}}
		for i := range values {
			if err = s.retainedGraphWithin(ctx, q, w.TenantID, w.ID, &values[i]); err != nil {
				return err
			}
			if i < limit {
				metadata, err := historicalMetadata(values[i])
				if err != nil {
					return err
				}
				out.Items = append(out.Items, metadata)
			}
		}
		if len(values) > limit {
			last := values[limit-1]
			cursor, err := encodeHistoryCursor(last.CreatedAt, last.ID, scope, s.Identity.HMACKey)
			if err != nil {
				return err
			}
			out.NextCursor = &cursor
		}
		return historyEnvelopeBound(out, policy.MaximumPageBytes)
	})
	if err != nil {
		return VersionHistory{}, err
	}
	return out, nil
}

// HistoricalVersion reads an exact ready or retired member of the anchor's family.
// It validates retained metadata only; existing ready/file admission stays separate.
func (s *Service) HistoricalVersion(ctx context.Context, a identity.Auth, workspace, anchor, version string) (HistoricalDetail, error) {
	var out HistoricalDetail
	if !admin(a) {
		return out, fail("ACCESS_DENIED")
	}
	if !nonzeroModelID(workspace) || !nonzeroModelID(anchor) || !nonzeroModelID(version) {
		return out, fail("RESOURCE_NOT_FOUND")
	}
	err := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		frame, err := s.historyAnchorWithin(ctx, q, a, workspace, anchor)
		if err != nil {
			return err
		}
		w, family, selected := frame.workspace, frame.family, frame.version
		if version != anchor {
			var member bool
			err = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT EXISTS(
 SELECT 1 FROM model_family_members WHERE tenant_id=$1 AND workspace_id=$2 AND family_id=$3 AND version_id=$4)`,
				w.TenantID, w.ID, family, version)).Scan(&member)
			if err != nil {
				return err
			}
			if !member {
				return fail("RESOURCE_NOT_FOUND")
			}
			selected, err = s.retainedVersionWithin(ctx, q, w.TenantID, w.ID, version)
			if err != nil {
				return err
			}
		}
		metadata, err := historicalMetadata(selected)
		if err != nil {
			return err
		}
		out = HistoricalDetail{HistoricalMetadata: metadata, FamilyID: family, AnchorVersionID: anchor, Manifest: selected.Manifest, DatasetLineage: selected.DatasetLineage}
		return historyEnvelopeBound(out, HistoryMaximumDetailBytes)
	})
	if err != nil {
		return HistoricalDetail{}, err
	}
	return out, nil
}
