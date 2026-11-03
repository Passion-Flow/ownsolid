package restorecompat

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const RestoreCompatibilityDomain = "projectone.offline.restore-compatibility.v1\x00"
const RestoreCompatibilitySchema = 22

type RestoreCompatibilityBinding struct {
	FormatVersion              int      `json:"format_version"`
	SourceScopeID              string   `json:"source_scope_id"`
	SourceBackupManifestSHA256 string   `json:"source_backup_manifest_sha256"`
	SourceReleaseID            string   `json:"source_release_id"`
	TargetScopeID              string   `json:"target_scope_id"`
	TargetManifestSHA256       string   `json:"target_manifest_sha256"`
	TargetReleaseID            string   `json:"target_release_id"`
	Profile                    string   `json:"profile"`
	SchemaVersion              int      `json:"schema_version"`
	StoreFormatVersion         int      `json:"store_format_version"`
	Stores                     []string `json:"stores"`
}

type RestoreCompatibility struct {
	FormatVersion int                         `json:"format_version"`
	Algorithm     string                      `json:"algorithm"`
	KeyID         string                      `json:"key_id"`
	Binding       RestoreCompatibilityBinding `json:"binding"`
	Signature     string                      `json:"signature"`
}

func (b RestoreCompatibilityBinding) Validate() error {
	identity := regexp.MustCompile(`^[a-f0-9]{32}$`)
	validRelease := func(v string) bool {
		return strings.TrimSpace(v) == v && v != "" && len(v) <= 128 && !strings.ContainsFunc(v, unicode.IsControl)
	}
	if b.FormatVersion != 1 || !identity.MatchString(b.SourceScopeID) || !identity.MatchString(b.TargetScopeID) || b.SourceScopeID == b.TargetScopeID || !wire.ValidSHA256(b.SourceBackupManifestSHA256) || !wire.ValidSHA256(b.TargetManifestSHA256) || !validRelease(b.SourceReleaseID) || !validRelease(b.TargetReleaseID) || b.SourceReleaseID == b.TargetReleaseID || b.Profile != wire.CumulativeCPUProfile || b.SchemaVersion != RestoreCompatibilitySchema || b.StoreFormatVersion != 1 || !slices.Equal(b.Stores, []string{"datasets", "models", "preparations", "training"}) {
		return errors.New("RELEASE_RESTORE_COMPATIBILITY_BINDING_INVALID")
	}
	return nil
}

func SignRestoreCompatibility(b RestoreCompatibilityBinding, key ed25519.PrivateKey) (RestoreCompatibility, error) {
	if b.Validate() != nil || len(key) != ed25519.PrivateKeySize || !bytes.Equal(ed25519.NewKeyFromSeed(key.Seed()), key) {
		return RestoreCompatibility{}, errors.New("RELEASE_RESTORE_COMPATIBILITY_SIGNING_INVALID")
	}
	raw, _ := json.Marshal(b)
	return RestoreCompatibility{FormatVersion: 1, Algorithm: wire.SignatureAlgorithm, KeyID: wire.Digest(key.Public().(ed25519.PublicKey)), Binding: b, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte(RestoreCompatibilityDomain), raw...)))}, nil
}

func VerifyRestoreCompatibility(c RestoreCompatibility, trust map[string]ed25519.PublicKey) error {
	key, ok := trust[c.KeyID]
	sig, e := base64.StdEncoding.Strict().DecodeString(c.Signature)
	if c.FormatVersion != 1 || c.Algorithm != wire.SignatureAlgorithm || c.Binding.Validate() != nil || !ok || len(key) != ed25519.PublicKeySize || wire.Digest(key) != c.KeyID || e != nil || len(sig) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(sig) != c.Signature {
		return errors.New("RELEASE_RESTORE_COMPATIBILITY_UNTRUSTED_OR_INVALID")
	}
	raw, _ := json.Marshal(c.Binding)
	if !ed25519.Verify(key, append([]byte(RestoreCompatibilityDomain), raw...), sig) {
		return errors.New("RELEASE_RESTORE_COMPATIBILITY_UNTRUSTED_OR_INVALID")
	}
	return nil
}
