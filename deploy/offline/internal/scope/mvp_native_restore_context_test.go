package scope

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func mvpNativeRestoreRuntimeFixture() (RestorePlan, string, wire.PostgreSQLRestoreRequest, restoreConnection, string, wire.RestoredRuntimeContext, *wire.NativePostgreSQLRestoreReference) {
	source, observed := mvpNativeRestoreFixture()
	source.SourceScopeID = strings.Repeat("1", 32)
	scopeID := strings.Repeat("3", 32)
	outer := configuration.RestoreRequest{RestoreSourceRequest: configuration.RestoreSourceDefaults(), Reason: "Synthetic original native restore context binding"}
	outer.BackupRoot, outer.ManifestSHA256 = "/synthetic/portable-backup", source.ManifestSHA256
	source.RequestSHA256 = wire.Digest(encode(outer.RestoreSourceRequest))
	plan := RestorePlan{FormatVersion: 1, AttemptID: observed.RestoreID, Request: outer, Source: source}
	h := strings.Repeat("a", 64)
	policy := wire.PostgreSQLBackupPolicy{DumpFile: "/usr/local/bin/pg_dump", DumpSHA256: h, RestoreFile: "/usr/local/bin/pg_restore", RestoreSHA256: h, ControlFile: "/usr/local/bin/control", ControlSHA256: h, ExpectedVersion: "18.6", ScratchRoot: "/synthetic/scratch", OperationSeconds: 60, TerminationGraceSeconds: 2, MaximumStoreBytes: 1 << 20, MaximumStoreFiles: 20, MaximumSQLBytes: 1 << 20, MaximumConfigurationBytes: 1 << 20, MaximumManifestBytes: 1 << 20, MinimumFreeBytes: 1 << 20}
	request := wire.PostgreSQLRestoreRequest{FormatVersion: 1, RestoreID: plan.AttemptID, TargetScopeID: scopeID, SourceManifestSHA256: source.NativePostgreSQL.CandidateSHA256, BackupRoot: "/synthetic/original-native-backup", RestoreRoot: "/synthetic/native-records/restore", TargetDatabase: "synthetic_target", TargetOperator: "synthetic_operator", RuntimeConfigurationFile: "/synthetic/configuration/runtime.yaml", ConfigurationRoot: "/synthetic/configuration", Reason: outer.Reason, Policy: policy, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile}
	connection := restoreConnection{database: request.TargetDatabase, operator: request.TargetOperator}
	archive := strings.Repeat("e", 64)
	context := wire.RestoredRuntimeContext{FormatVersion: 1, RestoreID: request.RestoreID, SourceScopeID: source.SourceScopeID, TargetScopeID: scopeID, BackupManifestSHA256: request.SourceManifestSHA256, SQLArchiveSHA256: archive, TenantID: source.Source.TenantID, SchemaVersion: 29, TargetDatabase: connection.database, TargetOperator: connection.operator, Reason: outer.Reason, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile}
	observed.NativePostgreSQL.RequestSHA256 = wire.Digest(encode(request))
	return plan, scopeID, request, connection, archive, context, observed.NativePostgreSQL
}

func TestMVPNativeRestoreRuntimeContextRetainsOriginalCandidateAndTarget(t *testing.T) {
	plan, scopeID, request, connection, archive, original, proof := mvpNativeRestoreRuntimeFixture()
	got, e := bindMVPNativeRestoreRuntimeContext(plan, scopeID, request, connection, archive, original, proof)
	if e != nil || !reflect.DeepEqual(got, original) || got.BackupManifestSHA256 == plan.Request.ManifestSHA256 {
		t.Fatal("original native context lost its candidate/target binding", e)
	}
	if _, e = bindMVPNativeRestoreRuntimeContext(plan, scopeID, request, connection, archive, original, nil); e == nil {
		t.Fatal("metadata-only context accepted without original completed observation")
	}
}

func TestMVPNativeRestoreRuntimeContextRejectsEveryChangedOriginalField(t *testing.T) {
	plan, scopeID, request, connection, archive, original, proof := mvpNativeRestoreRuntimeFixture()
	shape := reflect.TypeOf(original)
	for i := 0; i < shape.NumField(); i++ {
		t.Run(shape.Field(i).Name, func(t *testing.T) {
			changed := original
			field := reflect.ValueOf(&changed).Elem().Field(i)
			field.Set(reflect.Zero(field.Type()))
			if _, e := bindMVPNativeRestoreRuntimeContext(plan, scopeID, request, connection, archive, changed, proof); e == nil {
				t.Fatal("missing original context field accepted")
			}
		})
	}
}

func TestMVPNativeRestoreRuntimeContextRejectsReboundRequestsAndLegacyShape(t *testing.T) {
	for _, mutation := range []string{"outer-inventory", "native-request", "signed-source", "restore-id", "target-scope", "database", "operator", "archive", "reason", "legacy-application", "foreign-backup", "source-target-overlap"} {
		t.Run(mutation, func(t *testing.T) {
			plan, scopeID, request, connection, archive, original, proof := mvpNativeRestoreRuntimeFixture()
			switch mutation {
			case "outer-inventory":
				plan.Request.ManifestSHA256 = proof.ManifestSHA256
			case "native-request":
				request.Policy.OperationSeconds++
			case "signed-source":
				plan.Source.ReleaseManifestSHA256 = proof.ManifestSHA256
			case "restore-id":
				request.RestoreID = strings.Repeat("4", 32)
			case "target-scope":
				request.TargetScopeID = strings.Repeat("5", 32)
			case "database":
				connection.database = "synthetic_other"
			case "operator":
				connection.operator = "synthetic_other"
			case "archive":
				archive = strings.Repeat("0", 64)
			case "reason":
				plan.Request.Reason = "Changed original reason"
			case "legacy-application":
				request.ApplicationProfile = ""
			case "foreign-backup":
				proof.Backup.CompletionSHA256 = strings.Repeat("0", 64)
			case "source-target-overlap":
				scopeID = plan.Source.SourceScopeID
			}
			if _, e := bindMVPNativeRestoreRuntimeContext(plan, scopeID, request, connection, archive, original, proof); e == nil {
				t.Fatal("changed request/source/target accepted")
			}
		})
	}
}
