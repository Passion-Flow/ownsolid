// Package engine implements the frozen local Unix Docker API subset without pulls.
package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This authority is a Unix-HTTP protocol placeholder, never a DNS/deployment address.
const unixHTTPAuthority = "http://docker.invalid"
const NativeStatus = "passed_native_engine_profile_preflight_only"
const NativeQualification = "native_host_interface_only_not_NVIDIA_or_application_installation"
const LocalStatus = "passed_local_engine_protocol_preflight_only"
const LocalQualification = "local_Docker_Desktop_amd64_emulation_not_native_Ubuntu_NVIDIA_or_application_installation"

type Policy struct {
	FormatVersion  int      `json:"format_version"`
	APIVersion     string   `json:"api_version"`
	EngineVersions []string `json:"engine_versions"`
	Platform       struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
	DriverKey              string   `json:"store_driver_key"`
	DriverValue            string   `json:"store_driver_value"`
	NativeProfile          string   `json:"native_profile"`
	LocalProfile           string   `json:"local_profile"`
	LocalOperatorPlatforms []string `json:"local_operator_platforms"`
}
type Client struct {
	config configuration.Engine
	policy Policy
	socket string
	http   *http.Client
}
type versionReply struct {
	Version           string
	APIVersion        string `json:"ApiVersion"`
	MinimumAPIVersion string `json:"MinAPIVersion"`
	OS                string `json:"Os"`
	Architecture      string `json:"Arch"`
}
type infoReply struct {
	ID              string
	OperatingSystem string
	OSType          string
	Architecture    string
	DriverStatus    [][]string
	ServerVersion   string
}
type Observation struct {
	Status           string `json:"status"`
	Profile          string `json:"profile"`
	EngineSHA256     string `json:"engine_sha256"`
	EndpointSHA256   string `json:"endpoint_sha256"`
	EngineVersion    string `json:"engine_version"`
	APIVersion       string `json:"api_version"`
	HostArchitecture string `json:"host_architecture"`
	DeclaredPlatform string `json:"declared_platform"`
	Qualification    string `json:"qualification"`
}
type Image struct {
	ID           string `json:"Id"`
	OS           string `json:"Os"`
	Architecture string
	RepoTags     []string
	Config       *struct {
		Volumes              map[string]json.RawMessage
		User                 string
		Cmd, Entrypoint, Env []string
		Labels               map[string]string
		WorkingDir           string
	} `json:"Config"`
	RootFS struct {
		Type   string
		Layers []string
	}
	Descriptor *struct {
		Digest string `json:"digest"`
	} `json:"Descriptor"`
}

