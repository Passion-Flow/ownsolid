package distribution_test

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/google/uuid"
)

func TestLaneDGatewayHistoryTLS(t *testing.T) {
	binary := os.Getenv("LANE_D_CLI_BINARY")
	if binary == "" {
		t.Fatal("set LANE_D_CLI_BINARY to the compiled CLI")
	}
	c := setup(t)
	a := c.access(t)
	workspace, key := a.alias.WorkspaceID, a.key.Key.ID
	admit := func() string {
		t.Helper()
		input := a.input()
		if _, err := c.s.Admit(t.Context(), a.gateway.Gateway.ID, *a.gateway.Secret, input, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		return input.RequestID
	}
	report := func(outcome wire.Outcome) {
		t.Helper()
		if _, err := c.s.Report(t.Context(), a.gateway.Gateway.ID, *a.gateway.Secret, outcome, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	unreported := admit()
	cancelled := admit()
	code := "client_cancelled"
	report(wire.Outcome{RequestID: cancelled, Status: code, ErrorCode: &code, ElapsedMilliseconds: 23})
	known := success(admit())
	known.Usage = &wire.Usage{PromptTokens: 11, CompletionTokens: 0, TotalTokens: 11}
	report(known)
	unused, err := c.s.MintKey(t.Context(), c.p, keyInput(a.alias), uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	tenant, foreign := uuid.NewString(), uuid.NewString()
	if _, err := c.s.DB.Exec(t.Context(), "INSERT INTO tenants(id,name,status) VALUES($1,'Foreign history tenant','active')", tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := c.s.DB.Exec(t.Context(), "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign history workspace','active')", foreign, tenant); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	c.s.Identity.Config.Server.AdminOrigin = "https://" + server.Listener.Addr().String()
	handler := transport.New(c.s.Identity)
	if err := handler.LicenseInitializationError(); err != nil {
		t.Fatal(err)
	}
	if err := handler.Licensing.Service.VerifyStored(t.Context()); err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.StartTLS()

	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OWNSOLID_CONFIG_DIR", dir)
	t.Setenv("OWNSOLID_SERVER", server.URL)
	ca, password := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "password")
	config, err := json.Marshal(map[string]string{"ca_file": ca})
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string][]byte{
		ca:       pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}),
		password: []byte(c.password), filepath.Join(dir, "config.json"): config,
	} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}

	run := func(args ...string) ([]byte, string, error) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), binary, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.Output()
		for _, secret := range []string{c.password, *a.gateway.Secret, *a.key.Secret, *unused.Secret} {
			if bytes.Contains(stdout, []byte(secret)) || strings.Contains(stderr.String(), secret) {
				t.Fatal("CLI exposed a credential")
			}
		}
		return stdout, stderr.String(), err
	}
	ok := func(args ...string) []byte {
		t.Helper()
		out, stderr, err := run(args...)
		if err != nil || stderr != "" {
			t.Fatalf("CLI command failed: %v (%s)", err, stderr)
		}
		return out
	}
	refused := func(want string, args ...string) {
		t.Helper()
		out, stderr, err := run(args...)
		if len(out) != 0 || err == nil || !strings.Contains(stderr, want) {
			t.Fatalf("CLI did not fail closed with %s: %v (%s)", want, err, stderr)
		}
	}
	list := func(key string, cursor *string, output string) []string {
		args := []string{"gateway", "request", "list", "--limit", "1", "--api-key-id", key, "--output", output}
		if cursor != nil {
			args = append(args, "--cursor", *cursor)
		}
		return args
	}
	readPage := func(raw []byte) distribution.Page[distribution.RequestRecord] {
		t.Helper()
		var page distribution.Page[distribution.RequestRecord]
		if err := json.Unmarshal(raw, &page); err != nil || page.Items == nil {
			t.Fatalf("invalid CLI page: %v", err)
		}
		return page
	}

	refused("no saved Admin session", list(key, nil, "json")...)
	ok("login", "--email", "admin@example.invalid", "--password-file", password)
	seen := map[string]bool{unreported: false, cancelled: false, known.RequestID: false}
	var cursor *string
	for i := 0; i < len(seen); i++ {
		args := list(key, cursor, "json")
		raw := ok(args...)
		if !bytes.Equal(raw, ok(args...)) {
			t.Fatal("the unchanged page differed on retry")
		}
		page := readPage(raw)
		if len(page.Items) != 1 || (page.NextCursor == nil) != (i == len(seen)-1) {
			t.Fatal("incorrect page size or continuation")
		}
		row := page.Items[0]
		visited, expected := seen[row.ID]
		if !expected || visited || row.APIKeyID != key || row.WorkspaceID != workspace {
			t.Fatal("unexpected scope or duplicated request")
		}
		seen[row.ID] = true
		if i == 0 {
			table := string(ok(list(key, nil, "table")...))
			if !strings.Contains(table, row.ID) || !strings.Contains(table, *page.NextCursor) {
				t.Fatal("table hid the request or continuation")
			}
			refused("INVALID_CURSOR", list(unused.Key.ID, page.NextCursor, "json")...)
		}
		cursor = page.NextCursor
	}
	if page := readPage(ok(list(unused.Key.ID, nil, "json")...)); len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatal("unused key did not return an empty final page")
	}

	detail := func(id string) *wire.Outcome {
		t.Helper()
		var row distribution.RequestRecord
		if err := json.Unmarshal(ok("gateway", "request", "get", id, "--output", "json"), &row); err != nil || row.ID != id || row.WorkspaceID != workspace {
			t.Fatalf("unexpected scoped request detail: %v", err)
		}
		return row.Outcome
	}
	if detail(unreported) != nil {
		t.Fatal("detail invented a missing receipt")
	}
	if outcome := detail(cancelled); outcome == nil || outcome.Usage != nil || outcome.Status != code {
		t.Fatal("detail changed the cancelled receipt or manufactured usage")
	}
	if outcome := detail(known.RequestID); outcome == nil || outcome.Usage == nil || *outcome.Usage != *known.Usage {
		t.Fatal("detail changed a measured zero or the receipt total")
	}
	refused("must be a lowercase UUID", "gateway", "request", "get", "wrong-id")
	refused("RESOURCE_NOT_FOUND", "gateway", "request", "get", known.RequestID, "--workspace", foreign)
	refused("RESOURCE_NOT_FOUND", append(list(key, nil, "json"), "--workspace", foreign)...)

	if _, err := c.s.DB.Exec(t.Context(), "UPDATE gateway_requests SET outcome=jsonb_set(outcome,'{elapsed_milliseconds}','99') WHERE id=$1", known.RequestID); err != nil {
		t.Fatal(err)
	}
	refused("STORAGE_UNAVAILABLE", "gateway", "request", "get", known.RequestID)
	refused("STORAGE_UNAVAILABLE", "gateway", "request", "list", "--api-key-id", key, "--limit", "100")
	ok("logout")
}
