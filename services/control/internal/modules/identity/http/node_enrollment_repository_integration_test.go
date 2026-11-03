//go:build repository_integration

package http_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

// This observer forwards the exact real statement and Scan. Only the enrollment
// token has three string destinations; rate admission has three different types.
type enrollmentHTTPArmStore struct {
	transaction.Store
	proxy    nodeHTTPWireFault
	mode     commitfault.Mode
	once     sync.Once
	armed    bool
	before   int32
	armError error
}
type enrollmentHTTPArmExecutor struct {
	transaction.Executor
	owner *enrollmentHTTPArmStore
}
type enrollmentHTTPArmRow struct {
	transaction.Row
	owner *enrollmentHTTPArmStore
}

func (s *enrollmentHTTPArmStore) Within(ctx context.Context, read bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, read, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, enrollmentHTTPArmExecutor{q, s})
	})
}
func (q enrollmentHTTPArmExecutor) QueryRow(ctx context.Context, sql transaction.Statement) transaction.Row {
	return enrollmentHTTPArmRow{q.Executor.QueryRow(ctx, sql), q.owner}
}
func (r enrollmentHTTPArmRow) Scan(dest ...any) error {
	e := r.Row.Scan(dest...)
	if e != nil || len(dest) != 3 {
		return e
	}
	for _, value := range dest {
		if _, ok := value.(*string); !ok {
			return nil
		}
	}
	r.owner.once.Do(func() {
		r.owner.before = r.owner.proxy.Commits()
		r.owner.armed = true
		if r.owner.mode != commitfault.Pass {
			r.owner.armError = r.owner.proxy.Arm(r.owner.mode)
		}
	})
	return r.owner.armError
}

type enrollmentHTTPResult struct {
	Kind      string         `json:"kind"`
	Outcome   string         `json:"outcome"`
	RequestID string         `json:"request_id,omitempty"`
	Enrolled  *wire.Enrolled `json:"enrolled,omitempty"`
}
type enrollmentHTTPLog struct {
	sync.Mutex
	requests []string
}

func (l *enrollmentHTTPLog) record(request string) {
	l.Lock()
	defer l.Unlock()
	l.requests = append(l.requests, request)
}
func (l *enrollmentHTTPLog) values() []string {
	l.Lock()
	defer l.Unlock()
	return append([]string(nil), l.requests...)
}

func enrollmentHTTPServer(t *testing.T, x pairedNodeHTTP) (*httptest.Server, *enrollmentHTTPLog) {
	t.Helper()
	log := new(enrollmentHTTPLog)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		x.handler.ServeHTTP(w, r)
		if r.URL.Path == "/agent/v1/enroll" {
			log.record(w.Header().Get("X-Request-ID"))
		}
	}))
	t.Cleanup(server.Close)
	// The actual client uses the configured origin and real certificate SAN.
	// No Host override, invented DNS mapping or TLS-verification bypass is used.
	x.owner.Config.Server.AdminOrigin = server.URL
	return server, log
}

type enrollmentHTTPBoundedLog struct{ bytes.Buffer }

func (w *enrollmentHTTPBoundedLog) Write(p []byte) (int, error) {
	if w.Len()+len(p) > 65536 {
		return 0, errors.New("PROBE_LOG_LIMIT")
	}
	return w.Buffer.Write(p)
}

