package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestOnlyAnInferenceNodeWithAControlCADeclaresAGateway(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "agent.json")
	gateway := map[string]any{"listen_address": "0.0.0.0:18443", "origin": "https://gw.rack7.example:18443", "image": "sha256:" + strings.Repeat("a", 64), "root_directory": "/var/lib/ownsolid-gateway", "memory_bytes": 536870912}
	load := func(change func(map[string]any)) error {
		config := map[string]any{"control_origin": "https://agents.example.invalid", "state_directory": root, "disk_path": root, "ca_file": "/etc/ownsolid-agent/ca.pem", "execution_profile": wire.DockerInferenceProfile,
			"inference": map[string]any{"root_directory": "/var/lib/ownsolid-inference", "docker_socket": "/run/docker.sock"}, "gateway": gateway}
		change(config)
		raw, e := json.Marshal(config)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		_, e = LoadConfig(path)
		return e
	}
	if e := load(func(map[string]any) {}); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(map[string]any){
		"read-only node":   func(c map[string]any) { delete(c, "execution_profile"); delete(c, "inference") },
		"no control CA":    func(c map[string]any) { delete(c, "ca_file") },
		"inside the state": func(c map[string]any) { c["gateway"] = with(gateway, "root_directory", root+"/gateway") },
		"inside the inference": func(c map[string]any) {
			c["gateway"] = with(gateway, "root_directory", "/var/lib/ownsolid-inference/gateway")
		},
		"invalid gateway": func(c map[string]any) { c["gateway"] = with(gateway, "origin", "http://gw.rack7.example") },
		"unknown field":   func(c map[string]any) { c["gateway"] = with(gateway, "credential", "secret") },
	} {
		if load(change) == nil {
			t.Error(name)
		}
	}
}

func with(base map[string]any, key string, value any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	out[key] = value
	return out
}

func TestGatewayLeasesRunOnlyOnTheInferenceProfile(t *testing.T) {
	lease := wire.Lease{TaskID: "12345678-1234-1234-1234-123456789abc", Operation: wire.GatewayOperation("start"), LeaseToken: strings.Repeat("A", 43), Attempt: 1, Gateway: &wire.GatewayCommand{Action: "start", GatewayID: "22345678-1234-1234-1234-123456789abc", ControlOrigin: "https://admin.example.test"}}
	for _, profile := range []string{wire.ReadOnlyProfile, wire.SystemdComponentsProfile, wire.SystemdMaintenanceProfile, wire.DockerInferenceProfile, wire.DockerAIProfile, wire.DockerModelLifecycleProfile} {
		if e := validateExecutionLease(profile, lease); (e == nil) != (profile == wire.DockerInferenceProfile) {
			t.Fatal(profile, e)
		}
	}
	for name, change := range map[string]func(*wire.Lease){
		"missing command":   func(l *wire.Lease) { l.Gateway = nil },
		"mixed with model":  func(l *wire.Lease) { l.Inference = &wire.InferenceCommand{Action: "observe"} },
		"action mismatch":   func(l *wire.Lease) { l.Operation = wire.GatewayOperation("stop") },
		"smuggled in facts": func(l *wire.Lease) { l.Operation = wire.CollectFacts },
		"start without origin": func(l *wire.Lease) {
			command := *l.Gateway
			command.ControlOrigin = ""
			l.Gateway = &command
		},
	} {
		bad := lease
		change(&bad)
		if validateExecutionLease(wire.DockerInferenceProfile, bad) == nil {
			t.Error(name)
		}
	}
}
