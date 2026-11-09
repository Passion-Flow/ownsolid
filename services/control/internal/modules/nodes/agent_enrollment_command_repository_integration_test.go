//go:build repository_integration && linux

package nodes_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	nodehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentca"
	"github.com/google/uuid"
)

// TestActualAgentEnrollmentCommand runs a separately built Agent against the real node
// HTTPS handler and PostgreSQL repository. It has no installation or systemd dependency.
func TestActualAgentEnrollmentCommand(t *testing.T) {
	binary := os.Getenv("OWNSOLID_TEST_AGENT_BINARY")
	if binary == "" {
		t.Skip("an explicit local Agent executable is required")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("the Agent executable must be an absolute path")
	}
	x := newNodeReadActual(t)
	if x.f.Store.Dialect() != transaction.PostgreSQL {
		t.Skip("the Agent enrollment fixture requires PostgreSQL")
	}
	x.owner.HMACKey = make([]byte, security.MinimumHMACKeyBytes)
	if _, err := rand.Read(x.owner.HMACKey); err != nil {
		t.Fatal(err)
	}
	authority := agentca.Authority(t)
	x.s.Certificates = authority
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(authority.Bundle())) {
		t.Fatal("invalid fixture agent CA")
	}
	handler := nodehttp.Handler{Service: x.s}
	var enrollments atomic.Int32
	beats := make(chan string, 8)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scoped := "/agent/v1" + wire.LocalScope().Path() + "/nodes/"
		var heartbeat string
		switch {
		case r.URL.Path == "/agent/v1/enroll":
			enrollments.Add(1)
		case strings.HasPrefix(r.URL.Path, scoped) && strings.HasSuffix(r.URL.Path, "/heartbeat"):
			rest := strings.TrimPrefix(r.URL.Path, scoped)
			heartbeat = strings.TrimSuffix(rest, "/heartbeat")
			r.URL.Path = "/agent/v1/nodes/" + rest
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		request := uuid.NewString()
		data, status, err := handler.Machine(r, request)
		if err != nil {
			t.Logf("Nodes HTTPS refusal: %s", httpserver.PublicErrorCode(err))
			httpserver.Problem(w, request, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err == nil && heartbeat != "" {
			select {
			case beats <- heartbeat:
			default:
			}
		}
	}))
	server.TLS = &tls.Config{ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	root := t.TempDir()
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ca := filepath.Join(root, "control-ca.pem")
	write(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	wrongCA := filepath.Join(root, "unrelated-ca.pem")
	unrelated, _ := agentca.Material(t)
	write(wrongCA, unrelated)
	prepare := func(name, trust, token string) (string, string, string) {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		config, tokenFile := filepath.Join(dir, "agent.json"), filepath.Join(dir, "join-token")
		if token != "" {
			write(tokenFile, []byte(token+"\n"))
		}
		raw, err := json.Marshal(map[string]string{"control_origin": server.URL, "ca_file": trust, "state_directory": dir, "disk_path": "/", "join_token_file": tokenFile})
		if err != nil {
			t.Fatal(err)
		}
		write(config, raw)
		return dir, config, tokenFile
	}
	invoke := func(config, code string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(x.ctx, 8*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, binary, "enroll", "--if-needed", "--config", config).CombinedOutput()
		if code == "" && (err != nil || len(out) != 0) || code != "" && (err == nil || strings.TrimSpace(string(out)) != code) {
			t.Fatalf("Agent enrollment did not return the expected status %q (%v)", code, err)
		}
	}
	assertToken := func(token nodes.MintedToken, consumed bool) {
		t.Helper()
		var got bool
		if err := x.f.PostgreSQL.QueryRow(x.ctx, `SELECT consumed_at IS NOT NULL AND node_id IS NOT NULL FROM node_join_tokens WHERE id=$1`, token.ID).Scan(&got); err != nil || got != consumed {
			t.Fatal("unexpected persisted token state", err)
		}
	}
	assertNoIdentity := func(dir string) {
		t.Helper()
		if _, err := os.Stat(filepath.Join(dir, "identity.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("refused enrollment published a local identity")
		}
	}

	token := nodeEnrollmentMint(t, x, x.f.Workspace, "Agent command fresh node", wire.ReadOnlyProfile)
	wrong, config, _ := prepare("wrong-ca", wrongCA, token.Token)
	invoke(config, "CONTROL_CONNECTION_FAILED")
	assertNoIdentity(wrong)
	assertToken(token, false)
	if enrollments.Load() != 0 {
		t.Fatal("token reached a server with an untrusted certificate")
	}
	expired := nodeEnrollmentMint(t, x, x.f.Workspace, "Agent command expired token", wire.ReadOnlyProfile)
	x.exec(t, `UPDATE node_join_tokens SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, "", expired.ID)
	invalid, config, _ := prepare("expired", ca, expired.Token)
	invoke(config, "TOKEN_UNUSABLE")
	assertNoIdentity(invalid)
	assertToken(expired, false)

	dir, config, tokenFile := prepare("fresh", ca, token.Token)
	invoke(config, "")
	assertToken(token, true)
	private := func(file string) []byte {
		t.Helper()
		path := filepath.Join(dir, file)
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("enrollment state is not private", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	identity, certificate := private("identity.json"), private("certificate.json")
	var stored struct {
		NodeID string `json:"node_id"`
	}
	if json.Unmarshal(identity, &stored) != nil || stored.NodeID == "" {
		t.Fatal("enrollment did not store a node identity")
	}
	if err := os.Remove(tokenFile); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(x.ctx, 12*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, binary, "run", "--config", config)
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = process.Wait() }()
	select {
	case id := <-beats:
		if id != stored.NodeID {
			t.Fatal("Agent reported a different node")
		}
	case <-ctx.Done():
		t.Fatal("enrolled Agent did not complete a scoped heartbeat")
	}
	invoke(config, "")
	if enrollments.Load() != 2 || !bytes.Equal(identity, private("identity.json")) || !bytes.Equal(certificate, private("certificate.json")) {
		t.Fatal("a repeated command spent a token or changed the running node's state")
	}
}
