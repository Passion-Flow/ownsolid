package engine

import (
	"strings"
	"testing"
)

func TestRestoredAuthorityFixedConfigurationReasonAndNoDataMounts(t *testing.T) {
	base := storeRestoreTaskFixture(t, ModelFilesVerify)
	base.Kind = DatabaseRestoredAuthInvalidate
	base.Owner.Role = "database-" + base.Kind
	base.OutputOwner = nil
	base.Reason = "Synthetic restored authority invalidation"
	if !base.valid() || len(base.databaseMountProfiles()) != 1 || strings.Join(base.command(), " ") != "invalidate-restored-auth --config /run/projectone/operator/private/control.yaml --reason Synthetic restored authority invalidation" {
		t.Fatal("fixed restored authority task")
	}
	for _, change := range []func(*DatabaseTask){
		func(v *DatabaseTask) { v.Reason = "" }, func(v *DatabaseTask) { v.Reason = "bad\nreason" },
		func(v *DatabaseTask) { v.VolumeOwner.Role = "operator-config" }, func(v *DatabaseTask) { v.VolumeOwner.OperationID = strings.Repeat("e", 32) },
		func(v *DatabaseTask) { v.OutputOwner = &Ownership{v.Owner.ScopeID, v.Owner.OperationID, "datasets"} },
		func(v *DatabaseTask) { v.SourceOwner = &Ownership{v.Owner.ScopeID, v.Owner.OperationID, "models"} },
		func(v *DatabaseTask) { delete(v.Peers, "control") }, func(v *DatabaseTask) { v.StoreRestore = &StoreRestoreProfile{} },
	} {
		task := base
		task.Peers = map[string]string{}
		for k, v := range base.Peers {
			task.Peers[k] = v
		}
		change(&task)
		if task.valid() {
			t.Fatal("unsafe authority boundary admitted")
		}
	}
}
