package postgresbackup

import (
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
	"testing"
)

func TestPostgresRestoreRootsInspectionExplicitPolicy(t *testing.T) {
	p := release.PostgreSQLRestoreRootsInspectionPolicy{OperationSeconds: 15, MaximumRecordBytes: 65536}
	if e := applyRestoreRootsInspectionPolicy(&p, []byte(restoreRootsInspectionPolicyPrefix+"OPERATION_SECONDS=29\n"+restoreRootsInspectionPolicyPrefix+"MAXIMUM_RECORD_BYTES=32768\n")); e != nil || p.OperationSeconds != 29 || p.MaximumRecordBytes != 32768 {
		t.Fatal("explicit nondefault policy", e)
	}
	for _, line := range []string{"OPERATION_SECONDS=false", "OPERATION_SECONDS=", "OPERATION_SECONDS=01", "OPERATION_SECONDS=-1", "OPERATION_SECONDS=301", "MAXIMUM_RECORD_BYTES=4095", "MAXIMUM_RECORD_BYTES=1048577", "UNKNOWN=1", "OPERATION_SECONDS=15\n" + restoreRootsInspectionPolicyPrefix + "OPERATION_SECONDS=16"} {
		v := p
		if applyRestoreRootsInspectionPolicy(&v, []byte(restoreRootsInspectionPolicyPrefix+line+"\n")) == nil {
			t.Fatal("invalid/false/duplicate policy accepted", line)
		}
	}
}
