//go:build workflow_database_integration && knowledge_encoder_integration

package sso

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/url"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"testing"
	"time"
)

// Connector/flow/binding rows are explicit fixtures. Ordinary SSO issuance and
// every current-source transaction guard are real. No model call is fabricated.
func TestM13ActualEveryWorkflowOperationRechecksRevokedSSOSource(t *testing.T) {
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
			accounts := f.s.Identity
			accounts.Config.Workflows.Enabled = true
			accounts.Config.Knowledge = config.KnowledgeConfiguration{Enabled: true, PythonExecutable: os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON"), WorkerFile: filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "runtimes/knowledge/embed.py"), ModelDirectory: os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY"), ScratchRoot: scratch}
			policy := accounts.Config.Policy()
			policy.HTTP.RequestSeconds = 90
			policy.HTTP.StartupSeconds = 60
			policy.HTTP.ShutdownSeconds = 30
			accounts.Config.Runtime = &policy
			w := &workspaces.Service{DB: accounts.DB, Identity: accounts}
			d := &distribution.Service{DB: accounts.DB, Identity: accounts, Workspaces: w}
			k := &knowledge.Service{DB: accounts.DB, Identity: accounts, Workspaces: w, Distribution: d}
			if e = k.OpenConfiguredProcessor(ctx); e != nil {
				t.Fatal(e)
			}
			defer k.Processor.Close()
			s := &workflows.Service{DB: accounts.DB, Identity: accounts, Workspaces: w, Knowledge: k, Distribution: d}
			if e = s.Start(ctx); e != nil {
				t.Fatal(e)
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if e := s.Stop(cleanup); e != nil {
					t.Error(e)
				}
			}()
			workspace := a.Me.Workspace.ID
			input := workflows.CreateRequest{WorkspaceID: workspace, Name: "Current SSO workflow scope", Reason: "Synthetic current source acceptance"}
			key := uuid.NewString()
			draft, e := s.Create(ctx, a, input, key, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			var graph workflows.Graph
			if json.Unmarshal(draft.DraftGraph, &graph) != nil {
				t.Fatal("draft")
			}
			id := f.source.ConnectorID
			query := `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1,updated_at=clock_timestamp() WHERE id=$1`
			if revoked == "binding" {
				id = f.source.BindingID
				query = `UPDATE sso_bindings SET active=false,revision=revision+1 WHERE id=$1`
			}
			if _, e = accounts.DB.Exec(ctx, query, id); e != nil {
				t.Fatal(e)
			}
			reason := workflows.RevisionReason{ExpectedRevision: draft.Revision, Reason: "Refuse revoked SSO source"}
			conversation, run := uuid.NewString(), uuid.NewString()
			checks := map[string]func() error{
				"configuration": func() error { _, e := s.Configuration(ctx, a, workspace); return e },
				"list":          func() error { _, e := s.List(ctx, a, workspace, url.Values{}); return e },
				"resolve":       func() error { _, e := s.ResolveWorkspace(ctx, a, draft.ID); return e },
				"get":           func() error { _, e := s.Get(ctx, a, workspace, draft.ID); return e },
				"create": func() error {
					_, e := s.Create(ctx, a, workflows.CreateRequest{WorkspaceID: workspace, Name: "Refused workflow", Reason: reason.Reason}, uuid.NewString(), uuid.NewString())
					return e
				},
				"create_replay": func() error { _, e := s.Create(ctx, a, input, key, uuid.NewString()); return e },
				"update": func() error {
					_, e := s.Update(ctx, a, workspace, draft.ID, workflows.UpdateRequest{Name: draft.Name, Graph: graph, Enabled: false, ExpectedRevision: draft.Revision, Reason: reason.Reason}, uuid.NewString())
					return e
				},
				"validate": func() error { _, e := s.Validate(ctx, a, workspace, draft.ID, reason); return e },
				"publish": func() error {
					_, e := s.Publish(ctx, a, workspace, draft.ID, reason, uuid.NewString(), uuid.NewString())
					return e
				},
				"versions": func() error { _, e := s.Versions(ctx, a, workspace, draft.ID, url.Values{}); return e },
				"debug": func() error {
					_, e := s.Debug(ctx, a, workspace, draft.ID, workflows.DebugRequest{Question: "Synthetic question", ExpectedRevision: draft.Revision, Reason: reason.Reason}, uuid.NewString(), uuid.NewString())
					return e
				},
				"conversation_create": func() error {
					_, e := s.CreateConversation(ctx, a, workspace, draft.ID, workflows.ConversationRequest{VersionID: uuid.NewString(), Reason: reason.Reason}, uuid.NewString(), uuid.NewString())
					return e
				},
				"conversation_list": func() error { _, e := s.Conversations(ctx, a, workspace, draft.ID, url.Values{}); return e },
				"conversation_get":  func() error { _, e := s.Conversation(ctx, a, workspace, draft.ID, conversation); return e },
				"conversation_delete": func() error {
					return s.DeleteConversation(ctx, a, workspace, draft.ID, conversation, reason, uuid.NewString())
				},
				"run": func() error {
					_, e := s.RunConversation(ctx, a, workspace, draft.ID, conversation, workflows.RunRequest{Question: "Synthetic question", Reason: reason.Reason}, uuid.NewString(), uuid.NewString())
					return e
				},
				"run_get":               func() error { _, e := s.GetRun(ctx, a, workspace, draft.ID, run); return e },
				"run_list":              func() error { _, e := s.RunPage(ctx, a, workspace, draft.ID, nil, url.Values{}); return e },
				"conversation_run_list": func() error { _, e := s.RunPage(ctx, a, workspace, draft.ID, &conversation, url.Values{}); return e },
				"cancel": func() error {
					_, e := s.CancelRun(ctx, a, workspace, draft.ID, run, reason.Reason, uuid.NewString())
					return e
				},
			}
			for name, check := range checks {
				t.Run(name, func(t *testing.T) { requireCode(t, check(), "AUTH_SESSION_EXPIRED") })
			}
			var workflowsCount, versionsCount, conversationsCount, runsCount, keysCount int
			if e = accounts.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM workflows),(SELECT count(*) FROM workflow_versions),(SELECT count(*) FROM workflow_conversations),(SELECT count(*) FROM workflow_runs),(SELECT count(*) FROM api_keys)`).Scan(&workflowsCount, &versionsCount, &conversationsCount, &runsCount, &keysCount); e != nil || workflowsCount != 1 || versionsCount != 0 || conversationsCount != 0 || runsCount != 0 || keysCount != 0 {
				t.Fatal("revoked source changed workflow/model authority", e)
			}
			local, e := accounts.Authenticate(ctx, f.local, "admin")
			if e != nil {
				t.Fatal("local emergency identity", e)
			}
			if _, e = s.Get(ctx, local, workspace, draft.ID); e != nil {
				t.Fatal("local metadata recovery", e)
			}
		})
	}
}
