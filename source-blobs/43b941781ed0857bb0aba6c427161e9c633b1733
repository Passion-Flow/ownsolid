package knowledge

import (
	"context"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

type delegatedActor struct {
	tenant, workspace, actor, audience string
	epoch                              int64
	membership                         *string
}
type candidate struct {
	delegatedActor
	id, base, document, source, generation, kind string
	revision                                     int64
}
type workerState struct {
	base       baseRecord
	document   documentRecord
	generation Generation
	state      string
	lease      []byte
	expired    bool
	valid      bool
	failure    string
}

// Durable jobs retain local epoch/membership delegation, independently of browser logout.
// All state changes follow user -> Workspace -> base -> generation -> document -> attempt.
func (s *Service) lockDelegation(ctx context.Context, tx transaction.Executor, c delegatedActor) (identity.Auth, workspaces.Workspace, bool, error) {
	var epoch int64
	var status string
	var platformAdmin bool
	if e := tx.QueryRow(ctx, knowledgeSQL(tx, "SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=$1 FOR NO KEY UPDATE", `SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=? FOR UPDATE`, []any{c.actor}, []any{c.actor})).Scan(&epoch, &status, &platformAdmin); e != nil {
		return identity.Auth{}, workspaces.Workspace{}, false, e
	}
	a := identity.Auth{Me: identity.Me{User: identity.User{ID: c.actor, IsPlatformAdmin: platformAdmin}, Audience: c.audience}, Epoch: epoch}
	w := workspaces.Workspace{ID: c.workspace, TenantID: c.tenant}
	var tenantActive bool
	if e := tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT w.status,t.status='active' FROM workspaces w JOIN tenants t ON t.id=w.tenant_id JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=$1 AND w.id=$2 FOR NO KEY UPDATE OF w`, `SELECT w.status,t.status='active' FROM workspaces w JOIN tenants t ON t.id=w.tenant_id JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=? AND w.id=? FOR UPDATE`, []any{c.tenant, c.workspace}, []any{c.tenant, c.workspace})).Scan(&w.Status, &tenantActive); e != nil {
		return a, w, false, e
	}
	valid := status == "active" && epoch == c.epoch && tenantActive && w.Status == "active"
	if c.audience == "admin" {
		valid = valid && platformAdmin
	} else if c.audience == "portal" {
		var role, authority string
		e := tx.QueryRow(ctx, knowledgeSQL(tx, "SELECT role,authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", `SELECT role,authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?`, []any{c.tenant, c.workspace, c.actor}, []any{c.tenant, c.workspace, c.actor})).Scan(&role, &authority)
		if errors.Is(e, pgx.ErrNoRows) {
			valid = false
		} else if e != nil {
			return a, w, false, e
		} else {
			w.Role = &role
			valid = valid && c.membership != nil && authority == *c.membership && (role == "owner" || role == "member")
		}
	} else {
		valid = false
	}
	return a, w, valid, nil
}
func (s *Service) workerTransaction(ctx context.Context, c candidate, fn func(context.Context, transaction.Executor, identity.Auth, workspaces.Workspace, workerState) error) error {
	repo, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	e = repo.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		a, w, valid, e := s.lockDelegation(ctx, tx, c.delegatedActor)
		if e != nil {
			return e
		}
		b, e := lockedBase(ctx, tx, w, c.base)
		if e != nil {
			return e
		}
		var generation string
		if e = tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT id::text FROM knowledge_generations WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND id=$4 FOR UPDATE`, `SELECT id FROM knowledge_generations WHERE tenant_id=? AND workspace_id=? AND base_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, c.base, c.generation}, []any{w.TenantID, w.ID, c.base, c.generation})).Scan(&generation); e != nil {
			return e
		}
		g, e := loadGeneration(ctx, tx, w, c.base, generation)
		if e != nil {
			return e
		}
		d, e := lockedDocument(ctx, tx, w, c.base, c.document, true)
		if e != nil {
			return e
		}
		state := workerState{base: b, document: d, generation: g, valid: valid, failure: "KNOWLEDGE_AUTHORITY_INVALID"}
		if e = tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT state,lease_hash,coalesce(lease_expires_at<=clock_timestamp(),false) FROM knowledge_attempts WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND document_id=$4 AND source_id=$5 AND generation_id=$6 AND id=$7 AND requested_by=$8 AND requested_audience=$9 AND requested_epoch=$10 AND expected_document_revision=$11 AND kind=$12 FOR UPDATE`, `SELECT state,lease_hash,coalesce(lease_expires_at<=CURRENT_TIMESTAMP(6),false) FROM knowledge_attempts WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND source_id=? AND generation_id=? AND id=? AND requested_by=? AND requested_audience=? AND requested_epoch=? AND expected_document_revision=? AND kind=? FOR UPDATE`, []any{w.TenantID, w.ID, c.base, c.document, c.source, c.generation, c.id, c.actor, c.audience, c.epoch, c.revision, c.kind}, []any{w.TenantID, w.ID, c.base, c.document, c.source, c.generation, c.id, c.actor, c.audience, c.epoch, c.revision, c.kind})).Scan(&state.state, &state.lease, &state.expired); e != nil {
			return e
		}
		if valid {
			if !b.Enabled {
				state.valid = false
				state.failure = "KNOWLEDGE_BASE_DISABLED"
			} else if d.DeletedAt != nil {
				state.valid = false
				state.failure = "KNOWLEDGE_CANCELLED"
			} else if g.CatalogSHA256 != CatalogDigest() || g.Dimension != EmbeddingDimension {
				state.valid = false
				state.failure = "KNOWLEDGE_INDEX_INCOMPATIBLE"
			} else if c.kind == "ingest" {
				state.valid = g.State == "active" && b.activeID == c.generation && d.pendingID != nil && *d.pendingID == c.source && d.attemptID != nil && *d.attemptID == c.id && d.Revision == c.revision && (d.State == "pending" || d.State == "running")
				state.failure = "KNOWLEDGE_SOURCE_CHANGED"
			} else {
				state.valid = g.State == "building" && b.buildingID != nil && *b.buildingID == g.ID && d.visibleID != nil && *d.visibleID == c.source
				state.failure = "KNOWLEDGE_SOURCE_CHANGED"
			}
		}
		if e = fn(ctx, tx, a, w, state); e != nil {
			return e
		}
		return nil
	})
	return knowledgeError(e)
}
func (s *Service) nextCandidate(ctx context.Context) (candidate, error) {
	repo, e := s.repository()
	if e != nil {
		return candidate{}, e
	}

	var c candidate
	e = repo.QueryRow(ctx, knowledgeSQL(repo, `SELECT a.id::text,a.tenant_id::text,a.workspace_id::text,a.base_id::text,a.document_id::text,a.source_id::text,a.generation_id::text,a.kind,a.expected_document_revision,a.requested_by::text,a.requested_audience,a.requested_epoch,a.membership_authority::text FROM knowledge_attempts a JOIN installation_state i ON i.tenant_id=a.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND (a.state='pending' OR(a.state='running' AND a.lease_expires_at<=clock_timestamp())) ORDER BY a.created_at,a.id LIMIT 1`, `SELECT a.id,a.tenant_id,a.workspace_id,a.base_id,a.document_id,a.source_id,a.generation_id,a.kind,a.expected_document_revision,a.requested_by,a.requested_audience,a.requested_epoch,a.membership_authority FROM knowledge_attempts a JOIN knowledge_bases b ON b.tenant_id=a.tenant_id AND b.workspace_id=a.workspace_id AND b.id=a.base_id AND b.initialization_state='ready' JOIN installation_state i ON i.tenant_id=a.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND (a.state='pending' OR(a.state='running' AND a.lease_expires_at<=CURRENT_TIMESTAMP(6))) ORDER BY a.created_at,a.id LIMIT 1`, nil, nil)).Scan(&c.id, &c.tenant, &c.workspace, &c.base, &c.document, &c.source, &c.generation, &c.kind, &c.revision, &c.actor, &c.audience, &c.epoch, &c.membership)
	return c, e
}
