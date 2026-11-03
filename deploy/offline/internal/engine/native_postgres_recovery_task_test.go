package engine

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func nativeRecoveryFixture(kind string) DatabaseTask {
	old := nativeBackupVerifierTaskFixture()
	own := func(role string) Ownership {
		return Ownership{old.Owner.ScopeID, old.Owner.OperationID, role + "-" + old.Owner.OperationID}
	}
	r := old.NativePostgreSQLBackupVerifier.Request
	r.SourceScopeID = old.Owner.ScopeID
	r.ConfigurationRoot = "/fixture/configuration"
	r.RuntimeConfigurationFile = r.ConfigurationRoot + "/runtime.yaml"
	p := &NativePostgreSQLRecoveryProfile{ConfigurationOwner: own(NativePostgreSQLConfigurationRole), BackupOwner: own(NativePostgreSQLArtifactRole), ScratchOwner: own(NativePostgreSQLRecoveryScratchRole)}
	t := DatabaseTask{Owner: Ownership{old.Owner.ScopeID, old.Owner.OperationID, "database-" + kind}, VolumeOwner: own(NativePostgreSQLRecoveryInputRole), Kind: kind, Image: old.Image, UID: old.UID, GID: old.GID, NetworkID: strings.Repeat("7", 64), Peers: map[string]string{"postgres": strings.Repeat("8", 64), "control": strings.Repeat("9", 64)}, NativePostgreSQLRecovery: p}
	for i, role := range []string{"models", "datasets", "training", "preparations"} {
		p.Stores[i] = NativePostgreSQLStoreMount{Ownership{old.Owner.ScopeID, strings.Repeat("0", 32), role}, "/fixture/stores/" + role}
	}
	if kind == NativePostgreSQLBackup {
		p.BackupRequest = &r
	} else {
		b := *nativeBackupObservationFixture().NativePostgreSQL
		p.BackupReference = &b
		p.RestoreRequest = &wire.PostgreSQLRestoreRequest{FormatVersion: 1, RestoreID: strings.Repeat("6", 32), TargetScopeID: t.Owner.ScopeID, SourceManifestSHA256: b.CandidateSHA256, BackupRoot: r.BackupRoot, RestoreRoot: "/fixture/restores/" + strings.Repeat("6", 32), TargetDatabase: "synthetic_target", TargetOperator: "synthetic_operator", RuntimeConfigurationFile: r.RuntimeConfigurationFile, ConfigurationRoot: r.ConfigurationRoot, Reason: r.Reason, Policy: r.Policy, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile}
		o := own(NativePostgreSQLRestoreRecordsRole)
		p.RecordsOwner = &o
	}
	return t
}

func nativeRecoveryOutputFixture(t DatabaseTask) []byte {
	p := t.NativePostgreSQLRecovery
	var v any
	switch t.Kind {
	case NativePostgreSQLBackup:
		v = NativePostgreSQLBackupCompletion{Format: "projectone.postgres.backup-complete.v1", BackupID: p.BackupRequest.BackupID, ManifestSHA256: nativeObservationHash("e"), ReadbackSHA256: nativeObservationHash("f"), CompletedAt: "2026-11-03T00:00:00Z", Qualification: "Synthetic transport fixture"}
	case NativePostgreSQLRestore:
		r := p.RestoreRequest
		v = NativePostgreSQLRestoreCompletion{Format: "projectone.postgres.restore-complete.v1", RestoreID: r.RestoreID, IntentSHA256: nativeObservationHash("1"), FilesSHA256: nativeObservationHash("2"), ManifestSHA256: r.SourceManifestSHA256, ArchiveSHA256: nativeObservationHash("3"), TargetBindingSHA256: nativeObservationHash("4"), PostDataSHA256: nativeObservationHash("5"), CompletedAt: "2026-11-03T00:00:00Z", Qualification: "Synthetic transport fixture", RuntimeContext: wire.RestoredRuntimeContext{FormatVersion: 1, RestoreID: r.RestoreID, SourceScopeID: strings.Repeat("a", 32), TargetScopeID: r.TargetScopeID, BackupManifestSHA256: r.SourceManifestSHA256, SQLArchiveSHA256: nativeObservationHash("3"), TenantID: "11111111-1111-1111-1111-111111111111", SchemaVersion: wire.MVPPostgreSQLCPUV1SchemaVersion, TargetDatabase: r.TargetDatabase, TargetOperator: r.TargetOperator, Reason: r.Reason, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile}}
	default:
		r := p.RestoreRequest
		o := nativeRestoreObservationFixture()
		o.RestoreID = r.RestoreID
		o.ManifestSHA256 = r.SourceManifestSHA256
		ref := *o.NativePostgreSQL
		ref.Backup = *p.BackupReference
		ref.ManifestSHA256 = r.SourceManifestSHA256
		raw, _ := json.Marshal(r)
		ref.RequestSHA256 = wire.Digest(append(raw, '\n'))
		o.NativePostgreSQL = &ref
		v = o
	}
	raw, _ := json.Marshal(v)
	return append(raw, '\n')
}

