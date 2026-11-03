package knowledge

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

// PreparedAnswer is a Go-only capability. Its private source/question/authority
// fields cannot be reconstructed from HTTP JSON or persisted workflow input.
type PreparedAnswer struct {
	service                                                         *Service
	actor, audience, session, workspace, base, membership, question string
	tokenFingerprint                                                [32]byte
	alias                                                           distribution.Alias
	destination                                                     config.KnowledgeGateway
	retrieved                                                       SearchResult
	consumed                                                        atomic.Bool
}
type AnswerSourceEvidence struct {
	CitationID    string `json:"citation_id"`
	DocumentID    string `json:"document_id"`
	SourceID      string `json:"source_id"`
	SourceVersion int64  `json:"source_version"`
	SourceSHA256  string `json:"source_sha256"`
	StartByte     int    `json:"start_byte"`
	EndByte       int    `json:"end_byte"`
	ChunkOrdinal  int    `json:"chunk_ordinal"`
}
type StoredAnswerEvidence struct {
	BaseID               string                 `json:"base_id"`
	GenerationID         string                 `json:"generation_id"`
	EncoderCatalogSHA256 string                 `json:"encoder_catalog_sha256"`
	Sources              []AnswerSourceEvidence `json:"sources"`
}
type CompletedAnswer struct {
	prepared *PreparedAnswer
	value    Answer
}

func (p *PreparedAnswer) bound(s *Service, a identity.Auth) bool {
	if p == nil || p.service != s || p.actor != a.Me.User.ID || p.audience != a.Me.Audience || p.session != a.SessionID {
		return false
	}
	digest := sha256.Sum256(a.TokenHash)
	return subtle.ConstantTimeCompare(digest[:], p.tokenFingerprint[:]) == 1
}
func (p *PreparedAnswer) Evidence() StoredAnswerEvidence {
	out := StoredAnswerEvidence{BaseID: p.base, GenerationID: p.retrieved.GenerationID, EncoderCatalogSHA256: p.retrieved.EncoderCatalogSHA256, Sources: []AnswerSourceEvidence{}}
	for _, m := range p.retrieved.Matches {
		out.Sources = append(out.Sources, AnswerSourceEvidence{CitationID: m.CitationID, DocumentID: m.DocumentID, SourceID: m.SourceID, SourceVersion: m.SourceVersion, SourceSHA256: m.SourceSHA256, StartByte: m.StartByte, EndByte: m.EndByte, ChunkOrdinal: m.ChunkOrdinal})
	}
	return out
}
func (c *CompletedAnswer) Answer() Answer {
	out := c.value
	out.Sources = append([]Match(nil), out.Sources...)
	return out
}
func (c *CompletedAnswer) Evidence() StoredAnswerEvidence { return c.prepared.Evidence() }
func (c *CompletedAnswer) Alias() distribution.Alias      { return c.prepared.alias }