func failure(code string) error { return &diagnosticFailure{code: code} }
func New(c configuration.Engine) (*Client, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	socket, e := filepath.EvalSymlinks(c.SocketFile)
	if e != nil || !filepath.IsAbs(c.SocketFile) || filepath.Clean(c.SocketFile) != c.SocketFile {
		return nil, failure("OFFLINE_ENGINE_LOCAL_SOCKET_REQUIRED")
	}
	info, e := os.Stat(socket)
	if e != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, failure("OFFLINE_ENGINE_LOCAL_SOCKET_REQUIRED")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (int(owner.Uid) != os.Geteuid() && owner.Uid != 0) {
		return nil, failure("OFFLINE_ENGINE_SOCKET_OWNER_INVALID")
	}
	var policy Policy
	if wire.StrictJSON(wire.EngineProfile, &policy) != nil || policy.FormatVersion != 1 {
		return nil, failure("OFFLINE_ENGINE_POLICY_INVALID")
	}
	dialer := &net.Dialer{Timeout: time.Duration(c.DialSeconds) * time.Second}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxConnsPerHost: 1, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socket)
	}}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return failure("OFFLINE_ENGINE_REDIRECT_REFUSED") }}
	return &Client{config: c, policy: policy, socket: socket, http: client}, nil
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) request(ctx context.Context, method, path string, query url.Values, body io.Reader, seconds int) ([]byte, int, error) {
	contentType := ""
	if body != nil {
		contentType = "application/x-tar"
	}
	return c.requestContentType(ctx, method, path, query, body, seconds, contentType)
}
func (c *Client) requestContentType(ctx context.Context, method, path string, query url.Values, body io.Reader, seconds int, contentType string) ([]byte, int, error) {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#\r\n") {
		return nil, 0, failure("OFFLINE_ENGINE_API_PATH_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	address := unixHTTPAuthority + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, e := http.NewRequestWithContext(ctx, method, address, body)
	if e != nil {
		return nil, 0, failure("OFFLINE_ENGINE_REQUEST_INVALID")
	}
	request.GetBody = nil
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, e := c.http.Do(request)
	if e != nil {
		return nil, 0, failure("OFFLINE_ENGINE_TRANSPORT_OR_DEADLINE")
	}
	defer response.Body.Close()
	if response.ContentLength > c.config.ResponseBytes {
		return nil, response.StatusCode, failure("OFFLINE_ENGINE_RESPONSE_LIMIT")
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, c.config.ResponseBytes+1))
	if e != nil || int64(len(raw)) > c.config.ResponseBytes {
		return nil, response.StatusCode, failure("OFFLINE_ENGINE_RESPONSE_LIMIT_OR_READ_FAILED")
	}
	return raw, response.StatusCode, nil
}
func apiNumber(value string) (int, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] != "1" {
		return 0, failure("OFFLINE_ENGINE_API_UNSUPPORTED")
	}
	minor, e := strconv.Atoi(parts[1])
	if e != nil || minor < 0 {
		return 0, failure("OFFLINE_ENGINE_API_UNSUPPORTED")
	}
	return minor, nil
}
func architecture(value string) string {
	switch value {
	case "aarch64":
		return "arm64"
	case "x86_64":
		return "amd64"
	}
	return value
}
func (c *Client) observe(ctx context.Context) (Observation, error) {
	result := Observation{}
	raw, status, e := c.request(ctx, http.MethodGet, "/version", nil, nil, c.config.RequestSeconds)
	if e != nil {
		return result, e
	}
	var version versionReply
	if status != http.StatusOK || wire.UniqueJSON(raw, &version) != nil {
		return result, failure("OFFLINE_ENGINE_VERSION_RESPONSE_INVALID")
	}
	supported := false
	for _, value := range c.policy.EngineVersions {
		if version.Version == value {
			supported = true
		}
	}
	minimum, me := apiNumber(version.MinimumAPIVersion)
	maximum, ma := apiNumber(version.APIVersion)
	selected, se := apiNumber(c.policy.APIVersion)
	if !supported || me != nil || ma != nil || se != nil || selected < minimum || selected > maximum {
		return result, failure("OFFLINE_ENGINE_VERSION_OR_API_UNSUPPORTED")
	}
	raw, status, e = c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/info", nil, nil, c.config.RequestSeconds)
	if e != nil {
		return result, e
	}
	var info infoReply
	if status != http.StatusOK || wire.UniqueJSON(raw, &info) != nil || len(info.ID) < 1 || len(info.ID) > 256 || info.OSType != c.policy.Platform.OS || version.OS != info.OSType || architecture(info.Architecture) != architecture(version.Architecture) || info.ServerVersion != version.Version {
		return result, failure("OFFLINE_ENGINE_IDENTITY_RESPONSE_INVALID")
	}
	store := false
	for _, entry := range info.DriverStatus {
		if len(entry) == 2 && entry[0] == c.policy.DriverKey && entry[1] == c.policy.DriverValue {
			store = true
		}
	}
	if !store {
		return result, failure("OFFLINE_ENGINE_CONTAINERD_STORE_REQUIRED")
	}
	return Observation{Status: "observed_declared_engine_interface", Profile: c.config.Profile, EngineSHA256: wire.Digest([]byte(info.ID)), EndpointSHA256: wire.Digest([]byte(c.socket)), EngineVersion: version.Version, APIVersion: c.policy.APIVersion, HostArchitecture: architecture(info.Architecture), DeclaredPlatform: c.policy.Platform.OS + "/" + c.policy.Platform.Architecture}, nil
}
func (c *Client) Preflight(ctx context.Context) (Observation, error) {
	result, e := c.observe(ctx)
	if e != nil {
		return result, e
	}
	switch c.config.Profile {
	case c.policy.NativeProfile:
		if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || result.HostArchitecture != "amd64" {
			return Observation{}, failure("OFFLINE_ENGINE_NATIVE_HOST_PROFILE_REQUIRED")
		}
		raw, e := os.ReadFile("/etc/os-release")
		if e != nil || len(raw) > 65536 {
			return Observation{}, failure("OFFLINE_ENGINE_NATIVE_OS_IDENTITY_REQUIRED")
		}
		fields := map[string]string{}
		for _, line := range strings.Split(string(raw), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok {
				fields[key] = strings.Trim(value, `"`)
			}
		}
		if fields["ID"] != "ubuntu" || fields["VERSION_ID"] != "24.04" {
			return Observation{}, failure("OFFLINE_ENGINE_NATIVE_UBUNTU_PROFILE_REQUIRED")
		}
		result.Status = NativeStatus
		result.Qualification = NativeQualification
	case c.policy.LocalProfile:
		if !localOperatorPlatform(c.policy, runtime.GOOS, runtime.GOARCH) || (result.HostArchitecture != "amd64" && result.HostArchitecture != "arm64") {
			return Observation{}, failure("OFFLINE_ENGINE_EXPLICIT_MACOS_LOCAL_PROFILE_REQUIRED")
		}
		if runtime.GOOS == "linux" {
			// The existing local profile remains Docker Desktop qualification only.
			// A second same-ID observation does not turn an arbitrary Linux socket into
			// the local provider, and no runtime UID or host-account override is added.
			raw, status, err := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/info", nil, nil, c.config.RequestSeconds)
			var info infoReply
			if err != nil || status != http.StatusOK || wire.UniqueJSON(raw, &info) != nil || !localPublisherEngine(info, result) {
				return Observation{}, failure("OFFLINE_ENGINE_LOCAL_CONTAINER_PROVIDER_REQUIRED")
			}
		}
		result.Status = LocalStatus
		result.Qualification = LocalQualification
	default:
		return Observation{}, failure("OFFLINE_ENGINE_PROFILE_UNSUPPORTED")
	}
	return result, nil
}
func (c *Client) Inspect(ctx context.Context, imageID string) (Image, bool, error) {
	result := Image{}
	if !strings.HasPrefix(imageID, "sha256:") || !wire.ValidSHA256(strings.TrimPrefix(imageID, "sha256:")) {
		return result, false, failure("OFFLINE_ENGINE_IMMUTABLE_IMAGE_ID_REQUIRED")
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/images/"+url.PathEscape(imageID)+"/json", nil, nil, c.config.RequestSeconds)
	if e != nil {
		return result, false, e
	}
	if status == http.StatusNotFound {
		return result, false, nil
	}
	if status != http.StatusOK || wire.UniqueJSON(raw, &result) != nil || result.ID != imageID || result.OS != c.policy.Platform.OS || result.Architecture != c.policy.Platform.Architecture || result.RootFS.Type != "layers" {
		return Image{}, false, failure("OFFLINE_ENGINE_IMPORTED_IMAGE_IDENTITY_INVALID")
	}
	if result.Descriptor != nil && result.Descriptor.Digest != imageID {
		return Image{}, false, failure("OFFLINE_ENGINE_IMPORTED_IMAGE_DESCRIPTOR_INVALID")
	}
	for _, layer := range result.RootFS.Layers {
		if !strings.HasPrefix(layer, "sha256:") || !wire.ValidSHA256(strings.TrimPrefix(layer, "sha256:")) {
			return Image{}, false, failure("OFFLINE_ENGINE_IMPORTED_ROOTFS_INVALID")
		}
	}
	return result, true, nil
}
func (c *Client) LoadTagless(ctx context.Context, body io.Reader) error {
	platform := encodePlatform(c.policy.Platform.OS, c.policy.Platform.Architecture)
	raw, status, e := c.request(ctx, http.MethodPost, "/v"+c.policy.APIVersion+"/images/load", url.Values{"quiet": {"1"}, "platform": {platform}}, body, c.config.LoadSeconds)
	if e != nil {
		return e
	}
	if status != http.StatusOK {
		return failure("OFFLINE_ENGINE_LOAD_REJECTED_OR_UNCERTAIN")
	}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 4096), int(c.config.ResponseBytes))
	events := 0
	for scanner.Scan() {
		var event struct {
			Error       string `json:"error"`
			ErrorDetail *struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
			Stream string `json:"stream"`
		}
		if wire.UniqueJSON(scanner.Bytes(), &event) != nil {
			return failure("OFFLINE_ENGINE_LOAD_STREAM_INVALID_OR_UNCERTAIN")
		}
		if event.Error != "" || event.ErrorDetail != nil {
			return failure("OFFLINE_ENGINE_LOAD_STREAM_FAILED_OR_UNCERTAIN")
		}
		events++
	}
	if scanner.Err() != nil || events == 0 {
		return failure("OFFLINE_ENGINE_LOAD_STREAM_INVALID_OR_UNCERTAIN")
	}
	return nil
}
func encodePlatform(osName, arch string) string {
	return `{"os":"` + osName + `","architecture":"` + arch + `"}`
}

func localOperatorPlatform(p Policy, goos, goarch string) bool {
	for _, v := range p.LocalOperatorPlatforms {
		if v == goos+"/"+goarch {
			return true
		}
	}
	return false
}
func localPublisherEngine(info infoReply, observed Observation) bool {
	return info.OperatingSystem == "Docker Desktop" && info.OSType == "linux" && info.ServerVersion == observed.EngineVersion && wire.Digest([]byte(info.ID)) == observed.EngineSHA256 && architecture(info.Architecture) == observed.HostArchitecture
}
