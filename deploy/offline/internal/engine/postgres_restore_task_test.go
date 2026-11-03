package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestRestoreArchiveCancellationStopsFurtherLocalReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := strings.NewReader("verified archive bytes")
	reader := restoreContextReader{ctx, source}
	if _, e := io.ReadFull(reader, make([]byte, 4)); e != nil {
		t.Fatal(e)
	}
	remaining := source.Len()
	cancel()
	if _, e := io.ReadAll(reader); e != context.Canceled || source.Len() != remaining {
		t.Fatal("canceled validation/upload continued consuming the host artifact", e)
	}
}

// Explicit synthetic custom-dump/transport fixture; the tagged actual journey
// must prove genuine pg_restore and SQL/catalog behavior separately.
func restoreTaskFixture(t *testing.T) (PostgresBackupTask, []byte) {
	t.Helper()
	source, raw := backupArchiveFixture(t, "")
	raw = append(raw, make([]byte, archiveReadbackReserveBytes-int64(len(raw)))...)
	a, e := readPostgresBackupArchive(bytes.NewReader(raw), io.Discard, source, 1<<20, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	target := source
	target.Database = "synthetic_restored_database"
	target.User = "synthetic_restored_operator"
	target.Owner.Role = "database-" + PostgresRestoreKind
	target.InputOwner.Role = PostgresRestoreInputRole + "-" + target.Owner.OperationID
	target.OutputOwner.Role = PostgresRestoreOutputRole + "-" + target.Owner.OperationID
	target.Restore = &PostgresRestoreProfile{SourceOwner: Ownership{target.Owner.ScopeID, target.Owner.OperationID, PostgresRestoreSourceRole + "-" + target.Owner.OperationID}, SourceUID: target.UID, SourceGID: target.GID, ArchiveSHA256: wire.Digest(raw), ArchiveBytes: int64(len(raw)), ExpectedSource: a.Source, ExpectedFiles: a.Files}
	return target, raw
}

func TestPostgresRestoreOutputIdentityOwnershipAndFailureGuards(t *testing.T) {
	task, _ := restoreTaskFixture(t)
	for _, fault := range []string{"", "nonempty", "target", "tenant", "schema", "tool", "owner", "link", "missing", "trailer", "end"} {
		t.Run(map[bool]string{true: "valid", false: fault}[fault == ""], func(t *testing.T) {
			before := PostgresRestoreBefore{Database: task.Database, Operator: task.User, ServerVersionNumber: 180006, Empty: true}
			after := task.Restore.ExpectedSource
			after.Database, after.Operator = task.Database, task.User
			switch fault {
			case "nonempty":
				before.Empty = false
			case "target":
				before.Database = "outside"
			case "tenant":
				after.TenantID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
			case "schema":
				after.SchemaVersion++
			}
			b, _ := json.Marshal(before)
			a, _ := json.Marshal(after)
			tool := []byte("pg_restore (PostgreSQL) 18.6 (Synthetic build)\n")
			if fault == "tool" {
				tool = []byte("pg_restore (PostgreSQL) 18.60\n")
			}
			var data bytes.Buffer
			tw := tar.NewWriter(&data)
			tw.WriteHeader(&tar.Header{Name: "postgres-restore", Typeflag: tar.TypeDir, Mode: 0755})
			tw.WriteHeader(&tar.Header{Name: "postgres-restore/private", Typeflag: tar.TypeDir, Mode: 0700, Uid: task.UID, Gid: task.GID})
			for _, file := range []struct {
				name string
				body []byte
			}{{"before.json", b}, {"source.json", a}, {"tools.txt", tool}} {
				if fault == "missing" && file.name == "source.json" {
					continue
				}
				h := tar.Header{Name: "postgres-restore/private/" + file.name, Typeflag: tar.TypeReg, Mode: 0600, Uid: task.UID, Gid: task.GID, Size: int64(len(file.body))}
				if fault == "owner" {
					h.Uid++
				}
				if fault == "link" {
					h.Linkname = "/outside"
				}
				tw.WriteHeader(&h)
				tw.Write(file.body)
			}
			tw.Close()
			raw := data.Bytes()
			if fault == "trailer" {
				raw = append(raw, 1)
			}
			if fault == "end" {
				raw = raw[:len(raw)-1024]
			}
			r, e := postgresRestoreResultArchive(raw, task)
			if fault == "" {
				if e != nil || r.Status != "verified_exited_postgres_restore_process_and_source_metadata" {
					t.Fatal(r, e)
				}
			} else if e == nil {
				t.Fatal("invalid restoration outcome accepted")
			}
		})
	}
}

func TestPostgresRestoreCopiedRootMappingDoesNotWeakenBackupRoot(t *testing.T) {
	task, raw := restoreTaskFixture(t)
	var changed bytes.Buffer
	tr := tar.NewReader(bytes.NewReader(raw))
	tw := tar.NewWriter(&changed)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if strings.TrimSuffix(h.Name, "/") == postgresBackupRootName {
			h.Uid, h.Gid = task.UID, task.GID
		}
		if e = tw.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if _, e = io.Copy(tw, tr); e != nil {
			t.Fatal(e)
		}
	}
	tw.Close()
	data := changed.Bytes()
	if _, e := readPostgresBackupArchive(bytes.NewReader(data), io.Discard, task.restoreSourceArchiveTask(task.UID, task.GID), 1<<20, 1<<20); e == nil {
		t.Fatal("default backup root-owner guard weakened")
	}
	if _, e := readPostgresArchiveWithRoot(bytes.NewReader(data), io.Discard, task.restoreSourceArchiveTask(task.UID, task.GID), 1<<20, 1<<20, task.UID, task.GID); e != nil {
		t.Fatal("declared copied root mapping", e)
	}
}

