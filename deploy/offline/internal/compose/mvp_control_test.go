package compose

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

func TestMVPControlModelRetainsExistingM17ViewPeersUIDAndPrivateMounts(t *testing.T) {
	_, scope, base := modelFixture(t)
	control := base.Services["control"]
	control.Command = []string{"serve", "--config", "/run/projectone/control/private/runtime.yaml"}
	control.Mounts = append(control.Mounts,
		Mount{Type: "volume", Source: "control-config", Target: "/run/projectone/control", ReadOnly: true},
		Mount{Type: "bind", Source: filepath.Join(scope.ScopeRoot, view.RelativeDirectory), Target: view.ContainerDirectory, ReadOnly: true})
	base.Services["control"] = control
	base.Services["postgres"] = Service{Image: control.Image, User: "10002:10002", MemoryBytes: 64 << 20, MilliCPU: 250, PIDs: 32, Restart: "no", ReadOnly: true}
	base.Volumes = append(base.Volumes, "control-config")
	before, _ := json.Marshal(base)
	generation, image := strings.Repeat("c", 32), "sha256:"+strings.Repeat("d", 64)
	next, err := MVPControlModel(base, generation, image, scope.ScopeRoot)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(base)
	if string(before) != string(after) || !reflect.DeepEqual(base.Services["postgres"], next.Services["postgres"]) {
		t.Fatal("source or peer mutated")
	}
	current := next.Services["control"]
	if current.User != control.User || !reflect.DeepEqual(current.Command, control.Command) || current.Image != image || len(current.Mounts) != len(control.Mounts) || current.Mounts[len(current.Mounts)-1] != control.Mounts[len(control.Mounts)-1] || current.Mounts[len(current.Mounts)-2].Source != engine.CumulativeControlRole+"-"+generation {
		t.Fatal("runtime user, private command, view binding or target configuration changed")
	}
	for _, mutate := range []func(*Model){
		func(m *Model) { s := m.Services["control"]; s.User = "root"; m.Services["control"] = s },
		func(m *Model) {
			s := m.Services["control"]
			s.Mounts = append(s.Mounts, Mount{Type: "volume", Source: "store", Target: "/run/projectone/control/private", ReadOnly: false})
			m.Services["control"] = s
		},
		func(m *Model) {
			s := m.Services["control"]
			s.Mounts = append(s.Mounts, Mount{Type: "volume", Source: "undeclared", Target: "/foreign", ReadOnly: false})
			m.Services["control"] = s
		},
		func(m *Model) {
			s := m.Services["control"]
			s.Mounts = s.Mounts[:len(s.Mounts)-1]
			m.Services["control"] = s
		},
		func(m *Model) {
			s := m.Services["control"]
			s.Mounts[len(s.Mounts)-1].ReadOnly = false
			m.Services["control"] = s
		},
		func(m *Model) {
			s := m.Services["control"]
			s.Mounts[len(s.Mounts)-1].Source = "/foreign/view"
			m.Services["control"] = s
		},
		func(m *Model) {
			s := m.Services["control"]
			s.Mounts = append(s.Mounts, s.Mounts[len(s.Mounts)-1])
			m.Services["control"] = s
		},
	} {
		var changed Model
		json.Unmarshal(before, &changed)
		mutate(&changed)
		if _, err := MVPControlModel(changed, generation, image, scope.ScopeRoot); err == nil {
			t.Fatal("missing/foreign/duplicate/writable projection accepted")
		}
	}
	if _, err := CumulativeControlModel(base, generation, image, scope.ScopeRoot); err == nil {
		t.Fatal("legacy M16 producer silently accepts an existing view")
	}
}

func TestMVPRegistryRejectsShadowedForeignOrReadonlyMounts(t *testing.T) {
	model := Model{Services: map[string]Service{"control": {User: "1001:1002", Mounts: []Mount{{Type: "volume", Source: "models", Target: "/models"}}}}, Volumes: []string{"models"}}
	next, e := MVPRegistryModel(model, "/registry")
	if e != nil {
		t.Fatal(e)
	}
	if got, e := MVPRegistryTarget(next); e != nil || got != "/registry" {
		t.Fatal(got, e)
	}
	if len(model.Volumes) != 1 || len(model.Services["control"].Mounts) != 1 {
		t.Fatal("original model modified")
	}
	for _, target := range []string{"/models", "/models/child", "/", "relative"} {
		if _, e := MVPRegistryModel(model, target); e == nil {
			t.Fatal("shadow target accepted", target)
		}
	}
	s := next.Services["control"]
	s.Mounts[len(s.Mounts)-1].ReadOnly = true
	next.Services["control"] = s
	if _, e := MVPRegistryTarget(next); e == nil {
		t.Fatal("readonly registry accepted")
	}
}

func TestMVPRegistryReleaseHasOnlyItsIndependentReadonlyParent(t *testing.T) {
	base := Model{Services: map[string]Service{"control": {User: "1001:1002", Mounts: []Mount{{Type: "volume", Source: "models", Target: "/models"}, {Type: "volume", Source: "upgrade-control-abc", Target: "/config", ReadOnly: true}}}}, Volumes: []string{"models", "upgrade-control-abc"}}
	store, e := MVPRegistryModel(base, "/registry/store")
	if e != nil {
		t.Fatal(e)
	}
	model, e := MVPRegistryReleaseModel(store, "/registry")
	if e != nil {
		t.Fatal(e)
	}
	if root, e := MVPRegistryReleaseTarget(model); e != nil || root != "/registry" {
		t.Fatal(root, e)
	}
	for _, target := range []string{"/models", "/config", "/registry/store", "/", "/foreign"} {
		if _, e := MVPRegistryReleaseModel(store, target); e == nil {
			t.Fatal("foreign or shadowing artifact root", target)
		}
	}
	s := model.Services["control"]
	s.Mounts[len(s.Mounts)-1].ReadOnly = false
	model.Services["control"] = s
	if _, e := MVPRegistryReleaseTarget(model); e == nil {
		t.Fatal("writable artifact mount accepted")
	}
}
