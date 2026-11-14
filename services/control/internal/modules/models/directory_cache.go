package models

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// DirectoryCache contains data only. It cannot supply actor or scope authority.
type DirectoryCache interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte) error
}
type directoryEntry struct {
	Version        Version `json:"version"`
	ManifestSHA256 string  `json:"manifest_sha256"`
}

type cachedVersion struct {
	ID             string    `json:"id"`
	WorkspaceID    string    `json:"workspace_id"`
	Name           string    `json:"name"`
	ModelKey       string    `json:"model_key"`
	CatalogDigest  string    `json:"catalog_digest"`
	Kind           string    `json:"kind"`
	State          string    `json:"state"`
	CreatedAt      time.Time `json:"created_at"`
	ManifestBase64 string    `json:"manifest_base64"`
}

func encodeDirectory(values []Version) ([]byte, error) {
	cached := make([]cachedVersion, len(values))
	for i, v := range values {
		cached[i] = cachedVersion{ID: v.ID, WorkspaceID: v.WorkspaceID, Name: v.Name, ModelKey: v.ModelKey, CatalogDigest: v.CatalogDigest, Kind: v.Kind, State: v.State, CreatedAt: v.CreatedAt, ManifestBase64: base64.StdEncoding.EncodeToString(v.Manifest)}
	}
	return json.Marshal(cached)
}
func decodeDirectory(raw []byte) ([]Version, error) {
	var cached []cachedVersion
	if e := wire.DecodeStrict(raw, &cached); e != nil {
		return nil, e
	}
	if cached == nil {
		return nil, fail("MODEL_FILE_INVALID")
	}
	values := make([]Version, len(cached))
	for i, v := range cached {
		manifest, e := base64.StdEncoding.Strict().DecodeString(v.ManifestBase64)
		if e != nil {
			return nil, fail("MODEL_FILE_INVALID")
		}
		values[i] = Version{ID: v.ID, WorkspaceID: v.WorkspaceID, Name: v.Name, ModelKey: v.ModelKey, CatalogDigest: v.CatalogDigest, Kind: v.Kind, State: v.State, CreatedAt: v.CreatedAt.UTC(), Manifest: manifest}
	}
	return values, nil
}

type directoryFill struct {
	key   string
	value []byte
}

func (s *Service) directoryFingerprint(ctx context.Context, q transaction.Executor, w workspaces.Workspace) ([]directoryEntry, string, error) {
	rows, e := q.Query(ctx, modelSQL(q,
		`SELECT id::text,workspace_id::text,name,model_key,catalog_digest,state,created_at,kind,encode(sha256(convert_to(manifest::text,'UTF8')),'hex') FROM model_versions WHERE tenant_id=$1 AND workspace_id=$2 AND state='ready' ORDER BY created_at DESC,id DESC LIMIT $3`,
		`SELECT id,workspace_id,name,model_key,catalog_digest,state,created_at,kind,SHA2(CAST(manifest AS CHAR CHARACTER SET utf8mb4),256) FROM model_versions WHERE tenant_id=? AND workspace_id=? AND state='ready' ORDER BY created_at DESC,id DESC LIMIT ?`,
		w.TenantID, w.ID, s.Identity.Config.Policy().UI.LookupPageSize))
	if e != nil {
		return nil, "", e
	}
	defer rows.Close()
	entries := []directoryEntry{}
	for rows.Next() {
		var v directoryEntry
		if e := rows.Scan(&v.Version.ID, &v.Version.WorkspaceID, &v.Version.Name, &v.Version.ModelKey, &v.Version.CatalogDigest, &v.Version.State, &v.Version.CreatedAt, &v.Version.Kind, &v.ManifestSHA256); e != nil {
			return nil, "", e
		}
		v.Version.CreatedAt = v.Version.CreatedAt.UTC()
		entries = append(entries, v)
	}
	if e = rows.Err(); e != nil {
		return nil, "", e
	}
	raw, e := json.Marshal(struct {
		Limit   int
		Entries []directoryEntry
	}{s.Identity.Config.Policy().UI.LookupPageSize, entries})
	if e != nil {
		return nil, "", e
	}
	sum := sha256.Sum256(raw)
	return entries, "model-directory:v1:" + w.TenantID + ":" + w.ID + ":" + hex.EncodeToString(sum[:]), nil
}
func matchesDirectory(values []Version, entries []directoryEntry, tenant string) bool {
	if values == nil || len(values) != len(entries) {
		return false
	}
	for i, v := range values {
		header := entries[i].Version
		if v.ID != header.ID || v.WorkspaceID != header.WorkspaceID || v.Name != header.Name || v.ModelKey != header.ModelKey || v.CatalogDigest != header.CatalogDigest || v.Kind != header.Kind || v.State != header.State || !v.CreatedAt.Equal(header.CreatedAt) {
			return false
		}
		sum := sha256.Sum256(v.Manifest)
		if hex.EncodeToString(sum[:]) != entries[i].ManifestSHA256 || decodeVersionManifest(&v, v.Manifest, tenant) != nil {
			return false
		}
	}
	return true
}
func (s *Service) cachedDirectory(ctx context.Context, q transaction.Executor, w workspaces.Workspace) ([]Version, *directoryFill, error) {
	// Native CLOB bytes are read and validated directly; no database JSON reserialization
	// or unsupported digest function may establish the immutable cache identity.
	if s.DirectoryCache == nil || q.Dialect() == transaction.Dameng {
		v, e := s.readDirectory(ctx, q, w)
		return v, nil, e
	}
	entries, key, e := s.directoryFingerprint(ctx, q, w)
	if e != nil {
		return nil, nil, e
	}
	if raw, e := s.DirectoryCache.Get(ctx, key); e == nil && raw != nil {
		values, decodeError := decodeDirectory(raw)
		if decodeError == nil && matchesDirectory(values, entries, w.TenantID) {
			return values, nil, nil
		}
	}
	values, e := s.readDirectory(ctx, q, w)
	if e != nil {
		return nil, nil, e
	}
	// A concurrent mutation can change this second statement's snapshot. Never
	// publish those bytes under the earlier immutable directory fingerprint.
	if !matchesDirectory(values, entries, w.TenantID) {
		return values, nil, nil
	}
	raw, e := encodeDirectory(values)
	if e != nil {
		return values, nil, nil
	}
	return values, &directoryFill{key: key, value: raw}, nil
}
func (s *Service) readDirectory(ctx context.Context, q transaction.Executor, w workspaces.Workspace) ([]Version, error) {
	rows, e := q.Query(ctx, modelPageSQL(q, "SELECT "+nativeVersionFields+" FROM MODEL_VERSIONS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND STATE='ready' ORDER BY CREATED_AT DESC,ID DESC",
		"SELECT "+versionFields+" FROM model_versions WHERE tenant_id=$1 AND workspace_id=$2 AND state='ready' ORDER BY created_at DESC,id DESC LIMIT $3",
		"SELECT "+mysqlVersionFields+" FROM model_versions WHERE tenant_id=? AND workspace_id=? AND state='ready' ORDER BY created_at DESC,id DESC LIMIT ?",
		w.TenantID, w.ID, s.Identity.Config.Policy().UI.LookupPageSize))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		v, e := scanVersion(rows, w.TenantID)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return out, nil
}