func nativeCompletionTar(body []byte, uid, gid int) []byte {
	var raw bytes.Buffer
	w := tar.NewWriter(&raw)
	w.WriteHeader(&tar.Header{Name: "complete.json", Typeflag: tar.TypeReg, Mode: 0600, Uid: uid, Gid: gid, Size: int64(len(body))})
	w.Write(body)
	w.Close()
	return raw.Bytes()
}

func TestNativePostgreSQLRecoveryFixedProfilesAndMountModes(t *testing.T) {
	for _, kind := range []string{NativePostgreSQLBackup, NativePostgreSQLRestore, NativePostgreSQLRestoreVerify} {
		t.Run(kind, func(t *testing.T) {
			v := nativeRecoveryFixture(kind)
			if e := ValidateNativePostgreSQLRecoveryTask(v); e != nil {
				t.Fatal(e)
			}
			want := []string{kind, "--config", DatabaseTaskDestination + "/private/control.yaml", "--request", DatabaseTaskDestination + "/private/" + NativePostgreSQLRecoveryRequestFile}
			if !reflect.DeepEqual(v.command(), want) || v.databaseTaskNetworkMode() != v.NetworkID {
				t.Fatal("nonfixed command/network")
			}
			for _, m := range v.nativeRecoveryExtraMounts() {
				if !m.private {
					t.Fatal("volume root substituted")
				}
				if m.owner.Role == "models" && m.readOnly != (kind == NativePostgreSQLRestoreVerify) {
					t.Fatal("incorrect store access")
				}
				if v.NativePostgreSQLRecovery.RecordsOwner != nil && m.owner == *v.NativePostgreSQLRecovery.RecordsOwner && (m.target != "/fixture/restores" || m.readOnly != (kind == NativePostgreSQLRestoreVerify)) {
					t.Fatal("record root custody changed")
				}
			}
		})
	}
}

