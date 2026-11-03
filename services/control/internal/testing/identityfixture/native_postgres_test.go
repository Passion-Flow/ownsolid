package identityfixture

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These are explicitly non-executed file/metadata doubles. They establish no
// PostgreSQL, tool-distribution, TLS, restart or restore integration acceptance.
func nativeMetadataDouble(t *testing.T) (string, nativePostgresInput) {
	t.Helper()
	state := filepath.Join(t.TempDir(), "state")
	root := filepath.Join(state, "evidence", "m20", "native-account-recovery-20261023-120000-12345678")
	dataRoot := filepath.Join(state, "postgresql", "fixtures", strings.Repeat("a", 32))
	prefix := filepath.Join(state, "toolchains", "postgresql", "18.6-vector0.8.7", "darwin-arm64-"+strings.Repeat("b", 32))
	for _, path := range []string{root, filepath.Join(dataRoot, "data"), filepath.Join(dataRoot, "credentials"), filepath.Join(prefix, "bin")} {
		if os.MkdirAll(path, 0700) != nil {
			t.Fatal("METADATA_DOUBLE_DIRECTORY_FAILED")
		}
	}
	input := nativePostgresInput{Format: nativePostgresInputFormat, SourceSchema: "m20_repository_" + strings.Repeat("c", 32), RestoreDatabase: "m12_restore_" + strings.Repeat("d", 32) + "_test", Host: "127.0.0.1", Port: 15432, Database: "synthetic_native_test", User: "synthetic_fixture", DataDirectory: filepath.Join(dataRoot, "data"), SystemIdentifier: "123456789", Prefix: prefix, Tools: map[string]nativeTool{}, PasswordFile: filepath.Join(root, "libpq-password"), CAFile: filepath.Join(dataRoot, "credentials", "ca.pem"), RestartRequest: filepath.Join(root, "restart.request.json"), RestartAck: filepath.Join(root, "restart.ack.json"), Nonce: strings.Repeat("e", 32)}
	for _, name := range []string{"pg_dump", "pg_restore"} {
		file := filepath.Join(prefix, "bin", name)
		raw := []byte("Explicit non-executed tool file double: " + name + "\n")
		if os.WriteFile(file, raw, 0700) != nil {
			t.Fatal("METADATA_DOUBLE_TOOL_FAILED")
		}
		input.Tools[name] = nativeTool{Path: file, SHA256: nativeBytesSHA(raw)}
	}
	for path, data := range map[string][]byte{input.PasswordFile: []byte("Explicit non-working metadata credential double\n"), input.CAFile: []byte("Explicit non-certificate metadata double\n")} {
		if os.WriteFile(path, data, 0600) != nil {
			t.Fatal("METADATA_DOUBLE_PRIVATE_FILE_FAILED")
		}
		if path == input.PasswordFile {
			input.PasswordSHA256 = nativeBytesSHA(data)
		} else {
			input.CASHA256 = nativeBytesSHA(data)
		}
	}
	file := filepath.Join(root, "native-postgres.json")
	writeNativeMetadataDouble(t, file, input)
	return file, input
}

func writeNativeMetadataDouble(t *testing.T, file string, input nativePostgresInput) {
	t.Helper()
	raw, e := json.Marshal(input)
	if e != nil || os.WriteFile(file, raw, 0600) != nil {
		t.Fatal("METADATA_DOUBLE_DOCUMENT_FAILED")
	}
}

