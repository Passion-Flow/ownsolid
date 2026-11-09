package imageregistry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

type Service struct {
	identity    *identity.Service
	workspaces  *workspaces.Service
	nodes       *nodes.Service
	deployments *deployments.Service
	license     *licensing.Service
	config      Config
	remote      *distributionClient
	imports     chan struct{}
}
type Pin struct {
	ID                     string    `json:"id"`
	WorkspaceID            string    `json:"workspace_id"`
	AssetID                string    `json:"asset_id"`
	State                  string    `json:"state"`
	ManifestDigest         *string   `json:"manifest_digest"`
	ConfigDigest           *string   `json:"config_digest"`
	ArchiveSHA256          *string   `json:"archive_sha256"`
	ArchiveBytes           *int64    `json:"archive_bytes"`
	Revision               int64     `json:"revision"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	tenant, backend, actor string
	epoch                  int64
	inventory              []byte
	inventorySHA           *string
}
type Page struct {
	Items      []Pin   `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
type Mutation struct {
	WorkspaceID string `json:"workspace_id"`
	Reason      string `json:"reason"`
	Key         string `json:"idempotency_key"`
	RequestID   string `json:"-"`
}

func New(i *identity.Service, w *workspaces.Service, n *nodes.Service, d *deployments.Service, l *licensing.Service, c Config) (*Service, error) {
	if i == nil || w == nil || w.Identity != i || n == nil || n.Identity != i || n.Workspaces != w || d == nil || d.RegistryActions != nil || d.Nodes != n || d.Models == nil || d.Models.Identity != i || l == nil || !l.UsesIdentity(i) {
		return nil, failure("REGISTRY_OWNER_REQUIRED")
	}
	if _, e := i.Repository(); e != nil {
		return nil, e
	}
	remote, e := newDistribution(c)
	if e != nil {
		return nil, e
	}
	root, e := privateRoot(c.StoreRoot)
	if e != nil {
		remote.Close()
		return nil, e
	}
	root.Close()
	service := &Service{i, w, n, d, l, c, remote, make(chan struct{}, c.MaximumConcurrentImports)}
	d.RegistryActions = service
	return service, nil
}
func (s *Service) Close() {
	if s != nil && s.remote != nil {
		s.remote.Close()
	}
}
func vendorStatement(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, args...)
	default:
		return transaction.Statement{}
	}
}
func registryJSON(q transaction.Executor, raw []byte) any {
	if q.Dialect() == transaction.Dameng {
		return string(raw)
	}
	return raw
}
func statement(q transaction.Executor, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, args...)
	}
	return transaction.SQL(transaction.PostgreSQL, pg, args...)
}
func safeError(e error) error {
	if e == nil {
		return nil
	}
	if kind, ok := transaction.CommitOutcome(e); ok {
		return failure(string(kind))
	}
	if transaction.IsCancellationOnly(e) {
		if errors.Is(e, context.Canceled) {
			return failure(string(transaction.Cancelled))
		}
		return failure(string(transaction.Timeout))
	}
	if kind, ok := singleTransactionKind(e, 0); ok {
		return failure(string(kind))
	}
	code := e.Error()
	for _, prefix := range []string{"REGISTRY_", "LICENSE_", "AGENT_", "INFERENCE_", "AUTH_", "STORAGE_", "REPOSITORY_"} {
		if strings.HasPrefix(code, prefix) && !strings.ContainsAny(code, " \n\r\t:") {
			return failure(code)
		}
	}
	switch code {
	case "ACCESS_DENIED", "RESOURCE_NOT_FOUND", "VALIDATION_FAILED", "IDEMPOTENCY_CONFLICT", "VERSION_CONFLICT", "NODE_UNAVAILABLE", "NODE_BUSY":
		return failure(code)
	}
	return failure("STORAGE_UNAVAILABLE")
}
func (s *Service) store() (transaction.Store, error) {
	if s == nil || s.identity == nil || s.remote == nil {
		return nil, failure("REGISTRY_OWNER_REQUIRED")
	}
	return s.identity.Repository()
}
func reason(v string) bool {
	return v != "" && strings.TrimSpace(v) == v && utf8.ValidString(v) && utf8.RuneCountInString(v) <= 500 && !strings.ContainsFunc(v, unicode.IsControl)
}
func (m Mutation) validate() error {
	if !validID(m.WorkspaceID) || !validID(m.Key) || !validID(m.RequestID) || !reason(m.Reason) {
		return failure("VALIDATION_FAILED")
	}
	return nil
}
func (s *Service) actor(ctx context.Context, a identity.Auth, workspace string, admin, business bool, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	if ctx == nil || fn == nil || !validID(workspace) || a.Me.Audience != "admin" && a.Me.Audience != "portal" || admin && (a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin) {
		return failure("ACCESS_DENIED")
	}
	store, e := s.store()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.OperationTimeout())
	defer cancel()
	// Persist license-clock observation even when new business is denied, then
	// repeat real history admission in the owning business transaction.
	if business {
		if e = s.license.Check(ctx); e != nil {
			return e
		}
	}
	return safeError(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		w, e := s.workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if w.Status != "active" {
			return failure("ACCESS_DENIED")
		}
		if business {
			if e = s.license.CheckWithin(ctx, q); e != nil {
				return e
			}
		}
		return fn(ctx, q, w)
	}))
}
func registryAudit(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, action, target string, m Mutation) error {
	_, e := q.Exec(ctx, statement(q, `INSERT INTO audit_events(id,occurred_at,actor_user_id,audience,action,outcome,target_type,target_id,reason,request_id,tenant_id,workspace_id) VALUES($1,clock_timestamp(),$2,$3,$4,'success','image_registry_pin',$5,$6,$7,$8,$9)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,audience,action,outcome,target_type,target_id,reason,request_id,tenant_id,workspace_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'success','image_registry_pin',?,?,?,?,?)`, uuid.NewString(), a.Me.User.ID, a.Me.Audience, "image_registry."+action, target, m.Reason, m.RequestID, w.TenantID, w.ID))
	return e
}
func mutationFingerprint(intent any) ([32]byte, error) {
	raw, e := json.Marshal(intent)
	return sha256.Sum256(raw), e
}

func lookupMutationWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, operation, key string, fingerprint [32]byte) (string, bool, error) {
	var previous string
	var stored []byte
	e := q.QueryRow(ctx, statement(q, `SELECT target_id::text,fingerprint FROM image_registry_mutations WHERE tenant_id=$1 AND workspace_id=$2 AND actor_id=$3 AND operation=$4 AND idempotency_key=$5 FOR UPDATE`, `SELECT target_id,fingerprint FROM image_registry_mutations WHERE tenant_id=? AND workspace_id=? AND actor_id=? AND operation=? AND idempotency_key=? FOR UPDATE`, w.TenantID, w.ID, a.Me.User.ID, operation, key)).Scan(&previous, &stored)
	if errors.Is(e, sql.ErrNoRows) {
		return "", false, nil
	}
	if e != nil {
		return "", false, e
	}
	if !bytes.Equal(stored, fingerprint[:]) {
		return "", false, failure("IDEMPOTENCY_CONFLICT")
	}
	return previous, true, nil
}

func mutationWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, operation, target string, m Mutation, intent any) (string, bool, error) {
	fingerprint, e := mutationFingerprint(intent)
	if e != nil {
		return "", false, e
	}
	previous, replay, e := lookupMutationWithin(ctx, q, a, w, operation, m.Key, fingerprint)
	if e != nil || replay {
		return previous, replay, e
	}
	_, e = q.Exec(ctx, statement(q, `INSERT INTO image_registry_mutations(tenant_id,workspace_id,actor_id,operation,idempotency_key,fingerprint,target_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, `INSERT INTO image_registry_mutations(tenant_id,workspace_id,actor_id,operation,idempotency_key,fingerprint,target_id) VALUES(?,?,?,?,?,?,?)`, w.TenantID, w.ID, a.Me.User.ID, operation, m.Key, fingerprint[:], target))
	return target, false, e
}
func scanPin(row transaction.Row) (Pin, error) {
	var p Pin
	e := row.Scan(&p.ID, &p.tenant, &p.WorkspaceID, &p.AssetID, &p.State, &p.ManifestDigest, &p.ConfigDigest, &p.ArchiveSHA256, &p.ArchiveBytes, &p.Revision, &p.CreatedAt, &p.UpdatedAt, &p.backend, &p.actor, &p.epoch, &p.inventory, &p.inventorySHA)
	if e != nil {
		return Pin{}, e
	}
	p.CreatedAt = p.CreatedAt.UTC()
	p.UpdatedAt = p.UpdatedAt.UTC()
	return p, nil
}

const pinPG = `id::text,tenant_id::text,workspace_id::text,asset_id,state,manifest_digest,config_digest,archive_sha256,archive_bytes,revision,created_at,updated_at,backend_identity,created_by::text,actor_epoch,inventory,inventory_sha256`
const pinMY = `id,tenant_id,workspace_id,asset_id,state,manifest_digest,config_digest,archive_sha256,archive_bytes,revision,created_at,updated_at,backend_identity,created_by,actor_epoch,inventory,inventory_sha256`

func pinWithin(ctx context.Context, q transaction.Executor, tenant, workspace, id string) (Pin, error) {
	p, e := scanPin(q.QueryRow(ctx, statement(q, "SELECT "+pinPG+" FROM image_registry_pins WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", "SELECT "+pinMY+" FROM image_registry_pins WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE", tenant, workspace, id)))
	if errors.Is(e, sql.ErrNoRows) {
		return Pin{}, failure("RESOURCE_NOT_FOUND")
	}
	return p, e
}
func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, id string) (Pin, error) {
	var out Pin
	if !validID(id) {
		return out, failure("RESOURCE_NOT_FOUND")
	}
	e := s.actor(ctx, a, workspace, false, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, e = pinWithin(ctx, q, w.TenantID, w.ID, id)
		return e
	})
	if e != nil {
		return Pin{}, e
	}
	return out, nil
}
func (s *Service) List(ctx context.Context, a identity.Auth, workspace, cursor string) (Page, error) {
	out := Page{Items: []Pin{}}
	if cursor != "" && !validID(cursor) {
		return Page{}, failure("VALIDATION_FAILED")
	}
	e := s.actor(ctx, a, workspace, false, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		var query transaction.Statement
		if q.Dialect() == transaction.MySQL {
			query = transaction.SQL(transaction.MySQL, "SELECT "+pinMY+" FROM image_registry_pins WHERE tenant_id=? AND workspace_id=? AND (?='' OR id>?) ORDER BY id LIMIT ?", w.TenantID, w.ID, cursor, cursor, s.config.PageSize+1)
		} else {
			query = transaction.SQL(transaction.PostgreSQL, "SELECT "+pinPG+" FROM image_registry_pins WHERE tenant_id=$1 AND workspace_id=$2 AND ($3='' OR id>NULLIF($3,'')::uuid) ORDER BY id LIMIT $4", w.TenantID, w.ID, cursor, s.config.PageSize+1)
		}
		rows, e := q.Query(ctx, query)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			p, e := scanPin(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, p)
		}
		return rows.Err()
	})
	if e != nil {
		return Page{}, e
	}
	if len(out.Items) > s.config.PageSize {
		out.Items = out.Items[:s.config.PageSize]
		next := out.Items[len(out.Items)-1].ID
		out.NextCursor = &next
	}
	return out, nil
}
func cachedMatches(p Pin, image cachedImage) error {
	raw, e := json.Marshal(image)
	if e != nil || p.inventorySHA == nil || strings.TrimPrefix(digestBytes(raw), "sha256:") != *p.inventorySHA || p.ManifestDigest == nil || *p.ManifestDigest != image.Inventory.ManifestDigest || p.ConfigDigest == nil || *p.ConfigDigest != image.Inventory.ConfigDigest || p.ArchiveSHA256 == nil || *p.ArchiveSHA256 != image.Archive.TransportSHA256 || p.ArchiveBytes == nil || *p.ArchiveBytes != image.Archive.TransportBytes {
		return failure("REGISTRY_CONTENT_INVALID")
	}
	return nil
}