func TestNativePostgreSQLRecoveryRefusesMixedForeignAndOverlappingInputs(t *testing.T) {
	for name, change := range map[string]func(*DatabaseTask){
		"arbitrary command": func(v *DatabaseTask) { v.Kind = "shell" },
		"legacy application": func(v *DatabaseTask) {
			v.NativePostgreSQLRecovery.RestoreRequest.ApplicationProfile = wire.PostgreSQLLegacyApplicationProfile
		},
		"missing request": func(v *DatabaseTask) { v.NativePostgreSQLRecovery.RestoreRequest = nil },
		"both requests": func(v *DatabaseTask) {
			v.NativePostgreSQLRecovery.BackupRequest = nativeRecoveryFixture(NativePostgreSQLBackup).NativePostgreSQLRecovery.BackupRequest
		},
		"verifier union": func(v *DatabaseTask) {
			v.NativePostgreSQLBackupVerifier = nativeBackupVerifierTaskFixture().NativePostgreSQLBackupVerifier
		},
		"foreign target": func(v *DatabaseTask) {
			v.NativePostgreSQLRecovery.RestoreRequest.TargetScopeID = strings.Repeat("d", 32)
		},
		"source mismatch": func(v *DatabaseTask) {
			v.NativePostgreSQLRecovery.RestoreRequest.SourceManifestSHA256 = nativeObservationHash("0")
		},
		"foreign volume": func(v *DatabaseTask) { v.NativePostgreSQLRecovery.Stores[0].Owner.ScopeID = strings.Repeat("f", 32) },
		"swapped stores": func(v *DatabaseTask) {
			p := v.NativePostgreSQLRecovery
			p.Stores[0], p.Stores[1] = p.Stores[1], p.Stores[0]
		},
		"output alias": func(v *DatabaseTask) {
			v.NativePostgreSQLRecovery.ScratchOwner = v.NativePostgreSQLRecovery.BackupOwner
		},
		"root alias": func(v *DatabaseTask) {
			v.NativePostgreSQLRecovery.Stores[0].Target = v.NativePostgreSQLRecovery.Stores[1].Target
		},
		"ancestor overlay":    func(v *DatabaseTask) { v.NativePostgreSQLRecovery.Stores[0].Target = "/fixture/stores" },
		"input shadow":        func(v *DatabaseTask) { v.NativePostgreSQLRecovery.RestoreRequest.ConfigurationRoot = "/run/projectone" },
		"tool shadow":         func(v *DatabaseTask) { v.NativePostgreSQLRecovery.Stores[0].Target = "/usr/local/bin" },
		"external shadow":     func(v *DatabaseTask) { v.NativePostgreSQLRecovery.Stores[0].Target = "/Volumes/DevData" },
		"foreign environment": func(v *DatabaseTask) { v.NativePostgreSQLRecovery.OperatorEnvironmentFile = "/tmp/operator.env" },
		"network none":        func(v *DatabaseTask) { v.NetworkID = "none" },
		"legacy output":       func(v *DatabaseTask) { v.OutputOwner = &v.VolumeOwner },
		"runtime outside projection": func(v *DatabaseTask) {
			v.NativePostgreSQLRecovery.RestoreRequest.RuntimeConfigurationFile = "/fixture/elsewhere/runtime.yaml"
		},
	} {
		t.Run(name, func(t *testing.T) {
			v := nativeRecoveryFixture(NativePostgreSQLRestore)
			change(&v)
			if ValidateNativePostgreSQLRecoveryTask(v) == nil {
				t.Fatal("unsafe native context admitted")
			}
		})
	}
}

func TestNativePostgreSQLRecoveryOriginalOutputsAndPrivateRetention(t *testing.T) {
	for _, kind := range []string{NativePostgreSQLBackup, NativePostgreSQLRestore, NativePostgreSQLRestoreVerify} {
		t.Run(kind, func(t *testing.T) {
			v := nativeRecoveryFixture(kind)
			raw := nativeRecoveryOutputFixture(v)
			got, e := readNativePostgreSQLRecoveryOutput(bytes.NewReader(nativeVerifierFrame(1, raw)), v)
			if e != nil || got.Redacted || got.StdoutCode != "" || got.Bytes != len(raw) {
				t.Fatal("original structured output refused", e)
			}
			public, _ := json.Marshal(got)
			if bytes.Contains(public, []byte("runtime_context")) || bytes.Contains(public, []byte("manifest_sha256")) || bytes.Contains(public, raw) {
				t.Fatal("private output exposed")
			}
			for _, bad := range [][]byte{append(raw, raw...), append([]byte(`{"unknown":true,`), raw[1:]...), bytes.Replace(raw, []byte(`"format":`), []byte(`"format":"duplicate","format":`), 1)} {
				if _, e := decodeNativePostgreSQLRecoveryOutput(bad, v); e == nil {
					t.Fatal("malformed output admitted")
				}
			}
			if _, e := readNativePostgreSQLRecoveryOutput(bytes.NewReader(append(nativeVerifierFrame(1, raw), nativeVerifierFrame(2, []byte("private DSN"))...)), v); e == nil {
				t.Fatal("stderr accepted")
			}
			if got.NativePostgreSQLRestoreCompletion != nil {
				c := got.NativePostgreSQLRestoreCompletion
				if !bytes.Equal(c.Original, raw) || c.OriginalSHA256 != wire.Digest(raw) || !c.RuntimeContext.Valid() {
					t.Fatal("original completion lost")
				}
				raw[0] = '!'
				if c.Original[0] == '!' {
					t.Fatal("caller aliased original")
				}
			}
		})
	}
}

