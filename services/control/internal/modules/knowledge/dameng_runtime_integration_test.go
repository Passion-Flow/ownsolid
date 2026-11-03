//go:build repository_integration && knowledge_encoder_integration && cgo && odbc_manager

package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

// Operator-admitted, already migrated, exclusive empty schema; no CREATE/DROP.
func nativeKnowledgeStory(t *testing.T) knowledgeRuntimeStory {
	t.Helper()
	c, e := dameng.LoadConnectionConfiguration(os.Getenv("PROJECT_ONE_KNOWLEDGE_DAMENG_CONFIGURATION"), "")
	if e != nil || !regexp.MustCompile(`^M29_KNOWLEDGE_[A-F0-9]{32}_TEST$`).MatchString(c.Schema) {
		t.Fatal("KNOWLEDGE_EXCLUSIVE_NATIVE_TEST_SCHEMA_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	t.Cleanup(cancel)
	kernel, _, e := dameng.Open(ctx, c)
	if kernel != nil {
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), time.Duration(c.CleanupSeconds)*time.Second)
			defer stop()
			status, err := kernel.Close(cleanup)
			if err != nil || status.State != "closed" || status.PhysicalOwners != 0 || status.CleanupJobs != 0 {
				t.Error("native owner cleanup pending", err)
			}
		})
	}
	if e != nil {
		t.Fatal("native DM open", e)
	}
	store, e := transaction.FromDameng(kernel)
	if e != nil {
		t.Fatal(e)
	}
	f := repositoryfixture.Fixture{Store: store, Tenant: uuid.NewString(), Workspace: uuid.NewString(), User: uuid.NewString()}
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var complete bool
		if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT bootstrap_complete FROM installation_state WHERE id=1 FOR UPDATE`)).Scan(&complete); e != nil {
			return e
		}
		if complete {
			return &transaction.Error{Kind: transaction.CheckConflict}
		}
		var count int
		if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT (SELECT count(*) FROM users)+(SELECT count(*) FROM knowledge_bases) FROM DUAL`)).Scan(&count); e != nil {
			return e
		}
		if count != 0 {
			return &transaction.Error{Kind: transaction.CheckConflict}
		}
		for _, stmt := range []transaction.Statement{
			transaction.SQL(transaction.Dameng, `INSERT INTO tenants(id,name,status) VALUES(?,?,'active')`, f.Tenant, "Synthetic native knowledge tenant"),
			transaction.SQL(transaction.Dameng, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,?,'active')`, f.Workspace, f.Tenant, "Synthetic native knowledge workspace"),
			transaction.SQL(transaction.Dameng, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,?,'en','active',1)`, f.User, "native-knowledge@example.invalid", "native-knowledge@example.invalid", "Synthetic native actor"),
			transaction.SQL(transaction.Dameng, `INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role,authority_id) VALUES(?,?,?,'owner',?)`, f.Tenant, f.Workspace, f.User, uuid.NewString()),
			transaction.SQL(transaction.Dameng, `UPDATE installation_state SET bootstrap_complete=1,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=CURRENT_TIMESTAMP(6) WHERE id=1`, f.Tenant, f.Workspace, f.User),
		} {
			if _, e := q.Exec(ctx, stmt); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal("native isolated fixture admission/seed", e)
	}

	account := &identity.Service{Storage: store}
	processor, configuration := actualProcessor(t)
	account.Config.Knowledge = configuration
	s := &Service{Identity: account, Workspaces: &workspaces.Service{Identity: account}, Processor: processor}
	g := knowledgeRuntimeStory{f: f, ctx: ctx, s: s}
	for _, audience := range []string{"admin", "portal"} {
		var issued identity.IssuedSession
		if e := store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
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
func nativeKnowledgeCount(t *testing.T, g knowledgeRuntimeStory, sql string, args ...any) int {
	t.Helper()
	var n int
	if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.Dameng, sql, args...)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func nativeKnowledgeExec(t *testing.T, g knowledgeRuntimeStory, sql string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, transaction.SQL(transaction.Dameng, sql, args...)); e != nil {
		t.Fatal(e)
	}
}
func nativeKnowledgeReject(t *testing.T, g knowledgeRuntimeStory, sql string, args ...any) {
	t.Helper()
	rejected := false
	e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		_, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, sql, args...))
		if e != nil {
			var native *transaction.Error
			// A transport/buffer/grammar failure is not constraint evidence. The
			// scoped trigger uses -20012; declarative checks use CheckConflict.
			rejected = errors.As(e, &native) && (native.Kind == transaction.CheckConflict || native.NativeCode == -20012)
			return e
		}
		return errors.New("unexpected acceptance must roll back")
	})
	if !rejected || e == nil {
		t.Fatal("native constraint accepted invalid mutation")
	}
}

