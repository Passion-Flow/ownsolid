//go:build repository_integration

package datasets_test

import (
	"bytes"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
)

func TestActualPublishedReaderTrainingSnapshotAndRetirement(t *testing.T) {
	g := trainingInputSetup(t, false)
	reader, err := datasets.OpenPublishedReader(g.root, g.f.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	// The real writer stays open through its existing fixture cleanup. Only
	// the consuming service changes to the concrete independent reader.
	g.s.Store, g.s.PublishedReader = nil, reader
	input, err := trainingInputRead(g, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.s.VerifyTrainingInputSnapshot(g.ctx, input); err != nil {
		t.Fatal(err)
	}
	file, _, err := g.s.OpenTrainingAssetContext(g.ctx, input, "train.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(file)
	file.Close()
	expected, readError := os.ReadFile(trainingInputFile(g, "train.jsonl"))
	if err != nil || readError != nil || !bytes.Equal(actual, expected) {
		t.Fatal("reader did not open the actual retained partition", err, readError)
	}
	g.exec(t, "UPDATE datasets SET retired_at=clock_timestamp(),retire_reason='explicit reader source retirement' WHERE id=$1", "UPDATE datasets SET retired_at=UTC_TIMESTAMP(6),retire_reason='explicit reader source retirement' WHERE id=?", g.dataset)
	refused, err := trainingInputRead(g, false)
	trainingInputWant(t, refused, err, "DATASET_RETIRED")
	retained, err := trainingInputRead(g, true)
	if err != nil || !reflect.DeepEqual(retained, input) {
		t.Fatal("reader changed retained recovery identity", err)
	}
	changed := append([]byte{}, expected...)
	changed[0] ^= 1
	if err := os.WriteFile(trainingInputFile(g, "train.jsonl"), changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := g.s.VerifyTrainingInputSnapshot(g.ctx, retained); err == nil {
		t.Fatal("SQL metadata bypassed changed physical bytes")
	}
	if err := os.WriteFile(trainingInputFile(g, "train.jsonl"), expected, 0600); err != nil {
		t.Fatal(err)
	}
	if err := g.s.VerifyTrainingInputSnapshot(g.ctx, retained); err != nil {
		t.Fatal("exact physical repair refused", err)
	}
	reader.Close()
	refused, err = trainingInputRead(g, true)
	trainingInputWant(t, refused, err, "DATASET_STORAGE_UNAVAILABLE")
}
