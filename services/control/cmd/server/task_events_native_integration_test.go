//go:build repository_integration && task_events_native_integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/taskdeliveries"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/taskbusfixture"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

func nativeTaskRoot(t *testing.T) *serverCompositionStory {
	x, _ := nativeTaskRootWithBroker(t, nil)
	return x
}

func nativeTaskRootWithBroker(t *testing.T, adjust func(*taskbus.Configuration)) (*serverCompositionStory, *taskbusfixture.Native) {
	t.Helper()
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "postgresql" {
		t.Fatal("EXPLICIT_NATIVE_ROOT_POSTGRESQL_REQUIRED")
	}
	root := os.Getenv("PROJECT_ONE_REPOSITORY_ROOT")
	canonical, e := filepath.EvalSymlinks(root)
	if e != nil || canonical != root || !filepath.IsAbs(root) {
		t.Fatal("EXPLICIT_CANONICAL_ROOT_TEST_SOURCE_REQUIRED")
	}
	raw, e := os.ReadFile(filepath.Join(root, "services/control/config/examples/task-events/control.example.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	base, e := taskbus.DecodeYAMLConfiguration(raw)
	if e != nil {
		t.Fatal(e)
	}
	if adjust != nil {
		adjust(&base)
	}
	x := serverCompositionSetup(t, false)
	fixture := taskbusfixture.NewForTenant(t, base, x.f.Tenant)
	x.c.TaskEvents = fixture.Profiles[0].Configuration
	x.owner.Config, x.mail.Config = x.c, x.c
	a := x.session(t)
	state, e := x.h.Licensing.Service.StatusAdmin(x.ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Explicit synthetic task-event root fixture", DeploymentID: state.DeploymentID,
		IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), GraceUntil: now.Add(2 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
	raw, e = licensewire.Issue(claims, x.key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = x.h.Licensing.Service.Import(x.ctx, a, licensing.ImportInput{Artifact: string(raw), Reason: "Actual signed synthetic root acceptance"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	return x, fixture
}

func nativeTaskNode(t *testing.T, x *serverCompositionStory) wire.Enrolled {
	return nativeTaskNamedNode(t, x, "Synthetic native task root")
}

func nativeTaskNamedNode(t *testing.T, x *serverCompositionStory, name string) wire.Enrolled {
	t.Helper()
	join, e := x.h.Nodes.Service.MintForProfile(x.ctx, x.session(t), x.f.Workspace, name, wire.ReadOnlyProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-native-task-root", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
	n, e := x.h.Nodes.Service.Enroll(x.ctx, wire.Enrollment{JoinToken: join.Token, Handshake: wire.Handshake{ProtocolVersion: wire.Version, ExecutionProfile: wire.ReadOnlyProfile, Capabilities: wire.Capabilities(wire.ReadOnlyProfile), Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return n
}

func nativeTaskOwner(t *testing.T, x *serverCompositionStory) *taskdeliveries.Service {
	t.Helper()
	s, e := openTaskDeliveries(x.ctx, x.c, x.d.store, x.h)
	if e != nil {
		t.Fatal("actual root task admission", e)
	}
	x.h.TaskDeliveries = s
	if s.Identity != x.owner || s.Nodes != x.h.Nodes.Service || s.Workspaces != x.h.Workspaces.Service || s.Nodes.TaskEvents != s {
		t.Fatal("root owner/recorder identity mismatch")
	}
	if e := s.VerifyStored(x.ctx); e != nil {
		t.Fatal(e)
	}
	return s
}

func nativeRootTask(t *testing.T, x *serverCompositionStory, n wire.Enrolled) nodes.Task {
	t.Helper()
	task, e := x.h.Nodes.Service.CreateTask(x.ctx, x.session(t), n.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("actual root recorder task", e)
	}
	return task
}

func nativeRootReceipt(t *testing.T, x *serverCompositionStory, task string) string {
	t.Helper()
	observer, e := taskbus.NewJetStream(x.ctx, x.c.TaskEvents, x.f.Tenant)
	if e != nil {
		t.Fatal("actual private broker receipt observer", e)
	}
	defer func() {
		if e := observer.Close(); e != nil {
			t.Error("receipt observer join", e)
		}
	}()
	deadline := time.NewTimer(config.Seconds(5 * x.c.TaskEvents.RequestSeconds))
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT count(*) FROM task_event_receipts r JOIN task_event_outbox o ON o.id=r.event_id WHERE o.task_id=$1", "", task)).Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count == 1 {
			stats, e := observer.Inspect(x.ctx)
			if e != nil {
				t.Fatal("actual broker ACK readback", e)
			}
			if stats.AckPending == 0 && stats.Pending == 0 {
				var id string
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT id::text FROM task_event_outbox WHERE task_id=$1 AND state='received'", "", task)).Scan(&id); e != nil {
					t.Fatal(e)
				}
				return id
			}
		}
		if count > 1 {
			t.Fatal("duplicate logical root receipt", count)
		}
		select {
		case <-deadline.C:
			t.Fatal("actual root worker receipt timeout")
		case <-x.ctx.Done():
			t.Fatal(x.ctx.Err())
		case <-ticker.C:
		}
	}
}

func nativeRootCookie(t *testing.T, x *serverCompositionStory, audience string) []*http.Cookie {
	t.Helper()
	var issued identity.IssuedSession
	e := x.d.store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = x.owner.IssueSessionWithin(ctx, q, x.f.User, 1, audience, "", uuid.NewString(), nil)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return []*http.Cookie{{Name: "__Host-" + audience + "_session", Value: issued.Token, Secure: true, HttpOnly: true, Path: "/"}}
}

func nativeRootHTTPTask(t *testing.T, x *serverCompositionStory, node string, cookies []*http.Cookie) string {
	t.Helper()
	status, csrf, contextCookies := x.request(t, "admin", http.MethodGet, "/api/v1/auth/csrf", nil, cookies, "")
	if status != 200 {
		t.Fatal("actual root CSRF", status, csrf)
	}
	csrf = nativeRootHTTPData(t, csrf)
	token, ok := csrf["token"].(string)
	if !ok || token == "" {
		t.Fatal("actual CSRF token missing")
	}
	cookies = append(cookies, contextCookies...)
	r, e := http.NewRequestWithContext(x.ctx, http.MethodPost, x.origin(t, "admin")+"/api/v1/admin/nodes/"+node+"/tasks", bytes.NewBufferString(`{"operation":"collect_facts.v1"}`))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Origin", x.origin(t, "admin"))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", uuid.NewString())
	r.Header.Set("X-CSRF-Token", token)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	response, e := x.client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var value map[string]any
	if e != nil || len(raw) == 1<<20 || json.Unmarshal(raw, &value) != nil || response.StatusCode != 202 {
		t.Fatal("actual authenticated root task POST", response.StatusCode, string(raw), e)
	}
	value = nativeRootHTTPData(t, value)
	id, ok := value["id"].(string)
	if !ok || !taskbus.ValidID(id) {
		t.Fatal("actual task identifier missing")
	}
	return id
}

func nativeRootHTTPData(t *testing.T, envelope map[string]any) map[string]any {
	t.Helper()
	data, ok := envelope["data"].(map[string]any)
	if !ok || len(envelope) != 1 {
		t.Fatal("actual root success envelope mismatch")
	}
	return data
}

func TestActualNativeTaskEventRoot(t *testing.T) {
	for _, leaf := range []string{"admitted_recorder_worker_reopen", "authenticated_https_delivery_reads", "stopped_restore_atomic_fence"} {
		t.Run(leaf, func(t *testing.T) {
			x := nativeTaskRoot(t)
			n := nativeTaskNode(t, x)
			if leaf == "authenticated_https_delivery_reads" {
				stop := x.serve(t)
				admin, portal := nativeRootCookie(t, x, "admin"), nativeRootCookie(t, x, "portal")
				task := nativeRootHTTPTask(t, x, n.NodeID, admin)
				event := nativeRootReceipt(t, x, task)
				portalPath := "/api/v1/workspaces/" + x.f.Workspace + "/nodes/" + n.NodeID + "/task-deliveries/" + event
				adminPath := "/api/v1/admin/nodes/" + n.NodeID + "/task-deliveries/" + event + "?workspace_id=" + x.f.Workspace
				if status, _, _ := x.request(t, "admin", http.MethodGet, adminPath, nil, admin, ""); status != 200 {
					t.Fatal("current platform administrator required unrelated membership", status)
				}
				if status, _, _ := x.request(t, "portal", http.MethodGet, portalPath, nil, portal, ""); status != 404 {
					t.Fatal("nonmember Portal read admitted", status)
				}
				x.exec(t, "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')", "", x.f.Tenant, x.f.Workspace, x.f.User)
				for _, audience := range []string{"admin", "portal"} {
					path, cookies := "/api/v1/admin/nodes/"+n.NodeID+"/task-deliveries/"+event+"?workspace_id="+x.f.Workspace, admin
					if audience == "portal" {
						path, cookies = "/api/v1/workspaces/"+x.f.Workspace+"/nodes/"+n.NodeID+"/task-deliveries/"+event, portal
					}
					status, out, _ := x.request(t, audience, http.MethodGet, path, nil, cookies, "")
					if status != 200 {
						t.Fatal("real authorized delivery status", audience, status, out)
					}
					out = nativeRootHTTPData(t, out)
					if out["task_id"] != task || out["state"] != "received" {
						t.Fatal("real authorized task delivery read", status, out)
					}
				}
				if status, _, _ := x.request(t, "portal", http.MethodGet, "/api/v1/admin/nodes/"+n.NodeID+"/task-deliveries?workspace_id="+x.f.Workspace, nil, portal, ""); status != 404 {
					t.Fatal("portal reached admin task route", status)
				}
				if status, _, _ := x.request(t, "portal", http.MethodGet, "/api/v1/workspaces/"+uuid.NewString()+"/nodes/"+n.NodeID+"/task-deliveries", nil, portal, ""); status != 404 {
					t.Fatal("foreign Workspace delivery leaked", status)
				}
				x.exec(t, "DELETE FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", "", x.f.Tenant, x.f.Workspace, x.f.User)
				if status, _, _ := x.request(t, "portal", http.MethodGet, portalPath, nil, portal, ""); status != 404 {
					t.Fatal("revoked Portal membership remained readable", status)
				}
				if status, out, _ := x.request(t, "admin", http.MethodGet, "/health/ready", nil, nil, ""); status != 200 {
					t.Fatal("actual admitted readiness", status, out)
				}
				status, out, _ := x.request(t, "portal", http.MethodGet, "/runtime-config.json", nil, nil, "")
				if status != 200 {
					t.Fatal("actual public policy status", status)
				}
				out = nativeRootHTTPData(t, out)
				raw, _ := json.Marshal(out)
				if status != 200 || strings.Contains(string(raw), x.c.TaskEvents.NamespaceID) || strings.Contains(string(raw), x.c.TaskEvents.Endpoint) || out["task_events"].(map[string]any)["enabled"] != true {
					t.Fatal("private or missing public policy", status)
				}
				stop()
				connection, e := net.DialTimeout("tcp", x.c.Server.ListenAddress, config.Seconds(x.c.TaskEvents.ConnectSeconds))
				if e == nil {
					connection.Close()
					t.Fatal("actual HTTPS listener remained open after joined stop")
				}
				_, port, e := net.SplitHostPort(x.c.Server.ListenAddress)
				if e != nil {
					t.Fatal(e)
				}
				p, e := strconv.Atoi(port)
				if e != nil {
					t.Fatal(e)
				}
				proof, e := json.Marshal(map[string]any{"port": p, "pid": os.Getpid(), "joined_stop": true})
				if e != nil {
					t.Fatal(e)
				}
				t.Log("TASK_EVENTS_ROOT_HTTPS_CLOSED " + string(proof))
				return
			}
			s := nativeTaskOwner(t, x)
			current := s
			t.Cleanup(func() {
				if current != nil {
					if e := closeTaskDeliveries(context.Background(), current); e != nil {
						t.Error(e)
					}
				}
			})
			task := nativeRootTask(t, x, n)
			if leaf == "admitted_recorder_worker_reopen" {
				life := newRuntimeLifecycle(x.ctx)
				life.stops = append(life.stops, func(ctx context.Context) error { return closeTaskDeliveries(ctx, s) })
				life.Go("TASK_DELIVERY_WORKER", s.Run)
				nativeRootReceipt(t, x, task.ID)
				stop, cancel := context.WithTimeout(context.Background(), config.Seconds(x.c.TaskEvents.ShutdownSeconds))
				defer cancel()
				if e := life.Close(stop); e != nil {
					t.Fatal("root worker did not join cleanly", e)
				}
				current = nil
				x.h.TaskDeliveries = nil
				x.h.Nodes.Service.TaskEvents = nil
				current = nativeTaskOwner(t, x)
				if worked, e := current.ConsumeNext(x.ctx); e != nil || worked {
					t.Fatal("root reopen repeated receipt", worked, e)
				}
				return
			}
			if worked, e := s.ProcessNext(x.ctx); e != nil || !worked {
				t.Fatal("actual root publication", worked, e)
			}
			if e := closeTaskDeliveries(context.Background(), s); e != nil {
				t.Fatal(e)
			}
			current = nil
			var original string
			if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema() AND p.proname='task_event_outbox_guard'", "")).Scan(&original); e != nil {
				t.Fatal(e)
			}
			beforeCookies := nativeRootCookie(t, x, "admin")
			x.exec(t, "CREATE OR REPLACE FUNCTION task_event_outbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END;$$", "")
			if e := recovery.InvalidateRestored(x.ctx, x.owner, "Explicit synthetic root restore rollback"); e == nil {
				t.Fatal("substituted task-event guard accepted during restore")
			}
			var state string
			var credential bool
			if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT status,credential_hash IS NOT NULL FROM managed_nodes WHERE id=$1", "", n.NodeID)).Scan(&state, &credential); e != nil || state != "active" || !credential {
				t.Fatal("failed task fence committed physical authority invalidation", e, state)
			}
			if _, e := x.owner.Authenticate(x.ctx, beforeCookies[0].Value, "admin"); e != nil {
				t.Fatal("restore rollback invalidated current user", e)
			}
			x.exec(t, original, "")
			if e := recovery.InvalidateRestored(x.ctx, x.owner, "Explicit synthetic root task restore"); e != nil {
				t.Fatal("actual same-transaction task fence", e)
			}
			var unknown bool
			if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT state,outcome_unknown FROM task_event_outbox WHERE task_id=$1", "", task.ID)).Scan(&state, &unknown); e != nil || state != "dead" || !unknown {
				t.Fatal("restored publication not fenced", e, state, unknown)
			}
			if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT status,credential_hash IS NOT NULL FROM managed_nodes WHERE id=$1", "", n.NodeID)).Scan(&state, &credential); e != nil || state != "removed" || credential {
				t.Fatal("physical credential owner not fenced", e, state)
			}
			if _, e := x.owner.Authenticate(x.ctx, beforeCookies[0].Value, "admin"); e == nil {
				t.Fatal("restored pre-fence session remained valid")
			}
		})
	}
}
