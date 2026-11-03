package compose

import (
	"os"
	"path/filepath"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
	"strconv"
	"testing"
)

func TestReleaseViewExactReadonlyDistinctUIDMount(t *testing.T) {
	client, scope, model := modelFixture(t)
	dir := filepath.Join(scope.ScopeRoot, view.RelativeDirectory)
	if e := os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	os.Chmod(dir, 0755)
	service := model.Services["control"]
	service.User = strconv.Itoa(os.Geteuid()+100) + ":10034"
	service.Mounts = append(service.Mounts, Mount{Type: "bind", Source: dir, Target: view.ContainerDirectory, ReadOnly: true})
	model.Services["control"] = service
	if _, e := client.document(scope, model); e != nil {
		t.Fatal(e)
	}
	for _, fault := range []string{"write", "ancestor", "wrong-target", "same-uid", "foreign-service"} {
		t.Run(fault, func(t *testing.T) {
			v := service
			v.Mounts = append([]Mount{}, service.Mounts...)
			m := v.Mounts[len(v.Mounts)-1]
			switch fault {
			case "write":
				m.ReadOnly = false
			case "ancestor":
				m.Source = filepath.Dir(dir)
			case "wrong-target":
				m.Target = "/foreign"
			case "same-uid":
				v.User = strconv.Itoa(os.Geteuid()) + ":10034"
			}
			v.Mounts[len(v.Mounts)-1] = m
			changed := Model{Services: map[string]Service{"control": v}, Volumes: model.Volumes}
			if fault == "foreign-service" {
				delete(changed.Services, "control")
				changed.Services["gateway"] = v
			}
			if _, e := client.document(scope, changed); e == nil {
				t.Fatal("unsafe release-view mount admitted")
			}
		})
	}
}
