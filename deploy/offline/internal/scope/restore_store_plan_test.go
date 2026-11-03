package scope

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

func TestRestoreStoreJobsUseNewGenerationFixedKindsAndExactDataOwners(t *testing.T) {
	l, restore := completedRestoreSQLSnapshot(t)
	defer l.close()
	base, _, e := l.makeRestoreDatabaseRecord(context.Background(), restore)
	if e != nil {
		t.Fatal(e)
	}
	generation := strings.Repeat("e", 32)
	for _, kind := range restoredStoreCatalog() {
		t.Run(kind.role, func(t *testing.T) {
			target := engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: strings.Repeat("f", 32), Role: kind.role}
			profile := engine.StoreRestoreProfile{Artifact: engine.StoreRestoreArtifact{Role: kind.role, UID: base.BaseTask.UID, GID: base.BaseTask.GID, Bytes: 3072, SHA256: strings.Repeat("a", 64), MaximumEntries: 100}, FilesSHA256: strings.Repeat("b", 64), DirectoriesSHA256: strings.Repeat("c", 64)}
			job, e := assembleRestoreStoreJob(base.BaseTask, target, profile, generation, kind)
			if e != nil || job.Role != kind.role || job.Import.Kind != kind.importKind || job.Verify.Kind != kind.verifyKind || job.Import.Owner.OperationID != generation || job.Verify.Owner.OperationID != generation || job.Import.VolumeOwner == base.BaseTask.VolumeOwner || job.Import.SourceOwner == nil || job.Import.SourceOwner.Role != engine.StoreRestoreArchiveRole+"-"+kind.role+"-"+generation || *job.Import.OutputOwner != target || job.Verify.SourceOwner != nil || job.Verify.StoreRestore != nil || (job.Verify.Reason != "") != kind.verifyReason {
				t.Fatal("fixed generated store job", e)
			}
			for _, fault := range []string{"old-generation", "bad-generation", "source-role", "numeric-user", "numeric-group", "target-scope", "target-role", "target-generation", "missing-peer", "arbitrary-kind", "missing-reason", "unsafe-archive", "changed-inventory"} {
				t.Run(fault, func(t *testing.T) {
					task := base.BaseTask
					task.Peers = maps.Clone(task.Peers)
					data := target
					input := profile
					selected := kind
					id := generation
					switch fault {
					case "old-generation":
						id = task.Owner.OperationID
					case "bad-generation":
						id = "unsafe"
					case "source-role":
						input.Artifact.Role = "other"
					case "numeric-user":
						input.Artifact.UID++
					case "numeric-group":
						input.Artifact.GID++
					case "target-scope":
						data.ScopeID = strings.Repeat("a", 32)
					case "target-role":
						data.Role = "other"
					case "target-generation":
						data.OperationID = "bad"
					case "missing-peer":
						delete(task.Peers, "control")
					case "arbitrary-kind":
						selected.importKind = engine.DatabaseMigrate
					case "missing-reason":
						task.Reason = ""
					case "unsafe-archive":
						input.Artifact.Bytes = 1
					case "changed-inventory":
						input.FilesSHA256 = "bad"
					}
					if _, e := assembleRestoreStoreJob(task, data, input, id, selected); e == nil {
						t.Fatal("unsafe store job admitted")
					}
				})
			}
		})
	}
}
func TestRestoreStorePlanRequiresCompletedDatabaseAndUnusedGenerationBeforeSourceRead(t *testing.T) {
	l, restore := completedRestoreSQLSnapshot(t)
	defer l.close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e := l.makeRestoreStorePlan(ctx, restore, strings.Repeat("e", 32)); e == nil || e.Error() != "OFFLINE_SCOPE_OPERATION_INTERRUPTED" {
		t.Fatal("cancellation ignored", e)
	}
	for _, generation := range []string{"invalid", restore.AttemptID, l.events[0].OperationID} {
		if _, _, e := l.makeRestoreStorePlan(context.Background(), restore, generation); e == nil {
			t.Fatal("invalid/used generation admitted")
		}
	}
	if _, _, e := l.makeRestoreStorePlan(context.Background(), restore, strings.Repeat("e", 32)); e == nil || e.Error() != "OFFLINE_SCOPE_RESTORE_STORE_DATABASE_QUALIFICATION_REQUIRED" {
		t.Fatal("unqualified database read backup source", e)
	}
}
