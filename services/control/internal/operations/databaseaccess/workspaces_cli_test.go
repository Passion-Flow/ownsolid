//go:build m02_delivery_integration

package databaseaccess

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// Explicitly selected by the M02 delivery runner after building the real CLI.
func TestM02NativeUpgradeCommand(t *testing.T) {
	binary := os.Getenv("PROJECT_ONE_M02_CONTROL_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("ACTUAL_M02_CONTROL_BINARY_REQUIRED")
	}
	f := newActualFixture(t, "account")
	ctx := t.Context()
	if status, e := Prepare(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	password := identityfixture.Bootstrap(t, f.service)
	c, e := config.Load(filepath.Clean("../../../config/examples/control/workspaces.example.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	c.Database.MaxOpenConnections = f.input.configuration.Database.MaxOpenConnections
	c.Database.MaxIdleConnections = 1
	root := t.TempDir()
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	write := func(name string, raw []byte) string {
		p := filepath.Join(root, name)
		if e := os.WriteFile(p, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	ds := func(runtime bool) string {
		source := f.service.DB.Config().ConnConfig
		user, secret := source.User, source.Password
		if runtime {
			user, secret = f.input.connection.ConnConfig.User, f.input.connection.ConnConfig.Password
		}
		u := url.URL{Scheme: "postgresql", Host: source.Host + ":" + strconv.Itoa(int(source.Port)), Path: "/" + source.Database, User: url.UserPassword(user, secret)}
		q := url.Values{"sslmode": {"disable"}, "search_path": {source.RuntimeParams["search_path"]}}
		u.RawQuery = q.Encode()
		return u.String()
	}
	configuration := func(name, dsn string) string {
		copy := c
		copy.Database.DSNFile = write(name+"-dsn", []byte(dsn))
		raw, e := yaml.Marshal(copy)
		if e != nil {
			t.Fatal(e)
		}
		return write(name+".yaml", raw)
	}
	operator, runtime := configuration("operator", ds(false)), configuration("runtime", ds(true))
	// Release the fixture's lease so the production CLI must obtain its own lock.
	if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		command := exec.CommandContext(ctx, binary, WorkspacesUpgradeCommand, "--config", operator, "--runtime-config", runtime)
		output, e := command.CombinedOutput()
		if e != nil || strings.TrimSpace(string(output)) != WorkspacesUpgradeComplete {
			t.Fatalf("native upgrade failed: %v %s", e, output)
		}
	}
	profile := "workspaces"
	f.input.configuration.Server.ApplicationProfile = &profile
	if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_lock($1)", database.InstallationLockID); e != nil {
		t.Fatal(e)
	}
	if status, e := Check(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	f.service.Config.Server.ApplicationProfile = &profile
	admin := identityfixture.Login(t, f.service, "admin@example.invalid", password, "admin")
	portal := identityfixture.Login(t, f.service, "admin@example.invalid", password, "portal")
	w := workspaces.Service{DB: f.service.DB, Identity: f.service}
	workspace, e := w.Create(ctx, admin, "Native restart Workspace", admin.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	note, e := w.CreateNote(ctx, portal, workspace.ID, "Native retained note", "M02_RESTART_PERSISTED", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, token, _, e := f.service.Login(ctx, "admin@example.invalid", password, "portal", "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	c.Server.PortalAssets = os.Getenv("PROJECT_ONE_M02_PORTAL_ASSETS")
	c.Server.AdminAssets = os.Getenv("PROJECT_ONE_M02_ADMIN_ASSETS")
	if !filepath.IsAbs(c.Server.PortalAssets) || !filepath.IsAbs(c.Server.AdminAssets) {
		t.Fatal("ACTUAL_M02_ASSETS_REQUIRED")
	}
	socket, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := socket.Addr().(*net.TCPAddr).Port
	socket.Close()
	c.Server.ListenAddress = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	c.Server.PortalOrigin = "http://localhost:" + strconv.Itoa(port)
	c.Server.AdminOrigin = "http://127.0.0.1:" + strconv.Itoa(port)
	c.Security.AuthHMACKeyFile = write("hmac", f.service.HMACKey)
	c.Security.MailKeyringFile = filepath.Join(filepath.Dir(f.service.Config.Licensing.TrustFile), "keyring.json")
	c.Security.PasswordBlocklistFile = filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/security/password-blocklist.txt")
	c.Licensing = f.service.Config.Licensing
	c.SMTP.Host = "127.0.0.1"
	c.SMTP.CAFile = ""
	runtime = configuration("native-serve", ds(true))
	client := &http.Client{Timeout: 2 * time.Second}
	policy, _ := httpserver.NewCookiePolicy(config.TransportHTTP)
	get := func(path string) (int, []byte, error) {
		r, e := http.NewRequestWithContext(ctx, "GET", c.Server.PortalOrigin+path, nil)
		if e != nil {
			return 0, nil, e
		}
		r.AddCookie(&http.Cookie{Name: policy.Name("portal", httpserver.SessionCookie), Value: token})
		response, e := client.Do(r)
		if e != nil {
			return 0, nil, e
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return response.StatusCode, raw, e
	}
	for iteration := 0; iteration < 2; iteration++ {
		if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
			t.Fatal(e)
		}
		log, e := os.Create(filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), fmt.Sprintf("native-m02-serve-%d.log", iteration)))
		if e != nil {
			t.Fatal(e)
		}
		command := exec.CommandContext(ctx, binary, "serve", "--config", runtime)
		command.Stdout = log
		command.Stderr = log
		if e = command.Start(); e != nil {
			log.Close()
			t.Fatal(e)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		stopped := false
		stop := func() {
			if stopped {
				return
			}
			stopped = true
			_ = command.Process.Signal(os.Interrupt)
			select {
			case e := <-done:
				if e != nil {
					t.Error("native serving exit", e)
				}
			case <-time.After(10 * time.Second):
				_ = command.Process.Kill()
				<-done
				t.Error("native serving shutdown timeout")
			}
			log.Close()
		}
		t.Cleanup(stop)
		ready := false
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			status, _, e := get("/health/ready")
			if e == nil && status == 200 {
				ready = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ready {
			t.Fatal("native M02 serving readiness failed; inspect retained serve log")
		}
		for _, path := range []string{"/api/v1/workspaces/" + workspace.ID, "/api/v1/workspaces/" + workspace.ID + "/notes/" + note.ID, "/api/v1/workspaces/" + workspace.ID + "/notes/" + note.ID + "/export"} {
			status, raw, e := get(path)
			if e != nil || status != 200 {
				t.Fatal("native M02 persisted read failed", status, e)
			}
			if strings.Contains(path, "/notes/") && !strings.Contains(string(raw), "M02_RESTART_PERSISTED") {
				t.Fatal("native persisted note missing")
			}
		}
		// The shipped executable must reject disabled descendants after successful
		// account/note/export controls, before and after the actual restart.
		for _, tail := range []string{"nodes", "nodes/" + uuid.NewString() + "/components/node-exporter", "models", "deployments", "datasets", "training", "knowledge-bases", "workflows", "plugins", "middleware", "aws", "private-clusters"} {
			status, raw, e := get("/api/v1/workspaces/" + workspace.ID + "/" + tail)
			var problem struct {
				Code   string `json:"code"`
				Status int    `json:"status"`
			}
			if e != nil || status != 404 || json.Unmarshal(raw, &problem) != nil || problem.Code != "RESOURCE_NOT_FOUND" || problem.Status != 404 {
				t.Fatal("native disabled-module routing regression", tail, status, e)
			}
		}
		stop()
		if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_lock($1)", database.InstallationLockID); e != nil {
			t.Fatal(e)
		}
	}

	t.Log("actual upgrade-workspaces CLI repeated on populated M01; existing runtime role, data and login preserved; actual serve/restart retains Workspace/note/export and session; disabled descendants return structured404 from both processes")
}
