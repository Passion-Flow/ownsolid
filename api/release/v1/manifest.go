// Package release defines signed offline inventory semantics, independent of installation.
package release

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

const PlatformProfile = "ubuntu24.04-linux-amd64"
const FixtureProfile = "test-fixture-linux-amd64"
const SignatureAlgorithm = "Ed25519"
const LicenseCapability = "M19"
const LicenseSchemaVersion = 17
const LicenseTrustAssetKind = "license_trust"
const LicenseTrustAssetID = "platform-license-trust"
const LicenseValidationAssetID = "license-validation"

//go:embed factory-defaults.json
var FactoryDefaults []byte

//go:embed operator-defaults.json
var OperatorDefaults []byte

//go:embed engine-profile.json
var EngineProfile []byte

type Asset struct {
	ID                string   `json:"id"`
	Path              string   `json:"path"`
	Kind              string   `json:"kind"`
	Size              int64    `json:"size_bytes"`
	SHA256            string   `json:"sha256"`
	Licenses          []string `json:"licenses"`
	ImageID           string   `json:"image_id,omitempty"`
	ImageConfigDigest string   `json:"image_config_digest,omitempty"`
	Architecture      string   `json:"architecture,omitempty"`
}
type Manifest struct {
	Cumulative         *CumulativeSelection `json:"cumulative,omitempty"`
	FormatVersion      int                  `json:"format_version"`
	ReleaseID          string               `json:"release_id"`
	Profile            string               `json:"profile"`
	SourceSHA256       string               `json:"source_sha256"`
	SchemaVersion      int                  `json:"schema_version"`
	MinimumPredecessor int                  `json:"minimum_predecessor_schema"`
	MaximumPredecessor int                  `json:"maximum_predecessor_schema"`
	Capabilities       []string             `json:"capabilities"`
	Prerequisites      []string             `json:"prerequisites"`
	Assets             []Asset              `json:"assets"`
}
type Signature struct {
	Algorithm      string `json:"algorithm"`
	KeyID          string `json:"key_id"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Value          string `json:"signature"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,95}$`)
var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Digest(b []byte) string        { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func ValidSHA256(value string) bool { return digest.MatchString(value) }
func SafePath(s string) bool {
	if s == "" || len(s) > 240 || path.Clean(s) != s || strings.HasPrefix(s, "/") || s == "." || strings.ContainsAny(s, "\\:\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == ".." || !identifier.MatchString(part) {
			return false
		}
	}
	first := strings.SplitN(s, "/", 2)[0]
	return first != "manifest.json" && first != "signature.json"
}

// StrictJSON rejects duplicate keys at every depth and unknown first-party fields.
func StrictJSON(raw []byte, out any) error {
	return decodeJSON(raw, out, false, true)
}

// UniqueJSON retains third-party OCI extensibility and nullable configuration fields.
func UniqueJSON(raw []byte, out any) error { return decodeJSON(raw, out, true, false) }
func decodeJSON(raw []byte, out any, allowNull, strict bool) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 64 {
			return errors.New("RELEASE_JSON_DEPTH_INVALID")
		}
		t, e := d.Token()
		if e != nil {
			return errors.New("RELEASE_JSON_INVALID")
		}
		if t == nil && !allowNull {
			return errors.New("RELEASE_JSON_NULL_INVALID")
		}
		switch t {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				v, e := d.Token()
				key, ok := v.(string)
				if e != nil || !ok || seen[key] {
					return errors.New("RELEASE_JSON_DUPLICATE_KEY")
				}
				seen[key] = true
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
			v, e := d.Token()
			if e != nil || v != json.Delim('}') {
				return errors.New("RELEASE_JSON_INVALID")
			}
		case json.Delim('['):
			for d.More() {
				if e := walk(depth + 1); e != nil {
					return e
				}
			}
			v, e := d.Token()
			if e != nil || v != json.Delim(']') {
				return errors.New("RELEASE_JSON_INVALID")
			}
		case json.Delim('}'), json.Delim(']'):
			return errors.New("RELEASE_JSON_INVALID")
		}
		return nil
	}
	if e := walk(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return errors.New("RELEASE_JSON_TRAILING_DATA")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if e := decoder.Decode(out); e != nil {
		return errors.New("RELEASE_JSON_FIELDS_INVALID")
	}
	return nil
}

func (m Manifest) Validate(maximumAssets int, maximumAsset, maximumTotal int64, allowFixture bool) error {
	if m.FormatVersion != 1 || !identifier.MatchString(m.ReleaseID) || !digest.MatchString(m.SourceSHA256) || m.SchemaVersion < 1 || m.MinimumPredecessor < 1 || m.MaximumPredecessor < m.MinimumPredecessor || m.MaximumPredecessor > m.SchemaVersion {
		return errors.New("RELEASE_MANIFEST_INVALID")
	}
	fixture := m.Profile == FixtureProfile
	if m.Profile != PlatformProfile && !IsCumulativeCPUProfile(m.Profile) && (!fixture || !allowFixture) {
		return errors.New("RELEASE_PROFILE_UNSUPPORTED")
	}
	if err := m.validateCumulative(); err != nil {
		return err
	}
	if len(m.Assets) < 1 || len(m.Assets) > maximumAssets {
		return errors.New("RELEASE_ASSET_COUNT_INVALID")
	}
	ids := map[string]Asset{}
	paths := map[string]bool{}
	total := int64(0)
	kinds := map[string]bool{"image_archive": true, "license": true, LicenseTrustAssetKind: true, "deb": true, "compose": true, "operator": true, "config_schema": true, "config_example": true, "locale": true, "model": true, "plugin": true, "dependency_lock": true, "source_evidence": true, "runbook": true, "runtime_inputs": true}
	for _, a := range m.Assets {
		if !identifier.MatchString(a.ID) || !SafePath(a.Path) || paths[a.Path] || ids[a.ID].ID != "" || !kinds[a.Kind] || a.Size < 1 || a.Size > maximumAsset || !digest.MatchString(a.SHA256) || a.Size > maximumTotal-total {
			return errors.New("RELEASE_ASSET_INVALID")
		}
		if a.Kind == "image_archive" {
			if !validImageArchitecture(a) || !strings.HasPrefix(a.ImageID, "sha256:") || !digest.MatchString(strings.TrimPrefix(a.ImageID, "sha256:")) || !strings.HasPrefix(a.ImageConfigDigest, "sha256:") || !digest.MatchString(strings.TrimPrefix(a.ImageConfigDigest, "sha256:")) {
				return errors.New("RELEASE_IMAGE_IDENTITY_INVALID")
			}
		} else if a.ImageID != "" || a.ImageConfigDigest != "" || a.Architecture != "" {
			return errors.New("RELEASE_ASSET_IMAGE_FIELDS_INVALID")
		}
		ids[a.ID] = a
		paths[a.Path] = true
		total += a.Size
	}
	for name := range paths {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if paths[parent] {
				return errors.New("RELEASE_ASSET_PATH_CONFLICT")
			}
		}
	}
	for _, a := range m.Assets {
		if a.Kind == "license" {
			if len(a.Licenses) != 0 {
				return errors.New("RELEASE_LICENSE_INVALID")
			}
			continue
		}
		if len(a.Licenses) == 0 {
			return errors.New("RELEASE_LICENSE_MISSING")
		}
		seen := map[string]bool{}
		for _, id := range a.Licenses {
			if ids[id].Kind != "license" || seen[id] {
				return errors.New("RELEASE_LICENSE_INVALID")
			}
			seen[id] = true
		}
	}
	capabilities := map[string]bool{}
	for _, c := range m.Capabilities {
		if capabilities[c] || !regexp.MustCompile(`^M(0[1-9]|[12][0-9]|30)$`).MatchString(c) {
			return errors.New("RELEASE_CAPABILITY_INVALID")
		}
		capabilities[c] = true
	}
	if capabilities[LicenseCapability] && m.SchemaVersion < LicenseSchemaVersion {
		return errors.New("RELEASE_LICENSE_SCHEMA_REQUIRED")
	}
	if capabilities[PrivateClusterCapability] && m.SchemaVersion < PrivateClusterSchemaVersion {
		return errors.New("RELEASE_PRIVATE_CLUSTER_SCHEMA_REQUIRED")
	}
	if fixture {
		return nil
	}
	lastCapability := 16
	if IsCumulativeCPUProfile(m.Profile) {
		lastCapability = len(m.Capabilities)
	}
	if m.Profile == CumulativeCacheCPUProfile {
		// The selected cache slice does not activate intermediate M18-M22 owners.
		lastCapability = 17
	}
	if capabilities[LicenseCapability] {
		lastCapability = 19
	}
	if m.Profile == MVPPostgreSQLCPUProfile {
		lastCapability = 30
	}
	for i := 1; i <= lastCapability; i++ {
		if !capabilities[fmt.Sprintf("M%02d", i)] {
			return errors.New("RELEASE_CAPABILITY_MISSING")
		}
	}
	for id, kind := range m.RequiredAssets() {
		if ids[id].Kind != kind {
			return errors.New("RELEASE_REQUIRED_ASSET_MISSING")
		}
	}
	if len(m.Prerequisites) == 0 {
		return errors.New("RELEASE_PREREQUISITES_MISSING")
	}
	for _, p := range m.Prerequisites {
		if len(p) < 1 || len(p) > 1024 || strings.ContainsAny(p, "\x00\r\n") {
			return errors.New("RELEASE_PREREQUISITE_INVALID")
		}
	}
	return nil
}

// RequiredAssets binds additional requirements to the exact signed capability
// declaration without relabeling historical profiles or mutating shared maps.
func (m Manifest) RequiredAssets() map[string]string {
	required := RequiredAssets(m.Profile)
	if m.Profile == PlatformProfile {
		for _, capability := range m.Capabilities {
			if capability == LicenseCapability {
				required[LicenseTrustAssetID] = LicenseTrustAssetKind
				required[LicenseValidationAssetID] = "operator"
			}
			if capability == PrivateClusterCapability {
				required[PrivateClusterProbeAMD64AssetID] = "image_archive"
			}
		}
	}
	return required
}

// RequiredAssets returns a fresh immutable-profile requirement map, never a user override.
func RequiredAssets(profile string) map[string]string {
	if profile == MVPPostgreSQLCPUProfile {
		return mvpPostgreSQLCPURequiredAssets()
	}
	if profile == CumulativeKubernetesCPUProfile {
		return kubernetesRequiredAssets()
	}
	if IsCumulativeCPUProfile(profile) {
		return cumulativeRequiredAssets()
	}
	if profile != PlatformProfile {
		return map[string]string{}
	}
	return map[string]string{"control-image": "image_archive", "gateway-image": "image_archive", "agent-image": "image_archive", "training-image": "image_archive", "postgres-image": "image_archive", "compose": "compose", "operator": "operator", "runtime-config-schema": "config_schema", "configuration-example": "config_example", "locales": "locale", "base-model": "model", "embedding-model": "model", "builtin-plugin": "plugin", "os-packages-lock": "dependency_lock", "docker-engine": "deb", "docker-cli": "deb", "containerd": "deb", "compose-plugin": "deb", "source-evidence": "source_evidence", "operator-runbook": "runbook", "platform-license": "license"}
}
