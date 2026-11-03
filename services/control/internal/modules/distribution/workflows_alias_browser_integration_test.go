//go:build workflow_browser_integration

package distribution_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

// Actual production React -> session/CSRF/TLS -> Go -> PostgreSQL regression.
// The inherited synthetic machine observation only establishes a live alias;
// no inference result or physical target qualification is manufactured here.
func TestM13WorkflowProfileAdminAliasDisableBrowser(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) {
			ctx := t.Context()
			c := setup(t)
			if !c.s.Identity.Config.WorkflowsRuntime() {
				t.Fatal("explicit cumulative workflows profile required")
			}
			a := c.access(t)
			root := t.TempDir()
			certificate, caFile, _, _ := smtpsink.Certificate(t, root)
			server := httptest.NewUnstartedServer(nil)
			defer server.Close()
			server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
			accounts := c.s.Identity
			accounts.Config.Server.AdminOrigin = "https://" + server.Listener.Addr().String()
			_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
			accounts.Config.Server.PortalOrigin = "https://localhost:" + port
			accounts.Config.Server.PortalAssets = filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "apps/web/apps/portal/dist")
			accounts.Config.Server.AdminAssets = filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "apps/web/apps/admin/dist")
			scratch := filepath.Join(root, "encoder-scratch")
			if e := os.Mkdir(scratch, 0700); e != nil {
				t.Fatal(e)
			}
			accounts.Config.Knowledge = config.KnowledgeConfiguration{Enabled: true, PythonExecutable: os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON"), WorkerFile: filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "runtimes/knowledge/embed.py"), ModelDirectory: os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY"), ScratchRoot: scratch, Gateways: []config.KnowledgeGateway{{Origin: a.alias.ClientOrigin, DialAddresses: []string{"127.0.0.1"}, CAFile: caFile}}}
			accounts.Config.Workflows.Enabled = true
			handler := identityhttp.New(accounts)
			if e := handler.Knowledge.Service.OpenConfiguredProcessor(ctx); e != nil {
				t.Fatal(e)
			}
			defer handler.Knowledge.Service.Processor.Close()
			service := handler.Workflows.Service
			if e := service.Start(ctx); e != nil {
				t.Fatal(e)
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				defer cancel()
				if e := service.Stop(cleanup); e != nil {
					t.Error(e)
				}
			}()
			workspace := c.a.Me.Workspace.ID
			base, e := handler.Knowledge.Service.CreateBase(ctx, c.a, knowledge.CreateBase{WorkspaceID: workspace, Name: "Alias disable source", Reason: "Synthetic focused browser source metadata"}, uuid.NewString(), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			draft, e := service.Create(ctx, c.a, workflows.CreateRequest{WorkspaceID: workspace, Name: "Alias disable workflow " + locale, Reason: "Actual workflow profile browser regression"}, uuid.NewString(), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			var graph workflows.Graph
			if e = json.Unmarshal(draft.DraftGraph, &graph); e != nil {
				t.Fatal(e)
			}
			for i := range graph.Nodes {
				switch graph.Nodes[i].Kind {
				case workflows.RetrievalNode:
					graph.Nodes[i].Configuration, _ = json.Marshal(workflows.RetrievalConfiguration{BaseID: base.ID, TopK: 1, MinimumScore: 0.1})
				case workflows.AnswerNode:
					graph.Nodes[i].Configuration, _ = json.Marshal(workflows.AnswerConfiguration{AliasID: a.alias.ID, Instructions: "Answer using current sources."})
				}
			}
			draft, e = service.Update(ctx, c.a, workspace, draft.ID, workflows.UpdateRequest{Name: draft.Name, Graph: graph, Enabled: true, ExpectedRevision: draft.Revision, Reason: "Bind current immutable alias ID"}, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			validation, e := service.Validate(ctx, c.a, workspace, draft.ID, workflows.RevisionReason{ExpectedRevision: draft.Revision, Reason: "Alias available before browser disable"})
			if e != nil || !validation.Valid {
				t.Fatal("initial live workflow reference", e)
			}
			server.Config.Handler = handler
			server.StartTLS()
			leaf, e := x509.ParseCertificate(certificate.Certificate[0])
			if e != nil {
				t.Fatal(e)
			}
			pin := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
			output := filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "alias-disable-"+locale)
			if e = os.Mkdir(output, 0700); e != nil {
				t.Fatal(e)
			}
			payload, _ := json.Marshal(map[string]any{"admin": accounts.Config.Server.AdminOrigin, "admin_email": "admin@example.invalid", "admin_password": c.password, "browser_locale": locale, "application_profile": *accounts.Config.Server.ApplicationProfile, "workspace_id": workspace, "alias_id": a.alias.ID, "alias_revision": a.alias.Revision, "workflow_id": draft.ID, "workflow_name": draft.Name, "spki": base64.StdEncoding.EncodeToString(pin[:]), "output": output})
			file := filepath.Join(root, "browser.json")
			if e = os.WriteFile(file, payload, 0600); e != nil {
				t.Fatal(e)
			}
			operation, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()
			command := exec.CommandContext(operation, "node", filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "tests/acceptance/workflows/alias-disable.mjs"), file)
			command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+caFile)
			command.Stdout, command.Stderr = os.Stdout, os.Stderr
			if e = command.Run(); e != nil {
				t.Fatal("actual workflow-profile Admin alias browser", e)
			}
			var disabled bool
			var revision int64
			var audit, runs int
			if e = c.s.DB.QueryRow(ctx, "SELECT disabled_at IS NOT NULL,revision FROM model_aliases WHERE id=$1", a.alias.ID).Scan(&disabled, &revision); e != nil || !disabled || revision != a.alias.Revision+1 {
				t.Fatal("durable same-ID alias disable", e)
			}
			if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='model.alias_disabled' AND reason=$2 AND audience='admin'", a.alias.ID, "Actual Admin browser alias disable "+locale).Scan(&audit); e != nil || audit != 1 {
				t.Fatal("required reason and audited Admin disable", e, audit)
			}
			if _, e = service.Debug(ctx, c.a, workspace, draft.ID, workflows.DebugRequest{ExpectedRevision: draft.Revision, Question: "Must not execute a disabled alias", Reason: "Current alias authority refuses new run"}, uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "MODEL_ALIAS_UNAVAILABLE" {
				t.Fatal("current alias authority did not refuse run", e)
			}
			if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM workflow_runs WHERE workflow_id=$1", draft.ID).Scan(&runs); e != nil || runs != 0 {
				t.Fatal("refused browser/service run created durable work", e, runs)
			}
		})
	}
}
