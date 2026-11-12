package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"go.yaml.in/yaml/v3"
)

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(t.Context())
	return stdout.String(), stderr.String(), err
}

func privateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ownsolid")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, dir string, config fileConfig) {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, configName), raw, 0o600)
}

func token(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestServerPrecedence(t *testing.T) {
	dir := privateDir(t)
	t.Setenv("OWNSOLID_CONFIG_DIR", dir)
	t.Setenv("OWNSOLID_SERVER", "")
	writeConfig(t, dir, fileConfig{
		Server: "https://config.example.invalid",
		SiteID: "default",
		CellID: "default",
	})

	check := func(flag string, flagSet bool, want string) {
		t.Helper()
		cfg, err := loadSettings(flag, flagSet)
		if err != nil || cfg.origin.String() != want || cfg.dir != dir {
			t.Fatalf("settings origin %q and dir %q, error %v", cfg.origin.String(), cfg.dir, err)
		}
	}
	check("", false, "https://config.example.invalid")
	t.Setenv("OWNSOLID_SERVER", "https://env.example.invalid")
	check("", false, "https://env.example.invalid")
	check("https://FLAG.example.invalid:443/", true, "https://flag.example.invalid")
	if _, err := loadSettings("", true); err == nil {
		t.Fatal("explicitly empty flag unexpectedly used another source")
	}
}

func TestConfigDirectoryAndInvalidOrigins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OWNSOLID_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if dir, err := configDir(); err != nil || dir != filepath.Join(home, ".config", "ownsolid") {
		t.Fatalf("default config directory %q, error %v", dir, err)
	}
	t.Setenv("XDG_CONFIG_HOME", home)
	if dir, err := configDir(); err != nil || dir != filepath.Join(home, "ownsolid") {
		t.Fatalf("XDG config directory %q, error %v", dir, err)
	}
	for _, value := range []string{
		"admin.example.invalid", "https://admin.example.invalid/api",
		"https://admin.example.invalid?", "https://admin.example.invalid#",
		"https://user@admin.example.invalid", "https://admin.example.invalid:65536",
		"https://admin.example.invalid:", "ftp://admin.example.invalid",
	} {
		if _, err := parseOrigin(value); err == nil {
			t.Errorf("invalid origin accepted: %q", value)
		}
	}
}

func TestCommandHelp(t *testing.T) {
	for _, args := range [][]string{
		{}, {"login"}, {"logout"}, {"node"}, {"node", "list"}, {"node", "get"},
		{"model"}, {"model", "list"}, {"deployment"}, {"deployment", "list"},
		{"workspace"}, {"workspace", "list"}, {"workspace", "get"}, {"session"}, {"session", "list"}, {"session", "revoke"}, {"deployment", "get"}, {"version"}, {"help"},
		{"node", "disable"}, {"node", "enable"}, {"node", "rename"}, {"node", "remove"},
		{"node", "collect-facts"}, {"node", "tasks"}, {"node", "task"}, {"node", "task-log"}, {"node", "inference"}, {"node", "probe-inference"},
		{"node", "join-token"}, {"node", "join-token", "list"}, {"node", "join-token", "create"}, {"node", "join-token", "revoke"},
		{"node", "exporter"}, {"node", "exporter", "status"}, {"node", "exporter", "logs"},
		{"node", "exporter", "install"}, {"node", "exporter", "start"}, {"node", "exporter", "stop"},
		{"node", "exporter", "remove"}, {"node", "exporter", "observe"}, {"node", "exporter", "configure"},
		{"node", "exporter", "restart"}, {"node", "exporter", "upgrade"}, {"node", "exporter", "rollback"}, {"node", "exporter", "collect-logs"},
		{"model", "get"}, {"model", "retire"},
		{"model", "import"}, {"model", "import", "catalog"}, {"model", "import", "list"}, {"model", "import", "get"},
		{"model", "import", "start"}, {"model", "import", "resume"}, {"model", "import", "verify"}, {"model", "import", "cancel"},
		{"deployment", "placement"}, {"deployment", "create"}, {"deployment", "start"}, {"deployment", "stop"},
		{"deployment", "observe"}, {"deployment", "verify"}, {"deployment", "remove"},
		{"deployment", "alias"}, {"deployment", "alias", "gateways"}, {"deployment", "alias", "list"},
		{"deployment", "alias", "get"}, {"deployment", "alias", "provision"}, {"deployment", "alias", "publish"}, {"deployment", "alias", "disable"},
	} {
		out, _, err := run(t, append(slices.Clone(args), "--help")...)
		if err != nil || !strings.Contains(out, "Examples:") || !strings.Contains(out, "ownsolid ") {
			t.Errorf("missing help or example for %v: %q (%v)", args, out, err)
		}
	}
	t.Setenv("OWNSOLID_CONFIG_DIR", "invalid")
	out, _, err := run(t, "version", "-o", "json")
	if err != nil || !json.Valid([]byte(out)) || !strings.Contains(out, "version") {
		t.Fatalf("local version: %q, %v", out, err)
	}
}

