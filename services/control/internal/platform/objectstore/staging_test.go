package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func stageConfiguration(t *testing.T) (config.ObjectStorageConfiguration, string) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(root, 0700) != nil {
		t.Fatal("private root", e)
	}
	info, e := os.Stat(root)
	if e != nil {
		t.Fatal("staging root identity", e)
	}
	observation, e := json.Marshal(struct {
		Root        string `json:"root"`
		RuntimeTemp string `json:"runtime_temp"`
		GoTemp      string `json:"gotmpdir"`
		Stat        any    `json:"stat"`
	}{root, os.TempDir(), os.Getenv("GOTMPDIR"), info.Sys()})
	if e != nil {
		t.Fatal("staging storage observation", e)
	}
	t.Logf("STAGING_STORAGE_OBSERVATION %s", observation)
	c := config.ObjectStorageConfiguration{Enabled: true, Backend: "seaweedfs_s3", Endpoint: "https://store.example.invalid", Transport: "https", Region: "private", Bucket: "synthetic-artifact-fixture", RefusalBucket: "synthetic-refusal-fixture", RefusalAccessKeyFile: "/protected/refusal-access", RefusalSecretKeyFile: "/protected/refusal-secret", AccessKeyFile: "/protected/access", SecretKeyFile: "/protected/secret", DialAddresses: []string{"127.0.0.1"}, StagingRoot: root, PartBytes: 5 << 20, MaximumObjectBytes: 10 << 20, MinimumFreeBytes: 4096, OperationSeconds: 5, TransferSeconds: 10, UploadSeconds: 60, ObjectLifetimeSeconds: 3600, MaximumGrantSeconds: 300, MaximumConcurrentTransfers: 1, MaximumListPages: 1, WorkerPollSeconds: 1, Management: protocolManagementFixtures()}
	return c, uuid.NewString()
}
func TestObjectStagingNamespaceOwnershipDigestAndDescriptor(t *testing.T) {
	c, tenant := stageConfiguration(t)
	if e := InitializeStage(c, tenant); e != nil {
		t.Fatal(e)
	}
	if e := InitializeStage(c, tenant); e == nil {
		t.Fatal("populated namespace adopted")
	}
	store, e := OpenStage(c, tenant)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	if _, e = OpenStage(c, tenant); e == nil {
		t.Fatal("second namespace writer accepted")
	}
	source := []byte("Explicit synthetic private staging bytes \u4e16\u754c")
	sum := sha256.Sum256(source)
	digest := hex.EncodeToString(sum[:])
	id := uuid.NewString()
	v, e := store.Write(context.Background(), id, int64(len(source)), digest, bytes.NewReader(source))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.OpenVerified(context.Background(), id, int64(len(source)), digest); e == nil {
		t.Fatal("active descriptor ownership ignored")
	}
	v.File.Close()
	v, e = store.OpenVerified(context.Background(), id, int64(len(source)), digest)
	if e != nil {
		t.Fatal(e)
	}
	f, e := store.Detach(v)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	out, e := io.ReadAll(f)
	if e != nil || !bytes.Equal(out, source) {
		t.Fatal("detached descriptor changed", e)
	}
	if _, e = os.Lstat(filepath.Join(c.StagingRoot, "files", id)); !os.IsNotExist(e) {
		t.Fatal("download path retained")
	}
	changed := c
	changed.Bucket = "different-private-store"
	if _, e = OpenStage(changed, tenant); e == nil {
		t.Fatal("namespace reused in another backend")
	}
}
func TestObjectStagingCorruptionRefusalAndRetainedFailedWrite(t *testing.T) {
	c, tenant := stageConfiguration(t)
	if e := InitializeStage(c, tenant); e != nil {
		t.Fatal(e)
	}
	store, e := OpenStage(c, tenant)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	source := []byte("Synthetic immutable bytes")
	hash := sha256.Sum256(source)
	digest := hex.EncodeToString(hash[:])
	v, e := store.Write(context.Background(), uuid.NewString(), int64(len(source)), digest, bytes.NewReader(source))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = v.File.WriteAt([]byte("X"), 0); e != nil {
		t.Fatal(e)
	}
	if e = v.File.Truncate(int64(len(source) - 1)); e != nil {
		t.Fatal(e)
	}
	if e = store.Verify(v); e == nil {
		t.Fatal("changed descriptor accepted")
	}
	v.File.Close()
	failed := uuid.NewString()
	if _, e = store.Write(context.Background(), failed, int64(len(source)), digest, bytes.NewReader([]byte("wrong"))); e == nil {
		t.Fatal("truncated source accepted")
	}
	if _, e = os.Lstat(filepath.Join(c.StagingRoot, "files", failed)); e != nil {
		t.Fatal("failed private attempt lost before explicit cleanup", e)
	}
	store.minimumFree = 1 << 62
	if _, e = store.Write(context.Background(), uuid.NewString(), 1, digest, bytes.NewReader([]byte("x"))); e == nil {
		t.Fatal("disk reserve ignored")
	}
}

func TestStagingCleanupRequiresJournalInodeAndInactiveConsumer(t *testing.T) {
	c, tenant := stageConfiguration(t)
	if e := InitializeStage(c, tenant); e != nil {
		t.Fatal(e)
	}
	store, e := OpenStage(c, tenant)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	source := []byte("Synthetic journal ownership")
	sum := sha256.Sum256(source)
	digest := hex.EncodeToString(sum[:])
	id := uuid.NewString()
	staged, e := store.Write(context.Background(), id, int64(len(source)), digest, bytes.NewReader(source))
	if e != nil {
		t.Fatal(e)
	}
	records, e := store.Records(2)
	if e != nil || len(records) != 1 || records[0].ID != id {
		t.Fatal("missing durable record", e)
	}
	if e = store.RemoveAttempt(records[0]); e == nil {
		t.Fatal("active consumer removed")
	}
	staged.File.Close()
	wrong := records[0]
	wrong.Inode++
	if e = store.RemoveAttempt(wrong); e == nil {
		t.Fatal("foreign inode accepted")
	}
	if e = store.RemoveAttempt(records[0]); e != nil {
		t.Fatal(e)
	}
	if records, e = store.Records(2); e != nil || len(records) != 0 {
		t.Fatal("record retained after confirmed cleanup", e)
	}
	foreign := uuid.NewString()
	if e = os.WriteFile(filepath.Join(c.StagingRoot, "files", foreign), source, 0600); e != nil {
		t.Fatal(e)
	}
	if e = store.RemoveAttempt(StageRecord{ID: foreign, Namespace: store.Namespace(), Bytes: int64(len(source)), SHA256: digest}); e == nil {
		t.Fatal("unjournaled file adopted")
	}
	if _, e = os.Stat(filepath.Join(c.StagingRoot, "files", foreign)); e != nil {
		t.Fatal("foreign file removed")
	}
}
