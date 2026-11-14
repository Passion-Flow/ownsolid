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
	"sync"
	"syscall"
	"testing"
	"time"

	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func TestM20LicenseUnknownImportProductionBrowser(t *testing.T) {
	if os.Getenv("PROJECT_ONE_BROWSER") != "1" {
		t.Skip("explicit bounded browser gate required")
	}
	selected := os.Getenv("PROJECT_ONE_LICENSE_BROWSER_CASE")
	mode := commitfault.DropBeforeCommit
	committed := false
	if selected == "actual_server_ACK_suppressed" {
		mode, committed = commitfault.DropCommitAcknowledgement, true
	} else if selected != "commit_not_forwarded" {
		t.Fatal("exact single License browser journey required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	repo, e := filepath.Abs("../../../../../..")
	if e != nil {
		t.Fatal(e)
	}
	evidence := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if !filepath.IsAbs(evidence) || filepath.Clean(evidence) != evidence {
		t.Fatal("canonical owned evidence required")
	}
	out := filepath.Join(evidence, "license-unknown-browser")
	if e := os.Mkdir(out, 0700); e != nil {
		t.Fatal(e)
	}
	owner := fixture.NewUnlicensed(t)
	password := fixture.Bootstrap(t, owner)
	private := t.TempDir()
	if os.Chmod(private, 0700) != nil {
		t.Fatal("private browser fixture required")
	}
	cert, ca, _, _ := smtpsink.Certificate(t, private)
	server := httptest.NewUnstartedServer(nil)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	owner.Config.Server.AdminOrigin = "https://localhost:" + port
	owner.Config.Server.PortalOrigin = "https://127.0.0.1:" + port
	owner.Config.Server.AdminAssets = os.Getenv("PROJECT_ONE_BROWSER_ADMIN_ASSETS")
	owner.Config.Server.PortalAssets = os.Getenv("PROJECT_ONE_BROWSER_PORTAL_ASSETS")
	for _, path := range []string{owner.Config.Server.AdminAssets, owner.Config.Server.PortalAssets} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			t.Fatal("explicit immutable production bundle required")
		}
		if _, e := os.Stat(filepath.Join(path, "index.html")); e != nil {
			t.Fatal(e)
		}
	}
	policy := owner.Config.Policy()
	policy.UI.PollMilliseconds = 1000
	owner.Config.Runtime = &policy
	handler := transport.New(owner)
	if handler.LicenseInitializationError() != nil {
		t.Fatal("real License serving initialization failed")
	}
	trust, e := owner.Config.LicenseTrust()
	if e != nil {
		t.Fatal(e)
	}
	clock := &licenseClock{value: time.Now().UTC().Truncate(time.Second).Add(5 * time.Second)}
	plain, e := licensing.New(owner, trust, owner.Config.LicensePolicy(), clock.now)
	if e != nil {
		t.Fatal(e)
	}
	handler.Licensing.Service, owner.BusinessAdmission = plain, plain
	forward, proxy := licenseHTTPForwarding(t, owner, clock)
	var mu sync.Mutex
	armed, used, checked, finished := false, false, false, false
	imports := 0
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		licensePath := r.URL.Path == "/api/v1/license/status" || r.URL.Path == "/api/v1/admin/license" || r.URL.Path == "/api/v1/admin/license/diagnostics"
		if !licensePath {
			handler.ServeHTTP(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" && r.URL.Path == "/api/v1/admin/license" {
			imports++
			if armed {
				if e := proxy.Arm(mode); e != nil {
					http.Error(w, "Private wire arm refused", 500)
					return
				}
				handler.Licensing.Service = forward
				defer func() { handler.Licensing.Service = plain }()
				armed, used = false, true
			}
		}
		handler.ServeHTTP(w, r)
	})
	server.StartTLS()
	defer server.Close()
	c := licenseClaims(t, owner, clock.now())
	attempt := licenseInput(t, owner, c, "")
	artifact := filepath.Join(private, "signed-license.json")
	if os.WriteFile(artifact, []byte(attempt.Artifact), 0600) != nil {
		t.Fatal("private signed file")
	}
	token := security.Token()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "POST" || r.URL.Path != "/" || r.Header.Get("X-Control-Token") != token {
			http.Error(w, "Private control refused", 403)
			return
		}
		var request struct {
			Action string `json:"action"`
			Case   string `json:"case"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || request.Case != selected {
			http.Error(w, "Private control input refused", 400)
			return
		}
		count := func(query string) (int, error) {
			var n int
			bounded, stop := context.WithTimeout(ctx, 3*time.Second)
			defer stop()
			e := owner.DB.QueryRow(bounded, query).Scan(&n)
			return n, e
		}
		switch request.Action {
		case "arm":
			if armed || used {
				http.Error(w, "Private arm order refused", 409)
				return
			}
			armed = true
		case "check", "finish":
			if !used || armed || request.Action == "check" && checked || request.Action == "finish" && (!checked || finished) {
				http.Error(w, "Private readback order refused", 409)
				return
			}
			want := 0
			if committed || request.Action == "finish" {
				want = 1
			}
			rows, e1 := count("SELECT count(*) FROM platform_license_artifacts")
			audits, e2 := count("SELECT count(*) FROM audit_events WHERE action='license.imported'")
			wantImports := 1
			if request.Action == "finish" {
				wantImports = 2
			}
			if e1 != nil || e2 != nil || rows != want || audits != want || imports != wantImports || proxy.Commits() != 1 {
				http.Error(w, "Private actual durable readback mismatch", 500)
				return
			}
			if request.Action == "check" {
				select {
				case observed := <-proxy.Outcomes():
					if observed.Mode != mode || !observed.Suppressed || observed.Forwarded != committed || observed.ServerOK != committed {
						http.Error(w, "Actual COMMIT outcome mismatch", 500)
						return
					}
				case <-time.After(3 * time.Second):
					http.Error(w, "Actual COMMIT observation missing", 500)
					return
				}
				checked = true
			} else {
				finished = true
			}
		default:
			http.Error(w, "Private action refused", 400)
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
	input := map[string]any{"kind": "projectone-license-unknown-browser-v1", "case": selected, "portal": owner.Config.Server.PortalOrigin, "admin": owner.Config.Server.AdminOrigin, "email": "admin@example.invalid", "password": password, "spki": base64.StdEncoding.EncodeToString(pin[:]), "control": control.URL, "control_token": token, "output": out, "repository": repo, "artifact": artifact, "license_id": c.LicenseID, "committed": committed, "poll_milliseconds": policy.UI.PollMilliseconds}
	raw, _ := json.Marshal(input)
	path := filepath.Join(private, "browser-input.json")
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("private browser input")
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(repo, "tests/acceptance/licensing/unknown-import-flow.mjs"), path)
	command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+ca)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = 5 * time.Second
	if e := command.Run(); e != nil {
		t.Fatal("LICENSE_UNKNOWN_BROWSER_FAILED")
	}
	var result struct {
		Status   string `json:"status"`
		Case     string `json:"case"`
		Imports  int    `json:"imports"`
		Repeated bool   `json:"repeated"`
	}
	raw, e = os.ReadFile(filepath.Join(out, "result.json"))
	if e != nil || json.Unmarshal(raw, &result) != nil || result.Status != "passed" || result.Case != selected || result.Imports != 2 || result.Repeated != committed || !finished {
		t.Fatal("complete actual React/wire/durable recovery required")
	}
	t.Logf("actual_React_TLS_PG=true case=%s initial_import=1 explicit_retry=1 final_history=1 final_import_audit=1", selected)
}
