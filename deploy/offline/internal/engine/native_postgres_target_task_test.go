package engine

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func nativeTargetInitializationFixture(kind string) DatabaseTask {
	v := nativeRecoveryFixture(NativePostgreSQLRestore)
	r := wire.PostgreSQLRestoreTargetInitializationRequest{FormatVersion: 1, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, InitializationID: v.Owner.OperationID, TargetScopeID: v.Owner.ScopeID, Binding: wire.PostgreSQLRestoreTargetInitializationBinding{RestoreAttemptID: strings.Repeat("6", 32), ReservationPlanSHA256: nativeObservationHash("a"), TargetDescriptorSHA256: nativeObservationHash("b"), TargetRuntimeSHA256: nativeObservationHash("c"), SourceCandidateSHA256: nativeObservationHash("d"), NetworkID: v.NetworkID, PostgresContainerID: v.Peers["postgres"]}, SourceDatabase: "synthetic_source", TargetDatabase: "synthetic_target", TargetOperator: "synthetic_operator", Endpoint: wire.PostgreSQLRestoreTargetInitializationEndpoint{Host: "postgres", Port: 5432, SSLMode: "disable"}, ExpectedSystemIdentifier: "1234567890", ExpectedDatabaseOID: 12345, InitializationRoot: "/fixture/target-init/" + v.Owner.OperationID, Policy: wire.PostgreSQLRestoreTargetInitializationPolicy{OperationSeconds: 60, CleanupSeconds: 5, MaximumRecordBytes: 4096, MinimumFreeBytes: 1 << 20}}
	owner := func(role string) Ownership {
		return Ownership{v.Owner.ScopeID, v.Owner.OperationID, role + "-" + v.Owner.OperationID}
	}
	v.Kind = kind
	v.Owner.Role = "database-" + kind
	v.VolumeOwner = owner(NativePostgreSQLTargetInputRole)
	v.NativePostgreSQLRecovery = nil
	v.NativePostgreSQLTargetInitialization = &NativePostgreSQLTargetInitializationProfile{Request: r, RecordsOwner: owner(NativePostgreSQLTargetRecordsRole)}
	return v
}

func nativeTargetInitializationOutputFixture(t DatabaseTask) []byte {
	r := t.NativePostgreSQLTargetInitialization.Request
	inspect := t.Kind == wire.PostgreSQLRestoreTargetInspectCommand
	target := wire.PostgreSQLRestoreTargetInitializationIdentity{SystemIdentifier: "1234567890", DatabaseOID: 12345, Database: r.TargetDatabase, Operator: r.TargetOperator, BackendPID: 123, ServerVersion: 180006, Marker: r.Marker()}
	o := wire.PostgreSQLRestoreTargetInitializationObservation{Format: wire.PostgreSQLRestoreTargetObservationFormat, RequestSHA256: r.SHA256(), InitializationID: r.InitializationID, TargetScopeID: r.TargetScopeID, Binding: r.Binding, Target: target, TargetBindingSHA256: target.SHA256(), PristinePredicateSHA256: nativeObservationHash("1"), IntentSHA256: nativeObservationHash("2"), CompletionSHA256: nativeObservationHash("3"), ReadOnly: inspect, IdentityPinned: r.IdentityPinned(), OriginalIntentPresent: true, OriginalCompletionPresent: true, EffectObserved: true, State: "initialized", Qualification: "Synthetic original wire transport only"}
	if !r.IdentityPinned() {
		o.Target.Marker = ""
		o.TargetBindingSHA256 = o.Target.SHA256()
		o.State = "uninitialized"
		o.OriginalIntentPresent = false
		o.OriginalCompletionPresent = false
		o.EffectObserved = false
		o.IntentSHA256 = ""
		o.CompletionSHA256 = ""
	}
	raw, _ := json.Marshal(o)
	return append(raw, '\n')
}

