// Package privateclusters owns one scoped external Kubernetes proof-Job path.
package privateclusters

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const MinimumBodyBytes int64 = 4096
const MaximumBodyBytes int64 = 16 << 20

const ConfigEnvironment = "PROJECT_ONE_PRIVATE_CLUSTER_CONFIG_FILE"
const Purpose = "isolated-validation"
const ServerVersion = "v1.36.4"
const PodSecurityVersion = "v1.36"
const AMD64Profile = "kubernetes1.36.4-linux-amd64"
const LocalARM64Profile = "local-kind1.36.4-linux-arm64"
const NamespacePrefix = "projectone-test-"
const PurposeLabel = "io.projectone.purpose"
const InstallationLabel = "io.projectone.installation"
const WorkspaceLabel = "io.projectone.workspace"
const RunLabel = "io.projectone.private-cluster-run"
const SpecAnnotation = "io.projectone.private-cluster-spec-sha256"
const LeaseName = "projectone-private-cluster-proof"
const QuotaName = "projectone-private-cluster-quota"
const PolicyName = "projectone-private-cluster-deny"
const ControlServiceAccount = "projectone-private-cluster-control"
const ProbeServiceAccount = "projectone-private-cluster-probe"
const JobNamePrefix = "projectone-proof-"

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var namespaceName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var imageRepository = regexp.MustCompile(`^[a-z0-9][a-z0-9.:-]*/[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)

func fail(code string) error { return errors.New(code) }
func validID(v string) bool {
	return v != "00000000-0000-0000-0000-000000000000" && uuidPattern.MatchString(v)
}

type Policy struct {
	RequestSeconds   int   `json:"request_seconds"`
	OperationSeconds int   `json:"operation_seconds"`
	JobSeconds       int   `json:"job_seconds"`
	MaximumBodyBytes int64 `json:"maximum_body_bytes"`
	MaximumLogBytes  int64 `json:"maximum_log_bytes"`
	MaximumObjects   int   `json:"maximum_objects"`
	PageSize         int   `json:"page_size"`
	CPUMilli         int   `json:"cpu_milli"`
	MemoryMiB        int   `json:"memory_mib"`
	RunAsUser        int64 `json:"run_as_user"`
}
type Profile struct {
	ID                       string   `json:"id"`
	Name                     string   `json:"name"`
	ServerOrigin             string   `json:"server_origin"`
	TLSServerName            string   `json:"tls_server_name"`
	AllowedCIDRs             []string `json:"allowed_cidrs"`
	CAFile                   string   `json:"ca_file"`
	TokenFile                string   `json:"token_file"`
	ClusterUID               string   `json:"cluster_uid"`
	Namespace                string   `json:"namespace"`
	NamespaceUID             string   `json:"namespace_uid"`
	ControlServiceAccountUID string   `json:"control_service_account_uid"`
	InstallationID           string   `json:"installation_id"`
	WorkspaceID              string   `json:"workspace_id"`
	Profile                  string   `json:"profile"`
	ImageRepository          string   `json:"image_repository"`
	ImageProofFile           string   `json:"image_proof_file"`
}
type Config struct {
	FormatVersion int                 `json:"format_version"`
	Policy        Policy              `json:"policy"`
	Profiles      []Profile           `json:"profiles"`
	Verification  bundle.Verification `json:"verification"`
}

func Defaults() Config {
	return Config{FormatVersion: 1, Policy: Policy{RequestSeconds: 10, OperationSeconds: 60, JobSeconds: 30, MaximumBodyBytes: 4 << 20, MaximumLogBytes: 4096, MaximumObjects: 64, PageSize: 25, CPUMilli: 100, MemoryMiB: 32, RunAsUser: 65532}, Verification: bundle.Defaults()}
}
func (p Policy) Validate() error {
	if p.RequestSeconds < 1 || p.RequestSeconds > 120 || p.OperationSeconds < p.RequestSeconds || p.OperationSeconds > 3600 || p.JobSeconds < 1 || p.JobSeconds > 120 || p.MaximumBodyBytes < MinimumBodyBytes || p.MaximumBodyBytes > MaximumBodyBytes || p.MaximumLogBytes < 1024 || p.MaximumLogBytes > 16384 || p.MaximumObjects < 1 || p.MaximumObjects > 10000 || p.PageSize < 1 || p.PageSize > 100 || p.CPUMilli < 10 || p.CPUMilli > 1000 || p.MemoryMiB < 16 || p.MemoryMiB > 512 || p.RunAsUser < 1 || p.RunAsUser > 2147483647 {
		return fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
	}
	return nil
}
func (p Profile) Architecture() string {
	switch p.Profile {
	case AMD64Profile:
		return "amd64"
	case LocalARM64Profile:
		return "arm64"
	}
	return ""
}
func (p Profile) AssetID() string {
	if p.Architecture() == "arm64" {
		return release.PrivateClusterProbeARM64AssetID
	}
	if p.Architecture() == "amd64" {
		return release.PrivateClusterProbeAMD64AssetID
	}
	return ""
}
func protectedPath(v string) bool {
	return filepath.IsAbs(v) && filepath.Clean(v) == v && !strings.ContainsAny(v, "\x00\r\n")
}
func privateCIDR(v netip.Prefix) bool {
	for _, s := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "::1/128", "fc00::/7"} {
		p := netip.MustParsePrefix(s)
		if p.Addr().BitLen() == v.Addr().BitLen() && v.Bits() >= p.Bits() && p.Contains(v.Addr()) {
			return true
		}
	}
	return false
}
func validTLSName(v string) bool {
	if ip, e := netip.ParseAddr(v); e == nil {
		return ip.Zone() == "" && !ip.IsUnspecified() && !ip.IsMulticast()
	}
	return len(v) <= 253 && regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*$`).MatchString(v)
}
func validImageRepository(v string) bool {
	if !imageRepository.MatchString(v) {
		return false
	}
	host := strings.SplitN(v, "/", 2)[0]
	u, e := url.Parse("https://" + host)
	if e != nil || u.Hostname() == "" || !validTLSName(u.Hostname()) || strings.HasSuffix(host, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}
func (p Profile) Validate() error {
	u, e := url.Parse(p.ServerOrigin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(p.ServerOrigin, "?#") || strings.Contains(u.Host, "%") || strings.HasSuffix(u.Host, ":") || p.Architecture() == "" || !identifier.MatchString(p.ID) || len(p.Name) < 1 || len(p.Name) > 100 || strings.ContainsAny(p.Name, "\x00\r\n") || !namespaceName.MatchString(p.Namespace) || p.Namespace == "default" || strings.HasPrefix(p.Namespace, "kube-") || !strings.HasPrefix(p.Namespace, NamespacePrefix) || !validID(p.ClusterUID) || !validID(p.NamespaceUID) || p.ClusterUID == p.NamespaceUID || !validID(p.InstallationID) || !validID(p.WorkspaceID) || !validID(p.ControlServiceAccountUID) || !validImageRepository(p.ImageRepository) || !validTLSName(p.TLSServerName) {
		return fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
	}
	if raw := u.Port(); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 65535 {
			return fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
		}
	}
	if len(p.AllowedCIDRs) < 1 || len(p.AllowedCIDRs) > 32 {
		return fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
	}
	for _, raw := range p.AllowedCIDRs {
		n, e := netip.ParsePrefix(raw)
		if e != nil || n != n.Masked() || !privateCIDR(n) {
			return fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
		}
	}
	paths := map[string]bool{}
	for _, v := range []string{p.CAFile, p.TokenFile, p.ImageProofFile} {
		if !protectedPath(v) || paths[v] {
			return fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
		}
		paths[v] = true
	}
	return nil
}
func (c Config) Validate() error {
	if c.FormatVersion != 1 || c.Policy.Validate() != nil || c.Verification.Validate() != nil || c.Verification.AllowTestFixture || !protectedPath(c.Verification.MountRoot) || !protectedPath(c.Verification.TrustFile) || len(c.Profiles) < 1 || len(c.Profiles) > 64 {
		return fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
	}
	ids, scopes := map[string]bool{}, map[string]bool{}
	for _, p := range c.Profiles {
		scope := p.ClusterUID + ":" + p.NamespaceUID
		if p.Validate() != nil || ids[p.ID] || scopes[scope] {
			return fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
		}
		ids[p.ID] = true
		scopes[scope] = true
	}
	return nil
}
func Load(path string) (Config, error) {
	raw, e := bundle.ReadProtectedConfiguration(path)
	if e != nil {
		return Config{}, fail("PRIVATE_CLUSTER_CONFIGURATION_REQUIRED")
	}
	return Decode(raw)
}

// Decode preserves omitted policy defaults while rejecting explicit nulls and
// missing required version/profile/verification fields from the owning schema.
func Decode(raw []byte) (Config, error) {
	bad := fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
	var fields map[string]json.RawMessage
	if release.StrictJSON(raw, &fields) != nil || fields == nil {
		return Config{}, bad
	}
	for _, name := range []string{"format_version", "profiles", "verification"} {
		if _, ok := fields[name]; !ok {
			return Config{}, bad
		}
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Config{}, bad
		}
	}
	for _, name := range []string{"policy", "verification"} {
		value, present := fields[name]
		if !present {
			continue
		}
		var members map[string]json.RawMessage
		if release.StrictJSON(value, &members) != nil || members == nil {
			return Config{}, bad
		}
		for _, member := range members {
			if bytes.Equal(bytes.TrimSpace(member), []byte("null")) {
				return Config{}, bad
			}
		}
		if name == "verification" {
			for _, required := range []string{"mount_root", "trust_file"} {
				if _, ok := members[required]; !ok {
					return Config{}, bad
				}
			}
		}
	}
	c := Defaults()
	if decodeProtectedJSON(raw, &c) != nil || c.Validate() != nil {
		return Config{}, fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
	}
	return c, nil
}
func LoadEnvironment(values map[string]string) (Config, error) {
	if len(values) != 1 || values[ConfigEnvironment] == "" {
		return Config{}, fail("PRIVATE_CLUSTER_CONFIGURATION_REQUIRED")
	}
	return Load(values[ConfigEnvironment])
}
func (c Config) profile(id string) (Profile, error) {
	for _, p := range c.Profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return Profile{}, fail("RESOURCE_NOT_FOUND")
}
func fingerprint(v any) string {
	raw, e := json.Marshal(v)
	if e != nil {
		return ""
	}
	return release.Digest(raw)
}