func TestPostgresRestoreFixedProfileAndDefaultBackupCompatibility(t *testing.T) {
	task, _ := restoreTaskFixture(t)
	if !task.valid() || task.postgresKind() != PostgresRestoreKind || len(task.postgresOwners()) != 3 {
		t.Fatal("closed restore context")
	}
	mounts := task.postgresMounts()
	source := mounts[PostgresBackupOutputDestination]
	if !source.readOnly || source.subpath != "postgresql" || source.owner != task.Restore.SourceOwner || mounts[PostgresRestoreOutputDestination].readOnly || len(mounts) != 3 {
		t.Fatal("readonly dump/result mapping")
	}
	for _, flag := range []string{"--single-transaction", "--exit-on-error", "--no-owner", "--no-privileges"} {
		if !strings.Contains(task.command()[2], flag) {
			t.Fatal("required fixed restore flag", flag)
		}
	}
	for _, flag := range []string{"--clean", "--create", "--jobs", "--disable-triggers"} {
		if strings.Contains(task.command()[2], flag) {
			t.Fatal("unsafe restore flag", flag)
		}
	}
	task.Database = "name; $(synthetic_command)"
	if !task.valid() || strings.Contains(task.command()[2], task.Database) || task.command()[6] != task.Database {
		t.Fatal("database became shell source")
	}
	secret := "synthetic-secret-not-a-default"
	file, e := PostgresBackupPasswordFile(task, secret)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []any{task, file, task.command()} {
		raw, _ := json.Marshal(v)
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("secret in metadata/argv")
		}
	}
	backup := backupTaskFixture()
	raw, _ := json.Marshal(backup)
	if bytes.Contains(raw, []byte("restore")) || backup.postgresKind() != PostgresBackupKind || len(backup.postgresOwners()) != 2 || len(backup.postgresMounts()) != 2 || backup.postgresMounts()[PostgresBackupOutputDestination].readOnly || backup.postgresMounts()[PostgresBackupOutputDestination].subpath != "" {
		t.Fatal("default backup contract changed")
	}
}

func TestPostgresRestoreRejectsUnboundArtifactGenerationAndLiveData(t *testing.T) {
	for _, fault := range []string{"source-role", "source-generation", "source-scope", "source-uid", "input-role", "output-role", "archive-hash", "archive-size", "source-tenant", "source-version", "source-conninfo", "file-path", "file-hash", "duplicate-file"} {
		t.Run(fault, func(t *testing.T) {
			v, _ := restoreTaskFixture(t)
			switch fault {
			case "source-role":
				v.Restore.SourceOwner.Role = "postgres"
			case "source-generation":
				v.Restore.SourceOwner.OperationID = strings.Repeat("e", 32)
			case "source-scope":
				v.Restore.SourceOwner.ScopeID = strings.Repeat("e", 32)
			case "source-uid":
				v.Restore.SourceUID++
			case "input-role":
				v.InputOwner.Role = "operator-config"
			case "output-role":
				v.OutputOwner.Role = "postgres"
			case "archive-hash":
				v.Restore.ArchiveSHA256 = "bad"
			case "archive-size":
				v.Restore.ArchiveBytes = 1024
			case "source-tenant":
				v.Restore.ExpectedSource.TenantID = "00000000-0000-0000-0000-000000000000"
			case "source-version":
				v.Restore.ExpectedSource.SchemaVersion = 0
			case "source-conninfo":
				v.Restore.ExpectedSource.Database = "host=outside"
			case "file-path":
				v.Restore.ExpectedFiles[0].Name = "../outside"
			case "file-hash":
				v.Restore.ExpectedFiles[0].SHA256 = "bad"
			case "duplicate-file":
				v.Restore.ExpectedFiles[1] = v.Restore.ExpectedFiles[0]
			}
			if v.valid() {
				t.Fatal("invalid restore context accepted")
			}
		})
	}
}

func TestPostgresRestoreProtectedArchivePreflightAndTamperRefusal(t *testing.T) {
	task, raw := restoreTaskFixture(t)
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	name := filepath.Join(dir, "postgres.tar")
	if e := os.WriteFile(name, raw, 0600); e != nil {
		t.Fatal(e)
	}
	input, e := os.Open(name)
	if e != nil {
		t.Fatal(e)
	}
	defer input.Close()
	c := &Client{config: configuration.Engine{ResponseBytes: 1 << 20}}
	if result, e := c.ValidatePostgresRestoreArchive(context.Background(), task, input); e != nil || result.SHA256 != task.Restore.ArchiveSHA256 {
		t.Fatal(result, e)
	}
	wrong := task
	copyProfile := *task.Restore
	wrong.Restore = &copyProfile
	wrong.Restore.ArchiveSHA256 = strings.Repeat("f", 64)
	if _, e = c.ValidatePostgresRestoreArchive(context.Background(), wrong, input); e == nil {
		t.Fatal("wrong independent archive hash")
	}
	if e = os.Chmod(name, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ValidatePostgresRestoreArchive(context.Background(), task, input); e == nil {
		t.Fatal("public input accepted")
	}
	os.Chmod(name, 0600)
	if e = os.Link(name, filepath.Join(dir, "other")); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ValidatePostgresRestoreArchive(context.Background(), task, input); e == nil {
		t.Fatal("hardlinked input accepted")
	}
	os.Remove(filepath.Join(dir, "other"))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = c.ValidatePostgresRestoreArchive(cancelled, task, input); e == nil {
		t.Fatal("cancellation ignored")
	}
	f, e := os.OpenFile(name, os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteAt([]byte("X"), 0)
	f.Sync()
	f.Close()
	if _, e = c.ValidatePostgresRestoreArchive(context.Background(), task, input); e == nil {
		t.Fatal("tampered bytes accepted")
	}
}