func TestNativePostgresMetadataExplicitDoublesAndRefusals(t *testing.T) {
	t.Run("valid_metadata_is_not_real_fixture_acceptance", func(t *testing.T) {
		file, expected := nativeMetadataDouble(t)
		actual, e := loadNativePostgresInput(file)
		if e != nil || actual.SourceSchema != expected.SourceSchema || actual.RestoreDatabase != expected.RestoreDatabase || actual.scopeIdentity == nil {
			t.Fatal("VALID_METADATA_DOUBLE_REFUSED")
		}
	})
	for _, name := range []string{"remote_host", "connection_string_database", "non_test_database", "foreign_schema", "foreign_restore", "outside_data_directory", "outside_password_reference", "changed_tool", "changed_secret", "public_input", "input_symlink", "input_hardlink", "unknown_field", "duplicate_field", "boolean_port", "null_tools"} {
		t.Run(name, func(t *testing.T) {
			file, input := nativeMetadataDouble(t)
			switch name {
			case "remote_host":
				input.Host = "192.0.2.1"
			case "connection_string_database":
				input.Database = "host=192.0.2.1 dbname=foreign_test"
			case "non_test_database":
				input.Database = "customer"
			case "foreign_schema":
				input.SourceSchema = "public"
			case "foreign_restore":
				input.RestoreDatabase = "customer_test"
			case "outside_data_directory":
				input.DataDirectory = filepath.Join(t.TempDir(), "data")
			case "outside_password_reference":
				input.PasswordFile = filepath.Join(t.TempDir(), "password")
			}
			writeNativeMetadataDouble(t, file, input)
			switch name {
			case "changed_tool":
				if os.WriteFile(input.Tools["pg_dump"].Path, []byte("changed non-executed file double"), 0700) != nil {
					t.Fatal("DOUBLE_CHANGE_FAILED")
				}
			case "changed_secret":
				if os.WriteFile(input.PasswordFile, []byte("changed non-working metadata double"), 0600) != nil {
					t.Fatal("DOUBLE_CHANGE_FAILED")
				}
			case "public_input":
				if os.Chmod(file, 0644) != nil {
					t.Fatal("DOUBLE_MODE_CHANGE_FAILED")
				}
			case "input_symlink":
				target := file + ".original"
				if os.Rename(file, target) != nil || os.Symlink(target, file) != nil {
					t.Fatal("DOUBLE_SYMLINK_FAILED")
				}
			case "input_hardlink":
				if os.Link(file, file+".alias") != nil {
					t.Fatal("DOUBLE_HARDLINK_FAILED")
				}
			case "unknown_field", "duplicate_field", "boolean_port", "null_tools":
				raw, e := os.ReadFile(file)
				if e != nil {
					t.Fatal("DOUBLE_READ_FAILED")
				}
				switch name {
				case "unknown_field":
					raw = append([]byte(`{"unknown":true,`), raw[1:]...)
				case "duplicate_field":
					raw = append([]byte(`{"format":"duplicate",`), raw[1:]...)
				case "boolean_port":
					raw = bytes.ReplaceAll(raw, []byte(`"port":15432`), []byte(`"port":false`))
				case "null_tools":
					var object map[string]any
					if json.Unmarshal(raw, &object) != nil {
						t.Fatal("DOUBLE_JSON_FAILED")
					}
					object["tools"] = nil
					raw, _ = json.Marshal(object)
				}
				if os.WriteFile(file, raw, 0600) != nil {
					t.Fatal("DOUBLE_WRITE_FAILED")
				}
			}
			if _, e := loadNativePostgresInput(file); e == nil {
				t.Fatal("UNSAFE_METADATA_DOUBLE_ACCEPTED")
			}
		})
	}
}

