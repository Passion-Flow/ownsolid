package engine

import (
	"strings"
	"testing"
)

func TestRestoredQualificationFixedGeneratedInput(t *testing.T) {
	for _, kind := range []string{DatabaseSchemaCheck, DatabaseRuntimeRestorePrepare, DatabaseRuntimeCheck} {
		t.Run(kind, func(t *testing.T) {
			base := storeRestoreTaskFixture(t, ModelFilesVerify)
			base.Kind = kind
			base.Owner.Role = "database-" + kind
			base.OutputOwner = nil
			if !base.valid() || base.ValidateRestoredDatabaseTask() != nil || len(base.databaseMountProfiles()) != 1 {
				t.Fatal("fixed generated qualification configuration refused")
			}
			args := strings.Join(base.command(), " ")
			if kind == DatabaseRuntimeRestorePrepare && !strings.Contains(args, "--restore-context /run/projectone/operator/private/restored-runtime.json") {
				t.Fatal("protected restore context absent")
			}
			for _, change := range []func(*DatabaseTask){
				func(v *DatabaseTask) { v.VolumeOwner.OperationID = strings.Repeat("e", 32) },
				func(v *DatabaseTask) { v.VolumeOwner.Role = StoreRestoreConfigRole + "-" + strings.Repeat("e", 32) },
				func(v *DatabaseTask) { delete(v.Peers, "control") },
				func(v *DatabaseTask) { v.Reason = "unrelated override" },
				func(v *DatabaseTask) { v.OutputOwner = &Ownership{v.Owner.ScopeID, v.Owner.OperationID, "models"} },
				func(v *DatabaseTask) { v.SourceOwner = &Ownership{v.Owner.ScopeID, v.Owner.OperationID, "models"} },
			} {
				copy := base
				copy.Peers = map[string]string{}
				for k, v := range base.Peers {
					copy.Peers[k] = v
				}
				change(&copy)
				if copy.valid() {
					t.Fatal("unsafe generated qualification input accepted")
				}
			}
			legacy := base
			legacy.VolumeOwner.Role = "operator-config"
			if !legacy.valid() {
				t.Fatal("existing protected operator input regressed")
			}
			if legacy.ValidateRestoredDatabaseTask() == nil {
				t.Fatal("historical input accepted as generated restore input")
			}
		})
	}
}

func TestGeneratedQualificationCannotRunFreshMutations(t *testing.T) {
	for _, kind := range []string{DatabaseMigrate, DatabaseRuntimePrepare, DatabaseBootstrap, DatabaseBootstrapReconcile, DatabaseBootstrapResume} {
		base := storeRestoreTaskFixture(t, ModelFilesVerify)
		base.Kind = kind
		base.Owner.Role = "database-" + kind
		base.OutputOwner = nil
		if base.valid() {
			t.Fatalf("fresh mutation %s admitted restored configuration", kind)
		}
	}
}
