//go:build repository_integration

package modelpreparations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"reflect"
	"strings"
	"testing"
	"time"
)

func admissionPrepare(t *testing.T, g holdoutStory, input HoldoutRequest) *trainingAdmission {
	t.Helper()
	check, e := g.g.s.PrepareTrainingAdmission(g.g.ctx, g.g.a, training.CreateRequest{WorkspaceID: input.WorkspaceID, DatasetID: input.DatasetID, DatasetVersionID: input.DatasetVersionID})
	if e != nil {
		t.Fatal("actual outside-lock analyzed source", e)
	}
	actual, ok := check.(*trainingAdmission)
	if !ok || actual == nil {
		t.Fatal("actual private check required")
	}
	return actual
}
func admissionBorrow(g holdoutStory, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	return g.g.s.preparationActorTransaction(g.g.ctx, g.g.a, g.input.WorkspaceID, true, fn)
}
func admissionValidate(g holdoutStory, c *trainingAdmission) error {
	return admissionBorrow(g, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		return c.ValidateWithin(ctx, q, w, datasets.TrainingInputs{Version: c.snapshot.version})
	})
}
func admissionWant(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatal("admission error", e, code)
	}
}
func admissionRows(t *testing.T, path string) []datawire.Record {
	t.Helper()
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	records := []datawire.Record{}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var r datawire.Record
		if e = json.Unmarshal(line, &r); e != nil {
			t.Fatal(e)
		}
		records = append(records, r)
	}
	return records
}

type admissionQueryCounter struct {
	transaction.Executor
	queries *int
}

