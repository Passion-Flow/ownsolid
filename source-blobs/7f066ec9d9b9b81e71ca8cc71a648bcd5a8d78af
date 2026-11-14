package knowledge

import (
	"context"
	"crypto/subtle"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

type claimed struct {
	candidate
	token, text, sha256 string
	generationInfo      Generation
}

var safeError = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,79}$`)

func (s *Service) leaseDigest(id, token string) []byte {
	return security.MAC(s.Identity.HMACKey, "knowledge-lease", "v1", id, token)
}
func errorCode(e error) string {
	if e != nil && safeError.MatchString(e.Error()) {
		return e.Error()
	}
	return "KNOWLEDGE_WORKER_FAILED"
}
func (s *Service) failAttempt(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, c candidate, state workerState, code string) error {
	if state.state != "pending" && state.state != "running" {
		return nil
	}
	if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_attempts SET state='failed',lease_hash=NULL,lease_expires_at=NULL,error_code=$2,finished_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_attempts SET state='failed',lease_hash=NULL,lease_expires_at=NULL,error_code=?,finished_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{c.id, code}, []any{code, c.id})); e != nil {
		return e
	}
	if c.kind == "ingest" && state.document.DeletedAt == nil && state.document.attemptID != nil && *state.document.attemptID == c.id {
		if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_documents SET state='failed',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_documents SET state='failed',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{c.document}, []any{c.document})); e != nil {
			return e
		}
	}
	return s.audit(ctx, tx, a, w, "knowledge.processing_failed", "knowledge_document", c.document, code, uuid.NewString())
}
func (s *Service) claim(ctx context.Context, c candidate) (*claimed, error) {
	var out *claimed
	e := s.workerTransaction(ctx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, state workerState) error {
		if state.state == "running" {
			if state.expired {
				return s.failAttempt(ctx, tx, a, w, c, state, "KNOWLEDGE_WORKER_INTERRUPTED")
			}
			return nil
		}
		if state.state != "pending" {
			return nil
		}
		if !state.valid {
			return s.failAttempt(ctx, tx, a, w, c, state, state.failure)
		}
		var content []byte
		var digest string
		if e := tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT content,sha256 FROM knowledge_source_versions WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND document_id=$4 AND id=$5`, `SELECT content,sha256 FROM knowledge_source_versions WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND id=?`, []any{w.TenantID, w.ID, c.base, c.document, c.source}, []any{w.TenantID, w.ID, c.base, c.document, c.source})).Scan(&content, &digest); e != nil {
			return e
		}
		if e := s.validSource(string(content), digest); e != nil {
			return s.failAttempt(ctx, tx, a, w, c, state, "KNOWLEDGE_INVALID_SOURCE")
		}
		token := security.Token()
		if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_attempts SET state='running',lease_hash=$2,lease_expires_at=clock_timestamp()+make_interval(secs=>$3),started_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_attempts SET state='running',lease_hash=?,lease_expires_at=TIMESTAMPADD(SECOND,?,CURRENT_TIMESTAMP(6)),started_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{c.id, s.leaseDigest(c.id, token), s.policy().LeaseSeconds}, []any{s.leaseDigest(c.id, token), s.policy().LeaseSeconds, c.id})); e != nil {
			return e
		}
		if c.kind == "ingest" {
			if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_documents SET state='running',updated_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_documents SET state='running',updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{c.document}, []any{c.document})); e != nil {
				return e
			}
		}
		if e := s.audit(ctx, tx, a, w, "knowledge.processing_started", "knowledge_document", c.document, "Bounded offline encoder admitted", uuid.NewString()); e != nil {
			return e
		}
		out = &claimed{candidate: c, token: token, text: string(content), sha256: digest, generationInfo: state.generation}
		return nil
	})
	return knowledgeCommitted(out, e)
}

