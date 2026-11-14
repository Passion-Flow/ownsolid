package install

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestEncryptedArchiveRefusesIncompleteAndExtraData(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	db := filepath.Join(source, backupDatabaseName)
	fixture := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(fixture, []byte("database fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := copyPrivateFile(fixture, db)
	if err != nil {
		t.Fatal(err)
	}
	file.Path = backupDatabaseName
	manifest := backupManifest{FormatVersion: backupFormatVersion, SchemaVersion: 1, Database: file, Tables: []tableFingerprint{{Name: "test"}}}
	if err := writeManifest(filepath.Join(source, backupManifestName), manifest); err != nil {
		t.Fatal(err)
	}
	var encrypted bytes.Buffer
	if err := writeEncryptedArchive(t.Context(), &encrypted, source, identity.Recipient()); err != nil {
		t.Fatal(err)
	}
	reader, err := age.Decrypt(bytes.NewReader(encrypted.Bytes()), identity)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		input []byte
		ok    bool
	}{
		{"complete", plaintext, true},
		{"missing tar end", plaintext[:len(plaintext)-tarBlockSize], false},
		{"extra authenticated bytes", append(bytes.Clone(plaintext), 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readEncryptedArchive(t.Context(), bytes.NewReader(tc.input), t.TempDir())
			if tc.ok && (err != nil || got.Database != file) || !tc.ok && !errors.Is(err, errArchiveInvalid) {
				t.Fatalf("archive admission: %v", err)
			}
		})
	}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := publishEncryptedBackup(ctx, root, dir, "test.tar.age", source, identity.Recipient()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled archive publication: %v", err)
	}
	if entries, err := dir.ReadDir(-1); err != nil || len(entries) != 0 {
		t.Fatalf("cancelled archive left destination files: %v, %v", entries, err)
	}
}

func assertEncryptedBackupIsolatedDocker(t *testing.T, installer, config string, run func(string, ...string) (string, error)) {
	t.Helper()
	c, err := LoadOperational(config)
	if err != nil {
		t.Fatal(err)
	}
	o := newOperation(c, ExecRunner{}, io.Discard)
	query := func(sql string) string {
		t.Helper()
		out, err := o.query(t.Context(), databaseName, sql)
		if err != nil {
			t.Fatalf("PostgreSQL query: %v", err)
		}
		return out
	}
	command := func(t *testing.T, want string, fails bool, args ...string) {
		t.Helper()
		out, err := run(installer, append(args, "--config", config)...)
		t.Logf("%s:\n%s", args[0], out)
		if (err != nil) != fails || !strings.Contains(out, want) {
			t.Fatalf("want %s (failure=%t), got %v", want, fails, err)
		}
	}
	write := func(path string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) []byte {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	absent := func(path string) {
		t.Helper()
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected path %s: %v", path, err)
		}
	}

	archiveDir, keyDir := t.TempDir(), t.TempDir()
	if err := os.Chmod(archiveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	recipient := filepath.Join(keyDir, "recipient.txt")
	key := filepath.Join(keyDir, "recovery.txt")
	wrong := filepath.Join(keyDir, "different.txt")
	write(recipient, []byte("# backup recipient\n"+identity.Recipient().String()+"\n"))
	archive := filepath.Join(archiveDir, "verified.tar.age")
	retried := filepath.Join(archiveDir, "retried.tar.age")
	staging := filepath.Join(c.InstallRoot, encryptedWorkDirectory, "data")
	const tenant = "SELECT name FROM tenants WHERE id=(SELECT tenant_id FROM installation_state WHERE id=1)"
	const oid = "SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()"
	query("UPDATE tenants SET name='Encrypted recovery point' WHERE id=(SELECT tenant_id FROM installation_state WHERE id=1)")
	credential := read(c.CredentialPath())
	command(t, "BACKUP_COMPLETE", false, "backup", "--output", archive, "--recipient-file", recipient)
	absent(staging)
	ciphertext := read(archive)
	if !bytes.HasPrefix(ciphertext, []byte("age-encryption.org/v1\n")) || bytes.Contains(ciphertext, []byte("Encrypted recovery point")) {
		t.Fatal("published archive is not an age envelope")
	}
	if info, err := os.Stat(archive); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("archive permissions: %v", err)
	}
	entries, err := os.ReadDir(archiveDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(archive) {
		t.Fatalf("backup repository held extra files: %v, %v", entries, err)
	}
	t.Run("interrupted backup can retry without replacing an existing archive", func(t *testing.T) {
		interruptInstallerDocker(t, "before-dump")
		out, err := run(installer, "backup", "--config", config, "--output", retried, "--recipient-file", recipient)
		if err == nil || !strings.Contains(err.Error(), "killed") {
			t.Fatalf("backup did not receive SIGKILL: %v: %s", err, out)
		}
		if _, err := os.Stat(staging); err != nil {
			t.Fatalf("SIGKILL did not leave the staging fixture: %v", err)
		}
		absent(retried)
		command(t, "BACKUP_COMPLETE", false, "backup", "--output", retried, "--recipient-file", recipient)
		absent(staging)
		command(t, "INSTALL_ENCRYPTED_BACKUP_EXISTS", true, "backup", "--output", archive, "--recipient-file", recipient)
		if !bytes.Equal(ciphertext, read(archive)) {
			t.Fatal("previous recovery point changed after interruption or retry")
		}
	})
	if t.Failed() {
		return
	}

	query("UPDATE tenants SET name='Live after backup' WHERE id=(SELECT tenant_id FROM installation_state WHERE id=1)")
	liveOID := query(oid)
	write(key, []byte(identity.String()+"\n"))
	write(wrong, []byte(other.String()+"\n"))
	command(t, "INSTALL_ENCRYPTED_BACKUP_WRONG_IDENTITY", true, "restore", "--from", archive, "--identity-file", wrong)
	for _, failure := range []struct {
		name string
		data []byte
	}{{"tampered", bytes.Clone(ciphertext)}, {"truncated", ciphertext[:len(ciphertext)-1]}} {
		if failure.name == "tampered" {
			failure.data[len(failure.data)-1] ^= 1
		}
		bad := filepath.Join(archiveDir, failure.name+".tar.age")
		write(bad, failure.data)
		command(t, "INSTALL_ENCRYPTED_BACKUP_INVALID", true, "restore", "--from", bad, "--identity-file", key)
		absent(staging)
	}
	if query(oid) != liveOID || query(tenant) != "Live after backup" {
		t.Fatal("an invalid restore changed the serving database")
	}

	t.Run("interrupted encrypted restore resumes with the checked database", func(t *testing.T) {
		interruptInstallerDocker(t, "after-swap")
		out, err := run(installer, "restore", "--config", config, "--from", archive, "--identity-file", key)
		if err == nil || !strings.Contains(err.Error(), "killed") {
			t.Fatalf("restore did not receive SIGKILL: %v: %s", err, out)
		}
		command(t, "RESTORE_COMPLETE", false, "recover-restore")
	})
	if t.Failed() {
		return
	}
	if query(oid) == liveOID || query(tenant) != "Encrypted recovery point" {
		t.Fatal("encrypted restore did not activate the original recovery point")
	}
	query("UPDATE tenants SET name='Live after interrupted recovery' WHERE id=(SELECT tenant_id FROM installation_state WHERE id=1)")
	command(t, "RESTORE_COMPLETE", false, "restore", "--from", retried, "--identity-file", key)
	absent(staging)
	if query(tenant) != "Encrypted recovery point" {
		t.Fatal("retried archive did not recover the backed-up tenant")
	}
	plain := t.TempDir()
	command(t, "BACKUP_COMPLETE", false, "backup", "--output", plain)
	entries, err = os.ReadDir(plain)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ordinary backup publication: %v, %v", entries, err)
	}
	command(t, "RESTORE_COMPLETE", false, "restore", "--from", filepath.Join(plain, entries[0].Name()))
	command(t, c.Origins.Portal+": ready", false, "status")
	if !bytes.Equal(credential, read(c.CredentialPath())) || !bytes.Equal(ciphertext, read(archive)) {
		t.Fatal("recovery changed the administrator credential or the encrypted archive")
	}
}
