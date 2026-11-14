//go:build repository_integration

package datasets_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

func TestActualPublishedReaderTrainingSnapshotAndRetirement(t *testing.T) {
	g := trainingInputSetup(t, false)
	reader, err := datasets.OpenPublishedReader(g.root, g.f.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	assertWriterHeld := func() {
		t.Helper()
		second, err := datasets.OpenStore(g.root, g.f.Tenant, 4096)
		if err == nil {
			second.Close()
			t.Fatal("independent reader released the real dataset writer")
		}
		if err.Error() != "DATASET_STORE_IN_USE" {
			t.Fatal("dataset writer ownership refusal changed", err)
		}
	}
	assertWriterHeld()
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
	// Resolve fresh server authority before file I/O, then actually log out
	// that session before the final metadata read. A retained snapshot alone
	// must not bypass the current identity check.
	var issued identity.IssuedSession
	if err := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var err error
		issued, err = g.owner.IssueSessionWithin(ctx, q, g.f.User, 1, "admin", "", uuid.NewString(), nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	current, err := g.owner.Authenticate(g.ctx, issued.Token, "admin")
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := g.s.PublishedSourceSnapshot(g.ctx, current, g.f.Workspace, g.dataset, g.version, true)
	if err != nil || authorized.ID != retained.Version.ID {
		t.Fatal("current authenticated source snapshot", err)
	}
	file, _, err = g.s.OpenTrainingAssetContext(g.ctx, retained, "train.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, file); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	if err := g.owner.Logout(g.ctx, issued.Token, "admin", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	denied, err := g.s.PublishedSourceSnapshot(g.ctx, current, g.f.Workspace, g.dataset, g.version, true)
	if err == nil || !strings.HasPrefix(err.Error(), "AUTH_") || !reflect.DeepEqual(denied, datasets.Version{}) {
		t.Fatal("final authority read exposed a snapshot after logout", err)
	}
	reader.Close()
	assertWriterHeld()
	refused, err = trainingInputRead(g, true)
	trainingInputWant(t, refused, err, "DATASET_STORAGE_UNAVAILABLE")
}