func (s *Service) PrepareAnswer(ctx context.Context, a identity.Auth, workspace, base string, input AnswerRequest, request string) (*PreparedAnswer, error) {
	var zero *PreparedAnswer
	if e := s.enabled(); e != nil {
		return zero, e
	}
	if s.Distribution == nil || !validID(input.AliasID) {
		return zero, failure("KNOWLEDGE_GATEWAY_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().QuerySeconds))
	defer cancel()
	var membership string
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if w.Role == nil {
			return failure("ACCESS_DENIED")
		}
		var e error
		membership, e = s.Workspaces.MembershipAuthorityWithin(ctx, tx, w.TenantID, w.ID, a.Me.User.ID)
		return e
	})
	if e != nil {
		return zero, e
	}
	alias, e := s.Distribution.Alias(ctx, a, workspace, input.AliasID)
	if e != nil {
		return zero, e
	}
	if !alias.Available {
		return zero, failure("MODEL_ALIAS_UNAVAILABLE")
	}
	var destination *config.KnowledgeGateway
	for _, g := range s.Identity.Config.Knowledge.Gateways {
		if g.Origin == alias.ClientOrigin {
			copy := g
			destination = &copy
			break
		}
	}
	if destination == nil {
		return zero, failure("KNOWLEDGE_GATEWAY_UNAVAILABLE")
	}
	retrieved, e := s.Search(ctx, a, workspace, base, SearchRequest{Query: input.Question, TopK: input.TopK, MinimumScore: input.MinimumScore}, request)
	if e != nil {
		return zero, e
	}
	retrieved.Matches = boundedContext(retrieved.Matches, s.policy().ContextBytes)
	if len(retrieved.Matches) == 0 {
		return zero, failure("KNOWLEDGE_NO_CONTEXT")
	}

	return &PreparedAnswer{service: s, actor: a.Me.User.ID, audience: a.Me.Audience, session: a.SessionID, tokenFingerprint: sha256.Sum256(a.TokenHash), workspace: workspace, base: base, membership: membership, question: input.Question, alias: alias, destination: *destination, retrieved: retrieved}, nil
}
func (s *Service) CompletePreparedAnswer(ctx context.Context, a identity.Auth, p *PreparedAnswer, instructions, request string) (*CompletedAnswer, error) {
	if !p.bound(s, a) || !p.consumed.CompareAndSwap(false, true) {
		return nil, failure("KNOWLEDGE_AUTHORITY_INVALID")
	}
	if !utf8.ValidString(instructions) || len(instructions) > s.Identity.Config.Policy().Workflows.InstructionsBytes || strings.ContainsFunc(instructions, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		return nil, failure("VALIDATION_FAILED")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().QuerySeconds))
	defer cancel()
	if e := s.transaction(ctx, a, p.workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		return s.recheckPrepared(ctx, tx, a, p)
	}); e != nil {
		return nil, e
	}
	var completion chatResponse
	e := s.Distribution.WithTransientKey(ctx, a, distribution.KeyRequest{WorkspaceID: p.workspace, AliasID: p.alias.ID, Label: "Knowledge answer", TTLSeconds: s.policy().KeyTTLSeconds, Reason: "Scoped knowledge answer"}, config.Seconds(s.policy().KeyCleanupSeconds), request, func(key string) error {
		var e error
		completion, e = s.completionWithInstructions(ctx, p.destination, p.alias.Name, key, p.question, p.retrieved.Matches, instructions)
		return e
	})
	if e != nil {
		if e.Error() == "TRANSIENT_KEY_CLEANUP_FAILED" {
			return nil, failure("KNOWLEDGE_ANSWER_KEY_CLEANUP_FAILED")
		}
		return nil, e
	}
	answer := completion.Choices[0].Message.Content
	sources, e := citedSources(answer, p.retrieved.Matches)
	if e != nil {
		return nil, e
	}
	e = s.transaction(ctx, a, p.workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if e := s.recheckPrepared(ctx, tx, a, p); e != nil {
			return e
		}
		return s.audit(ctx, tx, a, w, "knowledge.answered", "knowledge_base", p.base, "Current-source cited model answer", request)
	})
	if e != nil {
		return nil, e
	}
	return &CompletedAnswer{prepared: p, value: Answer{BaseID: p.base, GenerationID: p.retrieved.GenerationID, AliasID: p.alias.ID, Answer: answer, Sources: sources, Usage: completion.Usage}}, nil
}

// RecheckCompletedAnswerInTransaction protects a later durable workflow commit
// with the same private current-source/model capability after model IO ends.
type CompletionBinding struct{ WorkspaceID, BaseID, AliasID, Question string }

func (s *Service) RecheckCompletedAnswerInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, expected CompletionBinding, completed *CompletedAnswer) error {
	return s.RecheckCompletedAnswerWithin(ctx, transaction.PostgreSQLTransaction(tx), a, expected, completed)
}

func (s *Service) RecheckCompletedAnswerWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, expected CompletionBinding, completed *CompletedAnswer) error {
	if e := s.borrowed(ctx, tx); e != nil {
		return e
	}

	if completed == nil || !completed.prepared.bound(s, a) {
		return failure("KNOWLEDGE_AUTHORITY_INVALID")
	}
	p := completed.prepared
	if p.workspace != expected.WorkspaceID || p.base != expected.BaseID || p.alias.ID != expected.AliasID || p.question != expected.Question {
		return failure("KNOWLEDGE_AUTHORITY_INVALID")
	}
	return s.recheckPrepared(ctx, tx, a, completed.prepared)
}
func (s *Service) recheckPrepared(ctx context.Context, tx transaction.Executor, a identity.Auth, p *PreparedAnswer) error {
	if !p.bound(s, a) {
		return failure("KNOWLEDGE_AUTHORITY_INVALID")
	}
	if _, e := tx.Exec(ctx, knowledgeSQL(tx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", `SELECT id FROM users WHERE id=? FOR UPDATE`, []any{a.Me.User.ID}, []any{a.Me.User.ID})); e != nil {
		return e
	}
	if e := s.Identity.RecheckOrderedWithin(ctx, tx, a); e != nil {
		return e
	}
	w, e := s.Workspaces.RecheckScopeWithin(ctx, tx, a, p.workspace)
	if e != nil {
		return e
	}
	if w.Status != "active" || w.Role == nil {
		return failure("ACCESS_DENIED")
	}
	authority, e := s.Workspaces.MembershipAuthorityWithin(ctx, tx, w.TenantID, w.ID, a.Me.User.ID)
	if e != nil {
		return e
	}
	if authority != p.membership {
		return failure("KNOWLEDGE_AUTHORITY_INVALID")
	}
	if e = s.Distribution.RecheckAnswerAliasWithin(ctx, tx, a, w.ID, p.alias); e != nil {
		return e
	}
	return s.checkSourceEvidence(ctx, tx, w, p.base, p.retrieved)
}
func (s *Service) checkSourceEvidence(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, base string, retrieved SearchResult) error {
	b, e := lockedBase(ctx, tx, w, base)
	if e != nil {
		return e
	}
	if !b.Enabled {
		return failure("KNOWLEDGE_BASE_DISABLED")
	}
	if b.activeID != retrieved.GenerationID {
		return failure("KNOWLEDGE_ANSWER_SOURCE_CHANGED")
	}
	for _, m := range retrieved.Matches {
		var valid bool
		e = tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT EXISTS(SELECT 1 FROM knowledge_documents d JOIN knowledge_source_versions v ON v.id=d.visible_source_id AND v.document_id=d.id AND v.tenant_id=d.tenant_id AND v.workspace_id=d.workspace_id AND v.base_id=d.base_id JOIN knowledge_chunks c ON c.tenant_id=v.tenant_id AND c.workspace_id=v.workspace_id AND c.base_id=v.base_id AND c.document_id=v.document_id AND c.source_id=v.id JOIN knowledge_attempts a ON a.id=c.attempt_id AND a.tenant_id=c.tenant_id AND a.workspace_id=c.workspace_id AND a.base_id=c.base_id AND a.generation_id=c.generation_id AND a.document_id=c.document_id AND a.source_id=c.source_id WHERE d.tenant_id=$1 AND d.workspace_id=$2 AND d.base_id=$3 AND d.id=$4 AND d.deleted_at IS NULL AND v.id=$5 AND v.sha256=$6 AND v.source_version=$7 AND c.generation_id=$8 AND c.ordinal=$9 AND c.start_byte=$10 AND c.end_byte=$11 AND a.state='ready')`, `SELECT EXISTS(SELECT 1 FROM knowledge_documents d JOIN knowledge_source_versions v ON v.id=d.visible_source_id AND v.document_id=d.id AND v.tenant_id=d.tenant_id AND v.workspace_id=d.workspace_id AND v.base_id=d.base_id JOIN knowledge_chunks c ON c.tenant_id=v.tenant_id AND c.workspace_id=v.workspace_id AND c.base_id=v.base_id AND c.document_id=v.document_id AND c.source_id=v.id JOIN knowledge_attempts a ON a.id=c.attempt_id AND a.tenant_id=c.tenant_id AND a.workspace_id=c.workspace_id AND a.base_id=c.base_id AND a.generation_id=c.generation_id AND a.document_id=c.document_id AND a.source_id=c.source_id WHERE d.tenant_id=? AND d.workspace_id=? AND d.base_id=? AND d.id=? AND d.deleted_at IS NULL AND v.id=? AND v.sha256=? AND v.source_version=? AND c.generation_id=? AND c.ordinal=? AND c.start_byte=? AND c.end_byte=? AND a.state='ready')`, []any{w.TenantID, w.ID, base, m.DocumentID, m.SourceID, m.SourceSHA256, m.SourceVersion, retrieved.GenerationID, m.ChunkOrdinal, m.StartByte, m.EndByte}, []any{w.TenantID, w.ID, base, m.DocumentID, m.SourceID, m.SourceSHA256, m.SourceVersion, retrieved.GenerationID, m.ChunkOrdinal, m.StartByte, m.EndByte})).Scan(&valid)
		if e != nil {
			return e
		}
		if !valid {
			return failure("KNOWLEDGE_ANSWER_SOURCE_CHANGED")
		}
	}

	return nil
}

// RecheckStoredAnswerEvidenceInTransaction is for caller-owned persisted run
// history, never an HTTP-supplied retrieval result. It checks every influencing
// source and current ordinary model authority before any stored body is shown.
func (s *Service) RecheckStoredAnswerEvidenceInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, workspace string, evidence StoredAnswerEvidence, alias distribution.Alias) error {
	return s.RecheckStoredAnswerEvidenceWithin(ctx, transaction.PostgreSQLTransaction(tx), a, workspace, evidence, alias)
}

func (s *Service) RecheckStoredAnswerEvidenceWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, workspace string, evidence StoredAnswerEvidence, alias distribution.Alias) error {
	if e := s.borrowed(ctx, tx); e != nil {
		return e
	}

	catalog, e := hex.DecodeString(evidence.EncoderCatalogSHA256)
	if !validID(workspace) || !validID(evidence.BaseID) || !validID(evidence.GenerationID) || e != nil || len(catalog) != sha256.Size || strings.ToLower(evidence.EncoderCatalogSHA256) != evidence.EncoderCatalogSHA256 || len(evidence.Sources) < 1 || len(evidence.Sources) > config.MaximumKnowledgeTopK {
		return failure("KNOWLEDGE_INVALID_SOURCE")
	}
	if _, e = tx.Exec(ctx, knowledgeSQL(tx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", `SELECT id FROM users WHERE id=? FOR UPDATE`, []any{a.Me.User.ID}, []any{a.Me.User.ID})); e != nil {
		return e
	}
	if e = s.Identity.RecheckOrderedWithin(ctx, tx, a); e != nil {
		return e
	}
	w, e := s.Workspaces.RecheckScopeWithin(ctx, tx, a, workspace)
	if e != nil {
		return e
	}
	if w.Status != "active" || w.Role == nil {
		return failure("ACCESS_DENIED")
	}
	if e = s.Distribution.CheckHistoricalAliasAccessWithin(ctx, tx, a, workspace, alias); e != nil {
		return e
	}
	base, e := lockedBase(ctx, tx, w, evidence.BaseID)
	if e != nil {
		return e
	}
	if !base.Enabled {
		return failure("KNOWLEDGE_BASE_DISABLED")
	}
	// History needs unchanged currently visible immutable originals, not retired
	// embedding rows or live compute. Rebuild/retention must not erase valid history.
	for i, m := range evidence.Sources {
		digest, e := hex.DecodeString(m.SourceSHA256)
		if !validID(m.DocumentID) || !validID(m.SourceID) || m.SourceVersion < 1 || m.StartByte < 0 || m.EndByte <= m.StartByte || m.EndByte > config.MaximumKnowledgeSourceBytes || m.EndByte-m.StartByte > config.MaximumKnowledgeChunkBytes || m.ChunkOrdinal < 0 || len(digest) != sha256.Size || e != nil || strings.ToLower(m.SourceSHA256) != m.SourceSHA256 || m.CitationID != "S"+strconv.Itoa(i+1) {
			return failure("KNOWLEDGE_INVALID_SOURCE")
		}
		var original []byte
		e = tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT substring(v.content FROM $6::integer+1 FOR $7::integer-$6::integer) FROM knowledge_documents d JOIN knowledge_source_versions v ON v.tenant_id=d.tenant_id AND v.workspace_id=d.workspace_id AND v.base_id=d.base_id AND v.document_id=d.id AND v.id=d.visible_source_id WHERE d.tenant_id=$1 AND d.workspace_id=$2 AND d.base_id=$3 AND d.id=$4 AND d.deleted_at IS NULL AND v.id=$5 AND v.source_version=$8 AND v.sha256=$9 AND $7::integer<=v.size_bytes`, `SELECT SUBSTRING(v.content,?+1,?-?) FROM knowledge_documents d JOIN knowledge_source_versions v ON v.tenant_id=d.tenant_id AND v.workspace_id=d.workspace_id AND v.base_id=d.base_id AND v.document_id=d.id AND v.id=d.visible_source_id WHERE d.tenant_id=? AND d.workspace_id=? AND d.base_id=? AND d.id=? AND d.deleted_at IS NULL AND v.id=? AND v.source_version=? AND v.sha256=? AND ?<=v.size_bytes`, []any{w.TenantID, w.ID, evidence.BaseID, m.DocumentID, m.SourceID, m.StartByte, m.EndByte, m.SourceVersion, m.SourceSHA256}, []any{m.StartByte, m.EndByte, m.StartByte, w.TenantID, w.ID, evidence.BaseID, m.DocumentID, m.SourceID, m.SourceVersion, m.SourceSHA256, m.EndByte})).Scan(&original)
		if errors.Is(e, pgx.ErrNoRows) {
			return failure("KNOWLEDGE_ANSWER_SOURCE_CHANGED")
		}
		if e != nil {
			return e
		}
		if len(original) != m.EndByte-m.StartByte || !utf8.Valid(original) {
			return failure("KNOWLEDGE_INVALID_SOURCE")
		}
	}
	return nil
}

