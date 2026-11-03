package restorecompat

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"strings"
	"testing"
)

func testBinding() RestoreCompatibilityBinding {
	return RestoreCompatibilityBinding{FormatVersion: 1, SourceScopeID: strings.Repeat("a", 32), SourceBackupManifestSHA256: strings.Repeat("b", 64), SourceReleaseID: "source", TargetScopeID: strings.Repeat("c", 32), TargetManifestSHA256: strings.Repeat("d", 64), TargetReleaseID: "target", Profile: wire.CumulativeCPUProfile, SchemaVersion: 22, StoreFormatVersion: 1, Stores: []string{"datasets", "models", "preparations", "training"}}
}

func TestCompatibilityExactSignatureAndTrust(t *testing.T) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{47}, 32))
	a, e := SignRestoreCompatibility(testBinding(), key)
	if e != nil {
		t.Fatal(e)
	}
	trust := map[string]ed25519.PublicKey{a.KeyID: key.Public().(ed25519.PublicKey)}
	if VerifyRestoreCompatibility(a, trust) != nil {
		t.Fatal("valid authorization refused")
	}
	for name, change := range map[string]func(*RestoreCompatibility){
		"source-backup":   func(v *RestoreCompatibility) { v.Binding.SourceBackupManifestSHA256 = strings.Repeat("e", 64) },
		"target-manifest": func(v *RestoreCompatibility) { v.Binding.TargetManifestSHA256 = strings.Repeat("e", 64) },
		"target-scope":    func(v *RestoreCompatibility) { v.Binding.TargetScopeID = strings.Repeat("e", 32) },
		"key":             func(v *RestoreCompatibility) { v.KeyID = strings.Repeat("e", 64) },
		"signature":       func(v *RestoreCompatibility) { v.Signature = base64.StdEncoding.EncodeToString(make([]byte, 64)) },
		"algorithm":       func(v *RestoreCompatibility) { v.Algorithm = "none" },
		"schema":          func(v *RestoreCompatibility) { v.Binding.SchemaVersion = 21 },
		"stores":          func(v *RestoreCompatibility) { v.Binding.Stores = []string{"models"} },
	} {
		t.Run(name, func(t *testing.T) {
			v := a
			change(&v)
			if VerifyRestoreCompatibility(v, trust) == nil {
				t.Fatal("changed signed authority accepted")
			}
		})
	}
	raw, _ := json.Marshal(a.Binding)
	a.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, raw))
	if VerifyRestoreCompatibility(a, trust) == nil {
		t.Fatal("release-independent signature domain accepted")
	}
	if _, e = SignRestoreCompatibility(testBinding(), nil); e == nil {
		t.Fatal("missing private key accepted")
	}
}

func TestCompatibilityClosedBinding(t *testing.T) {
	for name, change := range map[string]func(*RestoreCompatibilityBinding){"same-scope": func(v *RestoreCompatibilityBinding) { v.TargetScopeID = v.SourceScopeID }, "same-release": func(v *RestoreCompatibilityBinding) { v.TargetReleaseID = v.SourceReleaseID }, "profile": func(v *RestoreCompatibilityBinding) { v.Profile = "fixture" }, "store-version": func(v *RestoreCompatibilityBinding) { v.StoreFormatVersion = 2 }, "control-text": func(v *RestoreCompatibilityBinding) { v.SourceReleaseID = "source\n" }} {
		t.Run(name, func(t *testing.T) {
			b := testBinding()
			change(&b)
			if b.Validate() == nil {
				t.Fatal("invalid binding accepted")
			}
		})
	}
}
