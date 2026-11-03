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

func TestCumulativeControlModelPreservesPeersPrivateDataAndSecurity(t *testing.T) {
	_, scope, base := modelFixture(t)
	control := base.Services["control"]
	control.Command = []string{"serve", "--config", "/run/projectone/control/private/runtime.yaml"}
	control.Mounts = append(control.Mounts, Mount{Type: "volume", Source: "control-config", Target: "/run/projectone/control", ReadOnly: true})
	base.Services["control"] = control
	base.Services["postgres"] = Service{Image: control.Image, User: "10002:10002", MemoryBytes: 64 << 20, MilliCPU: 250, PIDs: 32, Restart: "no", ReadOnly: true}
	base.Volumes = append(base.Volumes, "control-config")
	before, _ := json.Marshal(base)
	generation := strings.Repeat("c", 32)
	image := "sha256:" + strings.Repeat("d", 64)
	next, e := CumulativeControlModel(base, generation, image, scope.ScopeRoot)
	if e != nil {
		t.Fatal(e)
	}
	after, _ := json.Marshal(base)
	if string(before) != string(after) || !reflect.DeepEqual(base.Services["postgres"], next.Services["postgres"]) {
		t.Fatal("source or peer mutated")
	}
	current := next.Services["control"]
	current.Image = control.Image
	current.Mounts = append([]Mount{}, current.Mounts[:len(current.Mounts)-1]...)
	current.Mounts[len(current.Mounts)-1].Source = "control-config"
	if !reflect.DeepEqual(current, control) {
		t.Fatal("unrelated user/resource/command/secret/data mount changed")
	}
	last := next.Services["control"].Mounts[len(next.Services["control"].Mounts)-1]
	if last != (Mount{Type: "bind", Source: filepath.Join(scope.ScopeRoot, view.RelativeDirectory), Target: view.ContainerDirectory, ReadOnly: true}) || next.Volumes[len(next.Volumes)-1] != engine.CumulativeControlRole+"-"+generation {
		t.Fatal("wrong generation or public mount")
	}
	for _, mutate := range []func(*Model){func(m *Model) { m.Volumes = append(m.Volumes, engine.CumulativeControlRole+"-"+generation) }, func(m *Model) {
		s := m.Services["control"]
		s.Mounts[len(s.Mounts)-1].ReadOnly = false
		m.Services["control"] = s
	}, func(m *Model) {
		s := m.Services["control"]
		s.Mounts = append(s.Mounts, last)
		m.Services["control"] = s
	}} {
		var changed Model
		json.Unmarshal(before, &changed)
		mutate(&changed)
		if _, e := CumulativeControlModel(changed, generation, image, scope.ScopeRoot); e == nil {
			t.Fatal("contradictory mount/generation admitted")
		}
	}
}