func TestNativePostgreSQLRecoveryCompletionArchiveStrictSingleFile(t *testing.T) {
	body := nativeRecoveryOutputFixture(nativeRecoveryFixture(NativePostgreSQLRestore))
	raw := nativeCompletionTar(body, 1000, 1000)
	got, e := nativePostgreSQLCompletionArchive(raw, 1000, 1000, int64(len(body)))
	if e != nil || !bytes.Equal(got, body) {
		t.Fatal("original completion archive refused", e)
	}
	for name, bad := range map[string][]byte{"truncated": raw[:len(raw)-1], "nonzero trailing": append(append([]byte(nil), raw...), bytes.Repeat([]byte{1}, 512)...), "duplicate archive": append(append([]byte(nil), raw...), raw...)} {
		t.Run(name, func(t *testing.T) {
			if _, e := nativePostgreSQLCompletionArchive(bad, 1000, 1000, 1<<20); e == nil {
				t.Fatal("corrupt archive admitted")
			}
		})
	}
	if _, e := nativePostgreSQLCompletionArchive(raw, 1001, 1000, 1<<20); e == nil {
		t.Fatal("foreign owner accepted")
	}
	if _, e := nativePostgreSQLCompletionArchive(raw, 1000, 1000, int64(len(body)-1)); e == nil {
		t.Fatal("oversized output accepted")
	}
}

func TestNativePostgreSQLRecoveryFreezeAndExactRequest(t *testing.T) {
	v := nativeRecoveryFixture(NativePostgreSQLRestore)
	raw, _ := json.Marshal(v.NativePostgreSQLRecovery.RestoreRequest)
	files := []ConfigurationFile{{Name: "control.yaml", Contents: []byte("private")}, {Name: NativePostgreSQLRecoveryRequestFile, Contents: raw}}
	if !v.nativeRecoveryFiles(files) {
		t.Fatal("original request refused")
	}
	frozen := v.freezeNativeRecovery()
	v.NativePostgreSQLRecovery.RestoreRequest.Reason = "changed"
	v.NativePostgreSQLRecovery.Stores[0].Target = "/different"
	v.Peers["postgres"] = strings.Repeat("f", 64)
	if !frozen.nativeRecoveryFiles(files) || frozen.NativePostgreSQLRecovery.RestoreRequest.Reason == v.NativePostgreSQLRecovery.RestoreRequest.Reason || frozen.NativePostgreSQLRecovery.Stores[0].Target == v.NativePostgreSQLRecovery.Stores[0].Target || frozen.Peers["postgres"] == v.Peers["postgres"] {
		t.Fatal("frozen task aliases caller")
	}
	files[1].Contents = []byte(`{}`)
	if frozen.nativeRecoveryFiles(files) {
		t.Fatal("changed request accepted")
	}
}

func TestNativePostgreSQLBackupVerifierParentLayoutPreservesOriginalTree(t *testing.T) {
	v := nativeBackupVerifierTaskFixture()
	v.NativePostgreSQLBackupVerifier.BackupParent = true
	if !v.valid() {
		t.Fatal("writer parent layout refused")
	}
	mounts := v.nativeVerifierExtraMounts()
	if mounts[1].target != "/fixture/backups" || !mounts[1].readOnly || !mounts[1].private {
		t.Fatal("parent tree not preserved readonly")
	}
	v.NativePostgreSQLBackupVerifier.Request.Policy.ScratchRoot = "/fixture/backups/scratch"
	if v.valid() {
		t.Fatal("scratch hidden in original backup parent")
	}
}

func TestNativePostgreSQLRecoveryRegistryScaffoldAndOriginalReference(t *testing.T) {
	v := nativeRecoveryFixture(NativePostgreSQLRestoreVerify)
	p := v.NativePostgreSQLRecovery
	owner := func(role string) Ownership {
		return Ownership{v.Owner.ScopeID, v.Owner.OperationID, role + "-" + v.Owner.OperationID}
	}
	p.Scaffold = &NativePostgreSQLVerifierPlaceholder{Owner: owner(NativePostgreSQLScaffoldRole), Target: "/fixture"}
	p.Registry = &NativePostgreSQLRegistryMount{Owner: Ownership{v.Owner.ScopeID, v.Owner.OperationID, NativePostgreSQLRegistryRole}, Target: "/fixture/registry"}
	p.PreparedRelease = &NativePostgreSQLPreparedMount{Owner: owner(NativePostgreSQLPreparedRole), Target: "/fixture/prepared"}
	p.BackupReference.RegistryConfigured = true
	p.BackupReference.Registry = &wire.NativePostgreSQLRegistryReference{ReceiptSHA256: nativeObservationHash("1"), ManifestSHA256: nativeObservationHash("2"), PayloadTreeSHA256: nativeObservationHash("3"), Pins: 1, Files: 1, Bytes: 1}
	if e := ValidateNativePostgreSQLRecoveryTask(v); e != nil {
		t.Fatal("exact same-scope registry/scaffold refused", e)
	}
	for _, m := range v.nativeRecoveryExtraMounts() {
		if m.owner.Role == NativePostgreSQLRegistryRole && !m.readOnly {
			t.Fatal("readonly observation acquired writable CAS")
		}
	}
	p.Scaffold.Target = "/fixture/registry"
	if ValidateNativePostgreSQLRecoveryTask(v) == nil {
		t.Fatal("scaffold replaced CAS")
	}
	p.Scaffold.Target = "/fixture"
	p.BackupReference.RegistryConfigured = false
	if ValidateNativePostgreSQLRecoveryTask(v) == nil {
		t.Fatal("source registry mismatch accepted")
	}
}

