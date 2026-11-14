package postgresbackup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func measuredPrivateRestoreRoots(t *testing.T) ([]string, []RootIdentity) {
	t.Helper()
	parent := privateTestRoot(t)
	var paths []string
	var identities []RootIdentity
	for _, name := range []string{"models", "datasets", "training", "preparations"} {
		path := filepath.Join(parent, name)
		if e := os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
		info, e := os.Lstat(path)
		if e != nil {
			t.Fatal(e)
		}
		id, e := rootIdentity(path, info)
		if e != nil {
			t.Fatal(e)
		}
		paths, identities = append(paths, path), append(identities, id)
	}
	return paths, identities
}

func TestPostgresRestoreMeasuredRootsBindBeforeConfigurationOrSQL(t *testing.T) {
	paths, facts := measuredPrivateRestoreRoots(t)
	if e := admitExpectedRestoreRoots(facts, paths); e != nil {
		t.Fatal(e)
	}
	if e := admitExpectedRestoreRoots(nil, []string{"/absent/legacy-path"}); e != nil {
		t.Fatal("changed legacy admission", e)
	}
	for name, change := range map[string]func([]RootIdentity){
		"inode":       func(v []RootIdentity) { v[0].Inode++ },
		"device":      func(v []RootIdentity) { v[0].Device++ },
		"owner":       func(v []RootIdentity) { v[0].UID++ },
		"group":       func(v []RootIdentity) { v[0].GID++ },
		"path":        func(v []RootIdentity) { v[0].Path += "-different" },
		"store-order": func(v []RootIdentity) { v[0], v[1] = v[1], v[0] },
	} {
		t.Run(name, func(t *testing.T) {
			v := append([]RootIdentity(nil), facts...)
			change(v)
			var runtime config.Config
			runtime.Models.ArtifactRoot, runtime.Datasets.ArtifactRoot, runtime.Training.ArtifactRoot, runtime.ModelPreparation.ArtifactRoot = paths[0], paths[1], paths[2], paths[3]
			r := restoreRequestFixture()
			r.TargetRoots = v
			// No source files, configuration, native tools or database exist.
			// A wrong fact must refuse at the first owning input boundary.
			if e := restoreInputBindings(context.Background(), config.Config{}, runtime, "/absent/operator.yaml", Manifest{}, r); e == nil || e.Error() != "POSTGRES_RESTORE_EXPECTED_ROOT_CHANGED" {
				t.Fatal("wrong roots reached configuration or SQL", e)
			}
		})
	}
}

func TestPostgresRestoreMeasuredRootsRefuseReplacementBeforeImport(t *testing.T) {
	paths, facts := measuredPrivateRestoreRoots(t)
	if e := admitExpectedRestoreRoots(facts, paths); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(paths[0], paths[0]+"-retained"); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(paths[0], 0700); e != nil {
		t.Fatal(e)
	}
	if e := admitExpectedRestoreRoots(facts, paths); e == nil {
		t.Fatal("equal-empty replacement admitted")
	}
	held, e := acquireFreshRoots(paths)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	if e = matchExpectedRestoreRoots(facts, held.Identities()); e == nil {
		t.Fatal("replacement between admission and native lock reached import")
	}
	if e = matchExpectedRestoreRoots(held.Identities(), held.Identities()); e != nil {
		t.Fatal("actual fresh locked facts refused", e)
	}
}

func TestPostgresRestoreMeasuredRootsRefuseLinksAndModeDrift(t *testing.T) {
	paths, facts := measuredPrivateRestoreRoots(t)
	if e := os.Rename(paths[0], paths[0]+"-retained"); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(paths[0]+"-retained", paths[0]); e != nil {
		t.Fatal(e)
	}
	if e := admitExpectedRestoreRoots(facts, paths); e == nil {
		t.Fatal("symlink admitted")
	}
	if e := os.Remove(paths[0]); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(paths[0]+"-retained", paths[0]); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(paths[0], 0750); e != nil {
		t.Fatal(e)
	}
	if e := admitExpectedRestoreRoots(facts, paths); e == nil {
		t.Fatal("non-private mode admitted")
	}
}
