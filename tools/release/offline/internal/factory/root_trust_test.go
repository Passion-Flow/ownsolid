package factory

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func rootTestKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func rootPublicKeys(keys ...ed25519.PrivateKey) map[string]string {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		public := key.Public().(ed25519.PublicKey)
		out[wire.Digest(public)] = base64.StdEncoding.EncodeToString(public)
	}
	return out
}

func rootTestMetadata(version uint64, expiry time.Time, release ed25519.PrivateKey, roots ...ed25519.PrivateKey) []byte {
	return Encode(rootTrust{FormatVersion: rootTrustFormatVersion, Version: version, Expires: expiry,
		Threshold: len(roots), RootKeys: rootPublicKeys(roots...), ReleaseKeys: rootPublicKeys(release)})
}

func rootTestSignatures(candidate []byte, keys ...ed25519.PrivateKey) []RootSignature {
	var out []RootSignature
	for _, key := range keys {
		public := key.Public().(ed25519.PublicKey)
		out = append(out, RootSignature{KeyID: wire.Digest(public),
			Value: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte(rootTrustDomain), candidate...)))})
	}
	return out
}

func TestTrustRotationRequiresBothThresholdsAndSuccessorVersion(t *testing.T) {
	now := time.Now().UTC()
	oldA, oldB, next, unrelated := rootTestKey(t), rootTestKey(t), rootTestKey(t), rootTestKey(t)
	release := rootTestKey(t)
	current := rootTestMetadata(1, now.Add(time.Hour), release, oldA, oldB)
	candidate := rootTestMetadata(2, now.Add(time.Hour), release, next)
	for _, test := range []struct {
		name, code string
		keys       []ed25519.PrivateKey
	}{
		{"missing old threshold", "RELEASE_TRUST_ROOT_THRESHOLD_NOT_MET", []ed25519.PrivateKey{oldA, next}},
		{"missing new threshold", "RELEASE_TRUST_ROOT_THRESHOLD_NOT_MET", []ed25519.PrivateKey{oldA, oldB}},
		{"duplicate", "RELEASE_TRUST_ROOT_DUPLICATE_SIGNATURE", []ed25519.PrivateKey{oldA, oldA, next}},
		{"unknown", "RELEASE_TRUST_ROOT_UNKNOWN_SIGNER", []ed25519.PrivateKey{oldA, oldB, unrelated, next}},
		{"valid", "", []ed25519.PrivateKey{oldA, oldB, next}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := verifyRootTransition(current, candidate, rootTestSignatures(candidate, test.keys...))
			if ErrorCode(err) != test.code {
				t.Fatalf("want %s, got %v", test.code, err)
			}
		})
	}
	if _, err := verifyRootTransition(current, append(bytes.Clone(candidate), '\n'), rootTestSignatures(candidate, oldA, oldB, next)); ErrorCode(err) != "RELEASE_TRUST_ROOT_SIGNATURE_INVALID" {
		t.Fatalf("accepted signatures over different root bytes: %v", err)
	}
	skipped := rootTestMetadata(3, now.Add(time.Hour), release, next)
	if _, err := verifyRootTransition(current, skipped, rootTestSignatures(skipped, oldA, oldB, next)); ErrorCode(err) != "RELEASE_TRUST_ROOT_VERSION_NOT_SUCCESSOR" {
		t.Fatalf("accepted skipped root version: %v", err)
	}
}

