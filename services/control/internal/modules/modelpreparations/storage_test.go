package modelpreparations_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func privatePreparationStore(t *testing.T) (*modelpreparations.Store, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "store")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	tenant := uuid.NewString()
	if e := modelpreparations.InitializeStore(root, tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := modelpreparations.OpenStore(root, tenant, 4096, wire.MaximumModelBytes, wire.MaximumModelFiles)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	return store, root, tenant
}

func TestPreparationPrivateStoreCurrentNamespaceLockPersistenceAndNonadoption(t *testing.T) {
	s, root, tenant := privatePreparationStore(t)
	if e := s.VerifyNamespace(); e != nil {
		t.Fatal(e)
	}
	if _, e := modelpreparations.OpenStore(root, tenant, 4096, wire.MaximumModelBytes, wire.MaximumModelFiles); e == nil || e.Error() != "MODEL_PREPARATION_STORE_IN_USE" {
		t.Fatal("concurrent owner", e)
	}
	if _, e := modelpreparations.OpenStore(root, uuid.NewString(), 4096, wire.MaximumModelBytes, wire.MaximumModelFiles); e == nil {
		t.Fatal("foreign tenant opened store")
	}
	id := s.Namespace()
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	opened, e := modelpreparations.OpenStore(root, tenant, 4096, wire.MaximumModelBytes, wire.MaximumModelFiles)
	if e != nil {
		t.Fatal(e)
	}
	defer opened.Close()
	if opened.Namespace() != id || opened.VerifyNamespace() != nil {
		t.Fatal("restart changed namespace")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if e = modelpreparations.InitializeStore(missing, tenant, os.Geteuid(), os.Getegid()); e == nil {
		t.Fatal("missing mount created")
	}
	if _, e = os.Lstat(missing); !os.IsNotExist(e) {
		t.Fatal("missing root substituted")
	}
	populated := t.TempDir()
	sentinel := filepath.Join(populated, "preserve")
	if e = os.WriteFile(sentinel, []byte("PRESERVE_UNKNOWN_DATA"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = modelpreparations.InitializeStore(populated, tenant, os.Geteuid(), os.Getegid()); e == nil {
		t.Fatal("populated directory adopted")
	}
	if raw, e := os.ReadFile(sentinel); e != nil || string(raw) != "PRESERVE_UNKNOWN_DATA" {
		t.Fatal("unrelated data changed", e)
	}
}

func TestPreparationPrivateStoreRejectsReplacedRootLockLinksAndMarker(t *testing.T) {
	for _, kind := range []string{"root", "lock", "marker", "marker_hardlink", "directory_symlink", "unexpected_entry"} {
		t.Run(kind, func(t *testing.T) {
			s, root, _ := privatePreparationStore(t)
			switch kind {
			case "root":
				if e := os.Rename(root, root+"-retained"); e != nil {
					t.Fatal(e)
				}
				if e := os.Mkdir(root, 0700); e != nil {
					t.Fatal(e)
				}
			case "lock":
				if e := os.Rename(filepath.Join(root, "owner.lock"), filepath.Join(root, "retained.lock")); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(filepath.Join(root, "owner.lock"), nil, 0600); e != nil {
					t.Fatal(e)
				}
			case "marker":
				if e := os.WriteFile(filepath.Join(root, "namespace.json"), []byte(`{"schema_version":1}`), 0600); e != nil {
					t.Fatal(e)
				}
			case "marker_hardlink":
				if e := os.Link(filepath.Join(root, "namespace.json"), filepath.Join(t.TempDir(), "linked-marker")); e != nil {
					t.Fatal(e)
				}
			case "directory_symlink":
				if e := os.Rename(filepath.Join(root, "uploads"), filepath.Join(root, "retained-uploads")); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(t.TempDir(), filepath.Join(root, "uploads")); e != nil {
					t.Fatal(e)
				}
			case "unexpected_entry":
				if e := os.WriteFile(filepath.Join(root, "unknown-data"), []byte("PRESERVE"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.VerifyNamespace(); e == nil {
				t.Fatal("mutated storage accepted")
			}
		})
	}
}
