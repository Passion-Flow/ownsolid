package compose

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

// Explicit metadata double; real pinned Compose creation is in the tagged CLI journey.
type runtimeDouble struct {
	image                             engine.Image
	container                         engine.RuntimeContainer
	network                           engine.RuntimeNetwork
	present, foreign, foreignEndpoint bool
	ingress                           engine.RuntimeNetwork
	ingressPresent                    bool
}

func (d *runtimeDouble) Inspect(context.Context, string) (engine.Image, bool, error) {
	return d.image, true, nil
}
func (d *runtimeDouble) InspectRuntime(context.Context, string, string) (engine.RuntimeContainer, bool, error) {
	return d.container, d.present, nil
}
func (d *runtimeDouble) InspectRuntimeNetwork(context.Context, string) (engine.RuntimeNetwork, bool, error) {
	return d.network, d.present, nil
}
func (d *runtimeDouble) InspectRuntimeIngress(context.Context, string) (engine.RuntimeNetwork, bool, error) {
	return d.ingress, d.ingressPresent, nil
}
func (d *runtimeDouble) RuntimeCandidates(_ context.Context, _ string, networks bool) ([]engine.RuntimeObject, error) {
	result := []engine.RuntimeObject{}
	if d.present {
		id := d.container.ID
		if networks {
			id = d.network.ID
		}
		result = append(result, engine.RuntimeObject{ID: id})
	}
	if networks && d.ingressPresent {
		result = append(result, engine.RuntimeObject{ID: d.ingress.ID})
	}
	if d.foreign {
		result = append(result, engine.RuntimeObject{ID: strings.Repeat("f", 64)})
	}
	return result, nil
}
func runtimeFixture(t *testing.T) (*Client, RenderContext, Model, *runtimeDouble) {
	t.Helper()
	c, scope, model := modelFixture(t)
	s := model.Services["control"]
	s.Mounts = nil
	s.Tmpfs = nil
	s.Ports = nil
	s.Command = []string{"$LITERAL"}
	model.Services["control"] = s
	model.Volumes = nil
	labels := map[string]string{scopeLabel: scope.ScopeID, purposeLabel: "isolated-validation", "com.docker.compose.project": engine.RuntimeProject(scope.ScopeID), "com.docker.compose.service": "control", "com.docker.compose.container-number": "1", "com.docker.compose.oneoff": "False"}
	d := &runtimeDouble{present: true}
	raw, _ := json.Marshal(map[string]any{"Id": strings.Repeat("c", 64), "Name": "/" + engine.RuntimeContainerName(scope.ScopeID, "control"), "Image": s.Image, "Platform": "linux", "Config": map[string]any{"Image": s.Image, "User": s.User, "Cmd": s.Command, "Labels": labels, "Env": []string{"PATH=/synthetic"}}, "State": map[string]any{"Status": "created"}, "HostConfig": map[string]any{"NetworkMode": engine.RuntimeNetworkName(scope.ScopeID), "IpcMode": "private", "CgroupnsMode": "private", "Runtime": "runc", "ReadonlyRootfs": true, "Memory": s.MemoryBytes, "NanoCPUs": s.MilliCPU * nanoCPUPerMilliCPU, "PidsLimit": s.PIDs, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "RestartPolicy": map[string]any{"Name": "no"}}, "NetworkSettings": map[string]any{"Networks": map[string]any{engine.RuntimeNetworkName(scope.ScopeID): map[string]any{}}}})
	if json.Unmarshal(raw, &d.container) != nil {
		t.Fatal("fixture")
	}
	raw, _ = json.Marshal(map[string]any{"Id": s.Image, "Os": "linux", "Architecture": "amd64", "Config": map[string]any{"Env": []string{"PATH=/synthetic"}}})
	json.Unmarshal(raw, &d.image)
	d.network.ID = strings.Repeat("d", 64)
	d.network.Name = engine.RuntimeNetworkName(scope.ScopeID)
	d.network.Driver = "bridge"
	d.network.Scope = "local"
	d.network.Internal = true
	d.network.Labels = labels
	d.network.Labels["com.docker.compose.network"] = "isolated"
	return c, scope, model, d
}
func TestRuntimeObservationRejectsChangedResourcesAndSecurity(t *testing.T) {
	for _, fault := range []string{"none", "absent", "foreign", "network-public", "network-driver", "network-scope", "network-label", "network-options", "host-pid", "host-ipc", "host-uts", "host-userns", "host-cgroup", "foreign-endpoint", "image", "user", "command", "entrypoint", "environment", "duplicate-env", "privileged", "memory", "cpu", "pids", "readonly", "capability", "security", "host-network", "devices", "binds", "auto-remove", "publish-all", "restart", "port", "tmpfs", "mount", "endpoint", "dead", "paused", "oom", "unknown-state", "running-without-pid"} {
		t.Run(fault, func(t *testing.T) {
			c, scope, model, d := runtimeFixture(t)
			switch fault {
			case "absent":
				d.present = false
			case "foreign":
				d.foreign = true
			case "network-public":
				d.network.Internal = false
			case "network-driver":
				d.network.Driver = "overlay"
			case "network-scope":
				d.network.Scope = "swarm"
			case "network-label":
				d.network.Labels[scopeLabel] = "foreign"
			case "network-options":
				d.network.Options = map[string]string{"foreign": "true"}
			case "host-pid":
				d.container.HostConfig.PidMode = "host"
			case "host-ipc":
				d.container.HostConfig.IpcMode = "host"
			case "host-uts":
				d.container.HostConfig.UTSMode = "host"
			case "host-userns":
				d.container.HostConfig.UsernsMode = "host"
			case "host-cgroup":
				d.container.HostConfig.CgroupnsMode = "host"
			case "foreign-endpoint":
				d.network.Containers = map[string]struct{ Name string }{strings.Repeat("f", 64): {Name: "foreign"}}
			case "image":
				d.container.Image = "sha256:" + strings.Repeat("f", 64)
			case "user":
				d.container.Config.User = "0:0"
			case "command":
				d.container.Config.Cmd = []string{"changed"}
			case "entrypoint":
				d.container.Config.Entrypoint = []string{"/bin/sh"}
			case "environment":
				d.container.Config.Env = append(d.container.Config.Env, "SECRET=foreign")
			case "duplicate-env":
				d.container.Config.Env = append(d.container.Config.Env, "PATH=/foreign")
			case "privileged":
				d.container.HostConfig.Privileged = true
			case "memory":
				d.container.HostConfig.Memory++
			case "cpu":
				d.container.HostConfig.NanoCPUs++
			case "pids":
				d.container.HostConfig.PIDs++
			case "readonly":
				d.container.HostConfig.ReadonlyRootfs = false
			case "capability":
				d.container.HostConfig.CapAdd = []string{"SYS_ADMIN"}
			case "security":
				d.container.HostConfig.SecurityOpt = nil
			case "host-network":
				d.container.HostConfig.NetworkMode = "host"
			case "devices":
				d.container.HostConfig.Devices = []json.RawMessage{json.RawMessage(`{}`)}
			case "binds":
				d.container.HostConfig.Binds = []string{"/host:/container"}
			case "auto-remove":
				d.container.HostConfig.AutoRemove = true
			case "publish-all":
				d.container.HostConfig.PublishAllPorts = true
			case "restart":
				d.container.HostConfig.RestartPolicy.Name = "always"
			case "port":
				d.container.HostConfig.PortBindings = map[string][]struct {
					HostIP   string `json:"HostIp"`
					HostPort string
				}{"80/tcp": {{HostIP: "0.0.0.0", HostPort: "80"}}}
			case "tmpfs":
				d.container.HostConfig.Tmpfs = map[string]string{"/foreign": "rw"}
			case "mount":
				d.container.Mounts = append(d.container.Mounts, struct {
					Type, Name, Source, Destination, Driver string
					RW                                      bool
				}{Type: "bind", Source: "/host", Destination: "/foreign", RW: true})
			case "endpoint":
				d.container.NetworkSettings.Networks = nil
			case "dead":
				d.container.State.Dead = true
			case "paused":
				d.container.State.Paused = true
			case "oom":
				d.container.State.OOMKilled = true
			case "unknown-state":
				d.container.State.Status = "removing"
			case "running-without-pid":
				d.container.State.Status = "running"
				d.container.State.Running = true
			}
			result, e := c.ObserveRuntime(context.Background(), d, scope, model)
			if fault == "none" {
				if e != nil || result.Status != "observed_complete_compose_resources" {
					t.Fatal(result, e)
				}
			} else if fault == "absent" {
				if e != nil || result.Status != "observed_absent_compose_resources" {
					t.Fatal(result, e)
				}
			} else if e == nil {
				t.Fatal("changed resource accepted", fault)
			}
		})
	}
}
func TestExistingRuntimeIsPreservedAndInterpolationEscapesLiteralValues(t *testing.T) {
	c, scope, model, d := runtimeFixture(t)
	if _, e := c.CreateRuntime(context.Background(), d, scope, model); e == nil || e.Error() != "OFFLINE_COMPOSE_EXISTING_RESOURCES_PRESERVED" {
		t.Fatal(e)
	}
	original := map[string]any{"command": []any{"$HOME", "${HOME}", "$$"}, "environment": map[string]any{"VALUE": "a$b"}}
	raw, _ := json.Marshal(escapeInterpolation(original))
	if string(raw) != `{"command":["$$HOME","$${HOME}","$$$$"],"environment":{"VALUE":"a$$b"}}` {
		t.Fatal(string(raw))
	}
}

