package engine

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func nativeRestoreRootsFixture() DatabaseTask {
	v := nativeRecoveryFixture(NativePostgreSQLRestore)
	old := v.NativePostgreSQLRecovery
	target := nativeTargetInitializationFixture(wire.PostgreSQLRestoreTargetInspectCommand).NativePostgreSQLTargetInitialization.Request
	r := wire.PostgreSQLRestoreRootsInspectionRequest{FormatVersion: 1, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, InspectionID: v.Owner.OperationID, TargetScopeID: v.Owner.ScopeID, Binding: target.Binding, RuntimeConfigurationRoot: old.RestoreRequest.ConfigurationRoot, RuntimeConfigurationSHA256: nativeObservationHash("a"), ExpectedUID: uint32(v.UID), ExpectedGID: uint32(v.GID), Policy: wire.PostgreSQLRestoreRootsInspectionPolicy{OperationSeconds: 60, MaximumRecordBytes: 8192}}
	p := &NativePostgreSQLRestoreRootsInspectionProfile{Request: r, ConfigurationOwner: Ownership{v.Owner.ScopeID, r.Binding.RestoreAttemptID, NativePostgreSQLConfigurationRole + "-" + r.Binding.RestoreAttemptID}, RuntimeConfigurationFile: old.RestoreRequest.RuntimeConfigurationFile, Stores: old.Stores}
	v.Kind = wire.PostgreSQLRestoreRootsInspectCommand
	v.Owner.Role = "database-" + v.Kind
	v.VolumeOwner = Ownership{v.Owner.ScopeID, v.Owner.OperationID, NativePostgreSQLRestoreRootsInputRole + "-" + v.Owner.OperationID}
	v.NativePostgreSQLRecovery = nil
	v.NativePostgreSQLRestoreRootsInspection = p
	return v
}
func nativeRestoreRootsOutputFixture(t DatabaseTask) []byte {
	p := t.NativePostgreSQLRestoreRootsInspection
	r := p.Request
	roots := []wire.PostgreSQLRestoreRoot{}
	for i, m := range p.Stores {
		roots = append(roots, wire.PostgreSQLRestoreRoot{Path: m.Target, Device: 1792, Inode: uint64(100 + i), UID: r.ExpectedUID, GID: r.ExpectedGID})
	}
	o := wire.PostgreSQLRestoreRootsInspectionObservation{Format: wire.PostgreSQLRestoreRootsInspectionFormat, RequestSHA256: r.SHA256(), InspectionID: r.InspectionID, TargetScopeID: r.TargetScopeID, Binding: r.Binding, RuntimeConfigurationSHA256: r.RuntimeConfigurationSHA256, RuntimeEnvironmentSHA256: r.RuntimeEnvironmentSHA256, EffectiveConfigurationSHA256: nativeObservationHash("e"), Roots: roots, RootsSHA256: wire.PostgreSQLRestoreRootsSHA256(roots), ReadOnly: true, ObservedAt: "2026-11-03T00:00:00Z", Qualification: wire.PostgreSQLRestoreRootsInspectionQualification}
	raw, _ := json.Marshal(o)
	return append(raw, '\n')
}
func TestNativePostgreSQLRestoreRootsFixedCommandAndReadonlyAuthority(t *testing.T) {
	v := nativeRestoreRootsFixture()
	p := v.NativePostgreSQLRestoreRootsInspection
	if e := ValidateNativePostgreSQLRestoreRootsInspectionTask(v); e != nil {
		t.Fatal(e)
	}
	expected := []string{wire.PostgreSQLRestoreRootsInspectCommand, "--config", "/fixture/configuration/runtime.yaml", "--request", DatabaseTaskDestination + "/private/native-roots-request.json"}
	if !reflect.DeepEqual(v.command(), expected) || v.databaseTaskNetworkMode() != "none" || v.NetworkID != p.Request.Binding.NetworkID {
		t.Fatal("nonfixed command or actor networking authority")
	}
	mounts := v.databaseMountProfiles()
	if len(mounts) != 6 || mounts[1].owner.OperationID != p.Request.Binding.RestoreAttemptID || mounts[1].target != p.Request.RuntimeConfigurationRoot {
		t.Fatal("configuration projection was copied or rebound to inspection")
	}
	for i, m := range mounts {
		if !m.readOnly || i > 0 && !m.private {
			t.Fatal("unexpected writable or public store mount")
		}
	}
	p.RuntimeEnvironmentFile = p.Request.RuntimeConfigurationRoot + "/runtime.env"
	p.Request.RuntimeEnvironmentSHA256 = nativeObservationHash("b")
	if !v.valid() || !reflect.DeepEqual(v.command(), append(expected, "--env-file", p.RuntimeEnvironmentFile)) {
		t.Fatal("explicit original runtime environment refused")
	}
	var image Image
	json.Unmarshal([]byte(`{"Config":{"Env":["IMAGE_SETTING=value"]}}`), &image)
	env, ok := v.databaseTaskEnvironment(image)
	if !ok || len(env) != 1 || env["TMPDIR"] != "" {
		t.Fatal("unexpected ambient policy or scratch setting")
	}
	json.Unmarshal([]byte(`{"Config":{"Env":["PGHOST=not-authorized"]}}`), &image)
	if _, ok = v.databaseTaskEnvironment(image); ok {
		t.Fatal("ambient PG configuration admitted")
	}
}
func TestNativePostgreSQLRestoreRootsRejectsForeignAndMixedContext(t *testing.T) {
	for name, edit := range map[string]func(*DatabaseTask){
		"other command": func(v *DatabaseTask) { v.Kind = NativePostgreSQLRestore },
		"inspection alias": func(v *DatabaseTask) {
			v.NativePostgreSQLRestoreRootsInspection.Request.InspectionID = v.NativePostgreSQLRestoreRootsInspection.Request.Binding.RestoreAttemptID
		},
		"foreign scope": func(v *DatabaseTask) {
			v.NativePostgreSQLRestoreRootsInspection.Request.TargetScopeID = strings.Repeat("f", 32)
		},
		"wrong UID":        func(v *DatabaseTask) { v.NativePostgreSQLRestoreRootsInspection.Request.ExpectedUID++ },
		"wrong GID":        func(v *DatabaseTask) { v.NativePostgreSQLRestoreRootsInspection.Request.ExpectedGID++ },
		"foreign network":  func(v *DatabaseTask) { v.NetworkID = nativeObservationHash("f") },
		"foreign postgres": func(v *DatabaseTask) { v.Peers["postgres"] = nativeObservationHash("f") },
		"projection on inspection": func(v *DatabaseTask) {
			v.NativePostgreSQLRestoreRootsInspection.ConfigurationOwner.OperationID = v.Owner.OperationID
		},
		"foreign projection": func(v *DatabaseTask) {
			v.NativePostgreSQLRestoreRootsInspection.ConfigurationOwner.ScopeID = strings.Repeat("f", 32)
		},
		"foreign input role": func(v *DatabaseTask) { v.VolumeOwner.Role = "models" },
		"outside runtime": func(v *DatabaseTask) {
			v.NativePostgreSQLRestoreRootsInspection.RuntimeConfigurationFile = "/other/runtime.yaml"
		},
		"env missing SHA": func(v *DatabaseTask) {
			v.NativePostgreSQLRestoreRootsInspection.RuntimeEnvironmentFile = "/fixture/configuration/runtime.env"
		},
		"env SHA missing file": func(v *DatabaseTask) {
			v.NativePostgreSQLRestoreRootsInspection.Request.RuntimeEnvironmentSHA256 = nativeObservationHash("e")
		},
		"projection shadow": func(v *DatabaseTask) {
			v.NativePostgreSQLRestoreRootsInspection.Request.RuntimeConfigurationRoot = DatabaseTaskDestination
		},
		"store order": func(v *DatabaseTask) {
			p := v.NativePostgreSQLRestoreRootsInspection
			p.Stores[0], p.Stores[1] = p.Stores[1], p.Stores[0]
		},
		"foreign store": func(v *DatabaseTask) {
			v.NativePostgreSQLRestoreRootsInspection.Stores[0].Owner.ScopeID = strings.Repeat("f", 32)
		},
		"store projection alias": func(v *DatabaseTask) {
			v.NativePostgreSQLRestoreRootsInspection.Stores[0].Target = "/fixture/configuration/models"
		},
		"store alias": func(v *DatabaseTask) {
			p := v.NativePostgreSQLRestoreRootsInspection
			p.Stores[1].Target = p.Stores[0].Target
		},
		"backup verifier": func(v *DatabaseTask) {
			v.NativePostgreSQLBackupVerifier = nativeBackupVerifierTaskFixture().NativePostgreSQLBackupVerifier
		},
		"full recovery": func(v *DatabaseTask) {
			v.NativePostgreSQLRecovery = nativeRecoveryFixture(NativePostgreSQLRestore).NativePostgreSQLRecovery
		},
		"SQL initializer": func(v *DatabaseTask) {
			v.NativePostgreSQLTargetInitialization = nativeTargetInitializationFixture(wire.PostgreSQLRestoreTargetInspectCommand).NativePostgreSQLTargetInitialization
		},
		"legacy source": func(v *DatabaseTask) { v.SourceOwner = &v.VolumeOwner },
	} {
		t.Run(name, func(t *testing.T) {
			v := nativeRestoreRootsFixture()
			edit(&v)
			if v.valid() {
				t.Fatal("unsafe roots inspection admitted")
			}
		})
	}
}
func TestNativePostgreSQLRestoreRootsProfileRequiresExactlyFourRawStores(t *testing.T) {
	original := nativeRestoreRootsFixture().NativePostgreSQLRestoreRootsInspection
	raw, _ := json.Marshal(original)
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	var stores []json.RawMessage
	json.Unmarshal(fields["stores"], &stores)
	for _, count := range []int{0, 3, 5} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			entries := append([]json.RawMessage{}, stores...)
			if count < 4 {
				entries = entries[:count]
			} else {
				entries = append(entries, stores[0])
			}
			fields["stores"], _ = json.Marshal(entries)
			bad, _ := json.Marshal(fields)
			var decoded NativePostgreSQLRestoreRootsInspectionProfile
			if json.Unmarshal(bad, &decoded) == nil {
				t.Fatal("raw store count truncated or padded")
			}
		})
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"stores":`), []byte(`"Stores":`), 1), bytes.Replace(raw, []byte(`"stores":[`), []byte(`"stores":null,"ignored":[`), 1), append([]byte(`{"unknown":true,`), raw[1:]...), bytes.Replace(raw, []byte(`"runtime_configuration_file":`), []byte(`"runtime_configuration_file":"duplicate","runtime_configuration_file":`), 1)} {
		decoded := *original
		if json.Unmarshal(bad, &decoded) == nil || !reflect.DeepEqual(decoded, *original) {
			t.Fatal("malformed profile accepted or partially assigned")
		}
	}
	var decoded NativePostgreSQLRestoreRootsInspectionProfile
	if json.Unmarshal(raw, &decoded) != nil || !reflect.DeepEqual(decoded, *original) {
		t.Fatal("original profile roundtrip changed")
	}
}
func TestNativePostgreSQLRestoreRootsFrozenInputAndPrivateOutput(t *testing.T) {
	v := nativeRestoreRootsFixture()
	request, _ := json.Marshal(v.NativePostgreSQLRestoreRootsInspection.Request)
	files := []ConfigurationFile{{Name: NativePostgreSQLRestoreRootsRequestFile, Contents: request}}
	if !v.nativeRestoreRootsFiles(files) || v.nativeRestoreRootsFiles(append(files, ConfigurationFile{Name: "control.yaml", Contents: []byte("not an operator input")})) {
		t.Fatal("input authority widened")
	}
	frozen := v.freezeNativeRestoreRoots()
	v.NativePostgreSQLRestoreRootsInspection.Stores[0].Target = "/changed"
	v.Peers["postgres"] = nativeObservationHash("f")
	if !frozen.valid() || frozen.NativePostgreSQLRestoreRootsInspection.Stores[0].Target == v.NativePostgreSQLRestoreRootsInspection.Stores[0].Target || frozen.Peers["postgres"] == v.Peers["postgres"] {
		t.Fatal("frozen profile aliases caller")
	}
	raw := nativeRestoreRootsOutputFixture(frozen)
	got, e := readNativePostgreSQLRestoreRootsInspectionOutput(bytes.NewReader(nativeVerifierFrame(1, raw)), frozen)
	if e != nil || got.NativePostgreSQLRestoreRootsInspection == nil || got.Bytes != len(raw) {
		t.Fatal("original observation refused", e)
	}
	private := got.NativePostgreSQLRestoreRootsInspection
	public, _ := json.Marshal(got)
	if bytes.Contains(public, []byte("roots_sha256")) || !bytes.Equal(private.Original, raw) || private.OriginalSHA256 != wire.Digest(raw) {
		t.Fatal("private custody lost or exposed")
	}
	raw[0] = '!'
	if private.Original[0] == '!' {
		t.Fatal("caller aliases retained observation")
	}
}
func TestNativePostgreSQLRestoreRootsRejectsMissingPinsAndContradictions(t *testing.T) {
	v := nativeRestoreRootsFixture()
	raw := nativeRestoreRootsOutputFixture(v)
	for name, edit := range map[string]func(*wire.PostgreSQLRestoreRootsInspectionObservation){
		"missing roots": func(o *wire.PostgreSQLRestoreRootsInspectionObservation) { o.Roots = nil; o.RootsSHA256 = "" },
		"three roots":   func(o *wire.PostgreSQLRestoreRootsInspectionObservation) { o.Roots = o.Roots[:3] },
		"fifth root":    func(o *wire.PostgreSQLRestoreRootsInspectionObservation) { o.Roots = append(o.Roots, o.Roots[0]) },
		"wrong path": func(o *wire.PostgreSQLRestoreRootsInspectionObservation) {
			o.Roots[0].Path = "/other/models"
			o.RootsSHA256 = wire.PostgreSQLRestoreRootsSHA256(o.Roots)
		},
		"wrong reservation": func(o *wire.PostgreSQLRestoreRootsInspectionObservation) {
			o.Binding.ReservationPlanSHA256 = nativeObservationHash("f")
		},
		"wrong raw config": func(o *wire.PostgreSQLRestoreRootsInspectionObservation) {
			o.RuntimeConfigurationSHA256 = nativeObservationHash("f")
		},
		"still locked": func(o *wire.PostgreSQLRestoreRootsInspectionObservation) { o.CurrentLockHeld = true },
		"writable":     func(o *wire.PostgreSQLRestoreRootsInspectionObservation) { o.ReadOnly = false },
	} {
		t.Run(name, func(t *testing.T) {
			var o wire.PostgreSQLRestoreRootsInspectionObservation
			if json.Unmarshal(raw, &o) != nil {
				t.Fatal("fixture invalid")
			}
			edit(&o)
			bad, _ := json.Marshal(o)
			if _, e := decodeNativePostgreSQLRestoreRootsInspectionOutput(bad, v); e == nil {
				t.Fatal("unbound or contradictory observation admitted")
			}
		})
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"current_lock_held":false,`), nil, 1), bytes.Replace(raw, []byte(`"read_only":`), []byte(`"Read_Only":`), 1), append(raw, raw...), append([]byte(`{"unknown":true,`), raw[1:]...)} {
		if _, e := decodeNativePostgreSQLRestoreRootsInspectionOutput(bad, v); e == nil {
			t.Fatal("nonclosed original observation admitted")
		}
	}
	if _, e := readNativePostgreSQLRestoreRootsInspectionOutput(bytes.NewReader(append(nativeVerifierFrame(1, raw), nativeVerifierFrame(2, []byte("private runtime"))...)), v); e == nil {
		t.Fatal("stderr accepted")
	}
}
