package databaseaccess

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestMySQLRestoredIntentHasIndependentVariantAndOriginalNormalBytes(t *testing.T) {
	i := mysqlLiteralInput(t)
	binding := strings.Repeat("b", 64)
	normal := mysqlRuntimeIntent{Format: mysqlRuntimeIntentFormat, ID: uuid.NewString(), TargetSHA256: binding, InputSHA256: i.fingerprint}
	raw, e := mysqlMarshal(normal)
	if e != nil || strings.Contains(string(raw), "restore_context_sha256") || normal.validate(binding, i) != nil {
		t.Fatal("normal v1 bytes/intent changed", e)
	}
	var decoded mysqlRuntimeIntent
	if e = json.Unmarshal(raw, &decoded); e != nil || decoded != normal {
		t.Fatal("original normal roundtrip", e)
	}
	restored := normal
	restored.Format = mysqlRestoredIntentFormat
	restored.RestoreSHA256 = strings.Repeat("c", 64)
	if restored.validate(binding, i) != nil || mysqlRuntimeAuditAction(restored) != "operator.runtime_access_restored" || mysqlRuntimeAuditAction(normal) != "operator.runtime_access_prepared" {
		t.Fatal("restored variant/audit routing")
	}
	if _, e = mysqlCreateStatement(i, restored, strings.Repeat("d", 64)); e != nil {
		t.Fatal("restored safe locked CREATE refused", e)
	}
	for _, bad := range []mysqlRuntimeIntent{
		{Format: mysqlRestoredIntentFormat, ID: normal.ID, TargetSHA256: binding, InputSHA256: i.fingerprint},
		{Format: mysqlRuntimeIntentFormat, ID: normal.ID, TargetSHA256: binding, InputSHA256: i.fingerprint, RestoreSHA256: restored.RestoreSHA256},
		{Format: mysqlRestoredIntentFormat, ID: normal.ID, TargetSHA256: binding, InputSHA256: i.fingerprint, RestoreSHA256: "foreign"},
	} {
		if bad.validate(binding, i) == nil {
			t.Fatal("cross-variant missing/foreign restore proof accepted")
		}
	}
}
func TestMySQLRestoredRoleRequiresActualIndependentRestoreOwner(t *testing.T) {
	for _, command := range []string{"prepare-restored-runtime-access", CheckRestoredMySQLCommand, ReconcileRestoredMySQLCommand, ResumeRestoredMySQLCommand, "foreign"} {
		status, e := RestoredMySQLAccess(t.Context(), nil, nil, config.Config{}, nil, MySQLRuntimeInput{}, "/synthetic/restore", command)
		if e == nil || status != "" {
			t.Fatal("missing actual restore projected role authority", e)
		}
	}
	if MySQLRestoredCommand("prepare-runtime-access") || !MySQLRestoredCommand(CheckRestoredMySQLCommand) {
		t.Fatal("normal/restored routing confused")
	}
}
