package v1

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func testLimits() Limits { return Limits{CapsuleBytes: 262144, ManifestBytes: 8192} }
func testManifest() Manifest {
	return Manifest{PluginID: PluginID, Version: "1.0.0", ABI: ABI, Executor: Executor, Capabilities: []string{Capability}, License: License}
}
func fixtureKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, map[string]ed25519.PublicKey) {
	t.Helper()
	public, private, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := KeyID(public)
	return public, private, map[string]ed25519.PublicKey{id: public}
}
func TestSignedCapsuleRoundtripBindsPublisherDigestAndDeclaration(t *testing.T) {
	_, private, trust := fixtureKey(t)
	raw, e := Sign(testManifest(), private, testLimits())
	if e != nil {
		t.Fatal(e)
	}
	out, e := Verify(raw, Digest(raw), trust, testLimits())
	if e != nil || out.Manifest.PluginID != PluginID || out.Manifest.Capabilities[0] != Capability || out.Manifest.License != License || out.CapsuleSHA256 != Digest(raw) || out.ManifestSHA256 != Digest(out.Payload) || len(out.KeyID) != 64 {
		t.Fatal("actual signature/digest/declaration roundtrip", e)
	}
	original := out.Capsule[0]
	raw[0] ^= 1
	if out.Capsule[0] != original {
		t.Fatal("verified capsule retained caller-owned mutable input")
	}
}
func TestCapsuleTamperRefusesEvenWhenAttackerRecomputesPackageDigest(t *testing.T) {
	_, private, trust := fixtureKey(t)
	raw, e := Sign(testManifest(), private, testLimits())
	if e != nil {
		t.Fatal(e)
	}
	var envelope Capsule
	if json.Unmarshal(raw, &envelope) != nil {
		t.Fatal("fixture capsule")
	}
	payload, _ := base64.StdEncoding.DecodeString(envelope.Payload)
	payload = []byte(strings.Replace(string(payload), "1.0.0", "1.0.1", 1))
	envelope.Payload = base64.StdEncoding.EncodeToString(payload)
	changed, _ := json.Marshal(envelope)
	if _, e = Verify(changed, Digest(changed), trust, testLimits()); e == nil || e.Error() != "PLUGIN_SIGNATURE_INVALID" {
		t.Fatal("digest substituted for publisher authentication", e)
	}
	if _, e = Verify(raw, strings.Repeat("0", 64), trust, testLimits()); e == nil || e.Error() != "PLUGIN_PACKAGE_DIGEST_MISMATCH" {
		t.Fatal("declared digest not bound", e)
	}
}
func TestMalformedTrustAndSignatureNeverPanicOrAuthenticate(t *testing.T) {
	public, private, trust := fixtureKey(t)
	raw, e := Sign(testManifest(), private, testLimits())
	if e != nil {
		t.Fatal(e)
	}
	id, _ := KeyID(public)
	for _, key := range []ed25519.PublicKey{nil, make([]byte, 31), make([]byte, 32), make([]byte, 33)} {
		if _, e = Verify(raw, Digest(raw), map[string]ed25519.PublicKey{id: key}, testLimits()); e == nil {
			t.Fatal("malformed/mismatched trust authenticated")
		}
	}
	var envelope Capsule
	_ = json.Unmarshal(raw, &envelope)
	for _, signature := range []string{"", base64.StdEncoding.EncodeToString(make([]byte, 63)), base64.StdEncoding.EncodeToString(make([]byte, 64)), envelope.Signature + "\n"} {
		envelope.Signature = signature
		changed, _ := json.Marshal(envelope)
		if _, e = Verify(changed, Digest(changed), trust, testLimits()); e == nil {
			t.Fatal("invalid signature authenticated")
		}
	}
	for _, key := range []ed25519.PrivateKey{nil, make([]byte, 31), make([]byte, 64), make([]byte, 65)} {
		if _, e = Sign(testManifest(), key, testLimits()); e == nil {
			t.Fatal("invalid private representation accepted")
		}
	}
}
func TestValidPublisherSignatureCannotEnableUnknownCodeCapabilityOrNoncanonicalJSON(t *testing.T) {
	public, private, trust := fixtureKey(t)
	id, _ := KeyID(public)
	base, _ := json.Marshal(testManifest())
	signed := func(payload []byte) []byte {
		signature := ed25519.Sign(private, append([]byte(SignatureDomain), payload...))
		raw, _ := json.Marshal(Capsule{CapsuleSchemaVersion, id, base64.StdEncoding.EncodeToString(payload), base64.StdEncoding.EncodeToString(signature)})
		return raw
	}
	payloads := []string{
		strings.Replace(string(base), "model-directory-v1", "arbitrary-shell-v1", 1),
		strings.Replace(string(base), "models.list", "models.delete", 1),
		strings.Replace(string(base), "projectone.tools.v1", "projectone.tools.v2", 1),
		strings.Replace(string(base), `"version":"1.0.0"`, `"version":"1.0.0","code":"rm -rf /"`, 1),
		strings.Replace(string(base), `"plugin_id":`, `"Plugin_id":`, 1),
		strings.Replace(string(base), `"version":"1.0.0"`, `"version":"1.0.0","version":"1.0.1"`, 1),
		string(base) + "\n", strings.Replace(string(base), `"plugin_id":`, `"plugin_id": `, 1),
		strings.Replace(string(base), `"license":`+`"`+License+`"`, `"license":"\ud800"`, 1),
	}
	for i, payload := range payloads {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			raw := signed([]byte(payload))
			if _, e := Verify(raw, Digest(raw), trust, testLimits()); e == nil {
				t.Fatal("signed unsafe declaration accepted")
			}
		})
	}
	raw := signed(base)
	for _, changed := range [][]byte{append(append([]byte(nil), raw...), 10), []byte(strings.Replace(string(raw), `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1)), []byte(strings.Replace(string(raw), `"schema_version":1`, `"schema_version":null`, 1)), []byte(strings.Replace(string(raw), `"key_id":`, `"key_ID":`, 1)), []byte(strings.Replace(string(raw), `"signature":`, `"unknown":`, 1))} {
		if _, e := Verify(changed, Digest(changed), trust, testLimits()); e == nil {
			t.Fatal("noncanonical/ambiguous capsule accepted")
		}
	}
}
func TestCapsuleAndVersionBoundsAreExplicit(t *testing.T) {
	for _, value := range []string{"", "1", "1.0", "1.0.0.0", "01.0.0", "1.00.0", "1.0.-1", "1.0.0+build", "1.0.0-rc", "2.0.0", "1.10000.0", "1.０.0"} {
		if _, e := ParseVersion(value); e == nil {
			t.Fatal("unsupported version accepted", value)
		}
	}
	a, _ := ParseVersion("1.0.10")
	b, _ := ParseVersion("1.0.9")
	if CompareVersion(a, b) != 1 || CompareVersion(b, a) != -1 || CompareVersion(a, a) != 0 {
		t.Fatal("numeric version ordering")
	}
	_, private, trust := fixtureKey(t)
	raw, _ := Sign(testManifest(), private, testLimits())
	for _, limits := range []Limits{{0, 0}, {2047, 512}, {2048, 2048}, {MaximumCapsuleBytes + 1, 8192}, {262144, MaximumManifestBytes + 1}} {
		if _, e := Verify(raw, Digest(raw), trust, limits); e == nil {
			t.Fatal("invalid configured limit accepted")
		}
	}
	oversized := append(append([]byte(nil), raw...), []byte(strings.Repeat(" ", testLimits().CapsuleBytes))...)
	if _, e := Verify(oversized, Digest(oversized), trust, testLimits()); e == nil {
		t.Fatal("oversized capsule accepted")
	}
}