func TestCreateRefusesImplicitImageVolumesBeforeAnyCreate(t *testing.T) {
	c, scope, model, d := runtimeFixture(t)
	d.present = false
	d.image.Config.Volumes = map[string]json.RawMessage{"/foreign": json.RawMessage(`{}`)}
	if _, e := c.CreateRuntime(context.Background(), d, scope, model); e == nil || e.Error() != "OFFLINE_COMPOSE_RUNTIME_IMAGE_MISMATCH" {
		t.Fatal("implicit image volume reached create", e)
	}
}

func TestImageDeclaredVolumesRequireExactNamedCoverage(t *testing.T) {
	target := "/var/lib/postgresql"
	declarations := map[string]json.RawMessage{target: json.RawMessage(`{ }`)}
	service := Service{Mounts: []Mount{{Type: "volume", Source: "postgres", Target: target}}}
	if !imageVolumeCoverage(service, declarations) || !imageVolumeCoverage(Service{}, nil) {
		t.Fatal("exact named coverage or no declarations refused")
	}
	for _, fault := range []string{"missing", "parent", "child", "bind", "tmpfs", "duplicate", "unsafe", "trailing", "null", "array", "value"} {
		t.Run(fault, func(t *testing.T) {
			s := service
			s.Mounts = append([]Mount{}, service.Mounts...)
			d := map[string]json.RawMessage{target: json.RawMessage(`{}`)}
			switch fault {
			case "missing":
				s.Mounts = nil
			case "parent":
				s.Mounts[0].Target = "/var/lib"
			case "child":
				s.Mounts[0].Target = target + "/data"
			case "bind":
				s.Mounts[0].Type = "bind"
			case "tmpfs":
				s.Mounts = nil
				s.Tmpfs = []Tmpfs{{Target: target}}
			case "duplicate":
				s.Mounts = append(s.Mounts, s.Mounts[0])
			case "unsafe":
				d = map[string]json.RawMessage{"/proc": json.RawMessage(`{}`)}
				s.Mounts[0].Target = "/proc"
			case "trailing":
				d = map[string]json.RawMessage{target + "/": json.RawMessage(`{}`)}
				s.Mounts[0].Target = target + "/"
			case "null":
				d[target] = json.RawMessage(`null`)
			case "array":
				d[target] = json.RawMessage(`[]`)
			case "value":
				d[target] = json.RawMessage(`{"foreign":true}`)
			}
			if imageVolumeCoverage(s, d) {
				t.Fatal("uncovered or malformed image declaration accepted")
			}
		})
	}
}

