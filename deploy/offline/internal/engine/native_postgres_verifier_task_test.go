package engine

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func nativeBackupVerifierTaskFixture() DatabaseTask {
	scope, attempt := strings.Repeat("1", 32), strings.Repeat("2", 32)
	owner := func(role string) Ownership { return Ownership{scope, attempt, role + "-" + attempt} }
	r := wire.PostgreSQLBackupRequest{FormatVersion: 1, BackupID: strings.Repeat("3", 32), SourceScopeID: strings.Repeat("4", 32), MaintenanceSHA256: nativeObservationHash("b"), SourceManifestSHA256: nativeObservationHash("c"), ControlImageSHA256: nativeObservationHash("a"), PostgresImageSHA256: nativeObservationHash("d"), TenantID: "11111111-1111-1111-1111-111111111111", Database: "synthetic_database", Operator: "synthetic_operator", RuntimeConfigurationFile: "/run/projectone/control.yaml", ConfigurationRoot: "/run/projectone", BackupRoot: "/fixture/backups/source", Reason: "Synthetic verifier transport only", ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile,
		Policy: wire.PostgreSQLBackupPolicy{DumpFile: "/native/bin/pg_dump", DumpSHA256: nativeObservationHash("a"), RestoreFile: "/native/bin/pg_restore", RestoreSHA256: nativeObservationHash("b"), ControlFile: "/native/bin/control", ControlSHA256: nativeObservationHash("c"), ExpectedVersion: "18.6", ScratchRoot: "/fixture/scratch", OperationSeconds: 60, TerminationGraceSeconds: 5, MaximumStoreBytes: 1 << 20, MaximumStoreFiles: 16, MaximumSQLBytes: 1 << 20, MaximumConfigurationBytes: 4096, MaximumManifestBytes: 4096, MinimumFreeBytes: 1 << 20}}
	return DatabaseTask{Owner: Ownership{scope, attempt, "database-" + NativePostgreSQLBackupVerify}, VolumeOwner: owner(NativePostgreSQLVerifierInputRole), Kind: NativePostgreSQLBackupVerify, Image: "sha256:" + r.ControlImageSHA256, UID: 1000, GID: 1000, NativePostgreSQLBackupVerifier: &NativePostgreSQLBackupVerifierProfile{Request: r, CandidateSHA256: nativeObservationHash("e"), BackupOwner: owner(NativePostgreSQLArtifactRole), ScratchOwner: owner(NativePostgreSQLVerifierScratchRole), Placeholder: &NativePostgreSQLVerifierPlaceholder{Owner: owner(NativePostgreSQLVerifierPlaceholderRole), Target: "/fixture"}}}
}

func TestNativePostgreSQLBackupVerifierHasClosedNetworkAndMounts(t *testing.T) {
	v := nativeBackupVerifierTaskFixture()
	if !v.valid() || v.databaseTaskNetworkMode() != "none" || len(v.Peers) != 0 {
		t.Fatal("fixed verifier context refused or acquired a network")
	}
	wanted := []string{NativePostgreSQLBackupVerify, "--request", DatabaseTaskDestination + "/private/" + NativePostgreSQLVerifierRequestFile, "--manifest-sha256", v.NativePostgreSQLBackupVerifier.CandidateSHA256}
	if !reflect.DeepEqual(v.command(), wanted) {
		t.Fatal("verifier command not fixed")
	}
	mounts := v.databaseMountProfiles()
	if len(mounts) != 4 || !mounts[0].readOnly || mounts[0].private || !mounts[1].readOnly || !mounts[1].private || !mounts[2].readOnly || !mounts[2].private || mounts[3].readOnly || !mounts[3].private {
		t.Fatal("payload/base must be readonly; only scratch may write")
	}
	var image Image
	if json.Unmarshal([]byte(`{"Config":{"Env":["PATH=/native/bin","TMPDIR=/foreign"]}}`), &image) != nil {
		t.Fatal("image fixture")
	}
	env, ok := v.databaseTaskEnvironment(image)
	if !ok || env["TMPDIR"] != v.NativePostgreSQLBackupVerifier.Request.Policy.ScratchRoot || image.Config.Env[1] != "TMPDIR=/foreign" {
		t.Fatal("typed durable scratch was not isolated from source image")
	}
}

