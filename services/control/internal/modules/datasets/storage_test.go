package datasets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
)

func initializedStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "explicit-empty")
	if os.Mkdir(path, 0755) != nil {
		t.Fatal("mkdir")
	}
	tenant := uuid.NewString()
	if e := InitializeStore(path, tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	s, e := OpenStore(path, tenant, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, path, tenant
}
func TestDatasetStoreInitializationOwnershipNamespaceAndExclusiveLock(t *testing.T) {
	s, path, tenant := initializedStore(t)
	if _, e := OpenStore(path, tenant, 4096); e == nil || e.Error() != "DATASET_STORE_IN_USE" {
		t.Fatalf("second owner: %v", e)
	}
	if _, e := OpenStore(path, uuid.NewString(), 4096); e == nil || e.Error() != "DATASET_STORE_NAMESPACE_INVALID" {
		t.Fatalf("foreign tenant: %v", e)
	}
	if e := InitializeStore(path, tenant, os.Geteuid(), os.Getegid()); e == nil {
		t.Fatal("existing store adopted")
	}
	if e := InitializeStore(filepath.Join(path, "missing"), tenant, os.Geteuid(), os.Getegid()); e == nil {
		t.Fatal("missing mount silently created")
	}
	marker := filepath.Join(path, "preserve")
	os.WriteFile(marker, []byte("unrelated"), 0600)
	s.Close()
	if _, e := OpenStore(path, tenant, 4096); e == nil {
		t.Fatal("unknown root file ignored")
	}
	raw, _ := os.ReadFile(marker)
	if string(raw) != "unrelated" {
		t.Fatal("unknown file altered")
	}
}
func TestDatasetSourceImmutableReplayFailuresAndRestart(t *testing.T) {
	s, path, tenant := initializedStore(t)
	raw := []byte("synthetic source\n")
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	id := uuid.NewString()
	source, e := s.PutSource(context.Background(), id, uuid.NewString(), digest, int64(len(raw)), bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.PutSource(context.Background(), id, uuid.NewString(), digest, int64(len(raw)), bytes.NewReader(nil))
	if e != nil || replay != source {
		t.Fatal("immutable uncertain replay", e)
	}
	changed := sha256.Sum256([]byte("other"))
	if _, e = s.PutSource(context.Background(), id, uuid.NewString(), hex.EncodeToString(changed[:]), 5, bytes.NewReader([]byte("other"))); e == nil {
		t.Fatal("source replaced")
	}
	actual, _ := os.ReadFile(source)
	if !bytes.Equal(actual, raw) {
		t.Fatal("original changed")
	}
	namespace := s.Namespace()
	s.Close()
	reopened, e := OpenStore(path, tenant, 4096)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if reopened.Namespace() != namespace {
		t.Fatal("namespace lost")
	}
	if _, e = reopened.Source(id, digest, int64(len(raw))); e != nil {
		t.Fatal(e)
	}
	incompleteID := uuid.NewString()
	upload := uuid.NewString()
	if _, e = reopened.PutSource(context.Background(), incompleteID, upload, digest, int64(len(raw)), bytes.NewReader(raw[:3])); e == nil || e.Error() != "DATASET_UPLOAD_INCOMPLETE" {
		t.Fatalf("%v", e)
	}
	if _, e = os.Stat(filepath.Join(path, "objects", incompleteID, "upload-"+upload+".jsonl")); e != nil {
		t.Fatal("uncertain stage removed")
	}
	if _, e = reopened.Source(incompleteID, digest, int64(len(raw))); e == nil {
		t.Fatal("partial upload authoritative")
	}
}
func TestDatasetAttemptPrivateIdentityDigestNoOverwriteAndFreeSpace(t *testing.T) {
	s, path, _ := initializedStore(t)
	raw := []byte("{}\n")
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	job := uuid.NewString()
	attempt := uuid.NewString()
	scope, input, output, e := s.CreateAttempt(job, attempt, bytes.NewReader(raw), digest, int64(len(raw)), 4096)
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Dir(input) != scope || filepath.Dir(output) != scope {
		t.Fatal("scope")
	}
	for _, p := range []string{scope, output, input} {
		info, e := os.Stat(p)
		if e != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatal("not private", e)
		}
	}
	if _, _, _, e = s.CreateAttempt(job, attempt, bytes.NewReader(raw), digest, int64(len(raw)), 4096); e == nil {
		t.Fatal("attempt overwritten")
	}
	link := filepath.Join(path, "objects", uuid.NewString())
	if os.Symlink(filepath.Dir(path), link) != nil {
		t.Fatal("symlink")
	}
	if _, e = s.PutSource(context.Background(), filepath.Base(link), uuid.NewString(), digest, int64(len(raw)), bytes.NewReader(raw)); e == nil {
		t.Fatal("symlink namespace accepted")
	}
	s.minimumFree = 9007199254740991
	if _, _, _, e = s.CreateAttempt(uuid.NewString(), uuid.NewString(), bytes.NewReader(raw), digest, int64(len(raw)), 4096); e == nil || e.Error() != "DATASET_STORAGE_FULL" {
		t.Fatalf("%v", e)
	}
}
