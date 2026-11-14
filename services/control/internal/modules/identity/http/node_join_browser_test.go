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

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func TestM20NodeJoinProductionBrowser(t *testing.T) {
	if os.Getenv("PROJECT_ONE_BROWSER") != "1" {
		t.Skip("explicit bounded browser gate required")
	}
	operation, selected := os.Getenv("PROJECT_ONE_NODE_JOIN_BROWSER_OPERATION"), os.Getenv("PROJECT_ONE_NODE_JOIN_BROWSER_CASE")
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "postgresql" || operation != "profiles" && operation != "mint" && operation != "revoke" {
		t.Fatal("exact original PG join browser scope required")
	}
	if operation == "profiles" && selected != "none" {
		t.Fatal("profile journey requires no fault")
	}
	mode, committed := commitfault.DropBeforeCommit, false
	if selected == "actual_server_ACK_suppressed" {
		mode, committed = commitfault.DropCommitAcknowledgement, true
	} else if !(operation == "profiles" && selected == "none") && selected != "commit_not_forwarded" {
		t.Fatal("exact browser fault selection required")
	}
	x := newPairedNodeHTTPWithTimeout(t, 180*time.Second)
	x.admin.s.Close()
	private := t.TempDir()
	if os.Chmod(private, 0700) != nil {
		t.Fatal("private browser input directory required")
	}
	repo, e := filepath.Abs("../../../../../..")
	if e != nil {
		t.Fatal(e)
	}
	evidence := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if !filepath.IsAbs(evidence) || filepath.Clean(evidence) != evidence {
		t.Fatal("canonical owned browser evidence required")
	}
	out := filepath.Join(evidence, "node-join-browser")
	if os.Mkdir(out, 0700) != nil {
		t.Fatal("exclusive browser output required")
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
			t.Fatal("immutable production bundle required")
		}
		if _, e = os.Stat(filepath.Join(p, "index.html")); e != nil {
			t.Fatal(e)
		}
	}
	store, proxy := nodeHTTPFaultStore(t, x)
	isolated := *x.owner
	isolated.Storage, isolated.DB = store, nil
	forward := &nodes.Service{Identity: &isolated, Workspaces: &workspaces.Service{Identity: &isolated}}
	original := x.handler.Nodes.Service
	var mu sync.Mutex
	armed, used, checked, recovering, reconciled, finished := false, false, false, false, false, false
	allWrites, faultWrites, historyReads := 0, 0, 0
	targetID, unknownID, requestID, before, checkpoint := "", "", "", "", ""
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/nodes") && !strings.Contains(r.URL.Path, "/node-join-tokens") {
			x.handler.ServeHTTP(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		joinWrite := r.Method == "POST" && strings.HasPrefix(r.URL.Path, nodeJoinHTTPPath)
		if joinWrite {
			allWrites++
			if used {
				faultWrites++
			}
		}
		if used && r.Method == "GET" && r.URL.Path == nodeJoinHTTPPath {
			historyReads++
		}
		target := nodeJoinHTTPPath
		if operation == "revoke" {
			target += "/" + targetID + "/revocations"
		}
		faulted := armed && r.Method == "POST" && r.URL.Path == target
		if faulted {
			if e := proxy.Arm(mode); e != nil {
				http.Error(w, "Private join wire arm refused", 500)
				return
			}
			x.handler.Nodes.Service = forward
			defer func() { x.handler.Nodes.Service = original }()
			armed, used, faultWrites = false, true, 1
		}
		x.handler.ServeHTTP(w, r)
		if faulted {
			requestID = w.Header().Get("X-Request-ID")
		}
	})
	server.StartTLS()
	defer server.Close()
	controlToken := security.Token()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "POST" || r.URL.Path != "/" || r.Header.Get("X-Control-Token") != controlToken {
			http.Error(w, "Private join control refused", 403)
			return
		}
		var input struct {
			Action    string `json:"action"`
			Operation string `json:"operation"`
			Case      string `json:"case"`
			TargetID  string `json:"target_id"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || input.Operation != operation || input.Case != selected {
			http.Error(w, "Private join input refused", 400)
			return
		}
		refuse := func(message string) { http.Error(w, message, 500) }
		switch input.Action {
		case "profiles_finish":
			if operation != "profiles" || selected != "none" || allWrites != 12 || proxy.Commits() != 0 || nodeJoinHTTPAudit(t, x, "node.join_token_created", "") != 6 || nodeJoinHTTPAudit(t, x, "node.join_token_revoked", "") != 6 {
				refuse("Real profile path or scoped audit mismatch")
				return
			}
			var profiles, revoked int
			if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT count(DISTINCT execution_profile),count(*) FILTER(WHERE revoked_at IS NOT NULL) FROM node_join_tokens WHERE node_name LIKE 'Synthetic browser profile %'`, "")).Scan(&profiles, &revoked); e != nil || profiles != 6 || revoked != 6 {
				refuse("Real profile persistence mismatch")
				return
			}
			finished = true
		case "arm":
			if operation == "profiles" || armed || used || allWrites != 1 {
				refuse("Private join arm order refused")
				return
			}
			id, e := uuid.Parse(input.TargetID)
			if e != nil || id == uuid.Nil || id.String() != input.TargetID {
				refuse("Canonical prepared token required")
				return
			}
			var current string
			if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT id::text FROM node_join_tokens WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND revoked_at IS NULL AND consumed_at IS NULL AND expires_at>clock_timestamp()`, "", input.TargetID, x.f.Tenant, x.f.Workspace)).Scan(&current); e != nil {
				refuse("Real prepared token missing")
				return
			}
			targetID = current
			before = nodeHTTPStoredDigest(t, x)
			armed = true
		case "check":
			if !used || armed || checked || faultWrites != 1 || historyReads != 0 || requestID == "" || proxy.Commits() != 1 || (nodeHTTPStoredDigest(t, x) == before) == committed {
				refuse("Unknown join retried or actual state mismatch")
				return
			}
			action := "node.join_token_created"
			if operation == "revoke" {
				action = "node.join_token_revoked"
			}
			want := 0
			if committed {
				want = 1
			}
			if nodeJoinHTTPAudit(t, x, action, requestID) != want {
				refuse("Real scoped unknown join audit mismatch")
				return
			}
			select {
			case observed := <-proxy.Outcomes():
				if observed.Mode != mode || !observed.Suppressed || observed.Forwarded != committed || observed.ServerOK != committed {
					refuse("Real join COMMIT observation mismatch")
					return
				}
			case <-time.After(3 * time.Second):
				refuse("Real join COMMIT observation missing")
				return
			}
			if operation == "mint" && committed {
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT target_id FROM audit_events WHERE request_id=$1 AND action='node.join_token_created'`, "", requestID)).Scan(&unknownID); e != nil {
					refuse("Actual uncertain mint identity missing")
					return
				}
			} else if operation == "revoke" {
				var revoked bool
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT revoked_at IS NOT NULL FROM node_join_tokens WHERE id=$1`, "", targetID)).Scan(&revoked); e != nil || revoked != committed {
					refuse("Actual uncertain revoke state mismatch")
					return
				}
			}
			checkpoint = nodeHTTPStoredDigest(t, x)
			checked = true
		case "recover":
			if !checked || recovering || faultWrites != 1 || historyReads != 0 {
				refuse("Private explicit refresh order refused")
				return
			}
			recovering = true
		case "reconciled":
			if !recovering || reconciled || faultWrites != 1 || historyReads != 1 || nodeHTTPStoredDigest(t, x) != checkpoint || proxy.Commits() != 1 {
				refuse("Authoritative refresh mutated state or resent write")
				return
			}
			reconciled = true
		case "finish":
			manual := 0
			if operation == "mint" {
				manual = 1
				if !committed {
					manual = 2
				}
			} else if !committed {
				manual = 1
			}
			if !reconciled || finished || faultWrites != 1+manual || proxy.Commits() != 1 {
				refuse("Explicit manual recovery write count mismatch")
				return
			}
			id := targetID
			if operation == "mint" {
				id = unknownID
				if !committed {
					if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT id::text FROM node_join_tokens WHERE node_name='Synthetic browser deliberate fresh join' AND tenant_id=$1 AND workspace_id=$2`, "", x.f.Tenant, x.f.Workspace)).Scan(&id); e != nil {
						refuse("Explicit fresh recovery token missing")
						return
					}
				}
			}
			var revoked bool
			if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT revoked_at IS NOT NULL FROM node_join_tokens WHERE id=$1`, "", id)).Scan(&revoked); e != nil || !revoked {
				refuse("Manual recovery did not persist revocation")
				return
			}
			finished = true
		default:
			refuse("Private join action refused")
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
	input := map[string]any{"kind": "projectone-node-join-browser-v1", "operation": operation, "case": selected, "admin": x.owner.Config.Server.AdminOrigin, "portal": x.owner.Config.Server.PortalOrigin, "email": "admin@example.invalid", "password": x.password, "spki": base64.StdEncoding.EncodeToString(pin[:]), "control": control.URL, "control_token": controlToken, "output": out, "repository": repo}
	raw, _ := json.Marshal(input)
	inputPath := filepath.Join(private, "browser-input.json")
	if os.WriteFile(inputPath, raw, 0600) != nil {
		t.Fatal("private join browser input required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", filepath.Join(repo, "tests/acceptance/nodes/join-write-flow.mjs"), inputPath)
	command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+ca)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = 5 * time.Second
	if e = command.Run(); e != nil {
		t.Fatal("NODE_JOIN_BROWSER_FAILED")
	}
	var result struct {
		Status    string `json:"status"`
		Operation string `json:"operation"`
		Case      string `json:"case"`
		Profiles  int    `json:"profiles"`
		Cleared   bool   `json:"private_state_cleared"`
		Recovery  bool   `json:"explicit_recovery"`
	}
	raw, e = os.ReadFile(filepath.Join(out, "result.json"))
	if e != nil || json.Unmarshal(raw, &result) != nil || result.Status != "passed" || result.Operation != operation || result.Case != selected || !finished || operation == "profiles" && result.Profiles != 6 || operation != "profiles" && (!result.Cleared || !result.Recovery) {
		t.Fatal("complete actual join browser result required")
	}
	t.Logf("actual_React_TLS_PG_join=true operation=%s case=%s finished=true", operation, selected)
}
