package configuration

import (
	"encoding/json"
	"os"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"strings"
	"testing"
)

func nativeBackupDescriptorFixture(t *testing.T) NativePostgreSQLRecoveryDescriptor {
	t.Helper()
	raw, e := os.ReadFile("../../../../services/control/config/examples/postgres-backup/mvp-postgresql-cpu-v1.backup.example.json")
	if e != nil {
		t.Fatal(e)
	}
	var r wire.PostgreSQLBackupRequest
	if wire.StrictJSON(raw, &r) != nil {
		t.Fatal("request fixture")
	}
	r.RuntimeConfigurationFile = r.ConfigurationRoot + "/runtime.yaml"
	return NativePostgreSQLRecoveryDescriptor{FormatVersion: 1, BackupTemplate: r, StoreRoots: [4]string{"/stores/models", "/stores/datasets", "/stores/training", "/stores/preparations"}, RegistryRoot: "/artifacts/registry", PreparedReleaseRoot: "/artifacts/prepared", RegistryScaffoldRoot: "/artifacts"}
}
func TestNativePostgreSQLRecoveryDescriptorBindsFreshAttemptBeforePublication(t *testing.T) {
	d := nativeBackupDescriptorFixture(t)
	template := d.BackupTemplate
	raw, _ := json.Marshal(d)
	if _, e := LoadNativePostgreSQLRecoveryDescriptor(raw); e != nil {
		t.Fatal(e)
	}
	ids := []string{strings.Repeat("1", 32), strings.Repeat("2", 32)}
	for _, id := range ids {
		r, e := d.BindBackup(template.SourceScopeID, id, strings.Repeat("1", 64), template.SourceManifestSHA256, template.ControlImageSHA256, template.PostgresImageSHA256, "Fresh explicit reason")
		if e != nil || r.BackupID != id || !strings.HasSuffix(r.BackupRoot, "/"+id) || r.MaintenanceSHA256 != strings.Repeat("1", 64) {
			t.Fatal(r, e)
		}
	}
	if d.BackupTemplate != template {
		t.Fatal("template mutated")
	}
	for _, raw := range []string{`null`, `{"format_version":1,"unknown":true}`, strings.Replace(string(raw), `"format_version":1`, `"format_version":1,"format_version":1`, 1)} {
		if _, e := LoadNativePostgreSQLRecoveryDescriptor([]byte(raw)); e == nil {
			t.Fatal("unsafe descriptor")
		}
	}
	for _, change := range []func(*NativePostgreSQLRecoveryDescriptor){func(d *NativePostgreSQLRecoveryDescriptor) { d.StoreRoots[1] = d.StoreRoots[0] }, func(d *NativePostgreSQLRecoveryDescriptor) {
		d.RegistryRoot = d.BackupTemplate.ConfigurationRoot + "/registry"
	}, func(d *NativePostgreSQLRecoveryDescriptor) { d.RegistryScaffoldRoot = "/" }, func(d *NativePostgreSQLRecoveryDescriptor) {
		d.BackupTemplate.RuntimeEnvironmentFile = "/operator/unbound.env"
	}, func(d *NativePostgreSQLRecoveryDescriptor) { d.BackupTemplate.BackupRoot = "/unrelated/leaf" }} {
		v := d
		change(&v)
		if v.Validate() == nil {
			t.Fatal("invalid private layout")
		}
	}
	if _, e := d.BindBackup(strings.Repeat("f", 32), ids[0], strings.Repeat("1", 64), template.SourceManifestSHA256, template.ControlImageSHA256, template.PostgresImageSHA256, "reason"); e == nil {
		t.Fatal("foreign source scope")
	}
}

func TestNativePostgreSQLRecoveryDescriptorRejectsActualArrayLengthBeforeTruncation(t *testing.T) {
	d := nativeBackupDescriptorFixture(t)
	valid, _ := json.Marshal(d)
	var members map[string]json.RawMessage
	if json.Unmarshal(valid, &members) != nil {
		t.Fatal("fixture")
	}
	cases := map[string]string{"missing": "", "null": "null", "empty": "[]", "short": `["/stores/models","/stores/datasets","/stores/training"]`, "long": `["/stores/models","/stores/datasets","/stores/training","/stores/preparations","/unadmitted/extra"]`, "invalid-element": `["/stores/models","/stores/datasets","/stores/training",false]`, "fifth-invalid": `["/stores/models","/stores/datasets","/stores/training","/stores/preparations",false]`}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			copy := map[string]json.RawMessage{}
			for k, v := range members {
				copy[k] = v
			}
			if value == "" {
				delete(copy, "store_roots")
			} else {
				copy["store_roots"] = json.RawMessage(value)
			}
			raw, _ := json.Marshal(copy)
			if _, e := LoadNativePostgreSQLRecoveryDescriptor(raw); e == nil {
				t.Fatal("actual malformed root array accepted")
			}
		})
	}
	if got, e := LoadNativePostgreSQLRecoveryDescriptor(valid); e != nil || got.StoreRoots != d.StoreRoots {
		t.Fatal("exact four original roots changed", e)
	}
}