func TestActualDamengKnowledgeLifecycle(t *testing.T) {
	g := nativeKnowledgeStory(t)
	b := g.base(t)
	if e := g.s.VerifyStored(g.ctx); e != nil {
		t.Fatal("native readiness", e)
	}
	text := "Caf\u00e9 \u4e16\u754c. The launch city is Oslo. Exact original UTF8 bytes survive publication."
	d := g.upload(t, b, text)
	g.process(t)
	d = g.document(t, b.ID, d.ID)
	if d.State != "ready" || d.VisibleSource == nil || d.CurrentAttempt == nil || d.CurrentAttempt.ChunkCount < 1 {
		t.Fatal("native actual encoder acceptance", d)
	}
	source, e := g.s.GetSource(g.ctx, g.portal, g.f.Workspace, b.ID, d.ID, d.VisibleSource.ID)
	if e != nil || source.Content != text || source.SHA256 != hashBytes([]byte(text)) {
		t.Fatal("native exact source", e)
	}
	query := SearchRequest{Query: "What is the launch city?", TopK: 3, MinimumScore: -1}
	found, e := g.s.Search(g.ctx, g.portal, g.f.Workspace, b.ID, query, uuid.NewString())
	if e != nil || len(found.Matches) == 0 || found.GenerationID != b.ActiveGeneration.ID || found.Matches[0].DocumentID != d.ID {
		t.Fatal("native exact retrieval", found, e)
	}
	for _, m := range found.Matches {
		if !strings.HasPrefix(text[m.StartByte:m.EndByte], m.Text) {
			t.Fatal("native UTF8 offset/excerpt mismatch")
		}
	}
	cross, e := g.s.Search(g.ctx, g.portal, uuid.NewString(), b.ID, query, uuid.NewString())
	if e == nil || len(cross.Matches) != 0 {
		t.Fatal("native foreign Workspace projection")
	}

	// Each invalid DML runs in a fresh transaction, preserving driver failures.
	nativeKnowledgeReject(t, g, `UPDATE knowledge_source_versions SET content=? WHERE id=?`, []byte("mutated"), source.SourceID)
	nativeKnowledgeReject(t, g, `UPDATE knowledge_chunks SET ordinal=ordinal+1 WHERE attempt_id=?`, d.CurrentAttempt.ID)
	nativeKnowledgeReject(t, g, `UPDATE knowledge_generations SET catalog_sha256=? WHERE id=?`, strings.Repeat("a", 64), b.ActiveGeneration.ID)
	nativeKnowledgeReject(t, g, `UPDATE knowledge_attempts SET expected_document_revision=expected_document_revision+1 WHERE id=?`, d.CurrentAttempt.ID)
	nativeKnowledgeReject(t, g, `UPDATE knowledge_bases SET initialization_state='initializing',active_generation_id=NULL WHERE id=?`, b.ID)
	for _, raw := range [][]byte{{0}, {0xc0, 0xaf}, {0xed, 0xa0, 0x80}, {0xf4, 0x90, 0x80, 0x80}, append([]byte(strings.Repeat("x", 4095)), 0xe2, 0x82)} {
		nativeKnowledgeReject(t, g, dmKnowledgeInsertSource, uuid.NewString(), g.f.Tenant, g.f.Workspace, b.ID, d.ID, int64(2), raw, hashBytes(raw), g.f.User, len(raw))
	}
	// Full allowed2MiB BLOB, including a UTF8 sequence across the4096-byte window.
	large := []byte(strings.Repeat("x", 4095) + "\u4e16\u754c" + strings.Repeat("x", config.MaximumKnowledgeSourceBytes-4101))
	largeID := uuid.NewString()
	nativeKnowledgeExec(t, g, dmKnowledgeInsertSource, largeID, g.f.Tenant, g.f.Workspace, b.ID, d.ID, int64(2), large, hashBytes(large), g.f.User, len(large))
	var readback []byte
	var size int
	if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.Dameng, `SELECT content,size_bytes FROM knowledge_source_versions WHERE id=?`, largeID)).Scan(&readback, &size); e != nil || size != len(large) || hashBytes(readback) != hashBytes(large) {
		t.Fatal("native full2MiB profile required", e)
	}
	nativeKnowledgeReject(t, g, dmKnowledgeInsertSource, uuid.NewString(), g.f.Tenant, g.f.Workspace, b.ID, d.ID, int64(3), []byte("valid"), strings.Repeat("0", 64), g.f.User, 5)
	invalidVectors := []string{`[1]`}
	for _, first := range []any{nil, "1", 2, 0} {
		values := make([]any, EmbeddingDimension)
		for i := range values {
			values[i] = 0
		}
		values[0] = first
		raw, e := json.Marshal(values)
		if e != nil {
			t.Fatal(e)
		}
		invalidVectors = append(invalidVectors, string(raw))
	}
	for _, vector := range invalidVectors {
		nativeKnowledgeReject(t, g, dmKnowledgeInsertChunk, g.f.Tenant, g.f.Workspace, b.ID, d.ID, b.ActiveGeneration.ID, source.SourceID, d.CurrentAttempt.ID, 4095, 0, 1, 1, vector)
	}

	replacement := "Caf\u00e9 \u4e16\u754c. The new launch city is Tokyo."
	updated, e := g.s.Replace(g.ctx, g.portal, g.f.Workspace, b.ID, d.ID, ReplaceSource{Content: replacement, SHA256: hashBytes([]byte(replacement)), ExpectedRevision: d.Revision, Reason: "Native replacement"}, uuid.NewString(), uuid.NewString())
	if e != nil || updated.VisibleSource == nil || updated.VisibleSource.ID != source.SourceID {
		t.Fatal("native last-good source", e)
	}
	g.process(t)
	d = g.document(t, b.ID, d.ID)
	if d.VisibleSource.ID == source.SourceID {
		t.Fatal("native replacement not activated")
	}
	for i := 0; i < 2; i++ {
		current, e := g.s.GetBase(g.ctx, g.portal, g.f.Workspace, b.ID)
		if e != nil {
			t.Fatal(e)
		}
		_, e = g.s.Rebuild(g.ctx, g.portal, g.f.Workspace, b.ID, RevisionReason{ExpectedRevision: current.Revision, Reason: "Native complete generation"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		g.process(t)
		if e = g.s.ReconcileGeneration(g.ctx); e != nil {
			t.Fatal(e)
		}
	}
	if n := nativeKnowledgeCount(t, g, `SELECT count(*) FROM knowledge_generations WHERE base_id=? AND state='retired' AND active_base_id IS NULL AND building_base_id IS NULL`, b.ID); n != 2 {
		t.Fatal("retired generation conditional uniqueness", n)
	}

	// Membership replacement after actual encoding cannot authorize an old claim.
	pending := g.upload(t, b, "Revoked work must never publish vectors.")
	candidate, e := g.s.nextCandidate(g.ctx)
	if e != nil {
		t.Fatal(e)
	}
	claim, e := g.s.claim(g.ctx, candidate)
	if e != nil || claim == nil {
		t.Fatal("native claim", e)
	}
	result, e := g.s.Processor.Execute(g.ctx, claim.text, true, claim.generationInfo.ChunkTokens, claim.generationInfo.OverlapTokens)
	if e != nil {
		t.Fatal(e)
	}
	nativeKnowledgeExec(t, g, `UPDATE workspace_memberships SET authority_id=? WHERE tenant_id=? AND workspace_id=? AND user_id=?`, uuid.NewString(), g.f.Tenant, g.f.Workspace, g.f.User)
	if e = g.s.finish(g.ctx, claim, result, nil); e != nil {
		t.Fatal(e)
	}
	pending = g.document(t, b.ID, pending.ID)
	if pending.State != "failed" || pending.VisibleSource != nil {
		t.Fatal("stale membership accepted vectors")
	}

	request := CreateBase{WorkspaceID: g.f.Workspace, Name: "Native reconciled base", Reason: "Unknown receipt"}
	key := uuid.NewString()
	unknown := g.wrapped(&knowledgeRuntimeFaultStore{Store: g.f.Store, unknown: true})
	ambiguous, e := unknown.s.CreateBase(g.ctx, g.portal, request, key, uuid.NewString())
	knowledgeRuntimeCode(t, e, string(transaction.CommitUnknown))
	if ambiguous.ID != "" {
		t.Fatal("unknown result projected success")
	}
	recovered, e := g.s.CreateBase(g.ctx, g.portal, request, key, uuid.NewString())
	if e != nil || recovered.ID == "" {
		t.Fatal("native same-key reconciliation", e)
	}
	before := nativeKnowledgeCount(t, g, `SELECT count(*) FROM audit_events WHERE tenant_id=?`, g.f.Tenant)
	rollback := g.wrapped(&knowledgeRuntimeFaultStore{Store: g.f.Store, rollback: true})
	request.Name = "Native rolled-back base"
	rejected, e := rollback.s.CreateBase(g.ctx, g.portal, request, uuid.NewString(), uuid.NewString())
	if e == nil || rejected.ID != "" || before != nativeKnowledgeCount(t, g, `SELECT count(*) FROM audit_events WHERE tenant_id=?`, g.f.Tenant) {
		t.Fatal("native rollback/audit atomicity", e)
	}
	page, e := g.s.BasePage(g.ctx, g.portal, g.f.Workspace, url.Values{"limit": {"1"}})
	if e != nil || page.NextCursor == nil {
		t.Fatal("native base page", e)
	}
	next, e := g.s.BasePage(g.ctx, g.portal, g.f.Workspace, url.Values{"limit": {"1"}, "cursor": {*page.NextCursor}})
	if e != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal("native exact timestamp continuation", e)
	}
	docs, e := g.s.DocumentPage(g.ctx, g.portal, g.f.Workspace, b.ID, url.Values{"limit": {"1"}})
	if e != nil || docs.NextCursor == nil {
		t.Fatal("native document page", e)
	}

	restored := g.upload(t, b, "Restore invalidates this queued authority.")
	if e = g.f.Store.Within(g.ctx, false, InvalidateAuthoritiesWithin); e != nil {
		t.Fatal(e)
	}
	restored = g.document(t, b.ID, restored.ID)
	if restored.State != "failed" {
		t.Fatal("native restore retained live authority")
	}
	if e = g.s.VerifyStored(g.ctx); e != nil {
		t.Fatal("native historical source/vector verification", e)
	}
	if _, e = g.s.Cleanup(g.ctx); e != nil {
		t.Fatal("native bounded retention SQL", e)
	}
	for _, id := range []string{pending.ID, restored.ID} {
		current := g.document(t, b.ID, id)
		if _, e = g.s.Delete(g.ctx, g.portal, g.f.Workspace, b.ID, id, RevisionReason{ExpectedRevision: current.Revision, Reason: "Native same-scope tombstone"}, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	if n := nativeKnowledgeCount(t, g, `SELECT count(*) FROM knowledge_documents WHERE base_id=? AND deleted_at IS NOT NULL AND live_base_id IS NULL AND live_name_key IS NULL`, b.ID); n != 2 {
		t.Fatal("native composite tombstone uniqueness", n)
	}
}
