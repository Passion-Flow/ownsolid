package install

import (
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

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