func TestNativePostgreSQLTargetInitializationFixedCommandsMountsAndPins(t *testing.T) {
	for _, kind := range []string{wire.PostgreSQLRestoreTargetInitializeCommand, wire.PostgreSQLRestoreTargetInspectCommand} {
		t.Run(kind, func(t *testing.T) {
			v := nativeTargetInitializationFixture(kind)
			if e := ValidateNativePostgreSQLTargetInitializationTask(v); e != nil {
				t.Fatal(e)
			}
			expected := []string{kind, "--config", DatabaseTaskDestination + "/private/control.yaml", "--request", DatabaseTaskDestination + "/private/" + NativePostgreSQLTargetRequestFile}
			if !reflect.DeepEqual(v.command(), expected) {
				t.Fatal("nonfixed original command")
			}
			mounts := v.databaseMountProfiles()
			if len(mounts) != 2 || !mounts[0].readOnly || !mounts[1].private || mounts[1].target != "/fixture/target-init" || mounts[1].readOnly != (kind == wire.PostgreSQLRestoreTargetInspectCommand) {
				t.Fatal("unexpected runtime/store/input/record authority")
			}
			v.NativePostgreSQLTargetInitialization.Request.ExpectedSystemIdentifier = ""
			v.NativePostgreSQLTargetInitialization.Request.ExpectedDatabaseOID = 0
			if (ValidateNativePostgreSQLTargetInitializationTask(v) == nil) != (kind == wire.PostgreSQLRestoreTargetInspectCommand) {
				t.Fatal("unbound discovery became initializer")
			}
		})
	}
}

func TestNativePostgreSQLTargetInitializationRejectsForeignMixedAndUnsafeContext(t *testing.T) {
	for name, change := range map[string]func(*DatabaseTask){
		"arbitrary command": func(v *DatabaseTask) { v.Kind = "execute-SQL" },
		"network mismatch": func(v *DatabaseTask) {
			v.NativePostgreSQLTargetInitialization.Request.Binding.NetworkID = nativeObservationHash("f")
		},
		"postgres mismatch": func(v *DatabaseTask) {
			v.NativePostgreSQLTargetInitialization.Request.Binding.PostgresContainerID = nativeObservationHash("f")
		},
		"scope mismatch": func(v *DatabaseTask) {
			v.NativePostgreSQLTargetInitialization.Request.TargetScopeID = strings.Repeat("f", 32)
		},
		"initialization mismatch": func(v *DatabaseTask) {
			v.NativePostgreSQLTargetInitialization.Request.InitializationID = strings.Repeat("f", 32)
		},
		"foreign records": func(v *DatabaseTask) {
			v.NativePostgreSQLTargetInitialization.RecordsOwner.ScopeID = strings.Repeat("f", 32)
		},
		"live records": func(v *DatabaseTask) { v.NativePostgreSQLTargetInitialization.RecordsOwner.Role = "models" },
		"input shadow": func(v *DatabaseTask) {
			v.NativePostgreSQLTargetInitialization.Request.InitializationRoot = DatabaseTaskDestination + "/private/initialization"
		},
		"root shadow": func(v *DatabaseTask) {
			v.NativePostgreSQLTargetInitialization.Request.InitializationRoot = "/proc/target"
		},
		"foreign env": func(v *DatabaseTask) {
			v.NativePostgreSQLTargetInitialization.OperatorEnvironmentFile = "/tmp/operator.env"
		},
		"mixed backup": func(v *DatabaseTask) {
			v.NativePostgreSQLBackupVerifier = nativeBackupVerifierTaskFixture().NativePostgreSQLBackupVerifier
		},
		"mixed recovery": func(v *DatabaseTask) {
			v.NativePostgreSQLRecovery = nativeRecoveryFixture(NativePostgreSQLRestore).NativePostgreSQLRecovery
		},
		"legacy output": func(v *DatabaseTask) { v.OutputOwner = &v.VolumeOwner },
		"source target alias": func(v *DatabaseTask) {
			v.NativePostgreSQLTargetInitialization.Request.SourceDatabase = v.NativePostgreSQLTargetInitialization.Request.TargetDatabase
		},
		"partial pins": func(v *DatabaseTask) { v.NativePostgreSQLTargetInitialization.Request.ExpectedDatabaseOID = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			v := nativeTargetInitializationFixture(wire.PostgreSQLRestoreTargetInitializeCommand)
			change(&v)
			if ValidateNativePostgreSQLTargetInitializationTask(v) == nil {
				t.Fatal("unsafe target task admitted")
			}
		})
	}
}

