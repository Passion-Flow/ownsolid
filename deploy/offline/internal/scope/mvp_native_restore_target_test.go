package scope

import (
	"path"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

func mvpNativeDomainFixture() (*locked, compose.Model, configuration.NativePostgreSQLRecoveryDescriptor) {
	l := &locked{marker: Marker{ScopeID: strings.Repeat("a", 32)}}
	d := configuration.NativePostgreSQLRecoveryDescriptor{StoreRoots: [4]string{"/stores/models/private", "/stores/datasets/private", "/stores/training/private", "/stores/preparations/private"}, RegistryRoot: "/registry/private"}
	service := compose.Service{User: "10001:10002"}
	for i, role := range []string{"models", "datasets", "training", "preparations", engine.NativePostgreSQLRegistryRole} {
		target := d.RegistryRoot
		if i < 4 {
			target = d.StoreRoots[i]
		}
		service.Mounts = append(service.Mounts, compose.Mount{Type: "volume", Source: role, Target: path.Dir(target)})
		v := VolumeRecord{Key: role, UID: 10001, GID: 10002, ResourceOperationID: strings.Repeat("b", 32), Phase: phasePrepared}
		l.events = append(l.events, Event{Action: "volume_prepare", Outcome: "completed", Volume: &v})
	}
	return l, compose.Model{Services: map[string]compose.Service{"control": service}}, d
}

func TestMVPNativeRestoreDomainMountsKeepActualPreparedOwnersAndPrivateRuntimeTargets(t *testing.T) {
	l, model, d := mvpNativeDomainFixture()
	stores, registry, e := l.mvpNativeRestoreDomainMounts(model, d)
	if e != nil {
		t.Fatal(e)
	}
	for i, role := range []string{"models", "datasets", "training", "preparations"} {
		if stores[i].Owner.Role != role || stores[i].Owner.ScopeID != l.marker.ScopeID || stores[i].Owner.OperationID != strings.Repeat("b", 32) || stores[i].Target != d.StoreRoots[i] {
			t.Fatal("actual owner replaced by a manufactured restore generation")
		}
	}
	if registry.Owner.Role != engine.NativePostgreSQLRegistryRole || registry.Target != d.RegistryRoot {
		t.Fatal("supplementary registry confused with four domain stores")
	}
}

func TestMVPNativeRestoreDomainMountsRejectChangedOwnerAndRuntimeBindings(t *testing.T) {
	for _, name := range []string{"missing", "uid", "gid", "unprepared", "readonly-runtime", "wrong-target", "host-bind", "duplicate", "missing-registry"} {
		t.Run(name, func(t *testing.T) {
			l, model, d := mvpNativeDomainFixture()
			service := model.Services["control"]
			switch name {
			case "missing":
				l.events = l.events[1:]
			case "uid":
				l.events[0].Volume.UID++
			case "gid":
				l.events[0].Volume.GID++
			case "unprepared":
				l.events[0].Volume.Phase = phaseReserved
			case "readonly-runtime":
				service.Mounts[0].ReadOnly = true
			case "wrong-target":
				service.Mounts[0].Target = "/different"
			case "host-bind":
				service.Mounts[0].Type = "bind"
			case "duplicate":
				service.Mounts = append(service.Mounts, service.Mounts[0])
			case "missing-registry":
				l.events = l.events[:4]
			}
			model.Services["control"] = service
			if _, _, e := l.mvpNativeRestoreDomainMounts(model, d); e == nil {
				t.Fatal("changed owner/runtime admitted")
			}
		})
	}
}
