package compose

import (
	"context"
	"encoding/json"
	"math"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
	"syscall"
)

const scopeLabel = "io.projectone.isolated-scope"
const purposeLabel = "io.projectone.purpose"
const milliCPUPerCore = 1000
const nanoCPUPerMilliCPU = 1000000
const maximumArguments = 64
const maximumArgumentBytes = 4096
const internalNetworkPriority = 100
const ingressNetworkPriority = 0

var scopeIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var modelNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

type RenderContext struct {
	ScopeID       string
	ScopeRoot     string
	AllowedImages map[string]bool
}
type Model struct {
	Services map[string]Service `json:"services"`
	Volumes  []string           `json:"volumes,omitempty"`
}
type Service struct {
	Image       string            `json:"image"`
	User        string            `json:"user"`
	Command     []string          `json:"command,omitempty"`
	MemoryBytes int64             `json:"memory_bytes"`
	MilliCPU    int64             `json:"milli_cpu"`
	PIDs        int64             `json:"pids_limit"`
	Restart     string            `json:"restart"`
	ReadOnly    bool              `json:"read_only"`
	Environment map[string]string `json:"environment,omitempty"`
	Mounts      []Mount           `json:"mounts,omitempty"`
	Ports       []Port            `json:"ports,omitempty"`
	Tmpfs       []Tmpfs           `json:"tmpfs,omitempty"`
}
type Mount struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}
type Port struct {
	HostIP    string `json:"host_ip"`
	Published int    `json:"published"`
	Target    int    `json:"target"`
}
type Tmpfs struct {
	Target    string `json:"target"`
	SizeBytes int64  `json:"size_bytes"`
	UID       int    `json:"uid"`
	GID       int    `json:"gid"`
}
type Rendered struct {
	Status         string `json:"status"`
	DocumentSHA256 string `json:"document_sha256"`
	Services       int    `json:"services"`
	Qualification  string `json:"qualification"`
	Document       []byte `json:"-"`
}

func DecodeModel(raw []byte, maximumBytes int64) (Model, error) {
	var model Model
	if maximumBytes < 1 || int64(len(raw)) > maximumBytes || wire.StrictJSON(raw, &model) != nil {
		return model, failure("OFFLINE_COMPOSE_MODEL_INPUT_INVALID")
	}
	return model, nil
}

