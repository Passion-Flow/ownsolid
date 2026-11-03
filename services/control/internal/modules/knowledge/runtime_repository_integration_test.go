//go:build repository_integration && knowledge_encoder_integration

package knowledge

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

// Only the account/Workspace and explicit interruption/fault metadata are
// synthetic. The configured real offline processor and actual knowledge owners
// create all normal source/attempt/chunk/generation/state/audit facts.
type knowledgeRuntimeStory struct {
	f         repositoryfixture.Fixture
	ctx       context.Context
	s         *Service
	a, portal identity.Auth
}

func knowledgeRuntimeSetup(t *testing.T) knowledgeRuntimeStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	t.Cleanup(cancel)
	account := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		account.Storage = nil
		account.DB = f.PostgreSQL
	}
	processor, c := actualProcessor(t)
	account.Config.Knowledge = c
	s := &Service{DB: f.PostgreSQL, Identity: account, Workspaces: &workspaces.Service{DB: f.PostgreSQL, Identity: account}, Processor: processor}
	g := knowledgeRuntimeStory{f: f, ctx: ctx, s: s}
	g.exec(t, "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES(?,?,?,'owner')", f.Tenant, f.Workspace, f.User)
	for _, audience := range []string{"admin", "portal"} {
		var issued identity.IssuedSession
		if e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			issued, e = account.IssueSessionWithin(ctx, q, f.User, 1, audience, "", uuid.NewString(), nil)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		a, e := account.Authenticate(ctx, issued.Token, audience)
		if e != nil {
			t.Fatal(e)
		}
		if audience == "admin" {
			g.a = a
		} else {
			g.portal = a
		}
	}
	return g
}
func (g knowledgeRuntimeStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(pg, my, args...)); e != nil {
		t.Fatal("actual constrained fixture mutation", e)
	}
}
func (g knowledgeRuntimeStory) base(t *testing.T) Base {
	t.Helper()
	b, e := g.s.CreateBase(g.ctx, g.portal, CreateBase{WorkspaceID: g.f.Workspace, Name: "Actual knowledge \u4e16\u754c " + uuid.NewString(), Reason: "Actual selected knowledge acceptance"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func (g knowledgeRuntimeStory) upload(t *testing.T, b Base, text string) Document {
	t.Helper()
	d, e := g.s.Upload(g.ctx, g.portal, g.f.Workspace, b.ID, UploadDocument{Name: "facts-" + uuid.NewString() + ".txt", Content: text, SHA256: hashBytes([]byte(text)), Reason: "Actual immutable source upload"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func (g knowledgeRuntimeStory) process(t *testing.T) {
	t.Helper()
	owned, e := g.s.ProcessNext(g.ctx)
	if e != nil || !owned {
		t.Fatal("actual selected encoder worker", owned, e)
	}
}
func (g knowledgeRuntimeStory) document(t *testing.T, b, d string) Document {
	t.Helper()
	value, e := g.s.GetDocument(g.ctx, g.portal, g.f.Workspace, b, d)
	if e != nil {
		t.Fatal(e)
	}
	return value
}
func (g knowledgeRuntimeStory) count(t *testing.T, table string) int {
	t.Helper()
	switch table {
	case "knowledge_bases", "knowledge_documents", "knowledge_source_versions", "knowledge_attempts", "knowledge_chunks", "knowledge_generations", "knowledge_mutation_intents", "audit_events":
	default:
		t.Fatal("unowned count")
	}
	var n int
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT count(*) FROM "+table+" WHERE tenant_id=$1", "SELECT count(*) FROM "+table+" WHERE tenant_id=?", g.f.Tenant)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func knowledgeRuntimeCode(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("want %s got %v", code, e)
	}
}

type knowledgeRuntimeFaultStore struct {
	transaction.Store
	rollback, unknown bool
	failFinal         bool
	calls             int
}

func (s *knowledgeRuntimeFaultStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	s.calls++
	number := s.calls
	e := s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		if s.failFinal && number == 2 {
			q = &knowledgeRuntimeFaultExecutor{Executor: q}
		}
		if e := fn(ctx, q); e != nil {
			return e
		}
		if s.rollback {
			return &transaction.Error{Kind: transaction.Unavailable}
		}
		return nil
	})
	if e != nil {
		return e
	}
	if s.unknown {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return nil
}

type knowledgeRuntimeFaultExecutor struct {
	transaction.Executor
	calls int
}

func (q *knowledgeRuntimeFaultExecutor) Exec(ctx context.Context, statement transaction.Statement) (int64, error) {
	q.calls++
	if q.calls == 2 {
		return 0, &transaction.Error{Kind: transaction.Unavailable}
	}
	return q.Executor.Exec(ctx, statement)
}
func (g knowledgeRuntimeStory) wrapped(store transaction.Store) knowledgeRuntimeStory {
	a := *g.s.Identity
	a.DB = nil
	a.Storage = store
	w := *g.s.Workspaces
	w.DB = nil
	w.Identity = &a
	s := *g.s
	s.DB = nil
	s.Identity = &a
	s.Workspaces = &w
	g.s = &s
	return g
}

func TestActualPairedKnowledgeRuntime(t *testing.T) {
	for _, name := range []string{"base_graph_replay_and_names", "current_roles_epoch_scope_and_pages", "source_version_replay_replace_delete", "actual_encoder_ingestion_search_offsets", "last_good_replacement_complete_rebuild", "expired_claim_requires_explicit_retry", "ready_empty_search_and_threshold", "rollback_and_unknown_zero_projections", "atomic_vector_acceptance_rollback", "generation_disable_and_recovery", "retention_current_data_and_restore", "readiness_initialization_and_borrowed_scope", "source_evidence_changes_fail_closed", "cross_workspace_vectors_are_isolated"} {
		t.Run(name, func(t *testing.T) {
			g := knowledgeRuntimeSetup(t)
			b := g.base(t)
			switch name {
			case "base_graph_replay_and_names":
				beforeAudit := g.count(t, "audit_events")
				input := CreateBase{WorkspaceID: g.f.Workspace, Name: "Exact graph \u4e16\u754c", Reason: "Exact initial graph receipt"}
				key := uuid.NewString()
				first, e := g.s.CreateBase(g.ctx, g.portal, input, key, uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				again, e := g.s.CreateBase(g.ctx, g.portal, input, key, uuid.NewString())
				if e != nil || again.ID != first.ID || again.ActiveGeneration.ID != first.ActiveGeneration.ID || again.ActiveGeneration.State != "active" {
					t.Fatal("exact graph replay", e)
				}
				input.Reason = "Different intent"
				out, e := g.s.CreateBase(g.ctx, g.portal, input, key, uuid.NewString())
				knowledgeRuntimeCode(t, e, "IDEMPOTENCY_CONFLICT")
				if !reflect.ValueOf(out).IsZero() {
					t.Fatal("conflict projected base")
				}
				input.Name = "exact graph \u4e16\u754c"
				_, e = g.s.CreateBase(g.ctx, g.portal, input, uuid.NewString(), uuid.NewString())
				knowledgeRuntimeCode(t, e, "KNOWLEDGE_BASE_EXISTS")
				if g.count(t, "knowledge_bases") != 2 || g.count(t, "knowledge_generations") != 2 || g.count(t, "knowledge_mutation_intents") != 2 || g.count(t, "audit_events") != beforeAudit+1 {
					t.Fatal("graph/audit cardinality")
				}
				if g.f.Store.Dialect() == transaction.MySQL {
					var state string
					if e = g.f.Store.QueryRow(g.ctx, g.f.Statement("", "SELECT initialization_state FROM knowledge_bases WHERE id=?", first.ID)).Scan(&state); e != nil || state != "ready" {
						t.Fatal("initialization committed before ready", e)
					}
				}
			case "current_roles_epoch_scope_and_pages":
				g.base(t)
				g.base(t)
				page, e := g.s.BasePage(g.ctx, g.portal, g.f.Workspace, url.Values{"limit": {"2"}})
				if e != nil || len(page.Items) != 2 || page.NextCursor == nil {
					t.Fatal("scoped page", e)
				}
				next, e := g.s.BasePage(g.ctx, g.portal, g.f.Workspace, url.Values{"limit": {"2"}, "cursor": {*page.NextCursor}})
				if e != nil || len(next.Items) != 1 || next.Items[0].CreatedAt.Location() != time.UTC {
					t.Fatal("keyset/UTC", e)
				}
				_, e = g.s.GetBase(g.ctx, g.portal, uuid.NewString(), b.ID)
				if e == nil {
					t.Fatal("foreign Workspace read")
				}
				g.exec(t, "UPDATE workspace_memberships SET role='viewer' WHERE user_id=$1", "UPDATE workspace_memberships SET role='viewer' WHERE user_id=?", g.f.User)
				if _, e = g.s.GetBase(g.ctx, g.portal, g.f.Workspace, b.ID); e != nil {
					t.Fatal("viewer read", e)
				}
				_, e = g.s.Update(g.ctx, g.portal, g.f.Workspace, b.ID, UpdateBase{Name: b.Name, Enabled: false, ExpectedRevision: b.Revision, Reason: "Refuse viewer mutation"}, uuid.NewString())
				knowledgeRuntimeCode(t, e, "ACCESS_DENIED")
				g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
				scope, e := g.s.ResolveBaseWorkspace(g.ctx, g.a, b.ID)
				if e == nil || scope != "" {
					t.Fatal("stale Admin scope", e)
				}
			case "source_version_replay_replace_delete":
				text := "Caf\u00e9 😀 \u4e16\u754c\r\nExact source bytes."
				input := UploadDocument{Name: "facts.txt", Content: text, SHA256: hashBytes([]byte(text)), Reason: "Actual source receipt"}
				key := uuid.NewString()
				d, e := g.s.Upload(g.ctx, g.portal, g.f.Workspace, b.ID, input, key, uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				again, e := g.s.Upload(g.ctx, g.portal, g.f.Workspace, b.ID, input, key, uuid.NewString())
				if e != nil || again.ID != d.ID {
					t.Fatal("upload replay", e)
				}
				source, e := g.s.GetSource(g.ctx, g.portal, g.f.Workspace, b.ID, d.ID, d.PendingSource.ID)
				if e != nil || source.Content != text || source.SizeBytes != len(text) {
					t.Fatal("exact persisted source", e)
				}
				deleted, e := g.s.Delete(g.ctx, g.portal, g.f.Workspace, b.ID, d.ID, RevisionReason{ExpectedRevision: d.Revision, Reason: "Delete queued source"}, uuid.NewString())
				if e != nil || deleted.DeletedAt == nil {
					t.Fatal("tombstone", e)
				}
				if _, e = g.s.GetSource(g.ctx, g.portal, g.f.Workspace, b.ID, d.ID, d.PendingSource.ID); e == nil {
					t.Fatal("deleted source visible")
				}
				if g.count(t, "knowledge_source_versions") != 1 {
					t.Fatal("source history erased")
				}
			case "actual_encoder_ingestion_search_offsets":
				text := "  Caf\u00e9 😀 \u6c49\u5b57\r\nThe project launch city is Lisbon. Terminal ZEBRA-TAIL.\r\n"
				d := g.upload(t, b, text)
				g.process(t)
				d = g.document(t, b.ID, d.ID)
				if d.State != "ready" || d.VisibleSource == nil || d.PendingSource != nil || d.CurrentAttempt == nil || d.CurrentAttempt.ChunkCount < 1 {
					t.Fatal("real complete ready source", d)
				}
				found, e := g.s.Search(g.ctx, g.portal, g.f.Workspace, b.ID, SearchRequest{Query: "Which city hosts the project launch?", TopK: 2, MinimumScore: -1}, uuid.NewString())
				if e != nil || len(found.Matches) < 1 {
					t.Fatal("real encoder/vector retrieval", e)
				}
				for _, m := range found.Matches {
					if m.SourceID != d.VisibleSource.ID || m.Text != text[m.StartByte:m.EndByte] || m.SourceSHA256 != hashBytes([]byte(text)) {
						t.Fatal("source-aligned retrieval evidence", m)
					}
				}
				if e = g.s.VerifyStored(g.ctx); e != nil {
					t.Fatal("selected retained integrity", e)
				}
			case "last_good_replacement_complete_rebuild":
				d := g.upload(t, b, "The project launch city is Lisbon.")
				g.process(t)
				d = g.document(t, b.ID, d.ID)
				old := d.VisibleSource.ID
				text := "The new project launch city is Kyoto."
				replaced, e := g.s.Replace(g.ctx, g.portal, g.f.Workspace, b.ID, d.ID, ReplaceSource{Content: text, SHA256: hashBytes([]byte(text)), ExpectedRevision: d.Revision, Reason: "Actual immutable replacement"}, uuid.NewString(), uuid.NewString())
				if e != nil || replaced.VisibleSource.ID != old || replaced.PendingSource == nil {
					t.Fatal("last good source lost", e)
				}
				g.process(t)
				d = g.document(t, b.ID, d.ID)
				if d.VisibleSource.ID == old || d.State != "ready" {
					t.Fatal("replacement not activated")
				}
				current, e := g.s.GetBase(g.ctx, g.portal, g.f.Workspace, b.ID)
				if e != nil {
					t.Fatal(e)
				}
				rebuilding, e := g.s.Rebuild(g.ctx, g.portal, g.f.Workspace, b.ID, RevisionReason{ExpectedRevision: current.Revision, Reason: "Actual complete generation rebuild"}, uuid.NewString(), uuid.NewString())
				if e != nil || rebuilding.BuildingGeneration == nil {
					t.Fatal("rebuild intent", e)
				}
				prior := rebuilding.ActiveGeneration.ID
				g.process(t)
				current, e = g.s.GetBase(g.ctx, g.portal, g.f.Workspace, b.ID)
				if e != nil || current.BuildingGeneration != nil || current.ActiveGeneration.ID == prior || current.ActiveGeneration.State != "active" {
					t.Fatal("complete atomic activation", e)
				}
			case "expired_claim_requires_explicit_retry":
				d := g.upload(t, b, "An exact interrupted source remains recoverable.")
				c, e := g.s.nextCandidate(g.ctx)
				if e != nil {
					t.Fatal(e)
				}
				claim, e := g.s.claim(g.ctx, c)
				if e != nil || claim == nil {
					t.Fatal("actual claim", e)
				}
				g.exec(t, "UPDATE knowledge_attempts SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE knowledge_attempts SET lease_expires_at=TIMESTAMPADD(SECOND,-1,CURRENT_TIMESTAMP(6)) WHERE id=?", c.id)
				g.process(t)
				d = g.document(t, b.ID, d.ID)
				if d.State != "failed" || d.CurrentAttempt.ErrorCode == nil || *d.CurrentAttempt.ErrorCode != "KNOWLEDGE_WORKER_INTERRUPTED" {
					t.Fatal("expired claim adopted output", d)
				}
				retried, e := g.s.Retry(g.ctx, g.portal, g.f.Workspace, b.ID, d.ID, RevisionReason{ExpectedRevision: d.Revision, Reason: "Explicit expiry recovery"}, uuid.NewString(), uuid.NewString())
				if e != nil || retried.CurrentAttempt.ID == c.id {
					t.Fatal("explicit retry", e)
				}
				g.process(t)
				if d = g.document(t, b.ID, d.ID); d.State != "ready" {
					t.Fatal("actual retry failed")
				}
			case "ready_empty_search_and_threshold":
				found, e := g.s.Search(g.ctx, g.portal, g.f.Workspace, b.ID, SearchRequest{Query: "No sources are indexed yet.", TopK: 1, MinimumScore: 1}, uuid.NewString())
				if e != nil || len(found.Matches) != 0 || found.GenerationID != b.ActiveGeneration.ID {
					t.Fatal("actual empty search", found, e)
				}
			case "rollback_and_unknown_zero_projections":
				input := CreateBase{WorkspaceID: g.f.Workspace, Name: "Atomic fault graph", Reason: "Actual transaction fault control"}
				key := uuid.NewString()
				before := g.count(t, "audit_events")
				broken := g.wrapped(&knowledgeRuntimeFaultStore{Store: g.f.Store, rollback: true})
				out, e := broken.s.CreateBase(g.ctx, g.portal, input, key, uuid.NewString())
				knowledgeRuntimeCode(t, e, string(transaction.Unavailable))
				if !reflect.ValueOf(out).IsZero() || g.count(t, "knowledge_bases") != 1 || g.count(t, "knowledge_generations") != 1 || g.count(t, "audit_events") != before {
					t.Fatal("rolled back graph leaked")
				}
				unknown := g.wrapped(&knowledgeRuntimeFaultStore{Store: g.f.Store, unknown: true})
				out, e = unknown.s.CreateBase(g.ctx, g.portal, input, key, uuid.NewString())
				knowledgeRuntimeCode(t, e, string(transaction.CommitUnknown))
				if !reflect.ValueOf(out).IsZero() {
					t.Fatal("unknown graph DTO")
				}
				out, e = g.s.CreateBase(g.ctx, g.portal, input, key, uuid.NewString())
				if e != nil || out.ID == "" || out.ActiveGeneration.ID == "" || g.count(t, "knowledge_bases") != 2 || g.count(t, "knowledge_generations") != 2 {
					t.Fatal("exact full graph reconciliation", e)
				}
				page, e := unknown.s.BasePage(g.ctx, g.portal, g.f.Workspace, url.Values{})
				knowledgeRuntimeCode(t, e, string(transaction.CommitUnknown))
				if len(page.Items) != 0 || page.NextCursor != nil {
					t.Fatal("unknown page projected")
				}
			case "atomic_vector_acceptance_rollback":
				d := g.upload(t, b, "An actual vector acceptance batch must be atomic.")
				broken := g.wrapped(&knowledgeRuntimeFaultStore{Store: g.f.Store, failFinal: true})
				owned, e := broken.s.ProcessNext(g.ctx)
				knowledgeRuntimeCode(t, e, string(transaction.Unavailable))
				if !owned || g.count(t, "knowledge_chunks") != 0 {
					t.Fatal("failed acceptance retained partial chunks")
				}
				d = g.document(t, b.ID, d.ID)
				if d.State != "running" || d.VisibleSource != nil {
					t.Fatal("failed acceptance published source")
				}
				g.exec(t, "UPDATE knowledge_attempts SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE knowledge_attempts SET lease_expires_at=TIMESTAMPADD(SECOND,-1,CURRENT_TIMESTAMP(6)) WHERE id=?", d.CurrentAttempt.ID)
				g.process(t)
				d = g.document(t, b.ID, d.ID)
				_, e = g.s.Retry(g.ctx, g.portal, g.f.Workspace, b.ID, d.ID, RevisionReason{ExpectedRevision: d.Revision, Reason: "Retry rolled back encoder acceptance"}, uuid.NewString(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				g.process(t)
				if d = g.document(t, b.ID, d.ID); d.State != "ready" || d.VisibleSource == nil {
					t.Fatal("recovery activation")
				}
			case "generation_disable_and_recovery":
				d := g.upload(t, b, "Disable must revoke queued immutable source processing.")
				updated, e := g.s.Update(g.ctx, g.portal, g.f.Workspace, b.ID, UpdateBase{Name: b.Name, Enabled: false, ExpectedRevision: b.Revision, Reason: "Disable queued base"}, uuid.NewString())
				if e != nil || updated.Enabled {
					t.Fatal(e)
				}
				if _, e = g.s.Search(g.ctx, g.portal, g.f.Workspace, b.ID, SearchRequest{Query: "Disabled data", TopK: 1, MinimumScore: 0}, uuid.NewString()); e == nil {
					t.Fatal("disabled search admitted")
				}
				d = g.document(t, b.ID, d.ID)
				if d.State != "failed" || d.CurrentAttempt.State != "cancelled" {
					t.Fatal("disable did not cancel queued attempt")
				}
			case "retention_current_data_and_restore":
				d := g.upload(t, b, "Current data survives selected retention and restore authority invalidation.")
				g.process(t)
				d = g.document(t, b.ID, d.ID)
				result, e := g.s.Cleanup(g.ctx)
				if e != nil || result.Chunks+result.Attempts+result.Sources+result.Documents+result.Generations != 0 {
					t.Fatal("current references purged", result, e)
				}
				pending := g.upload(t, b, "A pending source must not resume after coordinated restore.")
				if e = g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error { return InvalidateAuthoritiesWithin(ctx, q) }); e != nil {
					t.Fatal(e)
				}
				pending = g.document(t, b.ID, pending.ID)
				if pending.State != "failed" || pending.CurrentAttempt.State != "failed" {
					t.Fatal("restored delegation remains live")
				}
				source, e := g.s.GetSource(g.ctx, g.portal, g.f.Workspace, b.ID, d.ID, d.VisibleSource.ID)
				if e != nil || source.Content == "" {
					t.Fatal("restore erased immutable current source", e)
				}
			case "readiness_initialization_and_borrowed_scope":
				if e := g.s.VerifyStored(g.ctx); e != nil {
					t.Fatal(e)
				}
				if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
					value, e := g.s.CheckWorkflowBaseWithin(ctx, q, g.portal, g.f.Workspace, b.ID)
					if e == nil && value.ID != b.ID {
						return fmt.Errorf("borrowed source mismatch")
					}
					return e
				}); e != nil {
					t.Fatal("actual neutral workflow dependency", e)
				}
				if g.f.Store.Dialect() == transaction.MySQL {
					id := uuid.NewString()
					g.exec(t, "", `INSERT INTO knowledge_bases(id,tenant_id,workspace_id,name,name_key,created_by,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,?,?,?)`, id, g.f.Tenant, g.f.Workspace, "Explicit interrupted initial graph", id, g.f.User, uuid.NewString(), make([]byte, 32))
					if e := g.s.VerifyStored(g.ctx); e == nil {
						t.Fatal("half graph reported ready")
					}
					if value, e := g.s.GetBase(g.ctx, g.a, g.f.Workspace, id); e == nil || value.ID != "" {
						t.Fatal("half graph public projection")
					}
				}
			case "source_evidence_changes_fail_closed":
				d := g.upload(t, b, "Actual source evidence must remain current.")
				g.process(t)
				found, e := g.s.Search(g.ctx, g.portal, g.f.Workspace, b.ID, SearchRequest{Query: "What must remain current?", TopK: 1, MinimumScore: -1}, uuid.NewString())
				if e != nil || len(found.Matches) != 1 {
					t.Fatal(e)
				}
				e = g.s.transaction(g.ctx, g.portal, g.f.Workspace, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
					return g.s.checkSourceEvidence(ctx, q, w, b.ID, found)
				})
				if e != nil {
					t.Fatal(e)
				}
				d = g.document(t, b.ID, d.ID)
				_, e = g.s.Delete(g.ctx, g.portal, g.f.Workspace, b.ID, d.ID, RevisionReason{ExpectedRevision: d.Revision, Reason: "Invalidate source evidence"}, uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				e = g.s.transaction(g.ctx, g.portal, g.f.Workspace, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
					return g.s.checkSourceEvidence(ctx, q, w, b.ID, found)
				})
				knowledgeRuntimeCode(t, e, "KNOWLEDGE_ANSWER_SOURCE_CHANGED")
			case "cross_workspace_vectors_are_isolated":
				first := g.upload(t, b, "The first Workspace code is ORANGE.")
				g.process(t)
				foreign := uuid.NewString()
				g.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign vectors','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Foreign vectors','active')", foreign, g.f.Tenant)
				g.exec(t, "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES(?,?,?,'owner')", g.f.Tenant, foreign, g.f.User)
				other, e := g.s.CreateBase(g.ctx, g.portal, CreateBase{WorkspaceID: foreign, Name: "Other authorized base", Reason: "Isolated vector graph"}, uuid.NewString(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				text := "The second Workspace code is BLUE."
				_, e = g.s.Upload(g.ctx, g.portal, foreign, other.ID, UploadDocument{Name: "foreign.txt", Content: text, SHA256: hashBytes([]byte(text)), Reason: "Foreign vector fixture"}, uuid.NewString(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				g.process(t)
				found, e := g.s.Search(g.ctx, g.portal, g.f.Workspace, b.ID, SearchRequest{Query: "What is the Workspace code?", TopK: 20, MinimumScore: -1}, uuid.NewString())
				if e != nil || len(found.Matches) != 1 || found.Matches[0].DocumentID != first.ID || strings.Contains(found.Matches[0].Text, "BLUE") {
					t.Fatal("foreign scoped vector leak", found, e)
				}
			}
		})
	}
}