func TestTrustRotationKeepsThePinnedFileUntilAnUnexpiredChainVerifies(t *testing.T) {
	now := time.Now().UTC()
	one, two, three, release := rootTestKey(t), rootTestKey(t), rootTestKey(t), rootTestKey(t)
	current := rootTestMetadata(1, now.Add(-2*time.Hour), release, one)
	intermediate := rootTestMetadata(2, now.Add(-time.Hour), release, two)
	target := rootTestMetadata(3, now.Add(time.Hour), release, three)
	if _, err := releaseTrustKeys(current, now); ErrorCode(err) != "RELEASE_TRUST_ROOT_EXPIRED" {
		t.Fatalf("accepted expired pinned root: %v", err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	trust := filepath.Join(dir, "root.json")
	put(t, trust, current)
	first := RootUpdate{Candidate: filepath.Join(dir, "root-2.json"), Signatures: filepath.Join(dir, "signatures-2.json")}
	second := RootUpdate{Candidate: filepath.Join(dir, "root-3.json"), Signatures: filepath.Join(dir, "signatures-3.json")}
	put(t, first.Candidate, intermediate)
	put(t, first.Signatures, Encode(rootTestSignatures(intermediate, one, two)))
	put(t, second.Candidate, target)
	put(t, second.Signatures, Encode(rootTestSignatures(target, two, three)))
	if _, err := RotateRoot(trust, []RootUpdate{first}, now); ErrorCode(err) != "RELEASE_TRUST_ROOT_EXPIRED" {
		t.Fatalf("accepted an expired final root: %v", err)
	}
	if raw, err := os.ReadFile(trust); err != nil || !bytes.Equal(raw, current) {
		t.Fatalf("failed rotation changed trust: %v", err)
	}
	if version, err := RotateRoot(trust, []RootUpdate{first, second}, now); err != nil || version != 3 {
		t.Fatalf("rotation chain failed: %d, %v", version, err)
	}
	if raw, err := os.ReadFile(trust); err != nil || !bytes.Equal(raw, target) {
		t.Fatalf("did not persist exact successor: %v", err)
	}
	if _, err := RotateRoot(trust, []RootUpdate{first, second}, now); ErrorCode(err) != "RELEASE_TRUST_ROOT_VERSION_NOT_SUCCESSOR" {
		t.Fatalf("accepted a replay: %v", err)
	}
}

// TestCurrentRootVerifiesARealOfflineArchive uses the ephemeral release signer
// and archive produced by TestManufactureCurrentRelease.
func TestCurrentRootVerifiesARealOfflineArchive(t *testing.T) {
	archive, signer := os.Getenv("OWNSOLID_CURRENT_ROOT_ARCHIVE"), os.Getenv("OWNSOLID_CURRENT_ROOT_SIGNER")
	if archive == "" || signer == "" {
		t.Skip("needs a signed integration archive and an isolated Linux network namespace")
	}
	ifaces, err := net.Interfaces()
	if err != nil || len(ifaces) != 1 || ifaces[0].Flags&net.FlagLoopback == 0 {
		t.Fatalf("test namespace has an uplink: %v, %v", ifaces, err)
	}
	release, err := readReleaseSigningKey(signer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	trust := filepath.Join(dir, "root.json")
	currentKey, nextKey, unknown := rootTestKey(t), rootTestKey(t), rootTestKey(t)
	put(t, trust, rootTestMetadata(1, now.Add(time.Hour), unknown, currentKey))
	c, err := LoadConfig([]byte("{}"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c = WithCurrentProfile(c)
	for c.MountRoot = filepath.Dir(archive); mounted(c.MountRoot) != nil; c.MountRoot = filepath.Dir(c.MountRoot) {
		if c.MountRoot == "/" {
			t.Fatal("archive is not on a real mount")
		}
	}
	c.TrustFile = trust
	if _, err := Inspect(t.Context(), c, archive); ErrorCode(err) != "RELEASE_SIGNATURE_UNTRUSTED_OR_INVALID" {
		t.Fatalf("old release policy accepted an unknown signer: %v", err)
	}
	next := rootTestMetadata(2, now.Add(time.Hour), release, nextKey)
	update := RootUpdate{Candidate: filepath.Join(dir, "root-2.json"), Signatures: filepath.Join(dir, "signatures-2.json")}
	put(t, update.Candidate, next)
	var signatures []RootSignature
	for n, key := range []ed25519.PrivateKey{currentKey, nextKey} {
		path := filepath.Join(dir, []string{"old-root.key", "new-root.key"}[n])
		put(t, path, []byte(base64.StdEncoding.EncodeToString(key)))
		sig, err := SignRoot(update.Candidate, path)
		if err != nil {
			t.Fatal(err)
		}
		signatures = append(signatures, sig)
	}
	put(t, update.Signatures, Encode(signatures))
	if version, err := RotateRoot(trust, []RootUpdate{update}, now); err != nil || version != 2 {
		t.Fatalf("rotate trust: %d, %v", version, err)
	}
	result, err := Inspect(t.Context(), c, archive)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("root version 2 verified release %s (%s) with no network interface beyond loopback", result.ReleaseID, result.ManifestSHA256)
	expired := filepath.Join(dir, "expired-root.json")
	put(t, expired, rootTestMetadata(2, now.Add(-time.Hour), release, nextKey))
	c.TrustFile = expired
	if _, err := Inspect(t.Context(), c, archive); ErrorCode(err) != "RELEASE_TRUST_ROOT_EXPIRED" {
		t.Fatalf("accepted expired root: %v", err)
	}
}
