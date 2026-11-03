// Package models owns scoped immutable model imports and artifact metadata.
package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type Service struct {
	DirectoryCache DirectoryCache
	DB             *pgxpool.Pool
	Identity       *identity.Service
	Workspaces     *workspaces.Service
	Referenced     func(context.Context, transaction.Executor, string, string, string) (bool, error)
	RootDirectory  string
}
type Import struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	Name          string    `json:"name"`
	CatalogDigest string    `json:"catalog_digest"`
	ExpectedBytes int64     `json:"expected_bytes"`
	UploadedBytes int64     `json:"uploaded_bytes"`
	State         string    `json:"state"`
	ErrorCode     *string   `json:"error_code"`
	ModelID       *string   `json:"model_id"`
	Attempts      int       `json:"attempts"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
type ImportRequest struct {
	WorkspaceID         string `json:"workspace_id"`
	Name                string `json:"name"`
	CatalogDigest       string `json:"catalog_digest"`
	ExpectedBytes       int64  `json:"expected_bytes"`
	LicenseAcknowledged bool   `json:"license_acknowledged"`
	Reason              string `json:"reason"`
}
type Version struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	Name          string          `json:"name"`
	ModelKey      string          `json:"model_key"`
	CatalogDigest string          `json:"catalog_digest"`
	Kind          string          `json:"kind"`
	Manifest      json.RawMessage `json:"manifest"`
	State         string          `json:"state"`
	CreatedAt     time.Time       `json:"created_at"`
}

const importFields = "id::text,workspace_id::text,name,catalog_digest,expected_bytes,uploaded_bytes,state,error_code,model_id::text,attempts,created_at,updated_at"

func scanImport(row pgx.Row) (Import, error) {
	var value Import
	e := row.Scan(&value.ID, &value.WorkspaceID, &value.Name, &value.CatalogDigest, &value.ExpectedBytes, &value.UploadedBytes, &value.State, &value.ErrorCode, &value.ModelID, &value.Attempts, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(e, pgx.ErrNoRows) || errors.Is(e, sql.ErrNoRows) {
		return value, fail("RESOURCE_NOT_FOUND")
	}
	return value, e
}
func admin(a identity.Auth) bool { return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin }
func reasonValid(reason string) bool {
	return utf8.ValidString(reason) && strings.TrimSpace(reason) != "" && utf8.RuneCountInString(reason) <= 500
}
func (s *Service) transaction(ctx context.Context, a identity.Auth, workspace string, fn func(pgx.Tx, workspaces.Workspace) error) error {
	if !admin(a) {
		return fail("ACCESS_DENIED")
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	defer tx.Rollback(ctx)
	if e = s.Identity.Recheck(ctx, tx, a); e != nil {
		return e
	}
	w, e := s.Workspaces.RecheckScope(ctx, tx, a, workspace)
	if e != nil {
		return e
	}
	if w.Status != "active" {
		return fail("ACCESS_DENIED")
	}
	if e = fn(tx, w); e != nil {
		return e
	}
	if tx.Commit(ctx) != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return nil
}
func audit(ctx context.Context, tx pgx.Tx, actor, tenant, workspace, action, id, reason, request string) error {
	return auditTarget(ctx, tx, actor, tenant, workspace, action, "model_import", id, reason, request)
}
func auditTarget(ctx context.Context, tx pgx.Tx, actor, tenant, workspace, action, target, id, reason, request string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),NULLIF($2,'')::uuid,$3,$4,'admin',$5,'success',$6,$7,NULLIF($8,''),$9)`, uuid.NewString(), actor, tenant, workspace, action, target, id, reason, request)
	return e
}

func (s *Service) StartImport(ctx context.Context, a identity.Auth, input ImportRequest, key, request string) (Import, error) {
	return s.startImport(ctx, a, input, key, request)
}
func (s *Service) GetImport(ctx context.Context, a identity.Auth, id string) (Import, error) {
	return s.getImport(ctx, a, id)
}
func (s *Service) UploadChunk(ctx context.Context, a identity.Auth, id string, offset, size int64, digest string, source io.Reader) (Import, error) {
	return s.uploadChunk(ctx, a, id, offset, size, digest, source)
}
func (s *Service) QueueImport(ctx context.Context, a identity.Auth, id, reason, request string) (Import, error) {
	return s.queueImport(ctx, a, id, reason, request)
}
func (s *Service) List(ctx context.Context, a identity.Auth, workspace string) ([]Version, error) {
	return s.listVersions(ctx, a, workspace)
}

// StorageAvailable checks the configured private root without exposing its host path.
func (s *Service) StorageAvailable() bool {
	root, e := openStore(s.RootDirectory)
	if e != nil {
		return false
	}
	root.Close()
	return true
}
