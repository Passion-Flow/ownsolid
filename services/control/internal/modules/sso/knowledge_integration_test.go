//go:build knowledge_database_integration && knowledge_encoder_integration

package sso

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// Connector/flow/binding rows are explicit database fixtures. The ordinary SSO
// issuance/current-source guard and knowledge transaction/encoder are real.
func TestM12ActualEveryKnowledgeOperationRechecksRevokedSSOSource(t *testing.T) {
	for _, revoked := range []string{"connector", "binding"} {
		t.Run(revoked, func(t *testing.T) {
			f := sourceFixture(t)
			ctx := context.Background()
			issued := f.issue(t, "")
			a, e := f.s.Identity.Authenticate(ctx, issued.Token, "admin")
			if e != nil {
				t.Fatal(e)
			}
			scratch := t.TempDir()
			if os.Chmod(scratch, 0700) != nil {
				t.Fatal("private scratch")
			}
			f.s.Identity.Config.Knowledge = config.KnowledgeConfiguration{Enabled: true, PythonExecutable: os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON"), WorkerFile: filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "runtimes/knowledge/embed.py"), ModelDirectory: os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY"), ScratchRoot: scratch}
			w := &workspaces.Service{DB: f.s.Identity.DB, Identity: f.s.Identity}
			s := &knowledge.Service{DB: f.s.Identity.DB, Identity: f.s.Identity, Workspaces: w, Distribution: &distribution.Service{DB: f.s.Identity.DB, Identity: f.s.Identity, Workspaces: w}}
			if e = s.OpenConfiguredProcessor(ctx); e != nil {
				t.Fatal("actual encoder readiness", e)
			}
			defer s.Processor.Close()
			workspace := a.Me.Workspace.ID
			base, e := s.CreateBase(ctx, a, knowledge.CreateBase{WorkspaceID: workspace, Name: "SSO knowledge authority", Reason: "Synthetic source fixture"}, uuid.NewString(), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			text := "Synthetic source remains private after SSO revocation."
			digest := sha256.Sum256([]byte(text))
			hash := hex.EncodeToString(digest[:])
			document, e := s.Upload(ctx, a, workspace, base.ID, knowledge.UploadDocument{Name: "source.txt", Content: text, SHA256: hash, Reason: "Synthetic admission"}, uuid.NewString(), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			var admittedBefore int
			if e = s.DB.QueryRow(ctx, `SELECT count(*) FROM rate_limit_buckets`).Scan(&admittedBefore); e != nil {
				t.Fatal(e)
			}
			var query string
			if revoked == "connector" {
				query = `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1,updated_at=clock_timestamp() WHERE id=$1`
			} else {
				query = `UPDATE sso_bindings SET active=false,revision=revision+1 WHERE id=$1`
			}
			id := f.source.ConnectorID
			if revoked == "binding" {
				id = f.source.BindingID
			}
			if _, e = s.DB.Exec(ctx, query, id); e != nil {
				t.Fatal(e)
			}
			reason := knowledge.RevisionReason{ExpectedRevision: document.Revision, Reason: "Refused expired source"}
			checks := map[string]func() error{
				"configuration": func() error { _, e := s.Configuration(ctx, a, workspace); return e },
				"base_list":     func() error { _, e := s.BasePage(ctx, a, workspace, url.Values{}); return e },
				"base_resolve":  func() error { _, e := s.ResolveBaseWorkspace(ctx, a, base.ID); return e },
				"base_get":      func() error { _, e := s.GetBase(ctx, a, workspace, base.ID); return e },
				"base_create": func() error {
					_, e := s.CreateBase(ctx, a, knowledge.CreateBase{WorkspaceID: workspace, Name: "Refused", Reason: reason.Reason}, uuid.NewString(), uuid.NewString())
					return e
				},
				"base_update": func() error {
					_, e := s.Update(ctx, a, workspace, base.ID, knowledge.UpdateBase{Name: base.Name, Enabled: false, ExpectedRevision: base.Revision, Reason: reason.Reason}, uuid.NewString())
					return e
				},
				"document_list": func() error { _, e := s.DocumentPage(ctx, a, workspace, base.ID, url.Values{}); return e },
				"document_get":  func() error { _, e := s.GetDocument(ctx, a, workspace, base.ID, document.ID); return e },
				"source_get": func() error {
					_, e := s.GetSource(ctx, a, workspace, base.ID, document.ID, document.PendingSource.ID)
					return e
				},
				"upload": func() error {
					_, e := s.Upload(ctx, a, workspace, base.ID, knowledge.UploadDocument{Name: "refused.txt", Content: text, SHA256: hash, Reason: reason.Reason}, uuid.NewString(), uuid.NewString())
					return e
				},
				"replace": func() error {
					_, e := s.Replace(ctx, a, workspace, base.ID, document.ID, knowledge.ReplaceSource{Content: text, SHA256: hash, ExpectedRevision: document.Revision, Reason: reason.Reason}, uuid.NewString(), uuid.NewString())
					return e
				},
				"retry": func() error {
					_, e := s.Retry(ctx, a, workspace, base.ID, document.ID, reason, uuid.NewString(), uuid.NewString())
					return e
				},
				"delete": func() error {
					_, e := s.Delete(ctx, a, workspace, base.ID, document.ID, reason, uuid.NewString())
					return e
				},
				"rebuild": func() error {
					_, e := s.Rebuild(ctx, a, workspace, base.ID, knowledge.RevisionReason{ExpectedRevision: base.Revision, Reason: reason.Reason}, uuid.NewString(), uuid.NewString())
					return e
				},
				"search": func() error {
					_, e := s.Search(ctx, a, workspace, base.ID, knowledge.SearchRequest{Query: "Synthetic query", TopK: 1, MinimumScore: 0.2}, uuid.NewString())
					return e
				},
				"answer": func() error {
					_, e := s.Answer(ctx, a, workspace, base.ID, knowledge.AnswerRequest{Question: "Synthetic question", AliasID: uuid.NewString(), TopK: 1, MinimumScore: 0.2}, uuid.NewString())
					return e
				},
			}
			for name, check := range checks {
				t.Run(name, func(t *testing.T) { requireCode(t, check(), "AUTH_SESSION_EXPIRED") })
			}
			var attempts, bases, documents, limits, keys int
			if e = s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM knowledge_attempts),(SELECT count(*) FROM knowledge_bases),(SELECT count(*) FROM knowledge_documents),(SELECT count(*) FROM rate_limit_buckets),(SELECT count(*) FROM api_keys)`).Scan(&attempts, &bases, &documents, &limits, &keys); e != nil || attempts != 1 || bases != 1 || documents != 1 || limits != admittedBefore || keys != 0 {
				t.Fatal("revoked source changed knowledge/admission/key state", e, attempts, bases, documents, limits, keys)
			}
			local, e := f.s.Identity.Authenticate(ctx, f.local, "admin")
			if e != nil {
				t.Fatal("local emergency identity", e)
			}
			if _, e = s.GetDocument(ctx, local, workspace, base.ID, document.ID); e != nil {
				t.Fatal("local recovery lost admitted data", e)
			}
		})
	}
}