func enrollmentHTTPProbe(t *testing.T, x pairedNodeHTTP, server *httptest.Server, input wire.Enrollment, expected string) enrollmentHTTPResult {
	t.Helper()
	binary := os.Getenv("PROJECT_ONE_NODE_ENROLLMENT_CLIENT_PROBE_BINARY")
	want := os.Getenv("PROJECT_ONE_NODE_ENROLLMENT_CLIENT_PROBE_SHA256")
	resolved, e := filepath.EvalSymlinks(binary)
	if e != nil || resolved != binary || !filepath.IsAbs(binary) || len(want) != 64 {
		t.Fatal("SOURCE_BOUND_ACTUAL_CLIENT_PROBE_REQUIRED")
	}
	file, e := os.Open(binary)
	if e != nil {
		t.Fatal("ACTUAL_CLIENT_PROBE_UNAVAILABLE")
	}
	hash := sha256.New()
	_, copied := io.Copy(hash, file)
	closed := file.Close()
	if copied != nil || closed != nil || hex.EncodeToString(hash.Sum(nil)) != want {
		t.Fatal("ACTUAL_CLIENT_PROBE_HASH_MISMATCH")
	}
	root := t.TempDir()
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	ca := filepath.Join(root, "ca.pem")
	if e = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(map[string]any{"kind": "projectone-node-enrollment-control-probe-v1", "origin": server.URL, "ca_file": ca, "input": input, "expected": expected})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "probe.json")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(x.ctx, 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-test.run=^TestR25ActualEnrollmentControl$", "-test.v", "-test.count=1", "-test.timeout=23s")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "PROJECT_ONE_ENROLLMENT_CONTROL_PROBE=") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "PROJECT_ONE_ENROLLMENT_CONTROL_PROBE="+path, "GOMAXPROCS=1", "GOMEMLIMIT=128MiB")
	command.WaitDelay = 2 * time.Second
	output := new(enrollmentHTTPBoundedLog)
	command.Stdout, command.Stderr = output, output
	if e = command.Run(); e != nil {
		t.Fatal("ACTUAL_CLIENT_PROBE_FAILED; private inputs/results retained only until owned test cleanup")
	}
	if strings.Contains(output.String(), "--- SKIP:") || !strings.Contains(output.String(), "--- PASS: TestR25ActualEnrollmentControl") {
		t.Fatal("ACTUAL_CLIENT_PROBE_COMPLETE_PASS_REQUIRED")
	}
	resultPath := filepath.Join(root, "agent-result.json")
	info, e := os.Lstat(resultPath)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 65536 {
		t.Fatal("ACTUAL_CLIENT_PRIVATE_RESULT_REQUIRED")
	}
	raw, e = os.ReadFile(resultPath)
	if e != nil {
		t.Fatal("ACTUAL_CLIENT_RESULT_READ_FAILED")
	}
	var result enrollmentHTTPResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || result.Kind != "projectone-node-enrollment-control-result-v1" || result.Outcome != expected ||
		(expected == "uncertain" && (result.Enrolled != nil || result.RequestID == "")) || (expected == "acknowledged" && (result.Enrolled == nil || result.RequestID != "")) {
		t.Fatal("ACTUAL_CLIENT_PRIVATE_RESULT_INVALID")
	}
	t.Log("Actual separately compiled source-bound agent Client.Post passed against real trusted-loopback control HTTPS; no synthetic HTTP response")
	return result
}

