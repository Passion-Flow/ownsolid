// Package v1 defines the bounded manufacturer-only declarative tool capsule.
package v1

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const (
	CapsuleSchemaVersion    = 1
	PluginID                = "projectone.model-directory"
	ABI                     = "projectone.tools.v1"
	Executor                = "model-directory-v1"
	Capability              = "models.list"
	License                 = "Project-one-Platform-License"
	SignatureDomain         = "projectone.plugin.v1\x00"
	DefaultCapsuleBytes     = 262144
	DefaultManifestBytes    = 8192
	MaximumCapsuleBytes     = 1048576
	MaximumManifestBytes    = 16384
	MaximumVersionComponent = 9999
)

type Limits struct{ CapsuleBytes, ManifestBytes int }

func (l Limits) Validate() error {
	if l.CapsuleBytes < 2048 || l.CapsuleBytes > MaximumCapsuleBytes || l.ManifestBytes < 512 || l.ManifestBytes > MaximumManifestBytes || l.ManifestBytes >= l.CapsuleBytes {
		return failure("PLUGIN_PACKAGE_LIMIT_INVALID")
	}
	return nil
}

type Manifest struct {
	PluginID     string   `json:"plugin_id"`
	Version      string   `json:"version"`
	ABI          string   `json:"abi"`
	Executor     string   `json:"executor"`
	Capabilities []string `json:"capabilities"`
	License      string   `json:"license"`
}
type Capsule struct {
	SchemaVersion int    `json:"schema_version"`
	KeyID         string `json:"key_id"`
	Payload       string `json:"payload"`
	Signature     string `json:"signature"`
}
type Verified struct {
	Manifest                             Manifest
	KeyID, CapsuleSHA256, ManifestSHA256 string
	Capsule, Payload                     []byte
}
type Version [3]int