func TestRuntimeObservedImageVolumeDeclarationsMatchExactNamedMounts(t *testing.T) {
	for _, fault := range []string{"none", "image-uncovered", "container-missing", "container-extra", "container-value", "anonymous", "copy"} {
		t.Run(fault, func(t *testing.T) {
			c, scope, model, d := runtimeFixture(t)
			target := "/var/lib/projectone/models"
			service := model.Services["control"]
			service.Mounts = []Mount{{Type: "volume", Source: "models", Target: target}}
			model.Services["control"] = service
			model.Volumes = []string{"models"}
			d.image.Config.Volumes = map[string]json.RawMessage{target: json.RawMessage(`{}`)}
			raw, _ := json.Marshal(map[string]any{"Config": map[string]any{"Volumes": map[string]any{target: map[string]any{}}}, "HostConfig": map[string]any{"Mounts": []any{map[string]any{"Type": "volume", "Source": "projectone-" + scope.ScopeID + "-models", "Target": target, "ReadOnly": false, "VolumeOptions": map[string]any{"NoCopy": true}}}}, "Mounts": []any{map[string]any{"Type": "volume", "Name": "projectone-" + scope.ScopeID + "-models", "Destination": target, "Driver": "local", "RW": true}}})
			if json.Unmarshal(raw, &d.container) != nil {
				t.Fatal("volume observation fixture")
			}
			switch fault {
			case "image-uncovered":
				d.image.Config.Volumes["/foreign"] = json.RawMessage(`{}`)
			case "container-missing":
				d.container.Config.Volumes = nil
			case "container-extra":
				d.container.Config.Volumes["/foreign"] = json.RawMessage(`{}`)
			case "container-value":
				d.container.Config.Volumes[target] = json.RawMessage(`{"foreign":true}`)
			case "anonymous":
				d.container.Mounts[0].Name = strings.Repeat("f", 64)
			case "copy":
				d.container.HostConfig.Mounts[0].VolumeOptions.NoCopy = false
			}
			_, e := c.ObserveRuntime(context.Background(), d, scope, model)
			if fault == "none" && e != nil || fault != "none" && e == nil {
				t.Fatal(fault, e)
			}
		})
	}
}
func TestObservedPinnedNetworkDefaultsAreExact(t *testing.T) {
	c, scope, model, d := runtimeFixture(t)
	d.network.Options = map[string]string{"com.docker.network.enable_ipv4": "true", "com.docker.network.enable_ipv6": "false"}
	if _, e := c.ObserveRuntime(context.Background(), d, scope, model); e != nil {
		t.Fatal(e)
	}
	d.network.Options["com.docker.network.enable_ipv6"] = "true"
	if _, e := c.ObserveRuntime(context.Background(), d, scope, model); e == nil {
		t.Fatal("changed IPv6 behavior accepted")
	}
}