func TestNativePostgreSQLBackupVerifierRejectsForeignAndOverlappingContext(t *testing.T) {
	for name, change := range map[string]func(*DatabaseTask){
		"legacy application": func(v *DatabaseTask) {
			v.NativePostgreSQLBackupVerifier.Request.ApplicationProfile = wire.PostgreSQLLegacyApplicationProfile
		},
		"write command":          func(v *DatabaseTask) { v.Kind = "restore-kubernetes-postgres" },
		"host network":           func(v *DatabaseTask) { v.NetworkID = "host" },
		"live peer":              func(v *DatabaseTask) { v.Peers = map[string]string{"postgres": strings.Repeat("a", 64)} },
		"image not source bound": func(v *DatabaseTask) { v.Image = "sha256:" + nativeObservationHash("f") },
		"foreign volume":         func(v *DatabaseTask) { v.NativePostgreSQLBackupVerifier.BackupOwner.ScopeID = strings.Repeat("f", 32) },
		"foreign operation": func(v *DatabaseTask) {
			v.NativePostgreSQLBackupVerifier.ScratchOwner.OperationID = strings.Repeat("f", 32)
		},
		"wrong artifact role": func(v *DatabaseTask) { v.NativePostgreSQLBackupVerifier.BackupOwner.Role = "models" },
		"write output":        func(v *DatabaseTask) { v.OutputOwner = &v.VolumeOwner },
		"legacy source":       func(v *DatabaseTask) { v.SourceOwner = &v.VolumeOwner },
		"mutable live view":   func(v *DatabaseTask) { v.LocalizationOwner = &v.VolumeOwner },
		"scratch aliases payload": func(v *DatabaseTask) {
			v.NativePostgreSQLBackupVerifier.Request.Policy.ScratchRoot = v.NativePostgreSQLBackupVerifier.Request.BackupRoot
		},
		"scratch below payload": func(v *DatabaseTask) {
			v.NativePostgreSQLBackupVerifier.Request.Policy.ScratchRoot = v.NativePostgreSQLBackupVerifier.Request.BackupRoot + "/tmp"
		},
		"shadow private request": func(v *DatabaseTask) { v.NativePostgreSQLBackupVerifier.Placeholder.Target = "/run/projectone" },
		"external substitute":    func(v *DatabaseTask) { v.NativePostgreSQLBackupVerifier.Placeholder.Target = "/Volumes/DevData" },
		"shadow executable":      func(v *DatabaseTask) { v.NativePostgreSQLBackupVerifier.Placeholder.Target = "/usr" },
		"overlay payload": func(v *DatabaseTask) {
			v.NativePostgreSQLBackupVerifier.Placeholder.Target = v.NativePostgreSQLBackupVerifier.Request.BackupRoot
		},
		"no native profile": func(v *DatabaseTask) { v.NativePostgreSQLBackupVerifier = nil },
	} {
		t.Run(name, func(t *testing.T) {
			v := nativeBackupVerifierTaskFixture()
			change(&v)
			if v.valid() {
				t.Fatal("foreign/unsafe verifier admitted")
			}
		})
	}
}

func TestNativePostgreSQLBackupVerifierFreezesExactPublicRequest(t *testing.T) {
	v := nativeBackupVerifierTaskFixture()
	raw, _ := json.Marshal(v.NativePostgreSQLBackupVerifier.Request)
	files := []ConfigurationFile{{Name: NativePostgreSQLVerifierRequestFile, Contents: raw}}
	if !v.nativeVerifierFiles(files) {
		t.Fatal("exact original public request refused")
	}
	files[0].Name = "control.yaml"
	if v.nativeVerifierFiles(files) {
		t.Fatal("credential configuration admitted in readonly source verifier")
	}
	files[0].Name = NativePostgreSQLVerifierRequestFile
	changed := v.NativePostgreSQLBackupVerifier.Request
	changed.Reason = "different"
	files[0].Contents, _ = json.Marshal(changed)
	if v.nativeVerifierFiles(files) {
		t.Fatal("request changed independently from frozen task")
	}
	frozen := v.freezeNativeVerifier()
	v.NativePostgreSQLBackupVerifier.CandidateSHA256 = nativeObservationHash("0")
	v.NativePostgreSQLBackupVerifier.Placeholder.Target = "/foreign"
	if frozen.NativePostgreSQLBackupVerifier.CandidateSHA256 == v.NativePostgreSQLBackupVerifier.CandidateSHA256 || frozen.NativePostgreSQLBackupVerifier.Placeholder.Target != "/fixture" {
		t.Fatal("observer context aliases mutable caller")
	}
}