func TestNativePostgresCommandReferencesDoNotExposeCredentials(t *testing.T) {
	file, _ := nativeMetadataDouble(t)
	input, e := loadNativePostgresInput(file)
	if e != nil {
		t.Fatal("VALID_METADATA_DOUBLE_REFUSED")
	}
	t.Setenv("PGPASSWORD", "explicit-ambient-credential-double")
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGHOST", "192.0.2.1")
	t.Setenv("DOCKER_HOST", "tcp://192.0.2.1:1234")
	f := PostgresFixture{Database: input.Database, User: input.User, Schema: input.SourceSchema, native: &input}
	command := f.nativeCommand(t, context.Background(), "pg_dump", f.Database, "-Fc", "--schema", f.Schema)
	// Construct the fixed command only. Explicit fake executable bytes never run.
	for _, arg := range command.Args {
		if strings.Contains(arg, "credential") || strings.Contains(arg, "password") && arg != "--no-password" {
			t.Fatal("CREDENTIAL_IN_CLIENT_ARGV")
		}
	}
	environment := map[string]string{}
	for _, value := range command.Env {
		key, value, _ := strings.Cut(value, "=")
		environment[key] = value
	}
	if environment["PGSSLMODE"] != "verify-full" || environment["PGPASSFILE"] != input.PasswordFile || environment["PGSSLROOTCERT"] != input.CAFile || environment["PGPASSWORD"] != "" || environment["PGHOST"] != "" || environment["DOCKER_HOST"] != "" {
		t.Fatal("AMBIENT_NATIVE_CLIENT_AUTHORITY_LEAKED")
	}
	if !f.ownedSchema() || (PostgresFixture{Schema: input.SourceSchema}).ownedSchema() || !(PostgresFixture{Schema: "m01_" + strings.Repeat("f", 32)}).ownedSchema() {
		t.Fatal("NATIVE_AND_DOCKER_SCHEMA_AUTHORITIES_MIXED")
	}
	changed := f
	changed.Database = "foreign_test"
	if changed.ownedSchema() {
		t.Fatal("NATIVE_SOURCE_DATABASE_OVERRIDE_ACCEPTED")
	}
	changed = f
	changed.User = "foreign_user"
	if changed.ownedSchema() {
		t.Fatal("NATIVE_SOURCE_USER_OVERRIDE_ACCEPTED")
	}
}

func TestNativePostgresRestartHandshakeWithExplicitOwnerDouble(t *testing.T) {
	file, _ := nativeMetadataDouble(t)
	input, e := loadNativePostgresInput(file)
	if e != nil {
		t.Fatal("VALID_METADATA_DOUBLE_REFUSED")
	}
	f := PostgresFixture{Database: input.Database, User: input.User, Schema: input.SourceSchema, native: &input}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("EXPLICIT_RESTART_OWNER_DOUBLE_NOT_JOINED")
		}
	})
	go func() {
		defer close(done)
		for {
			raw, e := os.ReadFile(input.RestartRequest)
			if e == nil {
				var request map[string]string
				if json.Unmarshal(raw, &request) != nil || len(request) != 4 || request["format"] != "projectone.identity.native-restart-request.v1" || request["nonce"] != input.Nonce || request["source_schema"] != input.SourceSchema || request["system_identifier"] != input.SystemIdentifier {
					finished <- context.Canceled
					return
				}
				// Explicit acknowledgement double only: no postmaster exists or is
				// signaled. Fixed PIDs are fictitious protocol data, never authority.
				ack, _ := json.Marshal(map[string]any{"format": "projectone.identity.native-restart-ack.v1", "nonce": input.Nonce, "source_schema": input.SourceSchema, "system_identifier": input.SystemIdentifier, "previous_pid": 101, "new_pid": 102, "status": "restarted_owned_native_postgres"})
				pending := input.RestartAck + ".pending"
				if e = os.WriteFile(pending, ack, 0600); e == nil {
					e = os.Rename(pending, input.RestartAck)
				}
				finished <- e
				return
			}
			if !os.IsNotExist(e) {
				finished <- e
				return
			}
			select {
			case <-ctx.Done():
				finished <- ctx.Err()
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	f.RestartNative(t, ctx)
	if e := <-finished; e != nil {
		t.Fatal("EXPLICIT_RESTART_OWNER_DOUBLE_FAILED")
	}
	if _, e := os.Lstat(input.RestartRequest); e != nil {
		t.Fatal("DURABLE_RESTART_REQUEST_MISSING")
	}
}
