package recovery_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
)

func TestRecoveryReceiptRequiresPrivateBoundedRegularAndStrictFile(t *testing.T) {
	receipt := wire.InferenceRemovalReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), NodeID: uuid.NewString(), DeploymentID: uuid.NewString(), ModelID: uuid.NewString(), CatalogDigest: wire.ModelCatalogDigest(), GPUUUID: "GPU-12345678-1234-1234-1234-123456789abc", LocalRevision: 1, OwnedContainerAbsent: true, RuntimeCacheAbsent: true, CompletedAt: time.Now().UTC()}
	raw, _ := json.Marshal(receipt)
	root := t.TempDir()
	file := filepath.Join(root, "receipt.json")
	if e := os.WriteFile(file, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if value, e := recovery.LoadInferenceReceipt(file); e != nil || value.ReceiptID != receipt.ReceiptID {
		t.Fatal("private valid receipt rejected", e)
	}
	link := filepath.Join(root, "linked")
	os.Symlink(file, link)
	if _, e := recovery.LoadInferenceReceipt(link); e == nil {
		t.Fatal("symlink receipt accepted")
	}
	os.Chmod(file, 0644)
	if _, e := recovery.LoadInferenceReceipt(file); e == nil {
		t.Fatal("public receipt accepted")
	}
	os.Chmod(file, 0600)
	for _, invalid := range [][]byte{[]byte(strings.Repeat("x", 16385)), append(append([]byte(nil), raw...), []byte(" {}")...), append(raw[:len(raw)-1], []byte(",\"command\":\"unapproved\"}")...)} {
		if e := os.WriteFile(file, invalid, 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := recovery.LoadInferenceReceipt(file); e == nil {
			t.Fatal("unbounded or extra receipt content accepted")
		}
	}
}
