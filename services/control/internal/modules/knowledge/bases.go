package knowledge

import (
	"context"
	"errors"
	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

type baseRecord struct {
	Base
	activeID   string
	buildingID *string
}

const baseFields = `id::text,workspace_id::text,name,enabled,revision,active_generation_id::text,building_generation_id::text,created_by::text,created_at,updated_at`

func scanBase(row transaction.Row) (baseRecord, error) {
	var b baseRecord
	e := row.Scan(&b.ID, &b.WorkspaceID, &b.Name, &b.Enabled, &b.Revision, &b.activeID, &b.buildingID, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return b, failure("KNOWLEDGE_BASE_NOT_FOUND")
	}
	if e != nil {
		return baseRecord{}, e
	}
	b.CreatedAt = b.CreatedAt.UTC()
	b.UpdatedAt = b.UpdatedAt.UTC()
	return b, nil
}
func loadGeneration(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, base, id string) (Generation, error) {
	var g Generation
	e := tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT g.id::text,g.catalog_sha256,g.dimension,g.chunk_tokens,g.overlap_tokens,g.state,g.expected_documents,(SELECT count(DISTINCT a.document_id) FROM knowledge_attempts a JOIN knowledge_documents d ON d.id=a.document_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id WHERE a.generation_id=g.id AND a.state='ready' AND d.deleted_at IS NULL AND a.source_id=d.visible_source_id),(SELECT count(DISTINCT a.document_id) FROM knowledge_attempts a JOIN knowledge_documents d ON d.id=a.document_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id WHERE a.generation_id=g.id AND a.state IN ('failed','cancelled') AND d.deleted_at IS NULL AND (g.state<>'active' OR a.id=d.current_attempt_id)),g.error_code,g.created_at,g.activated_at,g.finished_at FROM knowledge_generations g WHERE g.tenant_id=$1 AND g.workspace_id=$2 AND g.base_id=$3 AND g.id=$4`, `SELECT g.id,g.catalog_sha256,g.dimension,g.chunk_tokens,g.overlap_tokens,g.state,g.expected_documents,(SELECT count(DISTINCT a.document_id) FROM knowledge_attempts a JOIN knowledge_documents d ON d.id=a.document_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id WHERE a.generation_id=g.id AND a.state='ready' AND d.deleted_at IS NULL AND a.source_id=d.visible_source_id),(SELECT count(DISTINCT a.document_id) FROM knowledge_attempts a JOIN knowledge_documents d ON d.id=a.document_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id WHERE a.generation_id=g.id AND a.state IN ('failed','cancelled') AND d.deleted_at IS NULL AND (g.state<>'active' OR a.id=d.current_attempt_id)),g.error_code,g.created_at,g.activated_at,g.finished_at FROM knowledge_generations g WHERE g.tenant_id=? AND g.workspace_id=? AND g.base_id=? AND g.id=?`, []any{w.TenantID, w.ID, base, id}, []any{w.TenantID, w.ID, base, id})).Scan(&g.ID, &g.CatalogSHA256, &g.Dimension, &g.ChunkTokens, &g.OverlapTokens, &g.State, &g.ExpectedDocuments, &g.CompletedDocuments, &g.FailedDocuments, &g.ErrorCode, &g.CreatedAt, &g.ActivatedAt, &g.FinishedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return g, failure("KNOWLEDGE_GENERATION_NOT_FOUND")
	}
	if e != nil {
		return Generation{}, e
	}
	g.CreatedAt = g.CreatedAt.UTC()
	g.ActivatedAt = utcKnowledge(g.ActivatedAt)
	g.FinishedAt = utcKnowledge(g.FinishedAt)
	return g, nil
}
func hydrateBase(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, b baseRecord) (Base, error) {
	var e error
	b.ActiveGeneration, e = loadGeneration(ctx, tx, w, b.ID, b.activeID)
	if e != nil {
		return knowledgeCommitted(b.Base, e)
	}
	if b.buildingID != nil {
		value, e := loadGeneration(ctx, tx, w, b.ID, *b.buildingID)
		if e != nil {
			return knowledgeCommitted(b.Base, e)
		}
		b.BuildingGeneration = &value
	}
	e = tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT count(*),count(*) FILTER(WHERE visible_source_id IS NOT NULL) FROM knowledge_documents WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND deleted_at IS NULL`, `SELECT count(*),COALESCE(SUM(visible_source_id IS NOT NULL),0) FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND deleted_at IS NULL`, []any{w.TenantID, w.ID, b.ID}, []any{w.TenantID, w.ID, b.ID})).Scan(&b.DocumentCount, &b.ReadyDocuments)
	return knowledgeCommitted(b.Base, e)
}
func lockedBase(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, id string) (baseRecord, error) {
	if !validID(id) {
		return baseRecord{}, failure("KNOWLEDGE_BASE_NOT_FOUND")
	}
	return scanBase(tx.QueryRow(ctx, knowledgeSQL(tx, "SELECT "+baseFields+" FROM knowledge_bases WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", `SELECT id,workspace_id,name,enabled,revision,active_generation_id,building_generation_id,created_by,created_at,updated_at FROM knowledge_bases WHERE initialization_state='ready' AND tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})))
}
func (s *Service) GetBase(ctx context.Context, a identity.Auth, workspace, id string) (Base, error) {
	var out Base
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		b, e := lockedBase(ctx, tx, w, id)
		if e != nil {
			return e
		}
		out, e = hydrateBase(ctx, tx, w, b)
		return e
	})
	return knowledgeCommitted(out, e)
}
func (s *Service) ResolveBaseWorkspace(ctx context.Context, a identity.Auth, id string) (string, error) {
	if !admin(a) || !validID(id) {
		return "", failure("KNOWLEDGE_BASE_NOT_FOUND")
	}
	repo, e := s.repository()
	if e != nil {
		return "", e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	var workspace string
	e = repo.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, tx, a); e != nil {
			return e
		}
		e = tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT b.workspace_id::text FROM knowledge_bases b JOIN installation_state i ON i.tenant_id=b.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND b.id=$1`, `SELECT b.workspace_id FROM knowledge_bases b JOIN installation_state i ON i.tenant_id=b.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND b.initialization_state='ready' AND b.id=?`, []any{id}, []any{id})).Scan(&workspace)
		if errors.Is(e, pgx.ErrNoRows) {
			return failure("KNOWLEDGE_BASE_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		w, e := s.Workspaces.RecheckScopeWithin(ctx, tx, a, workspace)
		if e != nil {
			return e
		}
		if w.Status != "active" {
			return failure("ACCESS_DENIED")
		}
		return nil
	})
	return knowledgeCommitted(workspace, e)
}
func (s *Service) BasePage(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[Base], error) {
	out := Page[Base]{Items: []Base{}}
	limit, cursor, scope, e := pagination.Parse(s.pageValues(values), "knowledge:bases:"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace)
	if e != nil {
		return knowledgeCommitted(out, e)
	}
	e = s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		query := "SELECT " + baseFields + " FROM knowledge_bases WHERE tenant_id=$1 AND workspace_id=$2"
		myQuery := "SELECT " + mysqlBaseFields + " FROM knowledge_bases WHERE initialization_state='ready' AND tenant_id=? AND workspace_id=?"
		args := []any{w.TenantID, w.ID}
		if cursor.ID != "" {
			args = append(args, cursor.Time, cursor.ID)
			query += " AND (created_at,id)<($3,$4::uuid)"
			myQuery += " AND (created_at,id)<(?,?)"
		}
		args = append(args, limit+1)
		query += " ORDER BY created_at DESC,id DESC LIMIT $" + strconv.Itoa(len(args))
		myQuery += " ORDER BY created_at DESC,id DESC LIMIT ?"
		rows, e := tx.Query(ctx, knowledgeSQL(tx, query, myQuery, args, args))
		if e != nil {
			return e
		}
		var records []baseRecord
		for rows.Next() {
			b, e := scanBase(rows)
			if e != nil {
				rows.Close()
				return e
			}
			records = append(records, b)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		if len(records) > limit {
			records = records[:limit]
			last := records[limit-1]
			out.NextCursor = pagination.Encode(last.CreatedAt, last.ID, scope)
		}
		for _, record := range records {
			b, e := hydrateBase(ctx, tx, w, record)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, b)
		}
		return nil
	})
	return knowledgeCommitted(out, e)
}
func (s *Service) CreateBase(ctx context.Context, a identity.Auth, input CreateBase, key, request string) (Base, error) {
	var out Base
	if e := s.enabled(); e != nil {
		return knowledgeCommitted(out, e)
	}
	if !validReason(input.Reason) {
		return out, failure("VALIDATION_FAILED")
	}
	name, nameKey, e := normalizedName(input.Name, 100)
	if e != nil {
		return knowledgeCommitted(out, e)
	}
	input.Name = name
	e = s.transaction(ctx, a, input.WorkspaceID, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		previous, digest, e := s.intent(ctx, tx, a, w, "create_base", key, input)
		if e != nil {
			return e
		}
		if previous != "" {
			b, e := lockedBase(ctx, tx, w, previous)
			if e != nil {
				return e
			}
			out, e = hydrateBase(ctx, tx, w, b)
			return e
		}
		var count int
		if e = tx.QueryRow(ctx, knowledgeSQL(tx, "SELECT count(*) FROM knowledge_bases WHERE tenant_id=$1 AND workspace_id=$2", `SELECT count(*) FROM knowledge_bases WHERE initialization_state='ready' AND tenant_id=? AND workspace_id=?`, []any{w.TenantID, w.ID}, []any{w.TenantID, w.ID})).Scan(&count); e != nil {
			return e
		}
		if count >= s.policy().MaximumBases {
			return failure("KNOWLEDGE_LIMIT_REACHED")
		}
		id, generation := uuid.NewString(), uuid.NewString()
		member, e := delegation(ctx, tx, a, w)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, `INSERT INTO knowledge_bases(id,tenant_id,workspace_id,name,name_key,active_generation_id,created_by,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, `INSERT INTO knowledge_bases(id,tenant_id,workspace_id,name,name_key,active_generation_id,created_by,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,NULL,?,?,?)`, []any{id, w.TenantID, w.ID, name, nameKey, generation, a.Me.User.ID, key, digest}, []any{id, w.TenantID, w.ID, name, nameKey, a.Me.User.ID, key, digest})); e != nil {
			return knowledgeUnique(e, "KNOWLEDGE_BASE_EXISTS")
		}
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, `INSERT INTO knowledge_generations(id,tenant_id,workspace_id,base_id,catalog_sha256,dimension,chunk_tokens,overlap_tokens,state,requested_by,requested_audience,requested_epoch,membership_authority,expected_documents,activated_at) VALUES($1,$2,$3,$4,$5,384,$6,$7,'active',$8,$9,$10,$11,0,clock_timestamp())`, `INSERT INTO knowledge_generations(id,tenant_id,workspace_id,base_id,catalog_sha256,dimension,chunk_tokens,overlap_tokens,state,requested_by,requested_audience,requested_epoch,membership_authority,expected_documents,activated_at) VALUES(?,?,?,?,?,384,?,?,'active',?,?,?,?,0,CURRENT_TIMESTAMP(6))`, []any{generation, w.TenantID, w.ID, id, CatalogDigest(), s.policy().ChunkTokens, s.policy().OverlapTokens, a.Me.User.ID, a.Me.Audience, a.Epoch, member}, []any{generation, w.TenantID, w.ID, id, CatalogDigest(), s.policy().ChunkTokens, s.policy().OverlapTokens, a.Me.User.ID, a.Me.Audience, a.Epoch, member})); e != nil {
			return e
		}
		if tx.Dialect() == transaction.MySQL {
			if _, e = tx.Exec(ctx, transaction.SQL(transaction.MySQL, "UPDATE knowledge_bases SET active_generation_id=?,initialization_state='ready' WHERE tenant_id=? AND workspace_id=? AND id=? AND initialization_state='initializing'", generation, w.TenantID, w.ID, id)); e != nil {
				return e
			}
		}
		if e = saveIntent(ctx, tx, a, w, "create_base", key, id, digest); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "knowledge.base_created", "knowledge_base", id, input.Reason, request); e != nil {
			return e
		}
		b, e := lockedBase(ctx, tx, w, id)
		if e != nil {
			return e
		}
		out, e = hydrateBase(ctx, tx, w, b)
		return e
	})
	return knowledgeCommitted(out, e)
}
func (s *Service) Configuration(ctx context.Context, a identity.Auth, workspace string) (Configuration, error) {
	p := s.policy()
	out := Configuration{ChunkBytes: p.ChunkBytes, Enabled: s.Identity.Config.Knowledge.Enabled, EncoderAvailable: s.Processor.Available(), EncoderCatalogSHA256: CatalogDigest(), Dimension: EmbeddingDimension, ChunkTokens: p.ChunkTokens, OverlapTokens: p.OverlapTokens, SourceBytes: p.SourceBytes, QueryBytes: p.QueryBytes, QueryTokens: p.QueryTokens, TopK: p.TopK, MaximumTopK: p.MaximumTopK, MinimumScore: p.MinimumScore, MaximumDocuments: p.MaximumDocuments}
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		out.CanWrite = out.Enabled && out.EncoderAvailable && canWrite(a, w)
		out.CanAsk = out.Enabled && out.EncoderAvailable && w.Role != nil && len(s.Identity.Config.Knowledge.Gateways) > 0
		return nil
	})
	return knowledgeCommitted(out, e)
}
