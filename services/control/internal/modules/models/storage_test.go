package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestImmutableChunkRecoveryConflictAndPathBoundary(t *testing.T) {
	path := t.TempDir()
	if e := os.Chmod(path, 0700); e != nil {
		t.Fatal(e)
	}
	root, e := openStore(path)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	id := uuid.NewString()
	payload := []byte("SYNTHETIC_RESUMABLE_MODEL_CHUNK")
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	write := func(id string, payload []byte, digest string) error {
		return storeChunk(context.Background(), root, id, 0, int64(len(payload)), digest, bytes.NewReader(payload))
	}
	if e = write(id, payload, digest); e != nil {
		t.Fatal(e)
	}
	// A durable file with a rolled-back metadata transaction reconciles only identical bytes.
	if e = write(id, payload, digest); e != nil {
		t.Fatal(e)
	}
	other := []byte("different bytes")
	changed := sha256.Sum256(other)
	if e = write(id, other, hex.EncodeToString(changed[:])); e == nil || e.Error() != "MODEL_CHUNK_CONFLICT" {
		t.Fatal("immutable chunk overwritten", e)
	}
	if e = write("../escape", payload, digest); e == nil {
		t.Fatal("untrusted path accepted")
	}
	if e = write(uuid.NewString(), other, digest); e == nil {
		t.Fatal("corrupt chunk accepted")
	}
	if value, e := os.ReadFile(filepath.Join(path, chunkPath(id, 0))); e != nil || !bytes.Equal(value, payload) {
		t.Fatal("prior bytes changed", e)
	}
	if e = os.Chmod(path, 0755); e != nil {
		t.Fatal(e)
	}
	if otherRoot, e := openStore(path); e == nil {
		otherRoot.Close()
		t.Fatal("public artifact root accepted")
	}
}
