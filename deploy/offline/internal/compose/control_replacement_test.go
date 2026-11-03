package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

func controlConfigurationFixture(t *testing.T) (Model, string) {
	t.Helper()
	_, _, model := modelFixture(t)
	service := model.Services["control"]
	service.Mounts = append(service.Mounts, Mount{Type: "volume", Source: "control-config", Target: "/run/projectone/control", ReadOnly: true})
	model.Services["control"] = service
	model.Volumes = append(model.Volumes, "control-config")
	return model, strings.Repeat("e", 32)
}
func TestControlConfigurationProjectionChangesOnlyOneSourceAndNeverAliases(t *testing.T) {
	model, generation := controlConfigurationFixture(t)
	before, _ := json.Marshal(model)
	projected, e := ControlConfigurationModel(model, generation)
	if e != nil {
		t.Fatal(e)
	}
	expectedRaw, _ := json.Marshal(model)
	var expected Model
	json.Unmarshal(expectedRaw, &expected)
	next := engine.TLSConfigurationRole + "-" + generation
	expected.Volumes[1] = next
	service := expected.Services["control"]
	service.Mounts[1].Source = next
	expected.Services["control"] = service
	if !reflect.DeepEqual(projected, expected) {
		t.Fatal("unrelated model changed")
	}
	actual, _ := json.Marshal(model)
	if string(before) != string(actual) {
		t.Fatal("original model mutated")
	}
	s := projected.Services["control"]
	s.Mounts[0].Source = "mutated"
	s.Command = []string{"new"}
	projected.Services["control"] = s
	projected.Volumes[0] = "mutated"
	actual, _ = json.Marshal(model)
	if string(before) != string(actual) {
		t.Fatal("projected model aliases original")
	}
}
func TestControlConfigurationProjectionRefusesUnsafeSharedOrCollidingRoles(t *testing.T) {
	for _, fault := range []string{"generation", "missing-declaration", "duplicate-declaration", "collision", "missing-mount", "duplicate-mount", "writable", "bind", "path", "shared"} {
		t.Run(fault, func(t *testing.T) {
			model, generation := controlConfigurationFixture(t)
			service := model.Services["control"]
			switch fault {
			case "generation":
				generation = "../unsafe"
			case "missing-declaration":
				model.Volumes = model.Volumes[:1]
			case "duplicate-declaration":
				model.Volumes = append(model.Volumes, "control-config")
			case "collision":
				model.Volumes = append(model.Volumes, engine.TLSConfigurationRole+"-"+generation)
			case "missing-mount":
				service.Mounts = service.Mounts[:1]
			case "duplicate-mount":
				service.Mounts = append(service.Mounts, service.Mounts[1])
			case "writable":
				service.Mounts[1].ReadOnly = false
			case "bind":
				service.Mounts[1].Type = "bind"
			case "path":
				service.Mounts[1].Target = "/run/../unsafe"
			case "shared":
				model.Services["postgres"] = service
			}
			model.Services["control"] = service
			if _, e := ControlConfigurationModel(model, generation); e == nil {
				t.Fatal("unsafe projection admitted")
			}
		})
	}
}
func TestControlReplacementPeerProofRequiresStoppedSamePeersNetworksAndNewControl(t *testing.T) {
	old := RuntimeStatus{Status: "observed_complete_compose_resources", NetworkID: strings.Repeat("a", 64), IngressNetworkID: strings.Repeat("b", 64), Services: map[string]RuntimeServiceStatus{"control": {ID: strings.Repeat("c", 64), State: "created"}, "postgres": {ID: strings.Repeat("d", 64), State: "exited"}}}
	absent := RuntimeStatus{Status: "observed_partial_compose_resources", NetworkID: old.NetworkID, IngressNetworkID: old.IngressNetworkID, Services: map[string]RuntimeServiceStatus{"postgres": old.Services["postgres"]}}
	complete := absent
	complete.Status = "observed_complete_compose_resources"
	complete.Services = map[string]RuntimeServiceStatus{"postgres": old.Services["postgres"], "control": {ID: strings.Repeat("e", 64), State: "created"}}
	if !replacementPeersMatch(old, absent, false) || !replacementPeersMatch(old, complete, true) {
		t.Fatal("expected initial replacement proof")
	}
	for _, fault := range []string{"network", "ingress", "peer-ID", "peer-state", "extra", "old-control", "started-control", "old-running", "missing-peer"} {
		t.Run(fault, func(t *testing.T) {
			beforeRaw, _ := json.Marshal(old)
			afterRaw, _ := json.Marshal(complete)
			var before, after RuntimeStatus
			json.Unmarshal(beforeRaw, &before)
			json.Unmarshal(afterRaw, &after)
			switch fault {
			case "network":
				after.NetworkID = strings.Repeat("f", 64)
			case "ingress":
				after.IngressNetworkID = ""
			case "peer-ID":
				p := after.Services["postgres"]
				p.ID = strings.Repeat("f", 64)
				after.Services["postgres"] = p
			case "peer-state":
				p := after.Services["postgres"]
				p.State = "running"
				after.Services["postgres"] = p
			case "extra":
				after.Services["gateway"] = old.Services["postgres"]
			case "old-control":
				after.Services["control"] = before.Services["control"]
			case "started-control":
				p := after.Services["control"]
				p.State = "running"
				after.Services["control"] = p
			case "old-running":
				p := before.Services["control"]
				p.State = "running"
				before.Services["control"] = p
			case "missing-peer":
				delete(after.Services, "postgres")
			}
			if replacementPeersMatch(before, after, true) {
				t.Fatal("changed/started replacement accepted")
			}
		})
	}
	if replacementPeersMatch(old, complete, false) || replacementPeersMatch(old, absent, true) {
		t.Fatal("create/remove outcome conflated")
	}
}

