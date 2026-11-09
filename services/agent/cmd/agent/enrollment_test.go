package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	agent "github.com/Passion-Flow/ownsolid/services/agent/internal/runtime"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
)

func TestEnrollIfNeededKeepsMatchingLegacyIdentity(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stateDirectory := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	origin := "https://127.0.0.1:1"
	identity := state.Identity{
		NodeID: "00000000-0000-0000-0000-000000000001", SiteID: wire.DefaultSiteID, CellID: wire.DefaultCellID,
		ControlOrigin: origin, ProtocolVersion: wire.Version, Credential: base64.RawURLEncoding.EncodeToString(secret[:]),
	}
	if err := state.SaveNew(stateDirectory, identity); err != nil {
		t.Fatal(err)
	}
	identityFile := filepath.Join(stateDirectory, "identity.json")
	before, err := state.ProtectedFile(identityFile)
	if err != nil {
		t.Fatal(err)
	}
	config := agent.Config{ControlOrigin: origin, StateDirectory: stateDirectory, DiskPath: "/", JoinTokenFile: filepath.Join(dir, "missing-token")}
	configFile := filepath.Join(dir, "agent.json")
	writeConfig := func() {
		t.Helper()
		body, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configFile, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig()
	args := []string{"enroll", "--if-needed", "--config", configFile}
	lock, err := state.Lock(stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	replayErr := execute(t.Context(), args)
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if replayErr != nil {
		t.Fatal("matching installed identity could not be recognized while the Agent holds its lock", replayErr)
	}
	if _, found, err := state.LoadCertificate(stateDirectory); err != nil || found {
		t.Fatal("replay changed legacy certificate state", err)
	}
	if err := execute(t.Context(), []string{"enroll", "--config", configFile}); err == nil || err.Error() != "IDENTITY_ALREADY_EXISTS" {
		t.Fatal("bare enroll no longer refuses a completed identity", err)
	}
	config.ControlOrigin = "https://127.0.0.1:2"
	writeConfig()
	if err := execute(t.Context(), args); err == nil || err.Error() != "IDENTITY_INVALID" {
		t.Fatal("changed origin accepted", err)
	}
	config.ControlOrigin = origin
	writeConfig()
	if err := state.SaveCertificate(stateDirectory, state.Certificate{PrivateKey: "invalid fixture key"}); err != nil {
		t.Fatal(err)
	}
	if err := execute(t.Context(), args); err == nil || err.Error() != "CERTIFICATE_STATE_INVALID" {
		t.Fatal("invalid key-only certificate state accepted", err)
	}
	after, err := state.ProtectedFile(identityFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("identity was rewritten", err)
	}
	if err := execute(t.Context(), []string{"run", "--if-needed", "--config", configFile}); err == nil || err.Error() != "AGENT_ARGUMENT_INVALID" {
		t.Fatal("another command accepted enrollment replay mode", err)
	}
}
