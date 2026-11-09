package install

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const objectBackupPayload = "verified object bytes\n"

type objectCheckpointFixture struct {
	stage     string
	local     backupManifest
	pin       objectBackupPin
	copy      objectCopyConfirmation
	inventory objectBackupInventory
}

func objectFixtureWrite(t *testing.T, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(name, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func objectFixtureJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newObjectCheckpointFixture(t *testing.T) objectCheckpointFixture {
	t.Helper()
	stage, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{objectBackupCopyDir, "files/config", "files/secrets"} {
		if err := os.MkdirAll(filepath.Join(stage, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	dump, keys := []byte("PGDMP unit-test logical bytes"), []byte("fixture configuration key")
	objectFixtureWrite(t, filepath.Join(stage, backupDatabaseName), dump)
	var files []backupFile
	for _, name := range []string{"config/auth-hmac", "config/mail-keyring.json", "secrets/postgres-password"} {
		objectFixtureWrite(t, filepath.Join(stage, backupFilesName, name), keys)
		files = append(files, backupFile{Path: name, SHA256: wire.Digest(keys), Bytes: int64(len(keys)), Mode: 0o600})
	}
	digest := wire.Digest([]byte(objectBackupPayload))
	objectFixtureWrite(t, filepath.Join(stage, objectBackupCopyDir, digest), []byte(objectBackupPayload))

	scope := objectBackupScope{
		SiteSHA256: wire.Digest([]byte("verified site")), OperationID: "55555555-5555-4555-8555-555555555555",
		Cluster: 8219333477329704989, DatabaseOID: 20101,
		TenantID: "11111111-1111-4111-8111-111111111111", StoreID: "22222222-2222-4222-8222-222222222222",
		BackendSHA256: wire.Digest([]byte("verified backend")),
	}
	etag := `"<opaque>&"`
	ready := objectBackupObject{
		WorkspaceID: "33333333-3333-4333-8333-333333333333", ObjectID: "44444444-4444-4444-8444-444444444441",
		State: "ready", Revision: 3, SizeBytes: int64(len(objectBackupPayload)), SHA256: digest, ETag: &etag,
	}
	fixture := objectCheckpointFixture{
		stage: stage,
		local: backupManifest{
			FormatVersion: backupFormatVersion, CreatedAt: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC),
			ControlImage: "sha256:" + wire.Digest([]byte("control")), PostgresImage: "sha256:" + wire.Digest([]byte("postgres")),
			SchemaVersion: 57,
			Database:      backupFile{Path: backupDatabaseName, SHA256: wire.Digest(dump), Bytes: int64(len(dump)), Mode: 0o600},
			Tables:        []tableFingerprint{{Name: "artifact_objects", Rows: "1", Sum: "42"}},
			Files:         files,
		},
		pin: objectBackupPin{
			Scope: scope, FenceSHA256: wire.Digest([]byte("trusted durable fixture fence")),
			CopyID: "66666666-6666-4666-8666-666666666666", DumpSHA256: wire.Digest(dump),
		},
		inventory: objectBackupInventory{
			FormatVersion: 1, Database: databaseName, DatabaseOID: int64(scope.DatabaseOID), Schema: "public",
			TenantID: scope.TenantID, StoreID: scope.StoreID, BackendSHA256: scope.BackendSHA256,
			Objects: []objectBackupObject{ready},
		},
	}
	fixture.writeInventory(t)
	return fixture
}

func (f *objectCheckpointFixture) writeInventory(t *testing.T) {
	t.Helper()
	f.inventory.InventorySHA256 = ""
	f.inventory.InventorySHA256 = wire.Digest(objectFixtureJSON(t, f.inventory))
	f.pin.InventorySHA256 = f.inventory.InventorySHA256
	f.copy = objectCopyConfirmation{Scope: f.pin.Scope, InventorySHA256: f.pin.InventorySHA256, ID: f.pin.CopyID}
	objectFixtureWrite(t, filepath.Join(f.stage, objectInventoryName), objectFixtureJSON(t, f.inventory))
}

func (f objectCheckpointFixture) seal(t *testing.T) string {
	t.Helper()
	digest, err := sealObjectBackup(t.Context(), f.stage, f.local, f.pin, f.copy)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestObjectBackupUsesTheControlInventoryDigest(t *testing.T) {
	f := newObjectCheckpointFixture(t)
	// Generated from the actual BackupInventory and BackupObjectVersion types
	// at 279f3031, with the same literal values and an ETag that Go JSON escapes.
	const want = "5ceecf9e044bcea9561014becf23118f9e2ff348f5f991b4215c1b92970e19c3"
	if f.inventory.InventorySHA256 != want {
		t.Fatalf("Control v1 inventory digest drifted: %s", f.inventory.InventorySHA256)
	}
	duplicate := f.inventory.Objects[0]
	duplicate.ObjectID = "44444444-4444-4444-8444-444444444442"
	tombstone := duplicate
	tombstone.ObjectID = "44444444-4444-4444-8444-444444444443"
	tombstone.State, tombstone.ETag = "deleted", nil
	tombstone.SHA256 = wire.Digest([]byte("deleted object"))
	f.inventory.Objects = append(f.inventory.Objects, duplicate, tombstone)
	f.writeInventory(t)

	expected := wire.Digest(objectFixtureJSON(t, objectBackupCheckpoint{objectBackupFormatVersion, f.pin, f.local}))
	if err := verifyObjectBackup(t.Context(), f.stage, f.pin, expected); !errors.Is(err, errObjectBackupRecovery) {
		t.Fatalf("unsealed stage returned %v", err)
	}
	if _, err := readBackup(f.stage); err == nil {
		t.Fatal("ordinary restore accepted an unsealed object checkpoint")
	}
	if seal := f.seal(t); seal != expected {
		t.Fatalf("unexpected sealed manifest: %s", seal)
	}
	if err := verifyObjectBackup(t.Context(), f.stage, f.pin, expected); err != nil {
		t.Fatal(err)
	}
	if f.seal(t) != expected {
		t.Fatal("exact retry changed the seal")
	}
	if _, err := readBackup(f.stage); err == nil {
		t.Fatal("ordinary restore accepted an object checkpoint")
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEncryptedArchive(t.Context(), io.Discard, f.stage, identity.Recipient()); err == nil {
		t.Fatal("the existing age path accepted an object checkpoint as a local backup")
	}
}

func TestObjectBackupRefusesUnprovenOrChangedInputs(t *testing.T) {
	remove := func(t *testing.T, name string) {
		t.Helper()
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		want error
		edit func(*testing.T, *objectCheckpointFixture)
	}{
		{"unconfirmed copy", errObjectBackupScope, func(_ *testing.T, f *objectCheckpointFixture) { f.copy = objectCopyConfirmation{} }},
		{"different provider copy", errObjectBackupScope, func(_ *testing.T, f *objectCheckpointFixture) { f.copy.ID = f.pin.Scope.OperationID }},
		{"different provider inventory", errObjectBackupScope, func(_ *testing.T, f *objectCheckpointFixture) { f.copy.InventorySHA256 = f.pin.FenceSHA256 }},
		{"different provider cluster", errObjectBackupScope, func(_ *testing.T, f *objectCheckpointFixture) { f.copy.Scope.Cluster++ }},
		{"different provider tenant", errObjectBackupScope, func(_ *testing.T, f *objectCheckpointFixture) { f.copy.Scope.TenantID = f.pin.Scope.StoreID }},
		{"missing durable fence", errObjectBackupScope, func(_ *testing.T, f *objectCheckpointFixture) { f.pin.FenceSHA256 = "" }},
		{"different inventory scope", errObjectBackupScope, func(t *testing.T, f *objectCheckpointFixture) {
			f.inventory.DatabaseOID++
			f.writeInventory(t)
		}},
		{"missing inventory", errObjectBackupInventory, func(t *testing.T, f *objectCheckpointFixture) {
			remove(t, filepath.Join(f.stage, objectInventoryName))
		}},
		{"modified inventory with recomputed hash", errObjectBackupInventory, func(t *testing.T, f *objectCheckpointFixture) {
			oldPin, oldCopy := f.pin, f.copy
			f.inventory.Objects[0].Revision++
			f.writeInventory(t)
			f.pin, f.copy = oldPin, oldCopy
		}},
		{"duplicate object row", errObjectBackupInventory, func(t *testing.T, f *objectCheckpointFixture) {
			f.inventory.Objects = append(f.inventory.Objects, f.inventory.Objects[0])
			f.writeInventory(t)
		}},
		{"unsettled object", errObjectBackupInventory, func(t *testing.T, f *objectCheckpointFixture) {
			f.inventory.Objects[0].State = "uploading"
			f.writeInventory(t)
		}},
		{"missing object bytes", errObjectBackupCopy, func(t *testing.T, f *objectCheckpointFixture) {
			remove(t, filepath.Join(f.stage, objectBackupCopyDir, f.inventory.Objects[0].SHA256))
		}},
		{"extra object bytes", errObjectBackupCopy, func(t *testing.T, f *objectCheckpointFixture) {
			data := []byte("unlisted payload")
			objectFixtureWrite(t, filepath.Join(f.stage, objectBackupCopyDir, wire.Digest(data)), data)
		}},
		{"changed object bytes", errObjectBackupCopy, func(t *testing.T, f *objectCheckpointFixture) {
			name := filepath.Join(f.stage, objectBackupCopyDir, f.inventory.Objects[0].SHA256)
			objectFixtureWrite(t, name, []byte(strings.Repeat("x", len(objectBackupPayload))))
		}},
		{"linked object bytes", errObjectBackupCopy, func(t *testing.T, f *objectCheckpointFixture) {
			name := filepath.Join(f.stage, objectBackupCopyDir, f.inventory.Objects[0].SHA256)
			linked := filepath.Join(f.stage, "linked")
			if err := os.Link(name, linked); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlinked object directory", errObjectBackupCopy, func(t *testing.T, f *objectCheckpointFixture) {
			dir := filepath.Join(f.stage, objectBackupCopyDir)
			moved := dir + "-old"
			if err := os.Rename(dir, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, dir); err != nil {
				t.Fatal(err)
			}
		}},
		{"changed dump", errObjectBackupDump, func(t *testing.T, f *objectCheckpointFixture) {
			objectFixtureWrite(t, filepath.Join(f.stage, backupDatabaseName), []byte("PGDMP changed dump"))
		}},
		{"different dump pin", errObjectBackupDump, func(_ *testing.T, f *objectCheckpointFixture) {
			f.pin.DumpSHA256 = f.pin.FenceSHA256
		}},
		{"missing required key entry", errObjectBackupFiles, func(_ *testing.T, f *objectCheckpointFixture) {
			f.local.Files = f.local.Files[1:]
		}},
		{"symlinked key directory", errObjectBackupFiles, func(t *testing.T, f *objectCheckpointFixture) {
			dir := filepath.Join(f.stage, "files/config")
			moved := dir + "-old"
			if err := os.Rename(dir, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, dir); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newObjectCheckpointFixture(t)
			tc.edit(t, &f)
			digest, err := sealObjectBackup(t.Context(), f.stage, f.local, f.pin, f.copy)
			if !errors.Is(err, tc.want) || digest != "" {
				t.Fatalf("want %v, got %v and digest %s", tc.want, err, digest)
			}
			if _, err := os.Lstat(filepath.Join(f.stage, backupManifestName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed checkpoint left a complete manifest")
			}
		})
	}
}

func TestObjectBackupRecoveryRequiresTheDurableExactRecord(t *testing.T) {
	f := newObjectCheckpointFixture(t)
	digest := f.seal(t)
	for _, tc := range []struct {
		name string
		edit func(*objectBackupPin)
	}{
		{"site", func(pin *objectBackupPin) { pin.Scope.SiteSHA256 = pin.FenceSHA256 }},
		{"operation", func(pin *objectBackupPin) { pin.Scope.OperationID = pin.CopyID }},
		{"cluster", func(pin *objectBackupPin) { pin.Scope.Cluster++ }},
		{"database OID", func(pin *objectBackupPin) { pin.Scope.DatabaseOID++ }},
		{"tenant", func(pin *objectBackupPin) { pin.Scope.TenantID = pin.Scope.StoreID }},
		{"store", func(pin *objectBackupPin) { pin.Scope.StoreID = pin.Scope.TenantID }},
		{"backend", func(pin *objectBackupPin) { pin.Scope.BackendSHA256 = pin.FenceSHA256 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pin := f.pin
			tc.edit(&pin)
			if err := verifyObjectBackup(t.Context(), f.stage, pin, digest); !errors.Is(err, errObjectBackupScope) {
				t.Fatalf("foreign scope returned %v", err)
			}
		})
	}
	if err := verifyObjectBackup(t.Context(), f.stage, f.pin, ""); !errors.Is(err, errObjectBackupRecovery) {
		t.Fatalf("unrecorded checkpoint returned %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := verifyObjectBackup(ctx, f.stage, f.pin, digest); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled checkpoint returned %v", err)
	}
	name := filepath.Join(f.stage, backupManifestName)
	original, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	changed := append(append([]byte{}, original...), '\n')
	objectFixtureWrite(t, name, changed)
	if err := verifyObjectBackup(t.Context(), f.stage, f.pin, digest); !errors.Is(err, errObjectBackupManifest) {
		t.Fatalf("changed manifest returned %v", err)
	}
	if _, err := sealObjectBackup(t.Context(), f.stage, f.local, f.pin, f.copy); !errors.Is(err, errObjectBackupManifest) {
		t.Fatalf("retry overwrote a different manifest: %v", err)
	}
	current, err := os.ReadFile(name)
	if err != nil || string(current) != string(changed) {
		t.Fatal("retry did not preserve the existing manifest")
	}
	objectFixtureWrite(t, name, original)
	objectFixtureWrite(t, filepath.Join(f.stage, objectBackupCopyDir, f.inventory.Objects[0].SHA256), []byte("broken copy"))
	if err := verifyObjectBackup(t.Context(), f.stage, f.pin, digest); !errors.Is(err, errObjectBackupCopy) {
		t.Fatalf("changed sealed copy returned %v", err)
	}
}

type interruptedObjectBackup struct {
	Stage string
	Local backupManifest
	Pin   objectBackupPin
	Copy  objectCopyConfirmation
}

func TestObjectBackupInterruptedCoordinator(t *testing.T) {
	f := newObjectCheckpointFixture(t)
	authority := filepath.Join(t.TempDir(), "authority.json")
	objectFixtureWrite(t, authority, objectFixtureJSON(t, interruptedObjectBackup{f.stage, f.local, f.pin, f.copy}))
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestObjectBackupCoordinatorHelper$")
	cmd.Env = append(os.Environ(), "OWNSOLID_OBJECT_BACKUP_TEST_AUTHORITY="+authority)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 73 {
		t.Fatalf("child did not exit at the checkpoint boundary: %v: %s", err, output)
	}
	if err := verifyObjectBackup(t.Context(), f.stage, f.pin, ""); !errors.Is(err, errObjectBackupRecovery) {
		t.Fatalf("child's interrupted handoff admitted restore: %v", err)
	}
	digest := f.seal(t)
	if err := verifyObjectBackup(t.Context(), f.stage, f.pin, digest); err != nil {
		t.Fatalf("exact retry after trusted reconciliation: %v", err)
	}
}

func TestObjectBackupCoordinatorHelper(t *testing.T) {
	authority := os.Getenv("OWNSOLID_OBJECT_BACKUP_TEST_AUTHORITY")
	if authority == "" {
		t.Skip("runs in the interrupted coordinator subprocess")
	}
	raw, err := os.ReadFile(authority)
	var f interruptedObjectBackup
	if err != nil || json.Unmarshal(raw, &f) != nil {
		t.Fatal("cannot read the trusted fixture authority")
	}
	if _, err := sealObjectBackup(t.Context(), f.Stage, f.Local, f.Pin, f.Copy); err != nil {
		t.Fatal(err)
	}
	// Simulate loss after the durable manifest but before acknowledging its
	// digest in the coordinator's independent record.
	os.Exit(73)
}

func TestObjectBackupLocalRecoveryPreservesUnknownFenceRecord(t *testing.T) {
	f := newObjectCheckpointFixture(t)
	c := defaults()
	c.InstallRoot = filepath.Join(f.stage, "installation")
	if err := os.Mkdir(c.InstallRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(c.InstallRoot, coordinatedBackupIntentName)
	raw := objectFixtureJSON(t, struct {
		Version int             `json:"version"`
		Binding objectBackupPin `json:"object_recovery"`
	}{2, f.pin})
	objectFixtureWrite(t, record, raw)
	if unlock, err := lockOperation(c); !errors.Is(err, errBackupPending) {
		if unlock != nil {
			unlock()
		}
		t.Fatalf("ordinary operation ignored the pending record: %v", err)
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return
	}
	t.Setenv("DOCKER_HOST", "unix:///run/docker.sock")
	runner := &fakeDatabase{}
	if err := RecoverCoordinatedBackup(t.Context(), c, runner, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "INSTALL_BACKUP_RECOVERY_INVALID") || len(runner.commands) != 0 {
		t.Fatalf("legacy recovery admitted an unknown fence record: %v; commands %v", err, runner.commands)
	}
	after, err := os.ReadFile(record)
	if err != nil || string(after) != string(raw) {
		t.Fatal("legacy recovery changed the unknown fence record")
	}
}

func TestObjectBackupNativeDump(t *testing.T) {
	path := os.Getenv("OWNSOLID_OBJECT_BACKUP_TEST_DUMP")
	if path == "" {
		t.Skip("requires a real PostgreSQL custom-format dump in the isolated Linux fixture")
	}
	f := newObjectCheckpointFixture(t)
	f.pin.Scope.Cluster, _ = strconv.ParseInt(os.Getenv("OWNSOLID_OBJECT_BACKUP_TEST_CLUSTER"), 10, 64)
	oid, _ := strconv.ParseUint(os.Getenv("OWNSOLID_OBJECT_BACKUP_TEST_OID"), 10, 32)
	f.pin.Scope.DatabaseOID, f.inventory.DatabaseOID = uint32(oid), int64(oid)
	f.local.Tables[0].Rows = os.Getenv("OWNSOLID_OBJECT_BACKUP_TEST_ROWS")
	f.local.Tables[0].Sum = os.Getenv("OWNSOLID_OBJECT_BACKUP_TEST_SUM")
	if f.local.Tables[0].Rows != "1" || f.local.Tables[0].Sum == "" {
		t.Fatal("the native PostgreSQL scratch restore must supply the verified row fingerprint")
	}
	target := filepath.Join(f.stage, backupDatabaseName)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	dump, err := copyPrivateFile(path, target)
	if err != nil {
		t.Fatal(err)
	}
	dump.Path = backupDatabaseName
	f.local.Database, f.pin.DumpSHA256 = dump, dump.SHA256
	f.writeInventory(t)
	seal := f.seal(t)
	if err := verifyObjectBackup(t.Context(), f.stage, f.pin, seal); err != nil {
		t.Fatal(err)
	}
	t.Logf("native PostgreSQL logical dump and immutable object fixture rechecked, dump_sha256=%s", dump.SHA256)
}
