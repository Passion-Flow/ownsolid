package engine

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func backupTaskFixture() PostgresBackupTask {
	scope, operation := strings.Repeat("a", 32), strings.Repeat("b", 32)
	return PostgresBackupTask{Owner: Ownership{scope, operation, "database-" + PostgresBackupKind}, InputOwner: Ownership{scope, operation, PostgresBackupInputRole + "-" + operation}, OutputOwner: Ownership{scope, operation, PostgresBackupOutputRole + "-" + operation}, Image: "sha256:" + strings.Repeat("c", 64), UID: 999, GID: 999, NetworkID: strings.Repeat("d", 64), Peers: map[string]string{"control": strings.Repeat("e", 64), "postgres": strings.Repeat("f", 64)}, Host: postgresPeerAlias, Port: 55432, Database: "synthetic_database", User: "synthetic_operator"}
}

func TestPostgresBackupTaskClosedContextAndSecretReference(t *testing.T) {
	task := backupTaskFixture()
	if !task.valid() {
		t.Fatal("synthetic task invalid")
	}
	secret := "synthetic:secret\\with escapes"
	file, e := PostgresBackupPasswordFile(task, secret)
	if e != nil || file.Name != PostgresBackupPasswordName || string(file.Contents) != "postgres:55432:synthetic_database:synthetic_operator:synthetic\\:secret\\\\with escapes\n" {
		t.Fatal("libpq secret escaping changed", e)
	}
	for _, value := range []any{task, file, task.command()} {
		raw, _ := json.Marshal(value)
		if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte("synthetic:secret")) {
			t.Fatal("credential entered metadata or argv")
		}
	}
	for _, fault := range []string{"host", "port", "database-wildcard", "user-wildcard", "input-generation", "output-generation", "input-role", "output-role", "foreign-scope", "missing-control", "duplicate-peer", "unknown-peer", "root-uid", "unsafe-database", "conninfo", "uri"} {
		t.Run(fault, func(t *testing.T) {
			value := backupTaskFixture()
			switch fault {
			case "host":
				value.Host = "host.docker.internal"
			case "port":
				value.Port = 0
			case "database-wildcard":
				value.Database = "*"
			case "user-wildcard":
				value.User = "*"
			case "input-generation":
				value.InputOwner.OperationID = strings.Repeat("e", 32)
			case "output-generation":
				value.OutputOwner.OperationID = strings.Repeat("e", 32)
			case "input-role":
				value.InputOwner.Role = "operator-config"
			case "output-role":
				value.OutputOwner.Role = "postgres"
			case "foreign-scope":
				value.OutputOwner.ScopeID = strings.Repeat("e", 32)
			case "missing-control":
				delete(value.Peers, "control")
			case "duplicate-peer":
				value.Peers["control"] = value.Peers["postgres"]
			case "unknown-peer":
				value.Peers["foreign"] = strings.Repeat("0", 64)
			case "root-uid":
				value.UID = 0
			case "unsafe-database":
				value.Database = "name\nother"
			case "conninfo":
				value.Database = "host=outside dbname=foreign"
			case "uri":
				value.Database = "postgresql://outside/foreign"
			}
			if value.valid() {
				t.Fatal("unsafe backup task accepted")
			}
		})
	}
	for _, secret := range []string{"", "invalid\nline", "invalid\rline", "invalid\x00byte"} {
		if _, e := PostgresBackupPasswordFile(task, secret); e == nil {
			t.Fatal("unsafe pgpass input accepted")
		}
	}
	// SQL identifiers remain data even when unusual; only fixed positional argv
	// carry them, with no interpolation into the installed shell program.
	task.Database = "name; $(synthetic_command)"
	if !task.valid() || strings.Contains(task.command()[2], task.Database) || task.command()[6] != task.Database {
		t.Fatal("connection value became shell source")
	}
}