// File-backed CLI/metadata double only; no Docker resource or backend is created.
type replacementCLIDouble struct {
	base   *runtimeDouble
	marker string
}

func (d replacementCLIDouble) present() bool { _, e := os.Stat(d.marker); return e == nil }
func (d replacementCLIDouble) Inspect(ctx context.Context, name string) (engine.Image, bool, error) {
	return d.base.Inspect(ctx, name)
}
func (d replacementCLIDouble) InspectRuntime(context.Context, string, string) (engine.RuntimeContainer, bool, error) {
	return d.base.container, d.present(), nil
}
func (d replacementCLIDouble) InspectRuntimeNetwork(ctx context.Context, scope string) (engine.RuntimeNetwork, bool, error) {
	return d.base.InspectRuntimeNetwork(ctx, scope)
}
func (d replacementCLIDouble) InspectRuntimeIngress(ctx context.Context, scope string) (engine.RuntimeNetwork, bool, error) {
	return d.base.InspectRuntimeIngress(ctx, scope)
}
func (d replacementCLIDouble) RuntimeCandidates(ctx context.Context, scope string, networks bool) ([]engine.RuntimeObject, error) {
	if networks {
		return d.base.RuntimeCandidates(ctx, scope, true)
	}
	objects := []engine.RuntimeObject{}
	if d.present() {
		objects = append(objects, engine.RuntimeObject{ID: d.base.container.ID})
	}
	if d.base.foreign {
		objects = append(objects, engine.RuntimeObject{ID: strings.Repeat("f", 64)})
	}
	return objects, nil
}
func replacementCLIFixture(t *testing.T, lost bool) (*Client, RenderContext, Model, RuntimeStatus, replacementCLIDouble, string, string) {
	t.Helper()
	_, scope, model, metadata := runtimeFixture(t)
	service := model.Services["control"]
	service.Mounts = []Mount{{Type: "volume", Source: "control-config", Target: "/run/projectone/control", ReadOnly: true}}
	model.Services["control"] = service
	model.Volumes = []string{"control-config"}
	model, e := ControlConfigurationModel(model, strings.Repeat("a", 32))
	if e != nil {
		t.Fatal(e)
	}
	before := RuntimeStatus{Status: "observed_complete_compose_resources", NetworkID: metadata.network.ID, Services: map[string]RuntimeServiceStatus{"control": {ID: metadata.container.ID, State: "created"}}}
	metadata.container.ID = strings.Repeat("e", 64)
	encoded, _ := json.Marshal(metadata.container)
	var object map[string]any
	json.Unmarshal(encoded, &object)
	volume := "projectone-" + scope.ScopeID + "-" + model.Volumes[0]
	object["HostConfig"].(map[string]any)["Mounts"] = []any{map[string]any{"Type": "volume", "Source": volume, "Target": "/run/projectone/control", "ReadOnly": true, "VolumeOptions": map[string]any{"NoCopy": true}}}
	object["Mounts"] = []any{map[string]any{"Type": "volume", "Name": volume, "Destination": "/run/projectone/control", "Driver": "local", "RW": false}}
	encoded, _ = json.Marshal(object)
	json.Unmarshal(encoded, &metadata.container)
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private test files")
	}
	marker, calls, input := filepath.Join(root, "created"), filepath.Join(root, "calls"), filepath.Join(root, "input.json")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	// Paths are generated test-only references. No input is evaluated by a shell.
	script := "case \" $* \" in *\" version \"*) printf '{\"version\":\"v5.5.1\"}' ;; *\" create \"*) printf x >> " + quote(calls) + "; cat > " + quote(input) + "; touch " + quote(marker) + "; "
	if lost {
		script += "exit 17"
	} else {
		script += "exit 0"
	}
	script += " ;; *) exit 91 ;; esac"
	_, client := fixture(t, script)
	return client, scope, model, before, replacementCLIDouble{base: metadata, marker: marker}, calls, input
}
func TestControlReplacementSelectedCLIAndExplicitLostResultObservation(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "lost-result"}[lost], func(t *testing.T) {
			client, scope, model, before, reader, calls, input := replacementCLIFixture(t, lost)
			actual, e := client.CreateReplacementControl(context.Background(), reader, scope, model, before)
			if lost {
				if e == nil {
					t.Fatal("lost CLI result accepted")
				}
				actual, e = client.ObserveControlReplacement(context.Background(), reader, scope, model, before, true)
			}
			if e != nil || actual.Services["control"].ID != reader.base.container.ID {
				t.Fatal("selected creation/readback", e)
			}
			body, e := os.ReadFile(input)
			if e != nil || !strings.Contains(string(body), "$$LITERAL") {
				t.Fatal("literal model not preserved", e)
			}
			if _, e = client.CreateReplacementControl(context.Background(), reader, scope, model, before); e == nil {
				t.Fatal("existing replacement resubmitted")
			}
			count, e := os.ReadFile(calls)
			if e != nil || string(count) != "x" {
				t.Fatal("CLI create replayed", e)
			}
		})
	}
}