// liveClaim is read-only and cancels local CPU work if its durable authority disappears.
// The final transaction repeats the checks with ordered locks before any vectors activate.
func (s *Service) liveClaim(ctx context.Context, c *claimed) (bool, error) {
	repo, e := s.repository()
	if e != nil {
		return false, e
	}

	var valid bool
	e = repo.QueryRow(ctx, knowledgeSQL(repo, `SELECT EXISTS(SELECT 1 FROM knowledge_attempts a JOIN knowledge_documents d ON d.id=a.document_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id JOIN knowledge_bases b ON b.id=a.base_id AND b.tenant_id=a.tenant_id AND b.workspace_id=a.workspace_id JOIN knowledge_generations g ON g.id=a.generation_id AND g.base_id=a.base_id AND g.tenant_id=a.tenant_id AND g.workspace_id=a.workspace_id JOIN users u ON u.id=a.requested_by JOIN workspaces w ON w.id=a.workspace_id AND w.tenant_id=a.tenant_id JOIN tenants t ON t.id=a.tenant_id JOIN installation_state i ON i.tenant_id=a.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND a.id=$1 AND a.lease_hash=$2 AND a.state='running' AND a.lease_expires_at>clock_timestamp() AND u.status='active' AND u.auth_epoch=a.requested_epoch AND w.status='active' AND t.status='active' AND b.enabled AND d.deleted_at IS NULL AND g.catalog_sha256=$3 AND ((a.requested_audience='admin' AND u.is_platform_admin) OR(a.requested_audience='portal' AND EXISTS(SELECT 1 FROM workspace_memberships m WHERE m.tenant_id=a.tenant_id AND m.workspace_id=a.workspace_id AND m.user_id=a.requested_by AND m.authority_id=a.membership_authority AND m.role IN ('owner','member')))) AND ((a.kind='ingest' AND b.active_generation_id=g.id AND g.state='active' AND d.pending_source_id=a.source_id AND d.current_attempt_id=a.id AND d.revision=a.expected_document_revision AND d.state='running') OR(a.kind='rebuild' AND b.building_generation_id=g.id AND g.state='building' AND d.visible_source_id=a.source_id)))`, `SELECT EXISTS(SELECT 1 FROM knowledge_attempts a JOIN knowledge_documents d ON d.id=a.document_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id JOIN knowledge_bases b ON b.id=a.base_id AND b.tenant_id=a.tenant_id AND b.workspace_id=a.workspace_id JOIN knowledge_generations g ON g.id=a.generation_id AND g.base_id=a.base_id AND g.tenant_id=a.tenant_id AND g.workspace_id=a.workspace_id JOIN users u ON u.id=a.requested_by JOIN workspaces w ON w.id=a.workspace_id AND w.tenant_id=a.tenant_id JOIN tenants t ON t.id=a.tenant_id JOIN installation_state i ON i.tenant_id=a.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND a.id=? AND a.lease_hash=? AND a.state='running' AND a.lease_expires_at>CURRENT_TIMESTAMP(6) AND u.status='active' AND u.auth_epoch=a.requested_epoch AND w.status='active' AND t.status='active' AND b.initialization_state='ready' AND b.enabled AND d.deleted_at IS NULL AND g.catalog_sha256=? AND ((a.requested_audience='admin' AND u.is_platform_admin) OR(a.requested_audience='portal' AND EXISTS(SELECT 1 FROM workspace_memberships m WHERE m.tenant_id=a.tenant_id AND m.workspace_id=a.workspace_id AND m.user_id=a.requested_by AND m.authority_id=a.membership_authority AND m.role IN ('owner','member')))) AND ((a.kind='ingest' AND b.active_generation_id=g.id AND g.state='active' AND d.pending_source_id=a.source_id AND d.current_attempt_id=a.id AND d.revision=a.expected_document_revision AND d.state='running') OR(a.kind='rebuild' AND b.building_generation_id=g.id AND g.state='building' AND d.visible_source_id=a.source_id)))`, []any{c.id, s.leaseDigest(c.id, c.token), CatalogDigest()}, []any{c.id, s.leaseDigest(c.id, c.token), CatalogDigest()})).Scan(&valid)
	return valid, e
}
func (s *Service) finish(ctx context.Context, c *claimed, result WorkerResult, executionError error) error {
	return s.workerTransaction(ctx, c.candidate, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, state workerState) error {
		if state.state != "running" || subtle.ConstantTimeCompare(state.lease, s.leaseDigest(c.id, c.token)) != 1 {
			return nil
		}
		if state.expired {
			return s.failAttempt(ctx, tx, a, w, c.candidate, state, "KNOWLEDGE_WORKER_INTERRUPTED")
		}
		if !state.valid {
			return s.failAttempt(ctx, tx, a, w, c.candidate, state, state.failure)
		}
		if executionError != nil {
			return s.failAttempt(ctx, tx, a, w, c.candidate, state, errorCode(executionError))
		}
		if result.SourceSHA256 != c.sha256 || s.Processor.validateResult(result, c.text, true, c.generationInfo.ChunkTokens) != nil {
			return s.failAttempt(ctx, tx, a, w, c.candidate, state, "KNOWLEDGE_WORKER_RESULT_INVALID")
		}
		if e := insertKnowledgeChunks(ctx, tx, w, c, result.Chunks); e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_attempts SET state='ready',lease_hash=NULL,lease_expires_at=NULL,chunk_count=$2,finished_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_attempts SET state='ready',lease_hash=NULL,lease_expires_at=NULL,chunk_count=?,finished_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{c.id, len(result.Chunks)}, []any{len(result.Chunks), c.id})); e != nil {
			return e
		}
		if c.kind == "ingest" {
			if state.document.visibleID != nil && *state.document.visibleID != c.source {
				if _, e := tx.Exec(ctx, knowledgeSQL(tx, "UPDATE knowledge_source_versions SET superseded_at=clock_timestamp() WHERE id=$1 AND superseded_at IS NULL", `UPDATE knowledge_source_versions SET superseded_at=CURRENT_TIMESTAMP(6) WHERE id=? AND superseded_at IS NULL`, []any{*state.document.visibleID}, []any{*state.document.visibleID})); e != nil {
					return e
				}
			}
			if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_documents SET visible_source_id=$2,pending_source_id=NULL,state='ready',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_documents SET visible_source_id=?,pending_source_id=NULL,state='ready',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{c.document, c.source}, []any{c.source, c.document})); e != nil {
				return e
			}
		}
		return s.audit(ctx, tx, a, w, "knowledge.processing_completed", "knowledge_document", c.document, "Validated complete offline encoder output", uuid.NewString())
	})
}
func (s *Service) ProcessNext(ctx context.Context) (bool, error) {
	if e := s.enabled(); e != nil {
		return false, e
	}
	if !s.Processor.reserve() {
		return false, nil
	}
	defer s.Processor.release()
	c, e := s.nextCandidate(ctx)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	claim, e := s.claim(ctx, c)
	if e != nil || claim == nil {
		return true, e
	}
	work, cancel := context.WithCancel(ctx)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		timer := time.NewTicker(time.Duration(s.policy().PollMilliseconds) * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-timer.C:
				probe, stop := context.WithTimeout(work, time.Duration(s.policy().CommitSeconds)*time.Second)
				valid, e := s.liveClaim(probe, claim)
				stop()
				if e != nil || !valid {
					cancel()
					return
				}
			}
		}
	}()
	result, executionError := s.Processor.execute(work, claim.text, true, claim.generationInfo.ChunkTokens, claim.generationInfo.OverlapTokens, true)
	cancel()
	<-monitorDone
	if ctx.Err() != nil {
		executionError = failure("KNOWLEDGE_WORKER_INTERRUPTED")
	}
	finish, stop := context.WithTimeout(context.Background(), time.Duration(s.policy().CommitSeconds)*time.Second)
	defer stop()
	if e := s.finish(finish, claim, result, executionError); e != nil {
		return true, e
	}
	return true, s.ReconcileGeneration(finish)
}
