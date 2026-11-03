package databaseaccess

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestReleasesCacheV1InstalledPrincipalRowsGrantsAndReadiness(t *testing.T) {
	f := newActualFixture(t, "localization")
	ctx := t.Context()
	if status, err := Prepare(ctx, f.service.DB, f.lease, f.input); err != nil || status != Verified {
		t.Fatal(status, err)
	}
	identityfixture.Bootstrap(t, f.service)
	var oid uint32
	var verifier string
	if err := f.lease.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", f.role).Scan(&oid, &verifier); err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]string {
		out := map[string]string{}
		for _, table := range append(runtimeTables(f.input), "schema_migrations") {
			var value string
			if err := f.lease.QueryRow(ctx, "SELECT md5(COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text,'[]')) FROM "+pgx.Identifier{table}.Sanitize()+" t").Scan(&value); err != nil {
				t.Fatal(table, err)
			}
			out[table] = value
		}
		return out
	}
	before := snapshot()
	if len(before) != 86 {
		t.Fatal("exact cumulative row inventory", len(before))
	}
	c := f.input.configuration
	profile := config.ReleasesCacheV1Profile
	c.Server.ApplicationProfile = &profile
	c.ReleaseViewConfigFile = "/run/projectone/admin-view.json"
	c.ModelCache = config.DefaultModelCacheConfiguration()
	c.ModelCache.Enabled = true
	c.ModelCache.Address = "127.0.0.1:44499"
	c.ModelCache.Username = "synthetic_cache"
	c.ModelCache.PasswordFile = "/run/projectone/private/cache-password"
	c.Models.ArtifactRoot = t.TempDir()
	c.Datasets = config.DatasetConfiguration{ArtifactRoot: t.TempDir(), PythonExecutable: "/usr/local/bin/python3.12", ProcessorFile: "/opt/projectone/data-processing/process.py"}
	c.Training = config.TrainingConfiguration{ArtifactRoot: t.TempDir(), ImageReference: "projectone/training@sha256:" + strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("b", 64), WorkerSourceSHA256: strings.Repeat("c", 64)}
	c.ModelPreparation = config.ModelPreparationConfiguration{ArtifactRoot: t.TempDir(), ImageReference: "projectone/model-lifecycle@sha256:" + strings.Repeat("d", 64), ImageID: "sha256:" + strings.Repeat("e", 64), WorkerSourceSHA256: strings.Repeat("f", 64)}
	policy := c.Policy()
	policy.HTTP.ShutdownSeconds = 20
	c.Runtime = &policy
	f.input.configuration = c
	for i := 0; i < 2; i++ {
		if status, err := UpgradeReleases(ctx, f.service.DB, f.lease, f.input); err != nil || status != ReleasesUpgradeComplete {
			t.Fatal(status, err)
		}
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("release admission changed a cumulative row")
		}
	}
	var afterOID uint32
	var afterVerifier string
	if err := f.lease.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", f.role).Scan(&afterOID, &afterVerifier); err != nil || afterOID != oid || afterVerifier != verifier {
		t.Fatal("original principal or SCRAM changed", err)
	}
	runtime, err := pgxpool.NewWithConfig(ctx, f.input.connection.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err = database.Check(ctx, runtime, config.ReleasesCacheV1Profile); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Exec(ctx, "CREATE TABLE cache_v1_forbidden_ddl(id integer)"); err == nil {
		t.Fatal("runtime DDL admitted")
	}
	if _, err = runtime.Exec(ctx, "UPDATE schema_migrations SET checksum=checksum"); err == nil {
		t.Fatal("runtime ledger DML admitted")
	}
	if status, err := Check(ctx, f.service.DB, f.lease, f.input); err != nil || status != Verified {
		t.Fatal(status, err)
	}
	wrong := f.input
	wrong.configuration.Server.ApplicationProfile = nil
	if _, err = UpgradeReleases(ctx, f.service.DB, f.lease, wrong); err == nil {
		t.Fatal("broad platform admitted")
	}
	t.Log("Actual account22/86-table rows, original role OID/SCRAM and exact DML/no-DDL retained across repeated releases-cache-v1 admission; no schema effect was required or sent")
}
