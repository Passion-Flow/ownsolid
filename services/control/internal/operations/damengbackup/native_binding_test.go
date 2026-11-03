//go:build cgo && odbc_manager

package damengbackup

import (
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/registryconfig"
	"testing"
)

func TestMatchedStoreRestoreRequiresOriginalDatabaseLineage(t *testing.T) {
	m := MatchedStores{DatabaseRoot: "/opt/projectone/private/original", DatabaseCompleteSHA256: sha([]byte("original completion")), Database: State{ServerID: "original-server", RowsSHA256: sha([]byte("rows"))}}
	original := Intent{Kind: "restore", BackupRoot: m.DatabaseRoot, BackupCompleteSHA256: m.DatabaseCompleteSHA256, Source: m.Database}
	if !matchedRestoreLineage(original, m) {
		t.Fatal("original lineage refused")
	}
	for _, name := range []string{"kind", "root", "completion", "source"} {
		t.Run(name, func(t *testing.T) {
			i := original
			switch name {
			case "kind":
				i.Kind = "backup"
			case "root":
				i.BackupRoot = "/opt/projectone/private/different"
			case "completion":
				i.BackupCompleteSHA256 = sha([]byte("different completion"))
			case "source":
				i.Source.ServerID = "different-server"
			}
			if matchedRestoreLineage(i, m) {
				t.Fatal("different backup accepted solely from matching row fingerprint")
			}
		})
	}
}

func TestNativeConfigurationBindsPrivateReferencesAndEffectiveOwners(t *testing.T) {
	base := config.Config{}
	original := nativePlatformConfigurationSHA256(base)
	if !digest.MatchString(original) {
		t.Fatal("configuration digest unavailable")
	}
	for _, name := range []string{"release", "cluster", "monitoring", "middleware", "middleware-env", "taskbus", "native-input", "registry"} {
		t.Run(name, func(t *testing.T) {
			c := base
			switch name {
			case "release":
				c.ReleaseViewConfigFile = "/private/release.json"
			case "cluster":
				c.PrivateClusterConfigFile = "/private/cluster.json"
			case "monitoring":
				c.MonitoringConfigFile = "/private/monitoring.json"
			case "middleware":
				c.MiddlewareConfigFile = "/private/middleware.json"
			case "middleware-env":
				c.MiddlewareEnvironmentFile = "/private/middleware.env"
			case "taskbus":
				c.TaskEvents.Endpoint = "tls://private.example.invalid:4222"
			case "native-input":
				c.Database.Dameng = &config.DamengServingConfiguration{RuntimeConfigurationFile: "/private/native.json", SchemaConfigurationFile: "/private/schema.json", ReadinessSeconds: 30}
			case "registry":
				v := registryconfig.Defaults()
				v.Endpoint = "https://registry.example.invalid"
				c.ImageRegistry = &v
			}
			if nativePlatformConfigurationSHA256(c) == original {
				t.Fatal("effective configuration change omitted from original recovery binding")
			}
		})
	}
}

func TestFreshNativeRootCleanupClosesEveryOwnerAfterFailure(t *testing.T) {
	parent := t.TempDir()
	paths := make([]string, 4)
	for i, name := range storeKinds {
		paths[i] = filepath.Join(parent, name)
		if e := os.Mkdir(paths[i], 0700); e != nil {
			t.Fatal(e)
		}
	}
	held, e := lockNativeRoots(paths)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { held.Close() })
	roots := make([]*os.Root, len(held.held))
	for i, h := range held.held {
		roots[i] = h.root
	}
	// Model an already unavailable descriptor. Its failure must not short-circuit
	// the separate confined root owner or the other independently held stores.
	if e = held.held[0].file.Close(); e != nil {
		t.Fatal(e)
	}
	if e = held.Close(); e == nil {
		t.Fatal("lost descriptor incorrectly reported clean")
	}
	for _, root := range roots {
		if _, e = root.Stat("."); e == nil {
			t.Fatal("root owner leaked after a different owner failed to close")
		}
	}
	if len(held.held) != 0 {
		t.Fatal("consumed owners retained as active")
	}
}