func immutableImage(id string) bool {
	return strings.HasPrefix(id, "sha256:") && wire.ValidSHA256(strings.TrimPrefix(id, "sha256:"))
}
func below(root, name string) bool {
	relative, e := filepath.Rel(root, name)
	return e == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
func numericUser(user string) bool {
	parts := strings.Split(user, ":")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		n, e := strconv.ParseInt(part, 10, 32)
		if e != nil || n < 1 || strconv.FormatInt(n, 10) != part {
			return false
		}
	}
	return true
}
func containerTarget(target string) bool {
	if !strings.HasPrefix(target, "/") || path.Clean(target) != target || target == "/" || strings.ContainsAny(target, "\\\x00\r\n:$") {
		return false
	}
	for _, reserved := range []string{"/proc", "/sys", "/dev"} {
		if target == reserved || strings.HasPrefix(target, reserved+"/") {
			return false
		}
	}
	return !strings.HasSuffix(target, ".sock")
}
func (c *Client) document(context RenderContext, model Model) ([]byte, error) {
	bad := func() ([]byte, error) { return nil, failure("OFFLINE_COMPOSE_CONSTRAINED_MODEL_INVALID") }
	resolved, e := filepath.EvalSymlinks(context.ScopeRoot)
	info, se := os.Lstat(context.ScopeRoot)
	if !scopeIDPattern.MatchString(context.ScopeID) || e != nil || se != nil || resolved != context.ScopeRoot || !filepath.IsAbs(context.ScopeRoot) || filepath.Clean(context.ScopeRoot) != context.ScopeRoot || !below(c.mount, context.ScopeRoot) || info.Mode() != os.ModeDir|0700 || !owner(info) || len(model.Services) < 1 || len(model.Services) > 3 {
		return bad()
	}
	labels := map[string]string{scopeLabel: context.ScopeID, purposeLabel: "isolated-validation"}
	volumes := map[string]any{}
	for _, name := range model.Volumes {
		if !modelNamePattern.MatchString(name) || volumes[name] != nil {
			return bad()
		}
		volumes[name] = map[string]any{"external": true, "name": "projectone-" + context.ScopeID + "-" + name}
	}
	networks := map[string]any{"isolated": map[string]any{"driver": "bridge", "internal": true, "labels": labels}}
	services := map[string]any{}
	published := map[string]bool{}
	for name, service := range model.Services {
		if (name != "control" && name != "postgres" && name != "gateway") || !immutableImage(service.Image) || !context.AllowedImages[service.Image] || !numericUser(service.User) || service.MemoryBytes < 1 || service.MilliCPU < 1 || service.MilliCPU > math.MaxInt64/nanoCPUPerMilliCPU || service.PIDs < 1 || service.Restart != "no" && service.Restart != "unless-stopped" || len(service.Command) > maximumArguments {
			return bad()
		}
		for _, argument := range service.Command {
			if len(argument) > maximumArgumentBytes || strings.ContainsRune(argument, '\x00') {
				return bad()
			}
		}
		for key, value := range service.Environment {
			if name != "postgres" || strings.ContainsAny(value, "\x00\r\n") {
				return bad()
			}
			switch key {
			case "POSTGRES_DB", "POSTGRES_USER":
				if !modelNamePattern.MatchString(value) {
					return bad()
				}
			case "PGDATA":
				if !containerTarget(value) {
					return bad()
				}
			case "POSTGRES_PASSWORD_FILE":
				if !strings.HasPrefix(value, "/run/projectone/") || !containerTarget(value) {
					return bad()
				}
			default:
				return bad()
			}
		}
		mounts := []any{}
		targets := map[string]bool{}
		for _, mount := range service.Mounts {
			if !containerTarget(mount.Target) || targets[mount.Target] {
				return bad()
			}
			targets[mount.Target] = true
			entry := map[string]any{"type": mount.Type, "source": mount.Source, "target": mount.Target, "read_only": mount.ReadOnly}
			switch mount.Type {
			case "volume":
				if volumes[mount.Source] == nil {
					return bad()
				}
				entry["volume"] = map[string]any{"nocopy": true}
			case "bind":
				if mount.Source == filepath.Join(context.ScopeRoot, view.RelativeDirectory) || mount.Target == view.ContainerDirectory {
					info, err := os.Lstat(mount.Source)
					uid, _, ue := strings.Cut(service.User, ":")
					reader, pe := strconv.Atoi(uid)
					if err != nil || !ue || pe != nil || name != "control" || !mount.ReadOnly || mount.Source != filepath.Join(context.ScopeRoot, view.RelativeDirectory) || mount.Target != view.ContainerDirectory || info.Mode() != os.ModeDir|0755 {
						return bad()
					}
					st, ok := info.Sys().(*syscall.Stat_t)
					if !ok || reader == int(st.Uid) {
						return bad()
					}
				}
				resolved, e := filepath.EvalSymlinks(mount.Source)
				info, se := os.Lstat(mount.Source)
				configuration := filepath.Join(context.ScopeRoot, "configuration")
				data := filepath.Join(context.ScopeRoot, "data")
				if e != nil || se != nil || resolved != mount.Source || filepath.Clean(mount.Source) != mount.Source || !info.IsDir() || !owner(info) || info.Mode().Perm()&0022 != 0 || !(mount.Source == configuration || below(configuration, mount.Source) || mount.Source == data || below(data, mount.Source)) {
					return bad()
				}
				entry["bind"] = map[string]any{"create_host_path": false}
			default:
				return bad()
			}
			mounts = append(mounts, entry)
		}
		if name == "postgres" && len(service.Ports) > 0 {
			return bad()
		}
		ports := []any{}
		for _, port := range service.Ports {
			ip, e := netip.ParseAddr(port.HostIP)
			key := port.HostIP + ":" + strconv.Itoa(port.Published)
			if e != nil || !ip.IsLoopback() || ip.Zone() != "" || ip.String() != port.HostIP || port.Published < 1 || port.Published > 65535 || port.Target < 1 || port.Target > 65535 || published[key] {
				return bad()
			}
			published[key] = true
			ports = append(ports, map[string]any{"host_ip": port.HostIP, "published": strconv.Itoa(port.Published), "target": port.Target, "protocol": "tcp"})
		}
		tmpfs := []string{}
		for _, tmp := range service.Tmpfs {
			if !containerTarget(tmp.Target) || targets[tmp.Target] || tmp.SizeBytes < 1 || tmp.UID < 1 || tmp.GID < 1 {
				return bad()
			}
			targets[tmp.Target] = true
			tmpfs = append(tmpfs, tmp.Target+":rw,noexec,nosuid,nodev,size="+strconv.FormatInt(tmp.SizeBytes, 10)+",uid="+strconv.Itoa(tmp.UID)+",gid="+strconv.Itoa(tmp.GID)+",mode=0700")
		}
		entry := map[string]any{"image": service.Image, "platform": "linux/amd64", "user": service.User, "pull_policy": "never", "mem_limit": service.MemoryBytes, "cpus": float64(service.MilliCPU) / milliCPUPerCore, "pids_limit": service.PIDs, "restart": service.Restart, "read_only": service.ReadOnly, "cap_drop": []string{"ALL"}, "security_opt": []string{"no-new-privileges:true"}, "networks": []string{"isolated"}, "labels": labels}
		if len(service.Ports) > 0 {
			networks["ingress"] = map[string]any{"driver": "bridge", "internal": false, "labels": labels}
			entry["networks"] = map[string]any{"isolated": map[string]any{"priority": internalNetworkPriority}, "ingress": map[string]any{"priority": ingressNetworkPriority}}
		}
		if len(service.Command) > 0 {
			entry["command"] = service.Command
		}
		if len(service.Environment) > 0 {
			entry["environment"] = service.Environment
		}
		if len(mounts) > 0 {
			entry["volumes"] = mounts
		}
		if len(ports) > 0 {
			entry["ports"] = ports
		}
		if len(tmpfs) > 0 {
			entry["tmpfs"] = tmpfs
		}
		services[name] = entry
	}
	return json.Marshal(map[string]any{"name": "projectone-" + context.ScopeID, "services": services, "networks": networks, "volumes": volumes})
}
func (c *Client) Render(ctx context.Context, scope RenderContext, model Model) (Rendered, error) {
	input, e := c.document(scope, model)
	if e != nil {
		return Rendered{}, e
	}
	if int64(len(input)) > c.settings.OutputBytes {
		return Rendered{}, failure("OFFLINE_COMPOSE_INPUT_LIMIT")
	}
	if _, e = c.Version(ctx); e != nil {
		return Rendered{}, e
	}
	raw, e := c.execute(ctx, []string{"--project-name", "projectone-" + scope.ScopeID, "--project-directory", scope.ScopeRoot, "-f", "-", "config", "--format", "json", "--no-env-resolution", "--no-path-resolution", "--no-interpolate"}, input)
	if e != nil {
		return Rendered{}, e
	}
	var canonical struct {
		Name     string `json:"name"`
		Services map[string]struct {
			Image       string                     `json:"image"`
			Platform    string                     `json:"platform"`
			PullPolicy  string                     `json:"pull_policy"`
			Labels      map[string]string          `json:"labels"`
			User        string                     `json:"user"`
			ReadOnly    bool                       `json:"read_only"`
			Memory      int64                      `json:"mem_limit"`
			CPU         float64                    `json:"cpus"`
			PIDs        int64                      `json:"pids_limit"`
			Restart     string                     `json:"restart"`
			Privileged  bool                       `json:"privileged"`
			NetworkMode string                     `json:"network_mode"`
			CapAdd      []string                   `json:"cap_add"`
			CapDrop     []string                   `json:"cap_drop"`
			Security    []string                   `json:"security_opt"`
			Networks    map[string]json.RawMessage `json:"networks"`
		} `json:"services"`
	}
	if wire.UniqueJSON(raw, &canonical) != nil || canonical.Name != "projectone-"+scope.ScopeID || len(canonical.Services) != len(model.Services) {
		return Rendered{}, failure("OFFLINE_COMPOSE_CANONICAL_MODEL_MISMATCH")
	}
	for name, service := range canonical.Services {
		expected, ok := model.Services[name]
		_, isolated := service.Networks["isolated"]
		if !ok || service.Image != expected.Image || service.Platform != "linux/amd64" || service.PullPolicy != "never" || service.Labels[scopeLabel] != scope.ScopeID || service.Labels[purposeLabel] != "isolated-validation" || service.User != expected.User || service.ReadOnly != expected.ReadOnly || service.Memory != expected.MemoryBytes || service.CPU != float64(expected.MilliCPU)/milliCPUPerCore || service.PIDs != expected.PIDs || service.Restart != expected.Restart || service.Privileged || service.NetworkMode != "" || len(service.CapAdd) > 0 || !slices.Equal(service.CapDrop, []string{"ALL"}) || !slices.Equal(service.Security, []string{"no-new-privileges:true"}) || !canonicalNetworks(service.Networks, len(expected.Ports) > 0) || !isolated {
			return Rendered{}, failure("OFFLINE_COMPOSE_CANONICAL_MODEL_MISMATCH")
		}
	}
	return Rendered{Status: "passed_constrained_Compose_render_only", DocumentSHA256: wire.Digest(raw), Services: len(model.Services), Qualification: "canonical_render_only_not_created_started_or_target_qualified", Document: raw}, nil
}

func canonicalNetworks(networks map[string]json.RawMessage, ingress bool) bool {
	if _, ok := networks["isolated"]; !ok {
		return false
	}
	if ingress {
		_, ok := networks["ingress"]
		return ok && len(networks) == 2
	}
	return len(networks) == 1
}