func failure(code string) error { return errors.New(code) }
func ParseVersion(value string) (Version, error) {
	var out Version
	parts := strings.Split(value, ".")
	if len(parts) != len(out) {
		return out, failure("PLUGIN_VERSION_INVALID")
	}
	for i, part := range parts {
		if len(part) < 1 || len(part) > 4 || len(part) > 1 && part[0] == '0' {
			return out, failure("PLUGIN_VERSION_INVALID")
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return out, failure("PLUGIN_VERSION_INVALID")
			}
		}
		n, e := strconv.Atoi(part)
		if e != nil || n > MaximumVersionComponent {
			return out, failure("PLUGIN_VERSION_INVALID")
		}
		out[i] = n
	}
	if out[0] != 1 {
		return out, failure("PLUGIN_VERSION_UNSUPPORTED")
	}
	return out, nil
}
func CompareVersion(a, b Version) int {
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
func ValidateManifest(m Manifest) error {
	if _, e := ParseVersion(m.Version); e != nil {
		return e
	}
	if m.PluginID != PluginID || m.ABI != ABI || m.Executor != Executor || len(m.Capabilities) != 1 || m.Capabilities[0] != Capability || m.License != License {
		return failure("PLUGIN_CAPABILITY_UNSUPPORTED")
	}
	return nil
}
func Digest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func KeyID(public ed25519.PublicKey) (string, error) {
	if len(public) != ed25519.PublicKeySize {
		return "", failure("PLUGIN_TRUST_KEY_INVALID")
	}
	return Digest(public), nil
}
func canonicalBase64(value string, maximum int) ([]byte, error) {
	if len(value) > base64.StdEncoding.EncodedLen(maximum) {
		return nil, failure("PLUGIN_PACKAGE_INVALID")
	}
	raw, e := base64.StdEncoding.Strict().DecodeString(value)
	if e != nil || len(raw) > maximum || base64.StdEncoding.EncodeToString(raw) != value {
		return nil, failure("PLUGIN_PACKAGE_INVALID")
	}
	return raw, nil
}
func DecodeManifest(raw []byte, limits Limits) (Manifest, error) {
	var out Manifest
	if limits.Validate() != nil || len(raw) < 1 || len(raw) > limits.ManifestBytes || strict.DecodeStrict(raw, &out) != nil {
		return out, failure("PLUGIN_PACKAGE_INVALID")
	}
	if e := ValidateManifest(out); e != nil {
		return out, e
	}
	canonical, e := json.Marshal(out)
	if e != nil || !bytes.Equal(canonical, raw) {
		return out, failure("PLUGIN_PACKAGE_INVALID")
	}
	return out, nil
}
func Sign(m Manifest, private ed25519.PrivateKey, limits Limits) ([]byte, error) {
	if limits.Validate() != nil || len(private) != ed25519.PrivateKeySize {
		return nil, failure("PLUGIN_SIGNING_KEY_INVALID")
	}
	if !bytes.Equal(ed25519.NewKeyFromSeed(private[:ed25519.SeedSize]), private) {
		return nil, failure("PLUGIN_SIGNING_KEY_INVALID")
	}
	if e := ValidateManifest(m); e != nil {
		return nil, e
	}
	payload, e := json.Marshal(m)
	if e != nil || len(payload) > limits.ManifestBytes {
		return nil, failure("PLUGIN_PACKAGE_INVALID")
	}
	public := private.Public().(ed25519.PublicKey)
	id, _ := KeyID(public)
	signed := ed25519.Sign(private, append([]byte(SignatureDomain), payload...))
	raw, e := json.Marshal(Capsule{CapsuleSchemaVersion, id, base64.StdEncoding.EncodeToString(payload), base64.StdEncoding.EncodeToString(signed)})
	if e != nil || len(raw) > limits.CapsuleBytes {
		return nil, failure("PLUGIN_PACKAGE_INVALID")
	}
	return raw, nil
}
func Verify(raw []byte, expectedSHA256 string, trusted map[string]ed25519.PublicKey, limits Limits) (Verified, error) {
	var out Verified
	if limits.Validate() != nil || len(raw) < 1 || len(raw) > limits.CapsuleBytes {
		return out, failure("PLUGIN_PACKAGE_INVALID")
	}
	if len(expectedSHA256) != 64 || Digest(raw) != expectedSHA256 {
		return out, failure("PLUGIN_PACKAGE_DIGEST_MISMATCH")
	}
	var capsule Capsule
	if strict.DecodeStrict(raw, &capsule) != nil || capsule.SchemaVersion != CapsuleSchemaVersion {
		return out, failure("PLUGIN_PACKAGE_INVALID")
	}
	canonical, e := json.Marshal(capsule)
	if e != nil || !bytes.Equal(canonical, raw) {
		return out, failure("PLUGIN_PACKAGE_INVALID")
	}
	public, ok := trusted[capsule.KeyID]
	id, e := KeyID(public)
	if !ok || e != nil || id != capsule.KeyID {
		return out, failure("PLUGIN_TRUST_KEY_INVALID")
	}
	payload, e := canonicalBase64(capsule.Payload, limits.ManifestBytes)
	if e != nil {
		return out, e
	}
	signature, e := canonicalBase64(capsule.Signature, ed25519.SignatureSize)
	if e != nil || len(signature) != ed25519.SignatureSize {
		return out, failure("PLUGIN_SIGNATURE_INVALID")
	}
	if !ed25519.Verify(public, append([]byte(SignatureDomain), payload...), signature) {
		return out, failure("PLUGIN_SIGNATURE_INVALID")
	}
	manifest, e := DecodeManifest(payload, limits)
	if e != nil {
		return out, e
	}
	out = Verified{Manifest: manifest, KeyID: id, CapsuleSHA256: expectedSHA256, ManifestSHA256: Digest(payload), Capsule: append([]byte(nil), raw...), Payload: append([]byte(nil), payload...)}
	return out, nil
}
