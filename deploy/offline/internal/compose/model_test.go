package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func modelFixture(t *testing.T) (*Client, RenderContext, Model) {
	t.Helper()
	_, client := fixture(t, `printf '{"version":"v5.5.1"}'`)
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private root")
	}
	id := "sha256:" + strings.Repeat("a", 64)
	return client, RenderContext{ScopeID: strings.Repeat("b", 32), ScopeRoot: root, AllowedImages: map[string]bool{id: true}}, Model{Services: map[string]Service{"control": {Image: id, User: "10033:10034", MemoryBytes: 64 << 20, MilliCPU: 250, PIDs: 32, Restart: "no", ReadOnly: true, Ports: []Port{{HostIP: "127.0.0.1", Published: 18443, Target: 8443}}, Mounts: []Mount{{Type: "volume", Source: "store", Target: "/var/lib/projectone/data"}}, Tmpfs: []Tmpfs{{Target: "/tmp", SizeBytes: 1 << 20, UID: 10033, GID: 10034}}}}, Volumes: []string{"store"}}
}
func TestConstrainedDocumentIdentityIsolationResourcesAndFalse(t *testing.T) {
	client, scope, model := modelFixture(t)
	configuration := filepath.Join(scope.ScopeRoot, "configuration")
	os.Mkdir(configuration, 0700)
	service := model.Services["control"]
	service.ReadOnly = false
	service.Mounts = append(service.Mounts, Mount{Type: "bind", Source: configuration, Target: "/run/projectone", ReadOnly: true})
	model.Services["control"] = service
	raw, e := client.document(scope, model)
	if e != nil {
		t.Fatal(e)
	}
	var parsed map[string]any
	if json.Unmarshal(raw, &parsed) != nil {
		t.Fatal("document invalid")
	}
	services := parsed["services"].(map[string]any)
	control := services["control"].(map[string]any)
	if control["read_only"] != false || control["pull_policy"] != "never" || control["cpus"] != 0.25 || control["mem_limit"] != float64(64<<20) || control["user"] != "10033:10034" {
		t.Fatal("typed/nondefault/false settings changed")
	}
	if strings.Contains(string(raw), "docker.sock") || !strings.Contains(string(raw), `"create_host_path":false`) || !strings.Contains(string(raw), "projectone-"+scope.ScopeID+"-store") || !strings.Contains(string(raw), `"internal":true`) {
		t.Fatal("isolation fields missing")
	}
}
func TestConstrainedModelStrictDecodeAndInputBound(t *testing.T) {
	for _, raw := range []string{`{"include":"https://invalid.example/compose.yaml"}`, `{"services":null}`, `{"services":{},"services":{}}`, `{"services":{"control":{"privileged":true}}}`} {
		if _, e := DecodeModel([]byte(raw), 4096); e == nil {
			t.Fatal("unsafe model JSON accepted")
		}
	}
	if _, e := DecodeModel([]byte(`{"services":{}}`), 1); e == nil {
		t.Fatal("oversized model accepted")
	}
}

func TestCanonicalResultRejectsChangedIdentityResourcesAndPrivilege(t *testing.T) {
	for _, fault := range []string{"none", "image", "user", "memory", "privileged", "cap-add", "network-mode", "network", "scope-label"} {
		t.Run(fault, func(t *testing.T) {
			client, scope, model := modelFixture(t)
			raw, e := client.document(scope, model)
			if e != nil {
				t.Fatal(e)
			}
			var document map[string]any
			json.Unmarshal(raw, &document)
			service := document["services"].(map[string]any)["control"].(map[string]any)
			service["networks"] = map[string]any{"isolated": map[string]any{"priority": 100}, "ingress": map[string]any{"priority": 0}}
			switch fault {
			case "image":
				service["image"] = "sha256:" + strings.Repeat("f", 64)
			case "user":
				service["user"] = "0:0"
			case "memory":
				service["mem_limit"] = float64(1)
			case "privileged":
				service["privileged"] = true
			case "cap-add":
				service["cap_add"] = []string{"SYS_ADMIN"}
			case "network-mode":
				service["network_mode"] = "host"
			case "network":
				service["networks"] = map[string]any{"foreign": nil}
			case "scope-label":
				service["labels"].(map[string]any)[scopeLabel] = strings.Repeat("f", 32)
			}
			canonical, _ := json.Marshal(document)
			quoted := "'" + strings.ReplaceAll(string(canonical), "'", "'\\''") + "'"
			body := "#!/bin/sh\ncase \" $* \" in *\" version \"*) printf '{\"version\":\"v5.5.1\"}' ;; *) printf '%s' " + quoted + " ;; esac\n"
			if os.WriteFile(client.docker.path, []byte(body), 0700) != nil {
				t.Fatal("explicit double")
			}
			client.docker, e = binary(client.docker.path)
			if e != nil {
				t.Fatal(e)
			}
			result, e := client.Render(context.Background(), scope, model)
			if fault == "none" {
				if e != nil || result.Services != 1 {
					t.Fatal(result, e)
				}
			} else if e == nil {
				t.Fatal("unsafe canonical result accepted")
			}
		})
	}
}
func TestConstrainedModelRejectsEscapesAndUnsafeBehavior(t *testing.T) {
	for _, fault := range []string{"scope", "foreign-root", "root-user", "tagged-image", "unallowed-image", "service", "zero-memory", "zero-cpu", "zero-pids", "public-port", "duplicate-port", "unknown-volume", "bind-escape", "target-socket", "target-root", "secret", "duplicate-volume", "tmpfs-overlap", "argument", "empty-services", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			client, scope, model := modelFixture(t)
			service := model.Services["control"]
			switch fault {
			case "scope":
				scope.ScopeID = "caller-controlled"
			case "foreign-root":
				scope.ScopeRoot = "/"
			case "root-user":
				service.User = "0:0"
			case "tagged-image":
				service.Image = "synthetic/image:tag"
			case "unallowed-image":
				scope.AllowedImages = map[string]bool{}
			case "service":
				model.Services["arbitrary"] = service
			case "zero-memory":
				service.MemoryBytes = 0
			case "zero-cpu":
				service.MilliCPU = 0
			case "zero-pids":
				service.PIDs = 0
			case "public-port":
				service.Ports[0].HostIP = "0.0.0.0"
			case "duplicate-port":
				service.Ports = append(service.Ports, service.Ports[0])
			case "unknown-volume":
				service.Mounts[0].Source = "foreign"
			case "bind-escape":
				service.Mounts = []Mount{{Type: "bind", Source: "/", Target: "/host"}}
			case "target-socket":
				service.Mounts[0].Target = "/run/docker.sock"
			case "target-root":
				service.Mounts[0].Target = "/"
			case "secret":
				service.Environment = map[string]string{"API_KEY": "explicit-test-secret"}
			case "duplicate-volume":
				model.Volumes = append(model.Volumes, "store")
			case "tmpfs-overlap":
				service.Tmpfs[0].Target = service.Mounts[0].Target
			case "argument":
				service.Command = []string{strings.Repeat("x", maximumArgumentBytes+1)}
			case "empty-services":
				if json.Unmarshal([]byte(`{"services":{}}`), &model) != nil {
					t.Fatal("fixture")
				}
			}
			model.Services["control"] = service
			if fault == "empty-services" {
				delete(model.Services, "control")
			}
			if fault == "cancelled" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if _, e := client.Render(ctx, scope, model); e == nil {
					t.Fatal("cancel ignored")
				}
				return
			}
			if _, e := client.document(scope, model); e == nil {
				t.Fatal("unsafe model admitted")
			}
		})
	}
}
