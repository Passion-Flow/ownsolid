//go:build repository_integration

package http_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func TestM20NodeUnknownReadProductionBrowser(t *testing.T) {
	if os.Getenv("PROJECT_ONE_BROWSER") != "1" {
		t.Skip("explicit bounded browser gate required")
	}
	operation, selected := os.Getenv("PROJECT_ONE_NODE_BROWSER_OPERATION"), os.Getenv("PROJECT_ONE_NODE_BROWSER_CASE")
	allowed := map[string]bool{"admin_get": true, "admin_list": true, "admin_tokens": true, "admin_token_secret": true, "admin_tasks": true, "portal_get": true, "portal_list": true, "portal_tasks": true}
	if !allowed[operation] || os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "postgresql" {
		t.Fatal("exact original PostgreSQL browser scope required; full MySQL frontend remains pending")
	}
	mode, committed := commitfault.DropBeforeCommit, false
	if selected == "actual_server_ACK_suppressed" {
		mode, committed = commitfault.DropCommitAcknowledgement, true
	} else if selected != "commit_not_forwarded" {
		t.Fatal("exact node browser fault required")
	}
	x := newPairedNodeHTTPWithTimeout(t, 180*time.Second)
	x.admin.s.Close()
	private := t.TempDir()
	if os.Chmod(private, 0700) != nil {
		t.Fatal("private node browser directory required")
	}
	repo, e := filepath.Abs("../../../../../..")
	if e != nil {
		t.Fatal(e)
	}
	evidence := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if !filepath.IsAbs(evidence) || filepath.Clean(evidence) != evidence {
		t.Fatal("canonical owned browser evidence required")
	}
	out := filepath.Join(evidence, "node-unknown-browser")
	if os.Mkdir(out, 0700) != nil {
		t.Fatal("exclusive node browser output required")
	}
	cert, ca, _, _ := smtpsink.Certificate(t, private)
	server := httptest.NewUnstartedServer(nil)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	x.owner.Config.Server.AdminOrigin = "https://localhost:" + port
	x.owner.Config.Server.PortalOrigin = "https://127.0.0.1:" + port
	x.owner.Config.Server.AdminAssets = os.Getenv("PROJECT_ONE_BROWSER_ADMIN_ASSETS")
	x.owner.Config.Server.PortalAssets = os.Getenv("PROJECT_ONE_BROWSER_PORTAL_ASSETS")
	for _, p := range []string{x.owner.Config.Server.AdminAssets, x.owner.Config.Server.PortalAssets} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			t.Fatal("immutable production bundles required")
		}
		if _, e = os.Stat(filepath.Join(p, "index.html")); e != nil {
			t.Fatal(e)
		}
	}
	policy := x.owner.Config.Policy()
	policy.UI.PollMilliseconds = 1000
	x.owner.Config.Runtime = &policy
	path := "/api/v1/admin/nodes"
	if strings.HasPrefix(operation, "portal_") {
		path = "/api/v1/workspaces/" + x.f.Workspace + "/nodes"
	}
	switch strings.SplitN(operation, "_", 2)[1] {
	case "get":
		path += "/" + x.nodes[0]
	case "tokens", "token_secret":
		path = "/api/v1/admin/node-join-tokens"
	case "tasks":
		path += "/" + x.nodes[0] + "/tasks"
	}
	store, proxy := nodeHTTPFaultStore(t, x)
	isolated := *x.owner
	isolated.Storage, isolated.DB = store, nil
	forward := &nodes.Service{Identity: &isolated, Workspaces: &workspaces.Service{Identity: &isolated}}
	original := x.handler.Nodes.Service
	var mu sync.Mutex
	armed, used, checked, recovering, finished := false, false, false, false, false
	requests, mutations := 0, 0
	before := nodeHTTPStoredDigest(t, x)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/nodes") && !strings.Contains(r.URL.Path, "/node-join-tokens") {
			x.handler.ServeHTTP(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "GET" {
			mutations++
		}
		if r.Method == "GET" && r.URL.Path == path {
			if used {
				requests++
			}
			if armed {
				if e := proxy.Arm(mode); e != nil {
					http.Error(w, "Private node wire arm refused", 500)
					return
				}
				x.handler.Nodes.Service = forward
				defer func() { x.handler.Nodes.Service = original }()
				armed, used = false, true
				requests = 1
			}
		}
		x.handler.ServeHTTP(w, r)
	})
	server.StartTLS()
	defer server.Close()
	token := security.Token()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "POST" || r.URL.Path != "/" || r.Header.Get("X-Control-Token") != token {
			http.Error(w, "Private node control refused", 403)
			return
		}
		var input struct {
			Action    string `json:"action"`
			Operation string `json:"operation"`
			Case      string `json:"case"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || input.Operation != operation || input.Case != selected {
			http.Error(w, "Private node control input refused", 400)
			return
		}
		switch input.Action {
		case "arm":
			if armed || used {
				http.Error(w, "Private node arm order refused", 409)
				return
			}
			before = nodeHTTPStoredDigest(t, x)
			mutations = 0
			armed = true
		case "check":
			if !used || armed || checked || requests != 1 || mutations != 0 || proxy.Commits() != 1 || nodeHTTPStoredDigest(t, x) != before {
				http.Error(w, "Unknown node read automatically retried or changed persistence", 500)
				return
			}
			select {
			case observed := <-proxy.Outcomes():
				if observed.Mode != mode || !observed.Suppressed || observed.Forwarded != committed || observed.ServerOK != committed {
					http.Error(w, "Actual node COMMIT outcome mismatch", 500)
					return
				}
			case <-time.After(3 * time.Second):
				http.Error(w, "Actual node COMMIT observation missing", 500)
				return
			}
			checked = true
		case "recover":
			if !checked || recovering || requests != 1 {
				http.Error(w, "Private node recovery order refused", 409)
				return
			}
			recovering = true
		case "finish":
			if !recovering || finished || requests < 2 || mutations != 0 || proxy.Commits() != 1 || nodeHTTPStoredDigest(t, x) != before {
				http.Error(w, "Explicit node read recovery mismatch", 500)
				return
			}
			finished = true
		default:
			http.Error(w, "Private node action refused", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer control.Close()
	leaf, e := x509.ParseCertificate(cert.Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	pin := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	input := map[string]any{"kind": "projectone-node-unknown-browser-v1", "operation": operation, "case": selected, "admin": x.owner.Config.Server.AdminOrigin, "portal": x.owner.Config.Server.PortalOrigin, "email": "admin@example.invalid", "password": x.password, "spki": base64.StdEncoding.EncodeToString(pin[:]), "control": control.URL, "control_token": token, "output": out, "repository": repo, "node_name": "HTTPS node " + x.nodes[0], "target_path": path, "poll_milliseconds": policy.UI.PollMilliseconds}
	raw, _ := json.Marshal(input)
	inputPath := filepath.Join(private, "browser-input.json")
	if os.WriteFile(inputPath, raw, 0600) != nil {
		t.Fatal("private node browser input required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", filepath.Join(repo, "tests/acceptance/nodes/unknown-read-flow.mjs"), inputPath)
	command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+ca)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = 5 * time.Second
	if e = command.Run(); e != nil {
		t.Fatal("NODE_UNKNOWN_BROWSER_FAILED")
	}
	var result struct {
		Status    string `json:"status"`
		Operation string `json:"operation"`
		Case      string `json:"case"`
		Cleared   bool   `json:"private_state_cleared"`
		Recovery  bool   `json:"explicit_recovery"`
		Secret    bool   `json:"one_time_secret_prepared"`
	}
	raw, e = os.ReadFile(filepath.Join(out, "result.json"))
	if e != nil || json.Unmarshal(raw, &result) != nil || result.Status != "passed" || result.Operation != operation || result.Case != selected || !result.Cleared || !result.Recovery || operation == "admin_token_secret" && !result.Secret || !finished {
		t.Fatal("complete actual React node read recovery required")
	}
	t.Logf("actual_React_TLS_PG=true operation=%s case=%s automatic_resends=0 explicit_recovery=true node_data_unchanged=true", operation, selected)
}