func TestNativePostgreSQLRecoveryRejectsWrongRuntimeAndLiveAuthorityOutput(t *testing.T) {
	v := nativeRecoveryFixture(NativePostgreSQLRestore)
	raw := nativeRecoveryOutputFixture(v)
	for name, edit := range map[string]func(*NativePostgreSQLRestoreCompletion){
		"wrong target": func(c *NativePostgreSQLRestoreCompletion) { c.RuntimeContext.TargetScopeID = strings.Repeat("f", 32) },
		"wrong archive": func(c *NativePostgreSQLRestoreCompletion) {
			c.RuntimeContext.SQLArchiveSHA256 = nativeObservationHash("0")
		},
		"wrong request reason": func(c *NativePostgreSQLRestoreCompletion) { c.RuntimeContext.Reason = "different" },
		"live authority":       func(c *NativePostgreSQLRestoreCompletion) { c.CurrentLockHeld = true },
		"obsolete schema":      func(c *NativePostgreSQLRestoreCompletion) { c.RuntimeContext.SchemaVersion = 22 },
	} {
		t.Run(name, func(t *testing.T) {
			var c NativePostgreSQLRestoreCompletion
			json.Unmarshal(raw, &c)
			edit(&c)
			bad, _ := json.Marshal(c)
			if _, e := decodeNativePostgreSQLRecoveryOutput(bad, v); e == nil {
				t.Fatal("contradictory original completion accepted")
			}
		})
	}
	var image Image
	json.Unmarshal([]byte(`{"Config":{"Env":["PGPASSWORD=private"]}}`), &image)
	if _, ok := v.databaseTaskEnvironment(image); ok {
		t.Fatal("ambient native PG connection configuration admitted")
	}
}

// The original producer explicitly reports a released historical lock. A Go
// bool zero value must not manufacture that report when a field is missing.
func TestNativePostgreSQLRestoreCompletionRequiresExplicitReleasedLock(t *testing.T) {
	task := nativeRecoveryFixture(NativePostgreSQLRestore)
	raw := nativeRecoveryOutputFixture(task)
	if !bytes.Contains(raw, []byte(`"current_lock_held":false`)) {
		t.Fatal("fixture omitted original explicit lock report")
	}
	got, err := decodeNativePostgreSQLRecoveryOutput(raw, task)
	if err != nil || got.NativePostgreSQLRestoreCompletion == nil || !bytes.Equal(got.NativePostgreSQLRestoreCompletion.Original, raw) {
		t.Fatal("original explicit false completion refused or changed", err)
	}
	for name, replacement := range map[string]string{
		"missing":    "",
		"null":       `"current_lock_held":null,`,
		"true":       `"current_lock_held":true,`,
		"string":     `"current_lock_held":"false",`,
		"number":     `"current_lock_held":0,`,
		"case alias": `"Current_Lock_Held":false,`,
		"duplicate":  `"current_lock_held":false,"current_lock_held":false,`,
	} {
		t.Run(name, func(t *testing.T) {
			bad := bytes.Replace(raw, []byte(`"current_lock_held":false,`), []byte(replacement), 1)
			if bytes.Equal(bad, raw) {
				t.Fatal("mutation did not change source bytes")
			}
			got, err := decodeNativePostgreSQLRecoveryOutput(bad, task)
			if err == nil || got.NativePostgreSQLRestoreCompletion != nil {
				t.Fatal("missing or contradictory original lock report admitted")
			}
		})
	}
}