func TestNativePostgreSQLTargetInitializationExactRequestAndFrozenPrivateOutput(t *testing.T) {
	for _, kind := range []string{wire.PostgreSQLRestoreTargetInitializeCommand, wire.PostgreSQLRestoreTargetInspectCommand} {
		t.Run(kind, func(t *testing.T) {
			v := nativeTargetInitializationFixture(kind)
			p := v.NativePostgreSQLTargetInitialization
			raw, _ := json.Marshal(p.Request)
			files := []ConfigurationFile{{Name: "control.yaml", Contents: []byte("private")}, {Name: NativePostgreSQLTargetRequestFile, Contents: raw}}
			if !v.nativeTargetInitializationFiles(files) {
				t.Fatal("original frozen request refused")
			}
			frozen := v.freezeNativeTargetInitialization()
			p.Request.TargetDatabase = "changed"
			v.Peers["postgres"] = nativeObservationHash("f")
			if !frozen.nativeTargetInitializationFiles(files) || frozen.Peers["postgres"] == v.Peers["postgres"] {
				t.Fatal("target observer aliases mutable caller")
			}
			raw = nativeTargetInitializationOutputFixture(frozen)
			out, e := readNativePostgreSQLTargetInitializationOutput(bytes.NewReader(nativeVerifierFrame(1, raw)), frozen)
			if e != nil || out.NativePostgreSQLTargetInitialization == nil || !bytes.Equal(out.NativePostgreSQLTargetInitialization.Original, raw) || out.NativePostgreSQLTargetInitialization.OriginalSHA256 != wire.Digest(raw) {
				t.Fatal("original output not retained", e)
			}
			public, _ := json.Marshal(out)
			if bytes.Contains(public, []byte("target_binding_sha256")) || bytes.Contains(public, []byte("source_candidate_sha256")) {
				t.Fatal("private target observation exposed")
			}
			var image Image
			json.Unmarshal([]byte(`{"Config":{"Env":["PGPASSWORD=private"]}}`), &image)
			if _, ok := frozen.databaseTaskEnvironment(image); ok {
				t.Fatal("ambient native PG configuration admitted")
			}
			files[1].Contents = []byte(`{}`)
			if frozen.nativeTargetInitializationFiles(files) {
				t.Fatal("altered request accepted")
			}
		})
	}
}

func TestNativePostgreSQLTargetInitializationPreservesDiscoveryAndMissingCompletion(t *testing.T) {
	v := nativeTargetInitializationFixture(wire.PostgreSQLRestoreTargetInspectCommand)
	p := v.NativePostgreSQLTargetInitialization
	p.Request.ExpectedSystemIdentifier = ""
	p.Request.ExpectedDatabaseOID = 0
	out, e := decodeNativePostgreSQLTargetInitializationOutput(nativeTargetInitializationOutputFixture(v), v)
	if e != nil || out.NativePostgreSQLTargetInitialization.Observation.IdentityPinned || out.NativePostgreSQLTargetInitialization.Observation.EffectObserved {
		t.Fatal("readonly discovery fabricated a pin/effect", e)
	}
	v = nativeTargetInitializationFixture(wire.PostgreSQLRestoreTargetInspectCommand)
	var observed wire.PostgreSQLRestoreTargetInitializationObservation
	json.Unmarshal(nativeTargetInitializationOutputFixture(v), &observed)
	observed.OriginalCompletionPresent = false
	observed.CompletionSHA256 = ""
	raw, _ := json.Marshal(observed)
	out, e = decodeNativePostgreSQLTargetInitializationOutput(raw, v)
	if e != nil || out.NativePostgreSQLTargetInitialization.Observation.OriginalCompletionPresent {
		t.Fatal("missing original completion fabricated", e)
	}
	v.Kind = wire.PostgreSQLRestoreTargetInitializeCommand
	v.Owner.Role = "database-" + v.Kind
	if _, e := decodeNativePostgreSQLTargetInitializationOutput(raw, v); e == nil {
		t.Fatal("readonly partial evidence became initialization success")
	}
	v = nativeTargetInitializationFixture(wire.PostgreSQLRestoreTargetInitializeCommand)
	raw = nativeTargetInitializationOutputFixture(v)
	if _, e := readNativePostgreSQLTargetInitializationOutput(bytes.NewReader(append(nativeVerifierFrame(1, raw), nativeVerifierFrame(2, []byte("private DSN"))...)), v); e == nil {
		t.Fatal("private stderr accepted")
	}
	if _, e := decodeNativePostgreSQLTargetInitializationOutput(append(raw, raw...), v); e == nil {
		t.Fatal("duplicate output admitted")
	}
}
