//go:build repository_integration && task_events_native_integration && task_events_integration && task_events_wire_integration

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
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

func nativeHTTPPolicy(c *taskbus.Configuration) {
	c.ConnectSeconds = 1
	c.RequestSeconds = 2
	c.PullSeconds = 1
	c.AckWaitSeconds = 8
	c.WorkerPollSeconds = 1
	c.RetrySeconds = 1
	c.ReconnectSeconds = 1
	c.MaximumReconnectAttempts = 3
	c.MaximumPublishAttempts = 1
	c.MaximumDeliveries = 3
	c.MaximumAckPending = 2
	c.StreamMaximumMessages = 100
	c.StreamMaximumBytes = 1 << 20
	c.StreamRetentionSeconds = 60
	c.DuplicateWindowSeconds = 2
	c.RetryLifetimeSeconds = 60
	c.HistoryRetentionSeconds = 128
	c.CleanupBatch = 8
	c.ShutdownSeconds = 4
}

func nativeHTTPPost(t *testing.T, x *serverCompositionStory, audience, path string, body []byte, cookies []*http.Cookie, headers map[string]string) (int, map[string]any) {
	t.Helper()
	r, e := http.NewRequestWithContext(x.ctx, http.MethodPost, x.origin(t, audience)+path, bytes.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Origin", x.origin(t, audience))
	r.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	response, e := x.client.Do(r)
	if e != nil {
		t.Fatal("actual verified HTTPS retry", e)
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var out map[string]any
	if e != nil || len(raw) == 1<<20 || json.Unmarshal(raw, &out) != nil {
		t.Fatal("bounded actual retry JSON", e)
	}
	return response.StatusCode, out
}
func nativeHTTPCSRF(t *testing.T, x *serverCompositionStory, audience string, cookies []*http.Cookie) (string, []*http.Cookie) {
	t.Helper()
	status, out, extra := x.request(t, audience, http.MethodGet, "/api/v1/auth/csrf", nil, cookies, "")
	if status != 200 {
		t.Fatal("actual CSRF status", status)
	}
	value, ok := nativeRootHTTPData(t, out)["token"].(string)
	if !ok || value == "" {
		t.Fatal("actual CSRF missing")
	}
	return value, append(cookies, extra...)
}
func nativeHTTPJoined(t *testing.T, x *serverCompositionStory, stop func()) {
	t.Helper()
	stop()
	connection, e := net.DialTimeout("tcp", x.c.Server.ListenAddress, config.Seconds(x.c.TaskEvents.ConnectSeconds))
	if e == nil {
		connection.Close()
		t.Fatal("HTTPS listener remains after joined stop")
	}
	_, port, e := net.SplitHostPort(x.c.Server.ListenAddress)
	if e != nil {
		t.Fatal(e)
	}
	p, e := strconv.Atoi(port)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(map[string]any{"port": p, "pid": os.Getpid(), "joined_stop": true})
	if e != nil {
		t.Fatal(e)
	}
	t.Log("TASK_EVENTS_ROOT_HTTPS_CLOSED " + string(raw))
}
func nativeHTTPCount(t *testing.T, x *serverCompositionStory, statement string, args ...any) int {
	t.Helper()
	var n int
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(statement, "", args...)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func nativeHTTPProblem(t *testing.T, status int, out map[string]any, want int, code string) {
	t.Helper()
	if status != want || out["code"] != code {
		t.Fatal("actual public denial", status, out, want, code)
	}
	id, ok := out["request_id"].(string)
	if !ok || !taskbus.ValidID(id) {
		t.Fatal("server request identity missing")
	}
}
func nativeHTTPWait(t *testing.T, x *serverCompositionStory, delay time.Duration) {
	t.Helper()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-x.ctx.Done():
		t.Fatal(x.ctx.Err())
	}
}
func nativeHTTPDeadTask(t *testing.T, x *serverCompositionStory, node string) string {
	t.Helper()
	original := x.c.TaskEvents
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	state, e := filepath.EvalSymlinks(os.Getenv("PROJECT_ONE_STATE_ROOT"))
	if e != nil || !strings.HasPrefix(root, state+string(os.PathSeparator)) {
		t.Fatal("owned external wire scope required")
	}
	proxy, selected, e := taskbus.NewWireFixture(x.ctx, original, root)
	if e != nil {
		t.Fatal(e)
	}
	closed := false
	closeProxy := func() {
		if closed {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), config.Seconds(original.ShutdownSeconds))
		defer cancel()
		if e := proxy.Close(ctx); e != nil {
			t.Error(e)
			return
		}
		closed = true
		connection, e := net.DialTimeout("tcp", strings.TrimPrefix(selected.Endpoint, "tls://"), config.Seconds(original.ConnectSeconds))
		if e == nil {
			connection.Close()
			t.Error("wire listener remains")
			return
		}
		raw, e := json.Marshal(map[string]any{"endpoint": selected.Endpoint, "observation": proxy.Observation(), "closed": true})
		if e != nil {
			t.Error(e)
			return
		}
		t.Log("TASK_EVENTS_ROOT_RETRY_WIRE " + string(raw))
	}
	t.Cleanup(closeProxy)
	x.c.TaskEvents = selected
	x.owner.Config = x.c
	x.mail.Config = x.c
	owner := nativeTaskOwner(t, x)
	current := owner
	t.Cleanup(func() {
		if current != nil {
			if e := closeTaskDeliveries(context.Background(), current); e != nil {
				t.Error(e)
			}
		}
	})
	task, e := x.h.Nodes.Service.CreateTask(x.ctx, x.session(t), node, "collect_facts.v1", uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e := proxy.Arm("publish_send_cut"); e != nil {
		t.Fatal(e)
	}
	if worked, e := owner.ProcessNext(x.ctx); e == nil || worked {
		t.Fatal("real publish cut falsely succeeded", worked, e)
	}
	var id, stateValue string
	var unknown bool
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT id::text,state,outcome_unknown FROM task_event_outbox WHERE task_id=$1", "", task.ID)).Scan(&id, &stateValue, &unknown); e != nil || stateValue != "dead" || !unknown {
		t.Fatal("actual dead intent missing", e, stateValue, unknown)
	}
	observed := proxy.Observation()
	if observed.Invalid || observed.Dropped != 1 || observed.PublishAttempts != 1 || observed.PublishForwarded != 0 {
		t.Fatal("real wire cut counts", observed)
	}
	if nativeHTTPCount(t, x, "SELECT count(*) FROM task_event_receipts WHERE event_id=$1", id) != 0 {
		t.Fatal("cut publish created receipt")
	}
	if e := closeTaskDeliveries(context.Background(), owner); e != nil {
		t.Fatal(e)
	}
	current = nil
	closeProxy()
	x.c.TaskEvents = original
	x.owner.Config = x.c
	x.mail.Config = x.c
	return id
}

func TestActualNativeTaskEventHTTP(t *testing.T) {
	for _, leaf := range []string{"authenticated_retry_security_and_replay", "broker_unavailable_readiness_reopen"} {
		t.Run(leaf, func(t *testing.T) {
			x, broker := nativeTaskRootWithBroker(t, nativeHTTPPolicy)
			node := nativeTaskNode(t, x)
			event := ""
			if leaf == "authenticated_retry_security_and_replay" {
				event = nativeHTTPDeadTask(t, x, node.NodeID)
			}
			stop := x.serve(t)
			joined := false
			joinedStop := func() {
				if joined {
					return
				}
				nativeHTTPJoined(t, x, stop)
				joined = true
			}
			t.Cleanup(joinedStop)
			admin := nativeRootCookie(t, x, "admin")
			if leaf == "broker_unavailable_readiness_reopen" {
				task := nativeRootHTTPTask(t, x, node.NodeID, admin)
				nativeRootReceipt(t, x, task)
				if status, _, _ := x.request(t, "admin", http.MethodGet, "/health/ready", nil, nil, ""); status != 200 {
					t.Fatal("initial actual readiness", status)
				}
				broker.RestartWithInterruption(func() {
					if status, _, _ := x.request(t, "admin", http.MethodGet, "/health/ready", nil, nil, ""); status != 503 {
						t.Fatal("stopped actual broker remained ready", status)
					}
				})
				deadline := time.NewTimer(config.Seconds(10 * x.c.TaskEvents.RequestSeconds))
				defer deadline.Stop()
				ticker := time.NewTicker(50 * time.Millisecond)
				defer ticker.Stop()
				for {
					status, _, _ := x.request(t, "admin", http.MethodGet, "/health/ready", nil, nil, "")
					if status == 200 {
						break
					}
					select {
					case <-deadline.C:
						t.Fatal("actual bounded broker reconnect failed", status)
					case <-ticker.C:
					case <-x.ctx.Done():
						t.Fatal(x.ctx.Err())
					}
				}
				second := nativeTaskNamedNode(t, x, "Second synthetic broker recovery node")
				task = nativeRootHTTPTask(t, x, second.NodeID, admin)
				nativeRootReceipt(t, x, task)
				if nativeHTTPCount(t, x, "SELECT count(*) FROM task_event_receipts") != 2 {
					t.Fatal("broker restart repeated effect")
				}
				joinedStop()
				return
			}
			token, admin := nativeHTTPCSRF(t, x, "admin", admin)
			portalToken, portal := nativeHTTPCSRF(t, x, "portal", nativeRootCookie(t, x, "portal"))
			path := "/api/v1/admin/nodes/" + node.NodeID + "/task-deliveries/" + event + "/retry?workspace_id=" + x.f.Workspace
			body := []byte("{\"generation\":1,\"reason\":\"Actual HTTPS recovery 😀 \u4e2d\u6587\"}")
			key := uuid.NewString()
			headers := map[string]string{"X-CSRF-Token": token, "Idempotency-Key": key}
			status, out := nativeHTTPPost(t, x, "admin", path, body, admin, map[string]string{"Idempotency-Key": key})
			nativeHTTPProblem(t, status, out, 403, "CSRF_INVALID")
			status, out = nativeHTTPPost(t, x, "admin", path, body, admin, map[string]string{"X-CSRF-Token": "invalid", "Idempotency-Key": key})
			nativeHTTPProblem(t, status, out, 403, "CSRF_INVALID")
			status, out = nativeHTTPPost(t, x, "admin", path, body, admin, map[string]string{"X-CSRF-Token": token, "Idempotency-Key": key, "Origin": "https://foreign.example.invalid"})
			nativeHTTPProblem(t, status, out, 403, "ORIGIN_REJECTED")
			status, out, _ = x.request(t, "admin", http.MethodGet, "/api/v1/admin/nodes/"+node.NodeID+"/task-deliveries?workspace_id="+x.f.Workspace, nil, nil, "")
			nativeHTTPProblem(t, status, out, 401, "AUTH_REQUIRED")
			status, out = nativeHTTPPost(t, x, "portal", path, body, portal, map[string]string{"X-CSRF-Token": portalToken, "Idempotency-Key": key})
			nativeHTTPProblem(t, status, out, 404, "RESOURCE_NOT_FOUND")
			status, out = nativeHTTPPost(t, x, "admin", path, body, admin, map[string]string{"X-CSRF-Token": token})
			nativeHTTPProblem(t, status, out, 400, "VALIDATION_FAILED")
			status, out = nativeHTTPPost(t, x, "admin", path, []byte(`{"generation":0,"reason":"Invalid generation"}`), admin, headers)
			nativeHTTPProblem(t, status, out, 400, "VALIDATION_FAILED")
			status, out = nativeHTTPPost(t, x, "admin", path, []byte(`{"generation":1,"reason":"Recovery","tenant_id":"foreign"}`), admin, headers)
			nativeHTTPProblem(t, status, out, 400, "VALIDATION_FAILED")
			status, out = nativeHTTPPost(t, x, "admin", path+"&workspace_id="+uuid.NewString(), body, admin, headers)
			nativeHTTPProblem(t, status, out, 400, "VALIDATION_FAILED")
			if nativeHTTPCount(t, x, "SELECT count(*) FROM task_event_outbox WHERE id=$1 AND generation=1 AND state='dead'", event) != 1 || nativeHTTPCount(t, x, "SELECT count(*) FROM audit_events WHERE action='task_event.retry_requested'") != 0 {
				t.Fatal("denied retry changed intent")
			}
			status, out = nativeHTTPPost(t, x, "admin", path, body, admin, headers)
			if status != 202 {
				t.Fatal("actual authorized retry failed", status, out)
			}
			first := nativeRootHTTPData(t, out)
			if first["event_id"] != event || first["generation"] != float64(2) {
				t.Fatal("actual retry receipt", first)
			}
			status, out = nativeHTTPPost(t, x, "admin", path, body, admin, headers)
			if status != 202 || !reflect.DeepEqual(nativeRootHTTPData(t, out), first) {
				t.Fatal("same retry key changed receipt", status, out)
			}
			status, out = nativeHTTPPost(t, x, "admin", path, []byte(`{"generation":1,"reason":"Changed reason"}`), admin, headers)
			nativeHTTPProblem(t, status, out, 409, "IDEMPOTENCY_CONFLICT")
			var task string
			if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT task_id::text FROM task_event_outbox WHERE id=$1", "", event)).Scan(&task); e != nil {
				t.Fatal(e)
			}
			nativeRootReceipt(t, x, task)
			if nativeHTTPCount(t, x, "SELECT count(*) FROM audit_events WHERE action='task_event.retry_requested' AND reason='Actual HTTPS recovery 😀 \u4e2d\u6587'") != 1 || nativeHTTPCount(t, x, "SELECT count(*) FROM node_tasks WHERE id=$1 AND state='pending'", task) != 1 {
				t.Fatal("retry repeated audit or executed physical task")
			}
			x.exec(t, "UPDATE platform_license_state SET observed_at=clock_timestamp()+interval '3 hours' WHERE id=1", "")
			status, out = nativeHTTPPost(t, x, "admin", path, body, admin, headers)
			nativeHTTPProblem(t, status, out, 403, "LICENSE_NEW_BUSINESS_UNAVAILABLE")
			if nativeHTTPCount(t, x, "SELECT count(*) FROM audit_events WHERE action='task_event.retry_requested'") != 1 || nativeHTTPCount(t, x, "SELECT count(*) FROM task_event_receipts WHERE event_id=$1", event) != 1 {
				t.Fatal("License denial changed business effects")
			}
			joinedStop()
		})
	}
}