func TestNativePostgreSQLBackupVerifierRejectsChangedPrivateSubpathMount(t *testing.T) {
	v := nativeBackupVerifierTaskFixture()
	host, live := []any{}, []any{}
	for _, m := range v.databaseMountProfiles() {
		subpath := ""
		if m.private {
			subpath = UtilityPrivateDirectory
		}
		host = append(host, map[string]any{"Type": "volume", "Source": m.owner.VolumeName(), "Target": m.target, "ReadOnly": m.readOnly, "VolumeOptions": map[string]any{"NoCopy": true, "Subpath": subpath}})
		live = append(live, map[string]any{"Type": "volume", "Name": m.owner.VolumeName(), "Destination": m.target, "Driver": "local", "RW": !m.readOnly})
	}
	raw, _ := json.Marshal(map[string]any{"HostConfig": map[string]any{"Mounts": host}, "Mounts": live})
	var observed RuntimeContainer
	if json.Unmarshal(raw, &observed) != nil || !v.validDatabaseMounts(observed) {
		t.Fatal("exact readonly private source mounts refused")
	}
	observed.HostConfig.Mounts[2].VolumeOptions.Subpath = ""
	if v.validDatabaseMounts(observed) {
		t.Fatal("volume-root substitution admitted")
	}
	observed.HostConfig.Mounts[2].VolumeOptions.Subpath = UtilityPrivateDirectory
	observed.HostConfig.Mounts[2].ReadOnly = false
	if v.validDatabaseMounts(observed) {
		t.Fatal("writable source substitution admitted")
	}
}

func nativeVerifierFrame(stream byte, body []byte) []byte {
	header := make([]byte, dockerOutputHeaderBytes)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(body)))
	return append(header, body...)
}

func nativeVerifierOutputFixture(v DatabaseTask) []byte {
	p := v.NativePostgreSQLBackupVerifier
	out := nativeBackupObservationFixture()
	out.BackupID = p.Request.BackupID
	out.RequestSHA256 = nativeVerifierRequestSHA256(p.Request)
	out.ManifestSHA256 = p.CandidateSHA256
	out.NativePostgreSQL.RequestSHA256 = out.RequestSHA256
	out.NativePostgreSQL.CandidateSHA256 = p.CandidateSHA256
	out.NativePostgreSQL.SourceManifestSHA256 = p.Request.SourceManifestSHA256
	raw, _ := json.Marshal(out)
	return raw
}

func TestNativePostgreSQLBackupVerifierOutputIsBoundedAndPrivate(t *testing.T) {
	v := nativeBackupVerifierTaskFixture()
	raw := nativeVerifierOutputFixture(v)
	framed := nativeVerifierFrame(1, raw)
	got, e := readNativePostgreSQLVerifierOutput(bytes.NewReader(framed), v)
	if e != nil || got.NativePostgreSQLBackup == nil || got.NativePostgreSQLBackup.OriginalSHA256 != wire.Digest(raw) {
		t.Fatal("valid original-output transport refused", e)
	}
	public, _ := json.Marshal(got)
	if bytes.Contains(public, []byte("native_postgresql")) || bytes.Contains(public, []byte(`"original"`)) {
		t.Fatal("private native envelope projected")
	}
	for name, body := range map[string][]byte{
		"stderr credentials": append(append([]byte(nil), framed...), nativeVerifierFrame(2, []byte("synthetic-private-marker"))...),
		"truncated frame":    framed[:len(framed)-1],
		"duplicate result":   append(append([]byte(nil), framed...), nativeVerifierFrame(1, raw)...),
		"invalid stream":     nativeVerifierFrame(0, raw),
	} {
		t.Run(name, func(t *testing.T) {
			out, e := readNativePostgreSQLVerifierOutput(bytes.NewReader(body), v)
			if e == nil || out.NativePostgreSQLBackup != nil || strings.Contains(e.Error(), "synthetic-private-marker") {
				t.Fatal("invalid output leaked or qualified")
			}
		})
	}
	v.NativePostgreSQLBackupVerifier.CandidateSHA256 = nativeObservationHash("0")
	if _, e := readNativePostgreSQLVerifierOutput(bytes.NewReader(framed), v); e == nil {
		t.Fatal("different candidate qualified")
	}
}