func TestPublishedRuntimeRequiresIngressProofAndActualPortBindings(t *testing.T) {
	for _, fault := range []string{"none", "missing-ingress", "internal-ingress", "foreign-ingress-endpoint", "missing-actual-port", "public-actual-port", "wrong-ingress-endpoint"} {
		t.Run(fault, func(t *testing.T) {
			c, scope, model, d := runtimeFixture(t)
			service := model.Services["control"]
			service.Ports = []Port{{HostIP: "127.0.0.1", Published: 18443, Target: 8443}}
			model.Services["control"] = service
			d.ingress = d.network
			d.ingress.ID = strings.Repeat("e", 64)
			d.ingress.Name = engine.RuntimeIngressName(scope.ScopeID)
			d.ingress.Internal = false
			d.ingress.Labels = map[string]string{scopeLabel: scope.ScopeID, purposeLabel: "isolated-validation", "com.docker.compose.project": engine.RuntimeProject(scope.ScopeID), "com.docker.compose.network": "ingress"}
			d.ingressPresent = true
			d.container.State.Status = "running"
			d.container.State.Running = true
			d.container.State.PID = 123
			endpoint := d.container.NetworkSettings.Networks[engine.RuntimeNetworkName(scope.ScopeID)]
			endpoint.NetworkID = d.network.ID
			d.container.NetworkSettings.Networks[engine.RuntimeNetworkName(scope.ScopeID)] = endpoint
			endpoint.NetworkID = d.ingress.ID
			d.container.NetworkSettings.Networks[engine.RuntimeIngressName(scope.ScopeID)] = endpoint
			bindings := map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string
			}{"8443/tcp": {{HostIP: "127.0.0.1", HostPort: "18443"}}}
			d.container.HostConfig.PortBindings = bindings
			d.container.NetworkSettings.Ports = bindings
			switch fault {
			case "missing-ingress":
				d.ingressPresent = false
			case "internal-ingress":
				d.ingress.Internal = true
			case "foreign-ingress-endpoint":
				d.ingress.Containers = map[string]struct{ Name string }{strings.Repeat("f", 64): {Name: "foreign"}}
			case "missing-actual-port":
				d.container.NetworkSettings.Ports = nil
			case "public-actual-port":
				d.container.NetworkSettings.Ports = map[string][]struct {
					HostIP   string `json:"HostIp"`
					HostPort string
				}{"8443/tcp": {{HostIP: "0.0.0.0", HostPort: "18443"}}}
			case "wrong-ingress-endpoint":
				endpoint.NetworkID = strings.Repeat("f", 64)
				d.container.NetworkSettings.Networks[engine.RuntimeIngressName(scope.ScopeID)] = endpoint
			}
			result, e := c.ObserveRuntime(context.Background(), d, scope, model)
			if fault == "none" {
				if e != nil || result.IngressNetworkID != d.ingress.ID || result.Status != "observed_complete_compose_resources" {
					t.Fatal(result, e)
				}
			} else if e == nil {
				t.Fatal("invalid ingress/publication accepted")
			}
		})
	}
}
func TestDatabaseCannotJoinPublishedIngress(t *testing.T) {
	c, scope, model := modelFixture(t)
	service := model.Services["control"]
	model.Services = map[string]Service{"postgres": service}
	if _, e := c.document(scope, model); e == nil {
		t.Fatal("database host publication accepted")
	}
}
