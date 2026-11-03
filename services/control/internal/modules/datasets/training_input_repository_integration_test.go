//go:build repository_integration

package datasets_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Publication SQL is explicit synthetic metadata; these private partition bytes
// are real and every constraint remains enabled. This does not run a processor.
func trainingInputSetup(t *testing.T, empty bool) publishedSourceStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	t.Cleanup(cancel)
	owner := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		owner.Storage, owner.DB = nil, f.PostgreSQL
	}
	ws := &workspaces.Service{DB: f.PostgreSQL, Identity: owner}
	root := filepath.Join(t.TempDir(), "published-source")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := datasets.InitializeStore(root, f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := datasets.OpenStore(root, f.Tenant, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := store.Close(); e != nil {
			t.Error(e)
		}
	})
	train := []byte("{\"source_id\":\"train-fixture\",\"prompt\":\"Train \u4e16\u754c\",\"completion\":\"answer\"}\n")
	validation := []byte("{\"source_id\":\"validation-fixture\",\"prompt\":\"Validation \u0627\u0644\u0639\u0631\u0628\u064a\u0629\",\"completion\":\"answer\"}\n")
	if empty {
		train = nil
	}
	payload := append(append([]byte{}, train...), validation...)

	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	g := publishedSourceStory{f: f, ctx: ctx, owner: owner, s: &datasets.Service{DB: f.PostgreSQL, Identity: owner, Workspaces: ws, Store: store}, dataset: uuid.NewString(), version: uuid.NewString(), attempt: uuid.NewString(), root: root, digest: digest, payload: payload}
	var issued identity.IssuedSession
	if e = f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = owner.IssueSessionWithin(ctx, q, f.User, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	g.a, e = owner.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	draft, job := uuid.NewString(), uuid.NewString()
	now := time.Date(2026, 10, 21, 1, 2, 3, 123456000, time.UTC)
	fp := bytes.Repeat([]byte{8}, 32)
	g.seed(t, "datasets", map[string]any{"id": g.dataset, "name": "Synthetic published \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", "name_key": g.dataset, "origin": "synthetic", "license": "synthetic", "rights_asserted": true, "created_by": f.User, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_drafts", map[string]any{"id": draft, "dataset_id": g.dataset, "created_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "source_object_id": uuid.NewString(), "source_name": "synthetic.jsonl", "source_sha256": digest, "expected_bytes": len(payload), "source_received_at": now, "state": "published", "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_jobs", map[string]any{"id": job, "dataset_id": g.dataset, "draft_id": draft, "kind": "publish", "requested_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic immutable publication source", "expected_revision": 1, "state": "succeeded", "version_id": g.version, "input_policy": publishedSourceJSON(`{}`), "split_seed": strings.Repeat("1", 32), "validation_basis_points": 1000, "completed_at": now, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_job_attempts", map[string]any{"id": g.attempt, "dataset_id": g.dataset, "draft_id": draft, "job_id": job, "attempt_number": 1, "state": "succeeded", "finished_at": now})
	manifest := wire.Manifest{SchemaVersion: 1, RowSchema: wire.RowSchema, ProcessorVersion: wire.ProcessorVersion, SplitAlgorithm: wire.SplitAlgorithm, SourceSHA256: digest, Seed: strings.Repeat("1", 32), ValidationBasisPoints: 1000, TotalRows: 2, UniqueRows: 2, SourceGroups: 2, ValidationGroups: 1, TrainRows: 1, ValidationRows: 1, ManifestFile: wire.Asset{Path: "manifest.json", SizeBytes: 2, SHA256: digest}}
	partitions := map[string][]byte{"samples.jsonl": payload, "train.jsonl": train, "validation.jsonl": validation, "assignments.jsonl": nil, "duplicates.jsonl": nil}
	for _, name := range []string{"samples.jsonl", "train.jsonl", "validation.jsonl", "assignments.jsonl", "duplicates.jsonl"} {
		sum := sha256.Sum256(partitions[name])
		manifest.Files = append(manifest.Files, wire.Asset{Path: name, SizeBytes: int64(len(partitions[name])), SHA256: hex.EncodeToString(sum[:])})
	}

	g.manifest = manifest
	raw, e := json.Marshal(manifest)
	if e != nil {
		t.Fatal(e)
	}
	g.seed(t, "dataset_versions", map[string]any{"id": g.version, "dataset_id": g.dataset, "draft_id": draft, "job_id": job, "attempt_id": g.attempt, "ordinal": 1, "published_by": f.User, "published_at": now, "raw_source_sha256": digest, "effective_source_sha256": digest, "manifest_sha256": digest, "manifest": publishedSourceJSON(raw), "row_mapping_sha256": digest})
	directory := filepath.Join(root, "versions", g.version, g.attempt)
	if e = os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	for name, bytes := range partitions {
		path := filepath.Join(directory, name)
		file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = file.Write(bytes); e != nil {
			t.Fatal(e)
		}
		if e = file.Sync(); e != nil {
			t.Fatal(e)
		}
		if e = file.Close(); e != nil {
			t.Fatal(e)
		}
	}
	dir, e := os.Open(directory)
	if e != nil {
		t.Fatal(e)
	}
	if e = dir.Sync(); e != nil {
		t.Fatal(e)
	}
	dir.Close()
	return g
}
func trainingInputRead(g publishedSourceStory, retained bool) (datasets.TrainingInputs, error) {
	var value datasets.TrainingInputs
	e := g.within(func(ctx context.Context, q transaction.Executor) error {
		var e error
		value, e = g.s.TrainingInputSnapshotWithin(ctx, q, g.f.Tenant, g.f.Workspace, g.dataset, g.version, retained)
		return e
	})
	if e != nil {
		return datasets.TrainingInputs{}, e
	}
	return value, nil
}
func trainingInputWant(t *testing.T, v datasets.TrainingInputs, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code || !reflect.DeepEqual(v, datasets.TrainingInputs{}) {
		t.Fatal("zero failed input snapshot", e, code, v)
	}
}
func trainingInputFile(g publishedSourceStory, name string) string {
	return filepath.Join(g.root, "versions", g.version, g.attempt, name)
}
func trainingInputDescriptors(t *testing.T, root string) int {
	t.Helper()
	entries, e := os.ReadDir("/dev/fd")
	if e != nil {
		t.Fatal(e)
	}
	n := 0
	for _, entry := range entries {
		path, e := os.Readlink(filepath.Join("/dev/fd", entry.Name()))
		if e == nil && strings.HasPrefix(path, root) {
			n++
		}
	}
	return n
}
func TestActualPairedTrainingInputSource(t *testing.T) {
	t.Run("actual_nonempty_typed_metadata_and_UTC", func(t *testing.T) {
		g := trainingInputSetup(t, false)
		v, e := trainingInputRead(g, false)
		if e != nil || v.Version.ID != g.version || v.Version.AttemptID != g.attempt || v.Version.PublishedAt.Location() != time.UTC || v.Train.SizeBytes < 1 || v.Validation.SizeBytes < 1 || v.Train.SHA256 == v.Validation.SHA256 {
			t.Fatal("actual typed immutable partitions", v, e)
		}
		if e = g.s.VerifyTrainingInputSnapshot(g.ctx, v); e != nil {
			t.Fatal(e)
		}
		for _, name := range []string{"train.jsonl", "validation.jsonl"} {
			f, asset, e := g.s.OpenTrainingAssetContext(g.ctx, v, name)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := io.ReadAll(f)
			f.Close()
			sum := sha256.Sum256(raw)
			if e != nil || asset.SizeBytes != int64(len(raw)) || hex.EncodeToString(sum[:]) != asset.SHA256 {
				t.Fatal("actual bytes", e)
			}
		}
	})
	t.Run("new_retirement_refusal_and_retained_recovery", func(t *testing.T) {
		g := trainingInputSetup(t, false)
		v, e := trainingInputRead(g, false)
		if e != nil {
			t.Fatal(e)
		}
		g.exec(t, "UPDATE datasets SET retired_at=clock_timestamp(),retire_reason='explicit training source retirement' WHERE id=$1", "UPDATE datasets SET retired_at=UTC_TIMESTAMP(6),retire_reason='explicit training source retirement' WHERE id=?", g.dataset)
		failed, e := trainingInputRead(g, false)
		trainingInputWant(t, failed, e, "DATASET_RETIRED")
		retained, e := trainingInputRead(g, true)
		if e != nil || !reflect.DeepEqual(v, retained) {
			t.Fatal("retained exact input", e)
		}
		if e = g.s.VerifyTrainingInputSnapshot(g.ctx, retained); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("empty_partition_scope_and_repository_refusal", func(t *testing.T) {
		g := trainingInputSetup(t, true)
		v, e := trainingInputRead(g, false)
		trainingInputWant(t, v, e, "TRAINING_DATASET_EMPTY_PARTITION")
		e = g.within(func(ctx context.Context, q transaction.Executor) error {
			for _, scope := range [][3]string{{uuid.NewString(), g.f.Workspace, g.dataset}, {g.f.Tenant, uuid.NewString(), g.dataset}, {g.f.Tenant, g.f.Workspace, uuid.NewString()}} {
				v, e := g.s.TrainingInputSnapshotWithin(ctx, q, scope[0], scope[1], scope[2], g.version, false)
				trainingInputWant(t, v, e, "DATASET_NOT_FOUND")
			}
			v, e := g.s.TrainingInputSnapshotWithin(ctx, publishedSourceWrongDialect{q}, g.f.Tenant, g.f.Workspace, g.dataset, g.version, false)
			trainingInputWant(t, v, e, "DATASET_STORAGE_MISMATCH")
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		saved := g.s.Store
		g.s.Store = nil
		v, e = trainingInputRead(g, false)
		trainingInputWant(t, v, e, "DATASET_STORAGE_UNAVAILABLE")
		g.s.Store = saved
	})
	t.Run("actual_partition_corruption_missing_and_private_paths", func(t *testing.T) {
		g := trainingInputSetup(t, false)
		v, e := trainingInputRead(g, false)
		if e != nil {
			t.Fatal(e)
		}
		path := trainingInputFile(g, "train.jsonl")
		original, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		bad := append([]byte{}, original...)
		bad[0] ^= 1
		if e = os.WriteFile(path, bad, 0600); e != nil {
			t.Fatal(e)
		}
		if e = g.s.VerifyTrainingInputSnapshot(g.ctx, v); e == nil || e.Error() != "DATASET_FILE_INVALID" {
			t.Fatal("corrupt actual partition accepted", e)
		}
		if e = os.WriteFile(path, original, 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(path, 0644); e != nil {
			t.Fatal(e)
		}
		if e = g.s.VerifyTrainingInputSnapshot(g.ctx, v); e == nil {
			t.Fatal("public partition accepted")
		}
		if e = os.Chmod(path, 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.Rename(path, path+".retained"); e != nil {
			t.Fatal(e)
		}
		if e = g.s.VerifyTrainingInputSnapshot(g.ctx, v); e == nil {
			t.Fatal("missing partition accepted")
		}
		if e = os.Symlink(path+".retained", path); e != nil {
			t.Fatal(e)
		}
		if e = g.s.VerifyTrainingInputSnapshot(g.ctx, v); e == nil {
			t.Fatal("symlink accepted")
		}
		if e = os.Remove(path); e != nil {
			t.Fatal(e)
		}
		if e = os.Rename(path+".retained", path); e != nil {
			t.Fatal(e)
		}
		if e = g.s.VerifyTrainingInputSnapshot(g.ctx, v); e != nil {
			t.Fatal("explicit source repair", e)
		}
	})
	t.Run("same_descriptor_verified_bytes_and_rewind", func(t *testing.T) {
		g := trainingInputSetup(t, false)
		v, e := trainingInputRead(g, false)
		if e != nil {
			t.Fatal(e)
		}
		path := trainingInputFile(g, "train.jsonl")
		info, e := os.Stat(path)
		if e != nil {
			t.Fatal(e)
		}
		expected, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		f, asset, e := g.s.OpenTrainingAssetContext(g.ctx, v, "train.jsonl")
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		current, e := f.Stat()
		position, seekErr := f.Seek(0, io.SeekCurrent)
		if e != nil || seekErr != nil || position != 0 || !os.SameFile(info, current) {
			t.Fatal("verified descriptor identity/rewind", e, seekErr)
		}
		if e = os.Rename(path, path+".frozen"); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, []byte("changed task-owned pathname"), 0600); e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(f)
		if e != nil || !bytes.Equal(raw, expected) || asset.SHA256 != v.Train.SHA256 {
			t.Fatal("descriptor lost verified immutable bytes", e)
		}
		f.Close()
		if e = os.Remove(path); e != nil {
			t.Fatal(e)
		}
		if e = os.Rename(path+".frozen", path); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("cancel_deadline_and_descriptor_cleanup", func(t *testing.T) {
		g := trainingInputSetup(t, false)
		v, e := trainingInputRead(g, false)
		if e != nil {
			t.Fatal(e)
		}
		baseline := trainingInputDescriptors(t, g.root)
		// Exercise an error after the real descriptor opens, beyond pre-open
		// cancellation/metadata refusal; repeated failures must not accumulate handles.
		path := trainingInputFile(g, "train.jsonl")
		original, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		corrupt := append([]byte{}, original...)
		corrupt[0] ^= 1
		if e = os.WriteFile(path, corrupt, 0600); e != nil {
			t.Fatal(e)
		}
		for range 10 {
			if e = g.s.VerifyTrainingInputSnapshot(g.ctx, v); e == nil || e.Error() != "DATASET_FILE_INVALID" {
				t.Fatal("real opened-file hash failure", e)
			}
		}
		if n := trainingInputDescriptors(t, g.root); n != baseline {
			t.Fatal("opened-file error descriptors leaked", baseline, n)
		}
		if e = os.WriteFile(path, original, 0600); e != nil {
			t.Fatal(e)
		}
		if e = g.s.VerifyTrainingInputSnapshot(g.ctx, v); e != nil {
			t.Fatal("restored partition verification", e)
		}
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		if e = g.s.VerifyTrainingInputSnapshot(ctx, v); e == nil || e.Error() != string(transaction.Cancelled) {
			t.Fatal("cancelled verification", e)
		}
		f, a, e := g.s.OpenTrainingAssetContext(ctx, v, "train.jsonl")
		if f != nil || !reflect.DeepEqual(a, wire.Asset{}) || !errors.Is(e, context.Canceled) {
			t.Fatal("failed file leaked handle/metadata", e)
		}
		ctx, cancel = context.WithDeadline(g.ctx, time.Now().Add(-time.Second))
		defer cancel()
		if e = g.s.VerifyTrainingInputSnapshot(ctx, v); e == nil || e.Error() != string(transaction.Timeout) {
			t.Fatal("verification deadline", e)
		}
		bad := v
		bad.Train.SHA256 = strings.Repeat("f", 64)
		for range 10 {
			f, a, e = g.s.OpenTrainingAssetContext(g.ctx, bad, "train.jsonl")
			if f != nil || !reflect.DeepEqual(a, wire.Asset{}) || e == nil {
				t.Fatal("forged metadata accepted", e)
			}
		}
		if n := trainingInputDescriptors(t, g.root); n != baseline {
			t.Fatal("failed input descriptors leaked", baseline, n)
		}
	})
	t.Run("metadata_no_file_io_and_caller_rollback", func(t *testing.T) {
		g := trainingInputSetup(t, false)
		v, e := trainingInputRead(g, false)
		if e != nil {
			t.Fatal(e)
		}
		dir := filepath.Dir(trainingInputFile(g, "train.jsonl"))
		if e = os.Rename(dir, dir+".retained"); e != nil {
			t.Fatal(e)
		}
		read, e := trainingInputRead(g, false)
		if e != nil || !reflect.DeepEqual(v, read) {
			t.Fatal("metadata read opened partition files", e)
		}
		sentinel := errors.New("EXPLICIT_TRAINING_INPUT_ROLLBACK")
		e = g.within(func(ctx context.Context, q transaction.Executor) error {
			if _, e := q.Exec(ctx, g.statement("UPDATE datasets SET name='explicit caller rollback' WHERE id=$1", "UPDATE datasets SET name='explicit caller rollback' WHERE id=?", g.dataset)); e != nil {
				return e
			}
			if _, e := g.s.TrainingInputSnapshotWithin(ctx, q, g.f.Tenant, g.f.Workspace, g.dataset, g.version, false); e != nil {
				return e
			}
			return sentinel
		})
		if e != sentinel {
			t.Fatal("rollback authority changed", e)
		}
		var name string
		if e = g.f.Store.QueryRow(g.ctx, g.statement("SELECT name FROM datasets WHERE id=$1", "SELECT name FROM datasets WHERE id=?", g.dataset)).Scan(&name); e != nil || name == "explicit caller rollback" {
			t.Fatal("borrowed metadata committed", name, e)
		}
		if e = os.Rename(dir+".retained", dir); e != nil {
			t.Fatal(e)
		}
		bad := v
		bad.Version.Manifest.Files = append(append([]wire.Asset{}, v.Version.Manifest.Files...), v.Train)
		if e = g.s.VerifyTrainingInputSnapshot(g.ctx, bad); e == nil || e.Error() != "DATASET_DATA_INVALID" {
			t.Fatal("duplicate partition metadata accepted", e)
		}
	})
	t.Run("actual_query_failure_and_zero_projection", func(t *testing.T) {
		g := trainingInputSetup(t, false)
		g.exec(t, "ALTER TABLE dataset_versions RENAME TO explicit_training_versions_unavailable", "RENAME TABLE dataset_versions TO explicit_training_versions_unavailable")
		v, e := trainingInputRead(g, false)
		trainingInputWant(t, v, e, "STORAGE_UNAVAILABLE")
		g.exec(t, "ALTER TABLE explicit_training_versions_unavailable RENAME TO dataset_versions", "RENAME TABLE explicit_training_versions_unavailable TO dataset_versions")
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		v, e = g.s.TrainingInputSnapshotWithin(ctx, g.f.Store, g.f.Tenant, g.f.Workspace, g.dataset, g.version, false)
		trainingInputWant(t, v, e, string(transaction.Cancelled))
		v, e = trainingInputRead(g, false)
		if e != nil || g.s.VerifyTrainingInputSnapshot(g.ctx, v) != nil {
			t.Fatal("explicit query recovery", e)
		}
	})
}
