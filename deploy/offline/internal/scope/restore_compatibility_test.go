package scope

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	compat "github.com/Passion-Flow/ownsolid/api/release/restorecompat"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Actual private files and journal, with existing Engine doubles. These controls
// establish signature/plan custody only, never successful data restoration.
func TestRestoreCompatibilityPrivatePlanReverifiesWithoutCertificate(t *testing.T) {
	c, l, r, source, target := restoreReservationFixture(t)
	defer l.close()
	l.marker.Profile = wire.CumulativeCPUProfile
	source.Profile = l.marker.Profile
	source.ReleaseID = "reviewed-source-release"
	source.Source.SchemaVersion = 22
	source.Stores = []string{"datasets", "models", "preparations", "training"}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{63}, 32))
	id := wire.Digest(key.Public().(ed25519.PublicKey))
	trust := encode(struct {
		Keys map[string]string `json:"keys"`
	}{map[string]string{id: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))}})
	if e := os.WriteFile(c.Verification.TrustFile, trust, 0600); e != nil {
		t.Fatal(e)
	}
	b := compat.RestoreCompatibilityBinding{FormatVersion: 1, SourceScopeID: source.SourceScopeID, SourceBackupManifestSHA256: source.ManifestSHA256, SourceReleaseID: source.ReleaseID, TargetScopeID: l.marker.ScopeID, TargetManifestSHA256: l.marker.ManifestSHA256, TargetReleaseID: l.marker.ReleaseID, Profile: source.Profile, SchemaVersion: 22, StoreFormatVersion: 1, Stores: source.Stores}
	a, e := compat.SignRestoreCompatibility(b, key)
	if e != nil {
		t.Fatal(e)
	}
	r.CompatibilityFile = filepath.Join(c.ScopeRoot, "test-compatibility.json")
	if e = os.WriteFile(r.CompatibilityFile, encode(a), 0600); e != nil {
		t.Fatal(e)
	}
	p, e := l.readRestoreCompatibility(r.CompatibilityFile, source)
	if e != nil {
		t.Fatal(e)
	}
	if restoreTargetIdentity(l.marker, source) == nil {
		t.Fatal("ordinary different-release guard lost")
	}
	if restoreTargetIdentityAuthorized(l.marker, source, p) != nil {
		t.Fatal("verified exact authorization refused")
	}
	for name, change := range map[string]func(*RestoreSourceResult){"backup": func(v *RestoreSourceResult) { v.ManifestSHA256 = strings.Repeat("e", 64) }, "release": func(v *RestoreSourceResult) { v.ReleaseID = "other" }, "scope": func(v *RestoreSourceResult) { v.SourceScopeID = strings.Repeat("e", 32) }, "schema": func(v *RestoreSourceResult) { v.Source.SchemaVersion = 21 }, "stores": func(v *RestoreSourceResult) { v.Stores = []string{"models"} }} {
		t.Run(name, func(t *testing.T) {
			s := source
			change(&s)
			if restoreTargetIdentityAuthorized(l.marker, s, p) == nil {
				t.Fatal("different binding accepted")
			}
		})
	}
	unverified := &restoreCompatibilityProof{Authorization: a}
	if restoreTargetIdentityAuthorized(l.marker, source, unverified) == nil || restoreTargetIdentityAuthorized(l.marker, source, nil, p) == nil {
		t.Fatal("unverified or ambiguous authorization accepted")
	}
	target.Stores = source.Stores
	target.CompatibilitySHA256 = wire.Digest(encode(p))
	target.BindingSHA256 = ""
	target.BindingSHA256 = wire.Digest(encode(target))
	if _, e = l.reserveRestore(context.Background(), r, source, target, "prepare-restore", func(plan RestorePlan) error { return writeBackupFile(l.root, restorePlanFile, encode(plan)) }, p); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(r.CompatibilityFile); e != nil {
		t.Fatal(e)
	}
	plan, e := l.readBoundRestorePlan()
	if e != nil || plan.Compatibility == nil || !plan.Compatibility.verified {
		t.Fatal("durable signed plan requires removed original file", e)
	}
	if proof, e := l.restoreReservationCompatibility(r, source); e != nil || proof == nil || !proof.verified {
		t.Fatal("recorded reservation requires removed original certificate", e)
	}
	changed := source
	changed.ManifestSHA256 = strings.Repeat("e", 64)
	if _, e := l.restoreReservationCompatibility(r, changed); e == nil {
		t.Fatal("recorded reservation adopted different backup")
	}
	before, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if e != nil {
		t.Fatal(e)
	}
	plan.Compatibility.Authorization.Binding.TargetManifestSHA256 = strings.Repeat("e", 64)
	if l.validRestorePlan(plan) {
		t.Fatal("changed certificate accepted")
	}
	if e = os.WriteFile(c.Verification.TrustFile, []byte("{\"keys\":{}}"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = l.readBoundRestorePlan(); e == nil {
		t.Fatal("removed trust accepted")
	}
	after, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("readonly refusal mutated journal", e)
	}
}

func TestRestoreCompatibilityOmissionKeepsHistoricalPlanBytes(t *testing.T) {
	_, l, r, source, target := restoreReservationFixture(t)
	defer l.close()
	p := RestorePlan{FormatVersion: 1, AttemptID: strings.Repeat("a", 32), Request: r, Source: source, Target: target, RequiredStages: restoreRequiredStages()}
	raw := encode(p)
	if bytes.Contains(raw, []byte("compatibility")) || !l.validRestorePlan(p) {
		t.Fatal("ordinary historical plan encoding/validation changed")
	}
	p.Request.CompatibilityFile = "/synthetic/unverified.json"
	if l.validRestorePlan(p) {
		t.Fatal("certificate reference without signed proof accepted")
	}
}