func enrollmentHTTPInput(token string) wire.Enrollment {
	return wire.Enrollment{JoinToken: token, Handshake: wire.Handshake{ProtocolVersion: wire.Version,
		Capabilities: wire.Capabilities(wire.ReadOnlyProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "Synthetic actual HTTPS \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", LogicalCPUs: 1, MemoryTotalBytes: 1048576, DiskPath: "/synthetic", DiskTotalBytes: 2097152, DiskAvailableBytes: 1048576, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}}
}

func TestM20PairedNodeEnrollmentHTTPBridge(t *testing.T) {
	t.Run("enroll", func(t *testing.T) {
		for _, fault := range []struct {
			name      string
			mode      commitfault.Mode
			committed bool
		}{
			{"normal_acknowledged_commit", commitfault.Pass, true},
			{"commit_not_forwarded", commitfault.DropBeforeCommit, false},
			{"actual_server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true},
		} {
			t.Run(fault.name, func(t *testing.T) {
				x := newPairedNodeHTTP(t)
				pending := nodeJoinHTTPMint(t, x, wire.ReadOnlyProfile)
				input := enrollmentHTTPInput(pending["token"].(string))
				original := x.handler.Nodes.Service
				server, log := enrollmentHTTPServer(t, x)
				store, proxy := nodeHTTPFaultStore(t, x)
				observer := &enrollmentHTTPArmStore{Store: store, proxy: proxy, mode: fault.mode}
				owner := *x.owner
				owner.Storage, owner.DB = observer, nil
				x.handler.Nodes.Service = &nodes.Service{Identity: &owner, Workspaces: &workspaces.Service{Identity: &owner}}
				expected := "uncertain"
				if fault.mode == commitfault.Pass {
					expected = "acknowledged"
				}
				result := enrollmentHTTPProbe(t, x, server, input, expected)
				requests := log.values()
				if len(requests) != 1 || !observer.armed || observer.armError != nil || observer.before != 2 || proxy.Commits() != observer.before+1 {
					t.Fatal("ACTUAL_CLIENT_RATE_AND_ENROLLMENT_COMMIT_OR_SINGLE_POST_MISMATCH")
				}
				request := requests[0]
				if id, e := uuid.Parse(request); e != nil || id == uuid.Nil || id.String() != request {
					t.Fatal("ACTUAL_BACKEND_REQUEST_ID_INVALID")
				}
				if expected == "uncertain" {
					if result.RequestID != request {
						t.Fatal("ACTUAL_CLIENT_UNCERTAIN_REQUEST_CONTEXT_MISMATCH")
					}
					select {
					case observed := <-proxy.Outcomes():
						if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
							t.Fatal("ACTUAL_HTTPS_ENROLLMENT_WIRE_OUTCOME_MISMATCH")
						}
					case <-x.ctx.Done():
						t.Fatal("ACTUAL_HTTPS_ENROLLMENT_WIRE_OUTCOME_ABSENT")
					}
				}
				var nodeID *string
				var consumed bool
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT node_id::text,consumed_at IS NOT NULL FROM node_join_tokens WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3", "SELECT node_id,consumed_at IS NOT NULL FROM node_join_tokens WHERE id=? AND tenant_id=? AND workspace_id=?", pending["id"], x.f.Tenant, x.f.Workspace)).Scan(&nodeID, &consumed); e != nil {
					t.Fatal(e)
				}
				var audits, count int
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT count(*) FROM audit_events WHERE action='node.enrolled' AND audience='system' AND actor_user_id IS NULL AND outcome='success' AND tenant_id=$1 AND workspace_id=$2 AND request_id=$3", "SELECT count(*) FROM audit_events WHERE action='node.enrolled' AND audience='system' AND actor_user_id IS NULL AND outcome='success' AND tenant_id=? AND workspace_id=? AND request_id=?", x.f.Tenant, x.f.Workspace, request)).Scan(&audits); e != nil {
					t.Fatal(e)
				}
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT count(*) FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND name=$3", "SELECT count(*) FROM managed_nodes WHERE tenant_id=? AND workspace_id=? AND name=?", x.f.Tenant, x.f.Workspace, pending["node_name"])).Scan(&count); e != nil {
					t.Fatal(e)
				}
				want := 0
				if fault.committed {
					want = 1
				}
				if consumed != fault.committed || (nodeID != nil) != fault.committed || count != want || audits != want {
					t.Fatal("ACTUAL_HTTPS_INDEPENDENT_NODE_TOKEN_AUDIT_READBACK_MISMATCH")
				}
				if fault.committed {
					node, e := original.Get(x.ctx, x.adminAuth, "", *nodeID)
					if e != nil || node.TenantID != x.f.Tenant || node.WorkspaceID != x.f.Workspace || node.Status != "active" || node.ExecutionProfile != wire.ReadOnlyProfile || node.Name != pending["node_name"] {
						t.Fatal("ACTUAL_HTTPS_PERSISTED_NODE_SCOPE_OR_PROFILE_MISMATCH")
					}
					if result.Enrolled != nil {
						var hash []byte
						if result.Enrolled.NodeID != *nodeID || x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT credential_hash FROM managed_nodes WHERE id=$1", "SELECT credential_hash FROM managed_nodes WHERE id=?", *nodeID)).Scan(&hash) != nil || !bytes.Equal(hash, security.Digest("node-credential:v1:"+result.Enrolled.Credential)) {
							t.Fatal("ACTUAL_HTTPS_ACKNOWLEDGED_CREDENTIAL_HASH_MISMATCH")
						}
					}
				}
				history, e := original.Tokens(x.ctx, x.adminAuth, url.Values{"limit": {"100"}})
				if e != nil {
					t.Fatal(e)
				}
				raw, _ := json.Marshal(history)
				if bytes.Contains(raw, []byte(input.JoinToken)) || bytes.Contains(raw, []byte("credential")) || bytes.Contains(raw, []byte("token_hash")) {
					t.Fatal("ACTUAL_HTTPS_INDEPENDENT_HISTORY_EXPOSED_SECRET")
				}
				// This gate establishes state and secrecy only. Actual MySQL orphan
				// removal remains a separate unfinished product path, never SQL cleanup.
				if fault.mode != commitfault.Pass && fault.committed {
					t.Log("Committed lost-credential product recovery tracked separately; MySQL Update/observers remain pending")
				}
				x.handler.Nodes.Service = original
				if !fault.committed {
					recovered := enrollmentHTTPProbe(t, x, server, input, "acknowledged")
					if recovered.Enrolled == nil || len(log.values()) != 2 || proxy.Commits() != observer.before+1 {
						t.Fatal("DELIBERATE_ACTUAL_HTTPS_RECOVERY_AFTER_CONFIRMED_ABSENCE_FAILED")
					}
				}
			})
		}
	})
}