// These are explicitly synthetic tar/format fixtures, never real pg_dump proof.
func backupArchiveFixture(t *testing.T, fault string) (PostgresBackupTask, []byte) {
	t.Helper()
	task := backupTaskFixture()
	source := PostgresBackupSource{Database: task.Database, Operator: task.User, ServerVersionNumber: 180006, TenantID: "01234567-89ab-4cde-8f01-23456789abcd", SchemaVersion: 16, VectorVersion: "0.8.7"}
	switch fault {
	case "database":
		source.Database = "foreign"
	case "operator":
		source.Operator = "foreign"
	case "tenant":
		source.TenantID = "00000000-0000-0000-0000-000000000000"
	case "schema":
		source.SchemaVersion = 0
	case "vector":
		source.VectorVersion = ""
	}
	raw, _ := json.Marshal(source)
	if fault == "duplicate-json" {
		raw = append(raw[:len(raw)-1], []byte(`,"database":"other"}`)...)
	}
	files := map[string][]byte{"database.dump": []byte("PGDMPExplicit synthetic test bytes"), "contents.list": []byte("; Archive created at synthetic-time\n;     dbname: " + task.Database + "\n"), "source.json": raw, "tools.txt": []byte("pg_dump (PostgreSQL) 18.6 (Synthetic build)\npg_restore (PostgreSQL) 18.6 (Synthetic build)\n")}
	if fault == "magic" {
		files["database.dump"][0] = 'X'
	}
	if fault == "tools" {
		files["tools.txt"] = []byte("pg_dump (PostgreSQL) 18.60\npg_restore (PostgreSQL) 18.60\n")
	}
	if fault == "toc" {
		files["contents.list"] = []byte("foreign")
	}
	if fault == "missing" {
		delete(files, "source.json")
	}
	if fault == "metadata-limit" {
		files["contents.list"] = bytes.Repeat([]byte("x"), 8192)
	}
	if fault == "dump-limit" {
		files["database.dump"] = append([]byte("PGDMP"), bytes.Repeat([]byte("x"), 65536)...)
	}
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	write := func(header *tar.Header, value []byte) {
		if e := writer.WriteHeader(header); e != nil {
			t.Fatal(e)
		}
		if len(value) > 0 {
			if _, e := writer.Write(value); e != nil {
				t.Fatal(e)
			}
		}
	}
	root := &tar.Header{Name: postgresBackupRootName, Mode: 0755, Uid: 0, Gid: 0, Typeflag: tar.TypeDir, Format: tar.FormatUSTAR}
	if fault == "root-mode" {
		root.Mode = 0777
	}
	write(root, nil)
	private := &tar.Header{Name: postgresBackupRootName + "/private", Mode: 0700, Uid: task.UID, Gid: task.GID, Typeflag: tar.TypeDir, Format: tar.FormatUSTAR}
	if fault == "private-owner" {
		private.Uid++
	}
	write(private, nil)
	for _, name := range []string{"database.dump", "contents.list", "source.json", "tools.txt"} {
		value, found := files[name]
		if !found {
			continue
		}
		header := &tar.Header{Name: postgresBackupRootName + "/private/" + name, Mode: 0600, Uid: task.UID, Gid: task.GID, Size: int64(len(value)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}
		if name == "database.dump" {
			switch fault {
			case "file-mode":
				header.Mode = 0644
			case "symlink":
				header.Typeflag = tar.TypeSymlink
				header.Linkname = "outside"
				header.Size = 0
				value = nil
			case "hardlink":
				header.Typeflag = tar.TypeLink
				header.Linkname = "outside"
				header.Size = 0
				value = nil
			case "path":
				header.Name = "../outside"
			}
		}
		write(header, value)
		if fault == "duplicate" && name == "tools.txt" {
			write(header, value)
		}
	}
	if fault == "extra" {
		write(&tar.Header{Name: postgresBackupRootName + "/private/other", Mode: 0600, Uid: task.UID, Gid: task.GID, Size: 1, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}, []byte("x"))
	}
	if e := writer.Close(); e != nil {
		t.Fatal(e)
	}
	result := output.Bytes()
	if fault == "trailing" {
		result = append(result, []byte("unexpected")...)
	}
	if fault == "no-end" {
		result = result[:len(result)-1024]
	}
	return task, result
}

func TestPostgresBackupArchiveCompleteBoundedPrivateReadback(t *testing.T) {
	for _, fault := range []string{"valid", "root-mode", "private-owner", "file-mode", "symlink", "hardlink", "path", "duplicate", "extra", "missing", "trailing", "no-end", "magic", "database", "operator", "tenant", "schema", "vector", "tools", "toc", "duplicate-json", "metadata-limit", "dump-limit"} {
		t.Run(fault, func(t *testing.T) {
			task, raw := backupArchiveFixture(t, fault)
			var sink bytes.Buffer
			limit := int64(1 << 20)
			metadata := int64(4096)
			if fault == "dump-limit" {
				limit = 16384
			}
			result, e := readPostgresBackupArchive(bytes.NewReader(raw), &sink, task, limit, metadata)
			if fault != "valid" {
				if e == nil {
					t.Fatal("unsafe backup archive accepted")
				}
				return
			}
			if e != nil || result.Status != "verified_exited_postgres_custom_archive_bytes" || result.Bytes != int64(len(raw)) || result.SHA256 != wire.Digest(raw) || len(result.Files) != 4 || !bytes.Equal(sink.Bytes(), raw) {
				t.Fatal("complete archive readback changed", result, e)
			}
		})
	}
}

type failedBackupSink struct{}

func (failedBackupSink) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestPostgresBackupArchiveSinkFailureIsNotSuccess(t *testing.T) {
	task, raw := backupArchiveFixture(t, "valid")
	if _, e := readPostgresBackupArchive(bytes.NewReader(raw), failedBackupSink{}, task, 1<<20, 4096); e == nil {
		t.Fatal("failed sink qualified")
	}
}