const (
	workspaceID  = "12345678-0000-4000-8000-000000000001"
	nodeID       = "12345678-0000-4000-8000-000000000002"
	secondNodeID = "12345678-0000-4000-8000-000000000003"
	modelID      = "12345678-0000-4000-8000-000000000004"
	deployID     = "12345678-0000-4000-8000-000000000005"
	adminPath    = "/api/v1/sites/site-a/cells/cell-b/admin/"
)

func TestOperatorFlow(t *testing.T) {
	password, session, csrf := token(t), token(t), token(t)
	email := "operator@example.invalid"
	node := map[string]any{"id": nodeID, "name": "gpu-01", "status": "active", "connectivity": "online", "workspace_id": workspaceID}
	second := map[string]any{"id": secondNodeID, "name": "gpu-012-long", "status": "active", "connectivity": "offline"}
	model := map[string]any{"id": modelID, "name": "true", "kind": "base", "state": "ready"}
	deployment := map[string]any{"id": deployID, "workspace_id": workspaceID, "node_id": nodeID, "model_id": modelID, "desired_state": "running", "observed_state": "ready"}
	var active, rejectLogout, ambiguous, rejectRead, capped atomic.Bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/auth/csrf":
			http.SetCookie(w, &http.Cookie{Name: "__Host-admin_csrf_context", Value: csrf, Path: "/", Secure: true, HttpOnly: true})
			respond(w, map[string]any{"token": csrf})
			return
		case "POST /api/v1/auth/login", "POST /api/v1/auth/logout":
			cookie, err := r.Cookie("__Host-admin_csrf_context")
			if err != nil || cookie.Value != csrf || r.Header.Get("X-CSRF-Token") != csrf || r.Header.Get("Origin") != "https://"+r.Host {
				t.Error("missing CSRF headers or context cookie")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/login") {
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["email"] != email || body["password"] != password {
					t.Error("incorrect login request")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				active.Store(true)
				http.SetCookie(w, &http.Cookie{Name: "__Host-admin_session", Value: session, Path: "/", Secure: true, HttpOnly: true})
				respond(w, map[string]any{"audience": "admin"})
				return
			}
		}

		if cookie, err := r.Cookie("__Host-admin_session"); err != nil || cookie.Value != session || !active.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if rejectRead.Load() && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"code":"ACCESS_DENIED"}`)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/auth/logout":
			if rejectLogout.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			active.Store(false)
			w.WriteHeader(http.StatusNoContent)
		case "GET /runtime-config.json":
			limit := 100
			if capped.Load() {
				limit = 1
			}
			respond(w, map[string]any{"ui": map[string]int{"lookup_page_size": limit}})
		case "GET /api/v1/me":
			respond(w, map[string]any{"workspace": map[string]string{"id": workspaceID}})
		case "GET " + adminPath + "nodes":
			if r.URL.Query().Get("limit") != "100" {
				t.Error("node list did not request the contract page size")
			}
			items := []any{node}
			var next any = "second-page"
			if r.URL.Query().Get("cursor") == "second-page" {
				items, next = []any{second}, nil
			}
			respond(w, map[string]any{"items": items, "next_cursor": next})
		case "GET " + adminPath + "nodes/" + nodeID:
			respond(w, node)
		case "GET " + adminPath + "models":
			if r.URL.Query().Get("workspace_id") != workspaceID {
				t.Error("model list lost its workspace")
			}
			respond(w, map[string]any{"items": []any{model}})
		case "GET " + adminPath + "deployments":
			query := r.URL.Query()
			if query.Get("workspace_id") != workspaceID {
				t.Error("deployment list lost its workspace")
			}
			items := []any{deployment}
			if id := query.Get("model_id"); id != "" {
				if id != modelID || query.Get("retained") != "true" {
					t.Error("deployment name lookup used incorrect filters")
				}
				if ambiguous.Load() {
					items = append(items, map[string]string{"id": secondNodeID})
				}
			}
			respond(w, map[string]any{"items": items})
		case "GET " + adminPath + "deployments/" + deployID:
			respond(w, deployment)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	dir := privateDir(t)
	t.Setenv("OWNSOLID_CONFIG_DIR", dir)
	t.Setenv("OWNSOLID_SERVER", "")
	ca := filepath.Join(dir, "ca.pem")
	writeFile(t, ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600)
	writeConfig(t, dir, fileConfig{Server: server.URL, CAFile: ca, SiteID: "site-a", CellID: "cell-b"})
	passwordFile := filepath.Join(dir, "password-input")
	writeFile(t, passwordFile, []byte(password+"\r\n"), 0o600)

	success := func(args ...string) string {
		t.Helper()
		out, errOut, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		for _, secret := range []string{password, session, csrf} {
			if strings.Contains(out+errOut, secret) {
				t.Fatal("command output exposed a credential")
			}
		}
		return out
	}

	success("login", "--email", email, "--password-file", passwordFile)
	cfg, err := loadSettings("", false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()
	info, err := store.root.Stat(store.name)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("session file is missing or does not have mode 0600", err)
	}
	if saved, err := store.read(); err != nil || saved != session {
		t.Fatal("saved credential differs from the issued session", err)
	}

	out := success("node", "list")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || strings.Contains(out, "\t") || strings.Index(lines[0], "ID") != strings.Index(lines[1], nodeID) ||
		strings.Index(lines[1], nodeID) != strings.Index(lines[2], secondNodeID) {
		t.Fatalf("unaligned or incomplete table:\n%s", out)
	}
	out = success("node", "list", "-o", "json")
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 2 || got[1]["id"] != secondNodeID {
		t.Fatalf("unexpected JSON node list: %q (%v)", out, err)
	}
	out = success("node", "get", "gpu-01", "-o", "json")
	var detail map[string]any
	if err := json.Unmarshal([]byte(out), &detail); err != nil || detail["id"] != nodeID {
		t.Fatalf("unexpected node detail: %q (%v)", out, err)
	}
	out = success("model", "list", "-o", "yaml")
	var yamlRows []map[string]any
	if err := yaml.Unmarshal([]byte(out), &yamlRows); err != nil || len(yamlRows) != 1 || yamlRows[0]["name"] != "true" {
		t.Fatalf("unexpected YAML model list: %q (%v)", out, err)
	}
	if out = success("deployment", "list", "--workspace", workspaceID); !strings.Contains(out, deployID) {
		t.Fatal("deployment missing from table")
	}
	for _, selector := range []string{"true", deployID} {
		if out := success("deployment", "get", selector); !strings.Contains(out, deployID) {
			t.Fatal("deployment missing from detail")
		}
	}

	if out, _, err := run(t, "deployment", "get", deployID, "--workspace", secondNodeID); out != "" || err == nil || !strings.Contains(err.Error(), "outside the selected workspace") {
		t.Fatalf("deployment lookup escaped the selected workspace: stdout %q, error %v", out, err)
	}

	ambiguous.Store(true)
	if _, _, err := run(t, "deployment", "get", "true"); err == nil || !strings.Contains(err.Error(), "multiple deployments") {
		t.Fatalf("ambiguous deployment accepted: %v", err)
	}
	capped.Store(true)
	if _, _, err := run(t, "deployment", "get", "true"); err == nil || !strings.Contains(err.Error(), "lookup limit") {
		t.Fatalf("capped model lookup accepted: %v", err)
	}
	rejectRead.Store(true)
	if out, _, err := run(t, "node", "list", "-o", "json"); out != "" || err == nil || !strings.Contains(err.Error(), "ACCESS_DENIED") {
		t.Fatalf("permission refusal returned stdout %q, error %v", out, err)
	}
	rejectLogout.Store(true)
	if _, _, err := run(t, "logout"); err == nil || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("retryable logout returned %v", err)
	}
	if saved, err := store.read(); err != nil || saved != session {
		t.Fatal("logout failure lost the saved session")
	}
	rejectLogout.Store(false)
	success("logout")
	success("logout")
	if _, _, err := run(t, "node", "list"); err == nil || !strings.Contains(err.Error(), "ownsolid login") {
		t.Fatalf("read after logout returned %v", err)
	}
	if active.Load() {
		t.Fatal("the server-side session survived logout")
	}
}

func respond(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func TestCredentialOriginAndFileProtection(t *testing.T) {
	dir := privateDir(t)
	t.Setenv("OWNSOLID_CONFIG_DIR", dir)
	cfg, err := loadSettings("https://first.example.invalid", true)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()
	lock, err := store.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if otherLock, err := store.lock(); err == nil {
		otherLock.Close()
		t.Fatal("concurrent session writer was not refused")
	}
	value := token(t)
	if err := store.save(value); err != nil {
		t.Fatal(err)
	}
	cfg.origin, err = parseOrigin("https://second.example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	other, err := openSessionStore(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.root.Close()
	if saved, err := other.read(); err != nil || saved != "" {
		t.Fatal("credential accessible from another origin")
	}
	if err := store.root.Chmod(store.name, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.read(); err == nil {
		t.Fatal("accepted insecure credential mode")
	}
	if err := store.remove(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "private")
	writeFile(t, target, []byte(value), 0o600)
	if err := os.Symlink(target, filepath.Join(dir, store.name)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.read(); err == nil {
		t.Fatal("followed a symlink while reading a session")
	}
}

func TestConnectionRefusedReportsAddressAndRecovery(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	listener.Close()
	t.Setenv("OWNSOLID_CONFIG_DIR", privateDir(t))
	cfg, err := loadSettings(origin, true)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()
	if err := store.save(token(t)); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, "node", "list", "--server", origin)
	if out != "" || err == nil {
		t.Fatal("unreachable server did not fail cleanly")
	}
	for _, part := range []string{origin, "connection was refused", "Admin service is running", "retry"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("connection error %q is missing %q", err, part)
		}
	}
}

func TestRedirectDoesNotSendRequestsToAnotherOrigin(t *testing.T) {
	var redirected atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Store(true)
	}))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	t.Setenv("OWNSOLID_CONFIG_DIR", privateDir(t))
	cfg, err := loadSettings(server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	client, err := newClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.http.CloseIdleConnections()
	var data any
	err = client.get(t.Context(), authPath+"/csrf", nil, &data)
	if err == nil || !strings.Contains(err.Error(), "redirects are refused") || redirected.Load() {
		t.Fatalf("redirect was not safely refused: %v", err)
	}
}

func TestEmptyListAndTerminalEscaping(t *testing.T) {
	var out bytes.Buffer
	empty := []json.RawMessage{}
	if err := render(&out, "json", nodes.columns, empty, empty); err != nil || out.String() != "[]\n" {
		t.Fatalf("empty list JSON %q, error %v", out.String(), err)
	}
	out.Reset()
	row := json.RawMessage(`{"name":"gpu\u0007\t\n"}`)
	if err := writeTable(&out, nodes.columns, []json.RawMessage{row}); err != nil ||
		strings.ContainsAny(out.String(), "\a\t") || !strings.Contains(out.String(), `gpu\a\t\n`) {
		t.Fatalf("unsafe table output %q, error %v", out.String(), err)
	}
}

func TestTableAlignsWideNames(t *testing.T) {
	rows := []json.RawMessage{
		json.RawMessage(`{"name":"x","id":"1"}`),
		json.RawMessage(`{"name":"\u8282\u70b9","id":"2"}`),
		json.RawMessage(`{"name":"e\u0301","id":"3"}`),
	}
	var out bytes.Buffer
	if err := writeTable(&out, []column{{"NAME", "name"}, {"ID", "id"}}, rows); err != nil {
		t.Fatal(err)
	}
	want := "NAME  ID\nx     1\n\u8282\u70b9  2\ne\u0301     3\n"
	if out.String() != want {
		t.Fatalf("table = %q, want %q", out.String(), want)
	}
}
