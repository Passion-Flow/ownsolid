package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"go.yaml.in/yaml/v3"
)

func TestUpgradePlanChecksPendingRecoveryBeforeCandidateMedia(t *testing.T) {
	dir := shortTempDir(t)
	root := filepath.Join(dir, "installation")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	var site map[string]any
	if err := yaml.Unmarshal([]byte(validYAML(t, root)), &site); err != nil {
		t.Fatal(err)
	}
	delete(site, "compose_file")
	delete(site, "images")
	raw, err := yaml.Marshal(site)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "site.yaml")
	if err := os.WriteFile(config, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ file, want string }{
		{restoreIntentName, "INSTALL_RESTORE_PENDING"},
		{coordinatedBackupIntentName, "INSTALL_BACKUP_RECOVERY_REQUIRED"},
		{"", "INSTALL_BUNDLE_VERIFICATION_FAILED"},
	} {
		t.Run(test.want, func(t *testing.T) {
			if test.file != "" {
				path := filepath.Join(root, test.file)
				if err := os.Symlink("absent-recovery-record", path); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Remove(path); err != nil {
						t.Error(err)
					}
				})
			}
			runner := &fakeDatabase{}
			var out bytes.Buffer
			err := PlanUpgrade(t.Context(), config, filepath.Join(dir, "missing.tar.gz"), bundle.Defaults(), runner, &out)
			if err == nil || !strings.HasPrefix(err.Error(), test.want) || out.Len() != 0 || len(runner.commands) != 0 {
				t.Fatalf("wanted %s without a plan or Docker call, got %v %q %v", test.want, err, out.String(), runner.commands)
			}
		})
	}
}

func TestUpgradePlanRequiresSignedPredecessorAndStableStorage(t *testing.T) {
	source := bundleBinding{ReleaseID: "release-old", ManifestSHA256: wire.Digest([]byte("old"))}
	candidate := bundle.Result{ReleaseID: "release-new", ManifestSHA256: wire.Digest([]byte("new"))}
	candidate.Manifest = wire.Manifest{SchemaVersion: 54, MinimumPredecessor: 52, MaximumPredecessor: 52}
	for i, role := range bundleImageRoles {
		id := "sha256:" + wire.Digest([]byte(role))
		source.ImageIDs[i] = id
		candidate.Manifest.Assets = append(candidate.Manifest.Assets, wire.Asset{ID: role, ImageID: id})
	}
	plan, err := makeUpgradePlan(source, 52, candidate)
	if err != nil || !plan.BackupRequired || !plan.MigrationRequired || plan.TargetSchema != 54 || len(plan.StopServices) != 1 || plan.StopServices[0] != "control" {
		t.Fatalf("unexpected plan: %+v %v", plan, err)
	}
	if _, err := makeUpgradePlan(source, 51, candidate); err == nil || !strings.Contains(err.Error(), "PREDECESSOR_UNSUPPORTED") {
		t.Fatalf("accepted a schema outside the signed interval: %v", err)
	}
	if _, err := makeUpgradePlan(source, 54, candidate); err == nil || !strings.Contains(err.Error(), "PREDECESSOR_UNSUPPORTED") {
		t.Fatalf("accepted a newer baseline without predecessor support: %v", err)
	}
	candidate.Manifest.Assets[1].ImageID = "sha256:" + wire.Digest([]byte("different PostgreSQL"))
	if _, err := makeUpgradePlan(source, 52, candidate); err == nil || !strings.Contains(err.Error(), "STORAGE_IMAGE_CHANGED") {
		t.Fatalf("accepted a different database image: %v", err)
	}
}

func TestApplicationRollbackRequiresTheExactLiveSchema(t *testing.T) {
	if err := checkRollbackSchema(52, 52); err != nil {
		t.Fatal(err)
	}
	if err := checkRollbackSchema(54, 52); err == nil || !strings.Contains(err.Error(), "DATABASE_RESTORE_REQUIRED") {
		t.Fatalf("accepted an incompatible live schema: %v", err)
	}
}
