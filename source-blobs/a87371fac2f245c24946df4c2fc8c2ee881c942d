//go:build repository_integration && assistant_http_dispatch

package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/productagent"
	productagenthttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/productagent/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

func TestAuthenticatedGuardedAssistantHTTP(t *testing.T) {
	owner := newAssistantFixture(t)
	password := identityfixture.Bootstrap(t, owner)
	owner.Config.Workflows.Enabled = true
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	ddl, err := os.ReadFile("../testdata/execution-budget.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.DB.Exec(ctx, string(ddl)); err != nil {
		t.Fatal(err)
	}
	dispatcher := identityhttp.New(owner)
	var enabled atomic.Bool
	enabled.Store(true)
	var mode atomic.Int32
	start := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	policy := productagent.ExecutionBudgetPolicy{
		UserAccess: productagent.BudgetAccessEnabled, WindowStart: start, WindowEnd: start.Add(3 * time.Hour),
		Installation: productagent.BudgetCeiling{Executions: 100, Tokens: 100},
		User:         productagent.BudgetCeiling{Executions: 100, Tokens: 100},
		RunSteps:     productagent.ExecutionRunLimits{Installation: 8, User: 5},
	}
	source := func(ctx context.Context, _ transaction.Executor, _ productagent.BudgetSubject) (productagent.ExecutionBudgetPolicy, error) {
		p := policy
		switch mode.Load() {
		case 1:
			p.UserAccess = productagent.BudgetAccessDisabled
		case 2:
			return p, errors.New("fixture private policy diagnostic")
		case 3:
			p.RunSteps = productagent.ExecutionRunLimits{}
		case 4:
			p.User.Executions = 0
		}
		return p, ctx.Err()
	}
	bind := func(source productagent.ExecutionBudgetPolicySource) {
		t.Helper()
		adapter, err := productagenthttp.NewGuarded(dispatcher.Applications.Service, func(ctx context.Context) (bool, error) {
			return enabled.Load(), ctx.Err()
		}, source)
		if err != nil {
			t.Fatal(err)
		}
		dispatcher.ProductAssistant = adapter
	}
	bind(source)
	server := httpfixture.NewTLSServer(t, dispatcher)
	cookies, err := httpserver.NewCookiePolicy(owner.Config.Server.EffectiveTransportMode())
	if err != nil {
		t.Fatal(err)
	}
	for _, audience := range []string{"admin", "portal"} {
		t.Run(audience, func(t *testing.T) {
			_, token, _, err := owner.Login(ctx, "admin@example.invalid", password, audience, "", uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			origin := httpfixture.AdminOrigin
			if audience == "portal" {
				origin = httpfixture.PortalOrigin
			}
			client := newAssistantClient(t, server.Client(), audience, origin, token, cookies)
			response := client.call(t, http.MethodPost, commandPath, `{"command":"open home"}`, nil)
			if result := response.result(t); result.Path != "/" {
				t.Fatalf("guarded navigation: %+v", result)
			}
			var count int
			if err := owner.DB.QueryRow(ctx, `SELECT count(*) FROM product_agent_runs r
				JOIN product_agent_steps s ON s.run_id=r.id AND s.id=r.id
				JOIN product_agent_budget_reservations b ON b.id=s.id
				JOIN audit_events a ON a.request_id=s.id
				WHERE r.id=$1 AND r.admitted_steps=1 AND s.state='completed' AND b.state='settled'
				AND b.maximum_tokens=0 AND b.actual_tokens=0 AND a.audience=$2 AND a.outcome='success'`, response.id, audience).Scan(&count); err != nil || count != 1 {
				t.Fatalf("correlated durable command: %d, %v", count, err)
			}
			assertDenied := func(body string, status int, code string, change func(*http.Request)) {
				t.Helper()
				reply := client.call(t, http.MethodPost, commandPath, body, change)
				reply.problem(t, status, code)
				if strings.Contains(reply.raw, "fixture private policy diagnostic") {
					t.Fatal("policy diagnostic escaped its server boundary")
				}
				if err := owner.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_agent_runs WHERE id=$1)+(SELECT count(*) FROM audit_events WHERE request_id=$1)`, reply.id).Scan(&count); err != nil || count != 0 {
					t.Fatalf("refused request produced state or success: %d, %v", count, err)
				}
			}
			enabled.Store(false)
			assertDenied(`{"command":"open home"}`, 403, "ACCESS_DENIED", nil)
			enabled.Store(true)
			mode.Store(1)
			assertDenied(`{"command":"open home"}`, 403, "ACCESS_DENIED", nil)
			mode.Store(2)
			assertDenied(`{"command":"open home"}`, 503, "SERVICE_NOT_READY", nil)
			mode.Store(3)
			assertDenied(`{"command":"open home"}`, 429, "RATE_LIMITED", nil)
			mode.Store(4)
			assertDenied(`{"command":"open home"}`, 429, "RATE_LIMITED", nil)
			mode.Store(0)
			assertDenied(`{"command":"open home","run_id":"caller choice"}`, 400, "VALIDATION_FAILED", nil)
			assertDenied(`{"command":"disable the installation"}`, 400, "VALIDATION_FAILED", nil)
			assertDenied(`{"command":"open home"}`, 403, "CSRF_INVALID", func(request *http.Request) { request.Header.Del("X-CSRF-Token") })
			bind(nil)
			assertDenied(`{"command":"open home"}`, 503, "SERVICE_NOT_READY", nil)
			bind(source)
			if result := client.call(t, http.MethodPost, commandPath, `{"command":"open home"}`, nil).result(t); result.Path != "/" {
				t.Fatal("guarded request did not recover")
			}
			if audience == "portal" {
				if result := client.call(t, http.MethodPost, commandPath, `{"command":"show applications"}`, nil).result(t); result.Applications == nil {
					t.Fatal("guarded typed read did not run without a model")
				}
				actor, err := owner.Authenticate(ctx, token, audience)
				if err != nil || actor.Me.Workspace == nil {
					t.Fatalf("lookup authentication: %v", err)
				}
				const name = "HTTP Caf\u00e9 \u5e94\u7528  %_\"quoted\""
				app, err := dispatcher.Workflows.Service.Create(ctx, actor, workflows.CreateRequest{
					WorkspaceID: actor.Me.Workspace.ID, Name: name, Reason: "Seed guarded name lookup",
				}, uuid.NewString(), uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				version := uuid.NewString()
				if _, err := owner.DB.Exec(ctx, `INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by)
SELECT $1,tenant_id,workspace_id,id,1,revision,draft_graph,draft_sha256,created_by FROM workflows WHERE id=$2`, version, app.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := owner.DB.Exec(ctx, `UPDATE workflows SET current_version_id=$1 WHERE id=$2`, version, app.ID); err != nil {
					t.Fatal(err)
				}
				for _, prefix := range []string{"find application", "\u67e5\u627e\u5e94\u7528"} {
					argument, _ := json.Marshal(name)
					body, _ := json.Marshal(map[string]string{"command": prefix + " " + string(argument)})
					reply := client.call(t, http.MethodPost, commandPath, string(body), nil)
					result := reply.result(t)
					if result.ApplicationResolution == nil || result.ApplicationResolution.State != "resolved" || result.ApplicationResolution.ApplicationID != app.ID || result.Path != "" {
						t.Fatalf("guarded name lookup: %+v", result)
					}
					if err := owner.DB.QueryRow(ctx, `SELECT count(*) FROM product_agent_steps s JOIN audit_events a ON a.request_id=s.id
WHERE s.id=$1 AND s.state='completed' AND a.action='product_assistant.find_application'`, reply.id).Scan(&count); err != nil || count != 1 {
						t.Fatalf("guarded lookup checkpoint and audit: %d, %v", count, err)
					}
					enabled.Store(false)
					assertDenied(string(body), 403, "ACCESS_DENIED", nil)
					enabled.Store(true)
				}
				assertDenied(`{"command":"find application \"missing\"","cursor":"caller continuation"}`, 400, "VALIDATION_FAILED", nil)
			}
		})
	}
}
