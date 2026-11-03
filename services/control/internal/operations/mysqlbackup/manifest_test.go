package mysqlbackup

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func parserBackupPolicy() config.MySQLBackupConfiguration {
	p := config.DefaultMySQLBackupPolicy()
	p.ClientFile, p.DumpFile, p.ArtifactManifestFile, p.ScratchRoot = "/synthetic/mysql", "/synthetic/mysqldump", "/synthetic/private/artifacts.json", "/synthetic/private/scratch"
	p.ClientSHA256, p.DumpSHA256 = strings.Repeat("a", 64), strings.Repeat("b", 64)
	return p
}
func parserManifest() BackupManifest {
	// Synthetic metadata for pure validation only. This is not an actual SQL
	// archive, native process receipt or accepted backup/restore fixture.
	sha := strings.Repeat("a", 64)
	data := DatabaseReadback{Format: "projectone.mysql.database-readback.v1", Tables: []TableReadback{{"audit_events", []string{"id"}, []string{"id"}, 1, sha}, {"schema_migrations", []string{"step"}, []string{"step"}, 3, sha}}}
	raw, _ := json.Marshal(data.Tables)
	data.SHA256 = mysqloperator.SHA256(raw)
	now := time.Now().UTC()
	m := BackupManifest{Format: "projectone.mysql.logical-backup.v1", BackupID: uuid.NewString(), CreatedAt: now, Reason: "Synthetic pure parser metadata", Source: mysqloperator.Target{Format: "projectone.mysql.operator-target.v1", ServerUUID: uuid.NewString(), Database: "synthetic_test", Principal: "synthetic_operator@localhost", Version: "8.4.8", InstallationID: uuid.NewString()}, SourceBindingSHA256: sha, TenantID: uuid.NewString(), SchemaSteps: 3, NativeArtifactManifestSHA256: sha, DumpSHA256: sha, SQLFile: archiveName, SQLSHA256: sha, SQLBytes: 100, Data: data}
	m.DumpExecution = NativeExecution{File: "/synthetic/mysqldump", Argv: []string{"--defaults-file=/synthetic/private/scratch/client.cnf", "--no-login-paths", "--single-transaction", "--quick", "--triggers", "--hex-blob", "--default-character-set=utf8mb4", "--no-tablespaces", "--set-gtid-purged=OFF", "--skip-add-locks", "--skip-disable-keys", "--skip-network-timeout", "--max-allowed-packet=16777216", "--skip-lock-tables", m.Source.Database}, GOOS: "linux", GOARCH: "arm64", PID: 123, StartedAt: now, CompletedAt: now, ExitCode: 0, Joined: true}
	for _, kind := range []string{"models", "datasets", "training", "preparations"} {
		m.Stores = append(m.Stores, StoreReadback{kind, snapshotDirectory + "/" + kind, TreeReadback{sha, 100, 1}})
	}
	return m
}

func TestBackupManifestRefusesForeignIncompleteFailedOrChangedMetadata(t *testing.T) {
	p := parserBackupPolicy()
	base := parserManifest()
	if e := base.validate(p); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*BackupManifest){
		func(m *BackupManifest) { m.Format = "postgresql" }, func(m *BackupManifest) { m.BackupID = "unknown" }, func(m *BackupManifest) { m.Reason = "" },
		func(m *BackupManifest) { m.Source.Principal = "user'; DROP USER root" }, func(m *BackupManifest) { m.Source.InstallationID = uuid.Nil.String() },
		func(m *BackupManifest) { m.DumpExecution.Joined = false }, func(m *BackupManifest) { m.DumpExecution.ExitCode = 1 }, func(m *BackupManifest) { m.DumpExecution.Argv[1] = "--password=FORBIDDEN" },
		func(m *BackupManifest) { m.DumpExecution.Argv[12] = "--max-allowed-packet=0" }, func(m *BackupManifest) { m.DumpExecution.GOARCH = "foreign" },
		func(m *BackupManifest) { m.SQLFile = "../foreign.sql" }, func(m *BackupManifest) { m.SQLBytes = p.MaximumSQLBytes + 1 },
		func(m *BackupManifest) { m.Data.Tables[0].Rows++ }, func(m *BackupManifest) { m.SchemaSteps++ },
		func(m *BackupManifest) { m.Stores[0].Directory = "../models" }, func(m *BackupManifest) { m.Stores[3] = m.Stores[0] }, func(m *BackupManifest) { m.Stores[0].Tree.Bytes = p.MaximumStoreBytes + 1 },
	} {
		raw, _ := json.Marshal(base)
		var bad BackupManifest
		if e := json.Unmarshal(raw, &bad); e != nil {
			t.Fatal(e)
		}
		change(&bad)
		if bad.validate(p) == nil {
			t.Fatal("foreign/incomplete/failed metadata accepted")
		}
	}
}

func TestBackupMissingActualOwnerReturnsZeroBeforeAnyNativeProcessOrFilesystem(t *testing.T) {
	c := config.Config{}
	p := parserBackupPolicy()
	c.Database.MySQLBackup = &p
	c.Models.ArtifactRoot, c.Datasets.ArtifactRoot, c.Training.ArtifactRoot, c.ModelPreparation.ArtifactRoot = "/synthetic/models", "/synthetic/datasets", "/synthetic/training", "/synthetic/preparations"
	proof, e := Backup(t.Context(), nil, c, nil, "/synthetic/backups/new", "Synthetic explicit operator", 203)
	if e == nil || !reflect.DeepEqual(proof, BackupManifest{}) {
		t.Fatal("missing actual source projected backup")
	}
	proof, e = ReconcileBackup(t.Context(), nil, c, "/synthetic/backups/new")
	if e == nil || !reflect.DeepEqual(proof, BackupManifest{}) {
		t.Fatal("missing actual source adopted completion")
	}
}
