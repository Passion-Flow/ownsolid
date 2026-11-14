// Package registryconfig owns the typed private registry configuration boundary.
package registryconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Config is injected at the component boundary. No request can override these values.
// Load accepts the protected JSON configuration file selected by deployment wiring.
type Config struct {
	Endpoint                 string              `json:"endpoint"`
	AllowedCIDRs             []string            `json:"allowed_cidrs"`
	CAFile                   string              `json:"ca_file"`
	CredentialFile           string              `json:"credential_file"`
	StoreRoot                string              `json:"store_root"`
	PreparedReleaseRoot      string              `json:"prepared_release_root"`
	ReleaseArchiveSHA256     string              `json:"release_archive_sha256"`
	ReleaseManifestSHA256    string              `json:"release_manifest_sha256"`
	Verification             bundle.Verification `json:"verification"`
	MaximumArchiveBytes      int64               `json:"maximum_archive_bytes"`
	MaximumExpandedBytes     int64               `json:"maximum_expanded_bytes"`
	MaximumBlobBytes         int64               `json:"maximum_blob_bytes"`
	MaximumRetainedBytes     int64               `json:"maximum_retained_bytes"`
	MaximumPins              int                 `json:"maximum_pins"`
	MaximumReferences        int                 `json:"maximum_references"`
	MaximumConcurrentImports int                 `json:"maximum_concurrent_imports"`
	MinimumHeadroomPercent   int                 `json:"minimum_headroom_percent"`
	OperationSeconds         int                 `json:"operation_seconds"`
	RequestSeconds           int                 `json:"request_seconds"`
	GrantSeconds             int                 `json:"grant_seconds"`
	PageSize                 int                 `json:"page_size"`
}

// MaximumConfigurationBytes is the existing private JSON document wire bound.
const MaximumConfigurationBytes int64 = 1 << 20

func Defaults() Config {
	return Config{Verification: bundle.Defaults(), MaximumArchiveBytes: 64 << 30, MaximumExpandedBytes: 64 << 30, MaximumBlobBytes: 32 << 30, MaximumRetainedBytes: 128 << 30, MaximumPins: 64, MaximumReferences: 4096, MaximumConcurrentImports: 1, MinimumHeadroomPercent: 30, OperationSeconds: 3600, RequestSeconds: 120, GrantSeconds: 900, PageSize: 50}
}
func Load(filename string) (Config, error) {
	raw, e := bundle.ReadProtectedConfiguration(filename)
	if e != nil {
		return Config{}, failure("REGISTRY_CONFIGURATION_REQUIRED")
	}
	return Decode(raw)
}

// Decode validates supplied bytes without opening paths, credentials, mounts or
// providers. The caller owns immutable input admission; legacy Load deliberately
// retains its existing separate file-reader strategy.
func Decode(raw []byte) (Config, error) {
	if len(raw) == 0 || int64(len(raw)) > MaximumConfigurationBytes {
		return Config{}, failure("REGISTRY_CONFIGURATION_INVALID")
	}
	c := Defaults()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(&c); e != nil {
		return Config{}, failure("REGISTRY_CONFIGURATION_INVALID")
	}
	// StrictJSON also rejects duplicate keys, null values and trailing documents.
	if release.StrictJSON(raw, &c) != nil || c.Validate() != nil {
		return Config{}, failure("REGISTRY_CONFIGURATION_INVALID")
	}
	return c, nil
}
func (c Config) Validate() error {
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.Hostname() == "" || strings.Contains(u.Host, "%") {
		return failure("REGISTRY_CONFIGURATION_INVALID")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return failure("REGISTRY_CONFIGURATION_INVALID")
		}
	}
	if len(c.AllowedCIDRs) == 0 {
		return failure("REGISTRY_CONFIGURATION_INVALID")
	}
	for _, v := range c.AllowedCIDRs {
		p, e := netip.ParsePrefix(v)
		if e != nil || p != p.Masked() || !privatePrefix(p) {
			return failure("REGISTRY_CONFIGURATION_INVALID")
		}
	}
	for _, v := range []string{c.CAFile, c.CredentialFile, c.StoreRoot, c.PreparedReleaseRoot, c.Verification.MountRoot, c.Verification.TrustFile} {
		if !filepath.IsAbs(v) || filepath.Clean(v) != v {
			return failure("REGISTRY_CONFIGURATION_INVALID")
		}
	}
	if !withinPath(c.Verification.MountRoot, c.StoreRoot) || !withinPath(c.Verification.MountRoot, c.PreparedReleaseRoot) || c.StoreRoot == c.PreparedReleaseRoot || withinPath(c.StoreRoot, c.PreparedReleaseRoot) || withinPath(c.PreparedReleaseRoot, c.StoreRoot) || !release.ValidSHA256(c.ReleaseArchiveSHA256) || !release.ValidSHA256(c.ReleaseManifestSHA256) || c.Verification.Validate() != nil || c.Verification.AllowTestFixture {
		return failure("REGISTRY_CONFIGURATION_INVALID")
	}
	if c.MaximumArchiveBytes < 1 || c.MaximumArchiveBytes > 1<<50 || c.MaximumExpandedBytes > 1<<50 || c.MaximumExpandedBytes < 1 || c.MaximumBlobBytes < 1 || c.MaximumBlobBytes > c.MaximumExpandedBytes || c.MaximumRetainedBytes < c.MaximumArchiveBytes*2 || c.MaximumPins < 1 || c.MaximumReferences < 1 || c.MaximumConcurrentImports < 1 || c.MaximumConcurrentImports > c.MaximumPins || c.MinimumHeadroomPercent < 30 || c.MinimumHeadroomPercent > 90 || c.OperationSeconds < 1 || c.OperationSeconds > 86400 || c.RequestSeconds < 1 || c.RequestSeconds > c.OperationSeconds || c.GrantSeconds < 1 || c.GrantSeconds > c.OperationSeconds || c.PageSize < 1 || c.PageSize > c.MaximumPins {
		return failure("REGISTRY_CONFIGURATION_INVALID")
	}
	return nil
}
func withinPath(root, name string) bool {
	v, e := filepath.Rel(root, name)
	return e == nil && v != ".." && !strings.HasPrefix(v, ".."+string(filepath.Separator))
}
func (c Config) OperationTimeout() time.Duration {
	return time.Duration(c.OperationSeconds) * time.Second
}
func failure(code string) error { return errors.New(code) }

// LoadEnvironment consumes only this component's explicitly parsed env-file map.
// The parent loader owns file selection/precedence; no scattered os.Getenv exists.
func LoadEnvironment(values map[string]string) (Config, error) {
	if len(values) != 1 || values["PROJECT_ONE_REGISTRY_CONFIG_FILE"] == "" {
		return Config{}, failure("REGISTRY_CONFIGURATION_REQUIRED")
	}
	return Load(values["PROJECT_ONE_REGISTRY_CONFIG_FILE"])
}

// The complete prefix must fit a private/loopback range. Checking its first
// address alone would incorrectly permit, for example, 10.0.0.0/7.
func privatePrefix(p netip.Prefix) bool {
	for _, raw := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "::1/128", "fc00::/7"} {
		allowed := netip.MustParsePrefix(raw)
		if p.Addr().BitLen() == allowed.Addr().BitLen() && p.Bits() >= allowed.Bits() && allowed.Contains(p.Addr()) {
			return true
		}
	}
	return false
}
