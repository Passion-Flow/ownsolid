package install

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

type agentSystemdFixture func(context.Context, string, ...string) (string, error)

func (f agentSystemdFixture) Run(ctx context.Context, name string, args ...string) (string, error) {
	return f(ctx, name, args...)
}

// TestAgentSignedMediaLinux exercises the signed installer with a native Agent
// and private HTTPS. The supplied parent must be owned by root and isolated.
func TestAgentSignedMediaLinux(t *testing.T) {
	inputs := os.Getenv("OWNSOLID_AGENT_INSTALL_FIXTURE")
	if inputs == "" {
		t.Skip("requires signed Agent media and a private Linux namespace")
	}
	if os.Geteuid() != 0 {
		t.Fatal("fixture needs root to drop to a service UID")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	put := func(path string, data []byte, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{27}, 32))
	credential := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{61}, 32))
	var requests atomic.Int32
	var failure atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var request agent.Enrollment
		if r.Method != http.MethodPost || r.URL.Path != "/agent/v1/enroll" ||
			json.NewDecoder(r.Body).Decode(&request) != nil || request.JoinToken != token || request.CertificateRequest == "" ||
			request.Handshake.Facts.Architecture != "amd64" || request.Handshake.ProtocolVersion != agent.ProfileVersion(agent.ReadOnlyProfile) {
			t.Error("invalid native enrollment request")
			http.Error(w, "bad fixture request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch failure.Load() {
		case 1:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"TOKEN_UNUSABLE"}`))
		case 2:
			w.Header().Set("Content-Type", "application/problem+json")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Request-ID", "11111111-1111-4111-8111-111111111111")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"type":"about:blank","title":"STORAGE_COMMIT_OUTCOME_UNKNOWN","status":409,"code":"STORAGE_COMMIT_OUTCOME_UNKNOWN","request_id":"11111111-1111-4111-8111-111111111111"}`))
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": agent.Enrolled{
				NodeID: "27e4f1af-0016-45f8-a810-268ca6081169", SiteID: "default", CellID: "default",
				Credential: credential, ProtocolVersion: agent.ProfileVersion(agent.ReadOnlyProfile),
				HeartbeatSeconds: agent.MinHeartbeatSeconds,
			}})
		}
	}))
	defer server.Close()
	ca := filepath.Join(inputs, "control.pem")
	put(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600)
	sourceToken := filepath.Join(inputs, "site-token")
	put(sourceToken, []byte(token+"\n"), 0o600)
	config := filepath.Join(inputs, "site.json")
	writeConfig := func(source string) {
		t.Helper()
		data, err := json.Marshal(agentSite{FormatVersion: 1, ControlOrigin: server.URL, CAFile: ca, JoinTokenFile: source})
		if err != nil {
			t.Fatal(err)
		}
		put(config, data, 0o600)
	}
	writeConfig(sourceToken)
	policy, err := LoadBundleVerification(filepath.Join(inputs, "verification.json"))
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(policy.MountRoot, "ownsolid-agent.tar.gz")
	owner := agentOwner{65534, 65534}
	createHost := func(t *testing.T) (agentHost, *[]string, *bool) {
		t.Helper()
		root, err := os.MkdirTemp(inputs, "isolated-host-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
		if err := os.Chmod(root, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, subdir := range []string{"etc/systemd/system", "opt", "var/lib"} {
			if err := os.MkdirAll(filepath.Join(root, subdir), 0o755); err != nil {
				t.Fatal(err)
			}
			// A protected outer umask must not prevent the service UID from traversing test parents.
			for path := filepath.Join(root, subdir); path != root; path = filepath.Dir(path) {
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
		}
		var commands []string
		var rejectStart bool
		host := agentHost{root: root, account: func(context.Context) (agentOwner, error) { return owner, nil }}
		host.runner = agentSystemdFixture(func(_ context.Context, name string, args ...string) (string, error) {
			commands = append(commands, name+" "+strings.Join(args, " "))
			if _, err := os.Stat(filepath.Join(root, "var/lib/ownsolid-agent", agentIdentity)); err != nil {
				t.Error("systemd called before successful Agent enrollment")
			}
			if _, err := os.Lstat(filepath.Join(root, "etc/ownsolid-agent", agentTokenName)); !errors.Is(err, os.ErrNotExist) {
				t.Error("systemd called before installed token cleanup")
			}
			if rejectStart && name == "systemctl" && args[0] == "enable" {
				return "", errors.New("fixture systemd refusal")
			}
			return "", nil
		})
		return host, &commands, &rejectStart
	}
	wantError := func(t *testing.T, err error, code string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), code) || strings.Contains(err.Error(), token) {
			t.Fatalf("expected %s, got %v", code, err)
		}
	}
	t.Run("wrong trust and modified media leave the host untouched", func(t *testing.T) {
		host, commands, _ := createHost(t)
		host.account = func(context.Context) (agentOwner, error) {
			t.Fatal("unverified media reached account provisioning")
			return owner, nil
		}
		wrong := policy
		wrong.TrustFile = filepath.Join(inputs, "wrong-trust.json")
		wantError(t, host.install(ctx, config, archive, wrong, &bytes.Buffer{}), "RELEASE_SIGNATURE_UNTRUSTED_OR_INVALID")
		wantError(t, host.install(ctx, config, filepath.Join(policy.MountRoot, "tampered.tar.gz"), policy, &bytes.Buffer{}), "RELEASE_ASSET_HASH_INVALID")
		if _, err := bundle.InspectCurrent(ctx, policy, archive); err == nil {
			t.Fatal("Compose verifier accepted Agent media")
		}
		if _, err := os.Stat(filepath.Join(host.root, "opt", agentAccount)); !errors.Is(err, os.ErrNotExist) || len(*commands) != 0 {
			t.Fatal("invalid media modified the host")
		}
	})
	t.Run("a protected token is required", func(t *testing.T) {
		host, commands, _ := createHost(t)
		if err := os.Chmod(sourceToken, 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(sourceToken, 0o600)
		wantError(t, host.install(ctx, config, archive, policy, &bytes.Buffer{}), "INSTALL_AGENT_TOKEN_INVALID")
		if requests.Load() != 0 || len(*commands) != 0 {
			t.Fatal("unsafe token reached Control or systemd")
		}
	})
	t.Run("definite refusal can retry but uncertainty fences a new request", func(t *testing.T) {
		host, commands, _ := createHost(t)
		failure.Store(1)
		wantError(t, host.install(ctx, config, archive, policy, &bytes.Buffer{}), "INSTALL_AGENT_TOKEN_UNUSABLE")
		tokenPath := filepath.Join(host.root, "etc/ownsolid-agent", agentTokenName)
		if _, err := os.Lstat(tokenPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("a definitively rejected token copy remained")
		}
		failure.Store(2)
		wantError(t, host.install(ctx, config, archive, policy, &bytes.Buffer{}), "INSTALL_AGENT_ENROLLMENT_UNRESOLVED: reconcile with Control before retrying (STORAGE_COMMIT_OUTCOME_UNKNOWN request_id=11111111-1111-4111-8111-111111111111)")
		wantError(t, host.install(ctx, config, archive, policy, &bytes.Buffer{}), "INSTALL_AGENT_ENROLLMENT_UNRESOLVED")
		info, err := os.Lstat(tokenPath)
		if err != nil || info.Mode() != 0o600 || !owner.owns(info) || requests.Load() != 2 || len(*commands) != 0 {
			t.Fatal("uncertain enrollment was replayed, started or lost its private token")
		}
	})
	t.Run("concurrent install refuses, then native enrollment and token-free start retry", func(t *testing.T) {
		failure.Store(0)
		host, commands, rejectStart := createHost(t)
		entered, release := make(chan struct{}), make(chan struct{})
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		host.account = func(context.Context) (agentOwner, error) {
			close(entered)
			<-release
			return owner, nil
		}
		first := make(chan error, 1)
		*rejectStart = true
		go func() { first <- host.install(ctx, config, archive, policy, &bytes.Buffer{}) }()
		select {
		case <-entered:
		case err := <-first:
			t.Fatalf("installer did not acquire its lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		wantError(t, host.install(ctx, config, archive, policy, &bytes.Buffer{}), "INSTALL_AGENT_BUSY")
		close(release)
		wantError(t, <-first, "INSTALL_AGENT_SERVICE_START_FAILED")
		host.account = func(context.Context) (agentOwner, error) { return owner, nil }
		*rejectStart = false
		writeConfig("")
		var output bytes.Buffer
		if err := host.install(ctx, config, archive, policy, &output); err != nil {
			t.Fatal(err)
		}
		wantCommands := []string{"systemctl daemon-reload", "systemctl enable --now ownsolid-agent.service"}
		if requests.Load() != 3 || !reflect.DeepEqual(*commands, append(wantCommands, wantCommands...)) ||
			!strings.Contains(output.String(), "Agent enrollment verified and service started") {
			t.Fatalf("unexpected replay result: requests %d, commands %v", requests.Load(), *commands)
		}
		for _, path := range []string{"etc/ownsolid-agent/agent.json", "etc/ownsolid-agent/ca.pem", "var/lib/ownsolid-agent/identity.json"} {
			info, err := os.Lstat(filepath.Join(host.root, path))
			if err != nil || info.Mode() != 0o600 || !owner.owns(info) {
				t.Fatalf("%s is not private to the service account", path)
			}
		}
		original, err := os.ReadFile(sourceToken)
		if err != nil || string(original) != token+"\n" {
			t.Fatal("original site token was modified")
		}
		t.Log("native Agent enrolled once, preserved its private identity, and replayed after a systemd refusal")
	})
}
