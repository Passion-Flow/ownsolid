package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	compat "github.com/Passion-Flow/ownsolid/api/release/restorecompat"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestIssuerExclusivePrivateReadbackAndRefusals(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{73}, 32))
	b := compat.RestoreCompatibilityBinding{FormatVersion: 1, SourceScopeID: strings.Repeat("a", 32), SourceBackupManifestSHA256: strings.Repeat("b", 64), SourceReleaseID: "source", TargetScopeID: strings.Repeat("c", 32), TargetManifestSHA256: strings.Repeat("d", 64), TargetReleaseID: "target", Profile: wire.CumulativeCPUProfile, SchemaVersion: 22, StoreFormatVersion: 1, Stores: []string{"datasets", "models", "preparations", "training"}}
	raw, _ := json.Marshal(b)
	binding := filepath.Join(root, "binding.json")
	keyFile := filepath.Join(root, "private-key")
	output := filepath.Join(root, "certificate.json")
	for name, body := range map[string][]byte{binding: raw, keyFile: []byte(base64.StdEncoding.EncodeToString(key))} {
		if e = os.WriteFile(name, body, 0600); e != nil {
			t.Fatal(e)
		}
	}
	args := []string{"--binding-file", binding, "--private-key-file", keyFile, "--output-file", output}
	var log bytes.Buffer
	if e = run(args, &log); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(log.String(), base64.StdEncoding.EncodeToString(key)) {
		t.Fatal("signing key leaked")
	}
	raw, e = os.ReadFile(output)
	if e != nil {
		t.Fatal(e)
	}
	var a compat.RestoreCompatibility
	if wire.StrictJSON(raw, &a) != nil || compat.VerifyRestoreCompatibility(a, map[string]ed25519.PublicKey{wire.Digest(key.Public().(ed25519.PublicKey)): key.Public().(ed25519.PublicKey)}) != nil {
		t.Fatal("readback not verifiable")
	}
	info, e := os.Stat(output)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("output not private", e)
	}
	if run(args, &log) == nil {
		t.Fatal("occupied output overwritten")
	}
	after, _ := os.ReadFile(output)
	if !bytes.Equal(raw, after) {
		t.Fatal("failed repeat changed output")
	}
	for _, mode := range []os.FileMode{0644, 0666} {
		if e = os.Chmod(keyFile, mode); e != nil {
			t.Fatal(e)
		}
		args[len(args)-1] = filepath.Join(root, "refused.json")
		if run(args, &log) == nil {
			t.Fatal("unprotected key accepted")
		}
	}
	if e = os.Chmod(keyFile, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString(make([]byte, 64))), 0600); e != nil {
		t.Fatal(e)
	}
	if run(args, &log) == nil {
		t.Fatal("noncanonical private key accepted")
	}
}