func (q admissionQueryCounter) QueryRow(ctx context.Context, s transaction.Statement) transaction.Row {
	*q.queries++
	return q.Executor.QueryRow(ctx, s)
}
func admissionSource(t *testing.T, g reviewStory, root string, records []datawire.Record) (HoldoutRequest, string) {
	t.Helper()
	dataset, version, draft, job, attempt := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	fp := bytes.Repeat([]byte{3}, 32)
	var payload []byte
	if records == nil {
		for _, label := range wire.Labels() {
			records = append(records, datawire.Record{SourceID: uuid.NewString(), Prompt: "Disjoint synthetic heldout " + uuid.NewString(), Completion: label})
		}
	}
	for _, record := range records {
		payload = append(payload, reviewRaw(t, record)...)
		payload = append(payload, '\n')
	}

	rows := int64(len(records))
	digest := hash(payload)
	put := func(table string, v map[string]any) { g.seed(t, table, v) }
	j := func(v any) reviewJSON { return reviewJSON(reviewRaw(t, v)) }
	put("datasets", map[string]any{"id": dataset, "name": "Synthetic creator heldout", "name_key": dataset, "origin": "local synthetic rows", "license": "CC0 synthetic fixture", "rights_asserted": true, "created_by": g.f.User, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	put("dataset_drafts", map[string]any{"id": draft, "dataset_id": dataset, "created_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "source_object_id": uuid.NewString(), "source_name": "heldout.jsonl", "source_sha256": digest, "expected_bytes": len(payload), "source_received_at": now, "state": "published", "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	put("dataset_jobs", map[string]any{"id": job, "dataset_id": dataset, "draft_id": draft, "kind": "publish", "requested_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic heldout metadata", "expected_revision": 1, "state": "succeeded", "version_id": version, "input_policy": reviewJSON(`{}`), "split_seed": strings.Repeat("1", 32), "validation_basis_points": 1000, "completed_at": now, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	put("dataset_job_attempts", map[string]any{"id": attempt, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_number": 1, "state": "succeeded", "finished_at": now})
	manifest := datawire.Manifest{SchemaVersion: 1, RowSchema: datawire.RowSchema, ProcessorVersion: datawire.ProcessorVersion, SplitAlgorithm: datawire.SplitAlgorithm, SourceSHA256: digest, Seed: strings.Repeat("1", 32), ValidationBasisPoints: 1000, TotalRows: rows, UniqueRows: rows, SourceGroups: rows, ValidationGroups: 1, TrainRows: rows - 1, ValidationRows: 1, ManifestFile: datawire.Asset{Path: "manifest.json", SizeBytes: 2, SHA256: digest}}
	for _, name := range []string{"samples.jsonl", "train.jsonl", "validation.jsonl", "assignments.jsonl", "duplicates.jsonl"} {
		manifest.Files = append(manifest.Files, datawire.Asset{Path: name, SizeBytes: 0, SHA256: hash(nil)})
	}
	put("dataset_versions", map[string]any{"id": version, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_id": attempt, "ordinal": 1, "published_by": g.f.User, "published_at": now, "raw_source_sha256": digest, "effective_source_sha256": digest, "manifest_sha256": digest, "manifest": j(manifest), "row_mapping_sha256": digest})
	directory := filepath.Join(root, "versions", version, attempt)
	if e := os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(directory, "effective.jsonl")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Write(payload); e != nil {
		t.Fatal(e)
	}
	if e = f.Sync(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	d, e := os.Open(directory)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Sync(); e != nil {
		t.Fatal(e)
	}
	d.Close()
	return HoldoutRequest{WorkspaceID: g.f.Workspace, DatasetID: dataset, DatasetVersionID: version, ExpectedManifestSHA256: digest, Name: "Actual synthetic holdout \u4e16\u754c", Reason: "Actual real-file reservation over synthetic publication metadata"}, path
}
func TestActualPairedTrainingHoldoutAdmission(t *testing.T) {
	t.Run("actual_no_reservations_and_typed_presence", func(t *testing.T) {
		g := holdoutSetup(t)
		var present bool
		e := admissionBorrow(g, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
			var e error
			present, e = g.g.s.HasReservationsWithin(ctx, q, w)
			return e
		})
		if e != nil || present {
			t.Fatal("actual empty registry", e)
		}
		if _, e = g.reserve(uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		e = admissionBorrow(g, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
			var e error
			present, e = g.g.s.HasReservationsWithin(ctx, q, w)
			return e
		})
		if e != nil || !present {
			t.Fatal("actual typed registry", e)
		}
		g.counts(t, 1)
	})
	t.Run("real_source_disjoint_and_group_overlap", func(t *testing.T) {
		g := holdoutSetup(t)
		if _, e := g.reserve(uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		input, _ := admissionSource(t, g.g, g.root, nil)
		c := admissionPrepare(t, g, input)
		if e := admissionValidate(g, c); e != nil {
			t.Fatal("disjoint actual source", e)
		}
		records := admissionRows(t, g.path)
		for i := range records {
			records[i].Prompt = "different training prompt " + uuid.NewString()
		}
		input, _ = admissionSource(t, g.g, g.root, records)
		c = admissionPrepare(t, g, input)
		admissionWant(t, admissionValidate(g, c), "MODEL_HOLDOUT_OVERLAP")
		g.counts(t, 1)
	})
	t.Run("raw_and_instruction_prompt_overlap", func(t *testing.T) {
		g := holdoutSetup(t)
		if _, e := g.reserve(uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		original := admissionRows(t, g.path)
		for _, wrapped := range []bool{false, true} {
			records := append([]datawire.Record{}, original...)
			for i := range records {
				records[i].SourceID = uuid.NewString()
				if wrapped {
					records[i].Prompt = wire.InstructionPrompt(records[i].Prompt)
				}
			}
			input, _ := admissionSource(t, g.g, g.root, records)
			c := admissionPrepare(t, g, input)
			admissionWant(t, admissionValidate(g, c), "MODEL_HOLDOUT_OVERLAP")
		}
		g.counts(t, 1)
	})
	t.Run("immutable_snapshot_scope_and_current_owner", func(t *testing.T) {
		g := holdoutSetup(t)
		c := admissionPrepare(t, g, g.input)
		e := admissionBorrow(g, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
			wrong := datasets.TrainingInputs{Version: c.snapshot.version}
			wrong.Version.ManifestSHA256 = strings.Repeat("f", 64)
			admissionWant(t, c.ValidateWithin(ctx, q, w, wrong), "MODEL_HOLDOUT_SOURCE_CHANGED")
			other := w
			other.TenantID = uuid.NewString()
			admissionWant(t, c.ValidateWithin(ctx, q, other, datasets.TrainingInputs{Version: c.snapshot.version}), "MODEL_HOLDOUT_SOURCE_CHANGED")
			other = w
			other.ID = uuid.NewString()
			return c.ValidateWithin(ctx, q, other, datasets.TrainingInputs{Version: c.snapshot.version})
		})
		admissionWant(t, e, "MODEL_HOLDOUT_SOURCE_CHANGED")
		g.g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.g.f.User)
		check, e := g.g.s.PrepareTrainingAdmission(g.g.ctx, g.g.a, training.CreateRequest{WorkspaceID: g.input.WorkspaceID, DatasetID: g.input.DatasetID, DatasetVersionID: g.input.DatasetVersionID})
		if check != nil {
			t.Fatal("failed analysis leaked capability")
		}
		admissionWant(t, e, "AUTH_SESSION_EXPIRED")
		g.counts(t, 0)
	})
	t.Run("unsupported_algorithm_and_invalid_fingerprint_refusal", func(t *testing.T) {
		g := holdoutSetup(t)
		c := admissionPrepare(t, g, g.input)
		valid := c.snapshot.groups
		c.snapshot.groups = map[string]bool{"not-a-digest": true}
		admissionWant(t, admissionValidate(g, c), "MODEL_HOLDOUT_DATA_INVALID")
		c.snapshot.groups = nil
		admissionWant(t, admissionValidate(g, c), "MODEL_HOLDOUT_DATA_INVALID")
		c.snapshot.groups = valid
		g.g.seed(t, "model_holdouts", map[string]any{"id": uuid.NewString(), "dataset_id": g.input.DatasetID, "dataset_version_id": g.input.DatasetVersionID, "name": "Explicit future algorithm envelope", "manifest_sha256": g.input.ExpectedManifestSHA256, "effective_source_sha256": g.input.ExpectedManifestSHA256, "row_mapping_sha256": g.input.ExpectedManifestSHA256, "source_bytes": c.snapshot.size, "row_count": 3, "label_counts": reviewJSON(`{"account":1,"billing":1,"technical":1}`), "recipe_id": wire.RecipeID, "recipe_sha256": wire.RecipeDigest(), "instruction_sha256": wire.InstructionDigest(), "fingerprint_algorithm": "explicit-unsupported-test-v2", "created_by": g.g.f.User, "audience": "admin", "reason": "Explicit synthetic incompatible algorithm", "creation_key": uuid.NewString(), "creation_fingerprint": bytes.Repeat([]byte{9}, 32)})
		e := admissionBorrow(g, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
			present, e := g.g.s.HasReservationsWithin(ctx, q, w)
			if present {
				t.Fatal("error leaked presence success")
			}
			return e
		})
		admissionWant(t, e, "MODEL_HOLDOUT_ALGORITHM_UNSUPPORTED")
		admissionWant(t, admissionValidate(g, c), "MODEL_HOLDOUT_ALGORITHM_UNSUPPORTED")
	})
	t.Run("bounded_multiple_fingerprint_batches", func(t *testing.T) {
		g := holdoutSetup(t)
		if _, e := g.reserve(uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		records := []datawire.Record{}
		for i := 0; i < admissionFingerprintBatchMaximum+1; i++ {
			records = append(records, datawire.Record{SourceID: uuid.NewString(), Prompt: "actual bounded multi-batch training " + uuid.NewString(), Completion: "synthetic training completion"})
		}
		input, _ := admissionSource(t, g.g, g.root, records)
		c := admissionPrepare(t, g, input)
		count := 0
		e := admissionBorrow(g, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
			return c.ValidateWithin(ctx, admissionQueryCounter{q, &count}, w, datasets.TrainingInputs{Version: c.snapshot.version})
		})
		if e != nil || count != 6 {
			t.Fatal("actual presence1 +group2 +prompt3 parameter batches", count, e)
		}
		g.counts(t, 1)
	})
	t.Run("caller_rollback_no_hidden_commit_or_file_io", func(t *testing.T) {
		g := holdoutSetup(t)
		c := admissionPrepare(t, g, g.input)
		before, e := os.ReadFile(g.path)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.Rename(g.path, g.path+".isolated"); e != nil {
			t.Fatal(e)
		}
		sentinel := errors.New("EXPLICIT_OWNER_ROLLBACK")
		e = admissionBorrow(g, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
			if _, e := q.Exec(ctx, preparationSQL(q, "UPDATE workspaces SET name='explicit caller rollback' WHERE id=$1", "UPDATE workspaces SET name='explicit caller rollback' WHERE id=?", w.ID)); e != nil {
				return e
			}
			if e := c.ValidateWithin(ctx, q, w, datasets.TrainingInputs{Version: c.snapshot.version}); e != nil {
				return e
			}
			return sentinel
		})
		admissionWant(t, e, "STORAGE_UNAVAILABLE")
		var name string
		if e = g.g.f.Store.QueryRow(g.g.ctx, g.g.f.Statement("SELECT name FROM workspaces WHERE id=$1", "SELECT name FROM workspaces WHERE id=?", g.input.WorkspaceID)).Scan(&name); e != nil || name == "explicit caller rollback" {
			t.Fatal("borrower secretly committed", name, e)
		}
		if e = os.Rename(g.path+".isolated", g.path); e != nil {
			t.Fatal(e)
		}
		after, e := os.ReadFile(g.path)
		if e != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("borrowed check touched source", e)
		}
		g.counts(t, 0)
	})
	t.Run("cancel_deadline_query_and_store_guards", func(t *testing.T) {
		g := holdoutSetup(t)
		c := admissionPrepare(t, g, g.input)
		ctx, cancel := context.WithCancel(g.g.ctx)
		cancel()
		admissionWant(t, c.ValidateWithin(ctx, g.g.f.Store, workspaces.Workspace{TenantID: g.g.f.Tenant, ID: g.input.WorkspaceID}, datasets.TrainingInputs{Version: c.snapshot.version}), string(transaction.Cancelled))
		ctx, cancel = context.WithDeadline(g.g.ctx, time.Now().Add(-time.Second))
		defer cancel()
		admissionWant(t, c.ValidateWithin(ctx, g.g.f.Store, workspaces.Workspace{TenantID: g.g.f.Tenant, ID: g.input.WorkspaceID}, datasets.TrainingInputs{Version: c.snapshot.version}), string(transaction.Timeout))
		g.g.exec(t, "ALTER TABLE model_holdout_sources RENAME TO explicit_admission_unavailable", "RENAME TABLE model_holdout_sources TO explicit_admission_unavailable")
		e := admissionValidate(g, c)
		admissionWant(t, e, "STORAGE_UNAVAILABLE")
		g.g.exec(t, "ALTER TABLE explicit_admission_unavailable RENAME TO model_holdout_sources", "RENAME TABLE explicit_admission_unavailable TO model_holdout_sources")
		saved := g.g.s.Workspaces
		g.g.s.Workspaces = &workspaces.Service{}
		admissionWant(t, admissionValidate(g, c), "MODEL_PREPARATION_IDENTITY_REQUIRED")
		g.g.s.Workspaces = saved
		g.counts(t, 0)
	})
}