// CitedStoredEvidence applies the same citation-membership validator used by a
// freshly generated answer, returning references without copied source text.
func CitedStoredEvidence(answer string, evidence StoredAnswerEvidence) ([]AnswerSourceEvidence, error) {
	matches := make([]Match, 0, len(evidence.Sources))
	byCitation := map[string]AnswerSourceEvidence{}
	for _, source := range evidence.Sources {
		matches = append(matches, Match{CitationID: source.CitationID})
		byCitation[source.CitationID] = source
	}
	cited, e := citedSources(answer, matches)
	if e != nil {
		return nil, e
	}
	out := make([]AnswerSourceEvidence, 0, len(cited))
	for _, source := range cited {
		out = append(out, byCitation[source.CitationID])
	}
	return out, nil
}

// RecheckPreparedAnswerInTransaction supports the owning workflow heartbeat;
// possession is checked before any DB effect and cannot be supplied through HTTP.
func (s *Service) RecheckPreparedAnswerInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, p *PreparedAnswer) error {
	return s.RecheckPreparedAnswerWithin(ctx, transaction.PostgreSQLTransaction(tx), a, p)
}

func (s *Service) RecheckPreparedAnswerWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, p *PreparedAnswer) error {
	if e := s.borrowed(ctx, tx); e != nil {
		return e
	}

	if !p.bound(s, a) {
		return failure("KNOWLEDGE_AUTHORITY_INVALID")
	}
	return s.recheckPrepared(ctx, tx, a, p)
}
