//go:build repository_integration

package models_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

type registrationStory struct {
	x            modelReadFixture
	c            lifecycle.DerivedCatalog
	w            workspaces.Workspace
	token        models.PreparedDerivedFiles
	review, name string
}

// Reports, physical-stop records, reviews and tiny weights are synthetic. These
// fixtures exercise actual guards/SQL and private tokens, never model quality.
func registrationSetup(t *testing.T) registrationStory {
	t.Helper()
	x := modelReadSetup(t)
	return registrationSetupOn(t, x)
}

func registrationSetupOn(t *testing.T, x modelReadFixture) registrationStory {
	t.Helper()
	accessRoot(t, x)
	p := retirementTraining(t, x, x.model)
	retirementPreparation(t, x, p, x.model)
	attempt, task, id := uuid.NewString(), uuid.NewString(), uuid.NewString()
	report := []byte(`{"status":"prepared","gate_passed":true,"event_count":1}`)
	sum := sha256.Sum256(report)
	digest := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	put := func(table string, v map[string]any) { retirementSeed(t, x, x.f.Store, table, v) }
	put("node_tasks", map[string]any{"id": task, "node_id": p.node, "operation": "collect_facts.v1", "state": "succeeded", "idempotency_key": uuid.NewString(), "requested_by": x.f.User, "deadline_at": now, "completed_at": now})
	put("model_preparation_attempts", map[string]any{"id": attempt, "preparation_id": p.preparation, "node_id": p.node, "task_id": task, "attempt_number": 1, "state": "prepared", "requested_by": x.f.User, "requested_audience": "admin", "requested_epoch": 1, "command_sha256": digest, "physical_stop_receipt": retirementJSON(`{}`), "physical_stop_sha256": digest, "physical_stopped_at": now, "outcome": retirementJSON(report), "outcome_bytes": report, "outcome_sha256": digest, "last_event_sequence": 1})
	put("model_preparation_uploads", map[string]any{"id": id, "preparation_id": p.preparation, "attempt_id": attempt, "manifest_sha256": digest, "manifest": retirementJSON(`{}`), "manifest_bytes": []byte(`{}`), "descriptor": retirementJSON(`{}`)})
	put("model_preparation_artifacts", map[string]any{"id": id, "preparation_id": p.preparation, "attempt_id": attempt, "upload_id": id, "manifest_sha256": digest, "descriptor": retirementJSON(`{}`), "report": retirementJSON(report), "report_bytes": report, "storage_path": "artifacts/" + p.preparation + "/" + attempt + "/" + id})
	x.exec(t, "UPDATE model_preparations SET state='awaiting_review',attempt_number=1,current_attempt_id=$1 WHERE id=$2", "UPDATE model_preparations SET state='awaiting_review',attempt_number=1,current_attempt_id=? WHERE id=?", attempt, p.preparation)
	var gpu string
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT gpu_uuid FROM model_preparations WHERE id=$1", "SELECT gpu_uuid FROM model_preparations WHERE id=?", p.preparation)).Scan(&gpu); e != nil {
		t.Fatal(e)
	}
	put("gpu_leases", map[string]any{"id": uuid.NewString(), "node_id": p.node, "gpu_uuid": gpu, "model_preparation_id": p.preparation, "revision": 1, "state": "released"})
	c := lifecycle.DerivedCatalog{SchemaVersion: 1, Kind: "merged_lora", Scope: lifecycle.Scope{TenantID: x.f.Tenant, WorkspaceID: x.f.Workspace, PreparationID: p.preparation}, ModelID: id, ArtifactID: id, BaseModelID: x.model, AttemptID: attempt, ReportSHA256: digest, RecipeSHA256: lifecycle.RecipeDigest(), BaseCatalogSHA256: lifecycle.ApprovedModelCatalogSHA256, BaseRepository: lifecycle.ApprovedBaseRepository, BaseRevision: lifecycle.ApprovedBaseRevision, License: lifecycle.ApprovedBaseLicense, Format: "safetensors", Architecture: lifecycle.ApprovedBaseArchitecture, ModelType: lifecycle.ApprovedBaseModelType, Precision: "BF16", MaximumContextTokens: lifecycle.MaximumContextTokens, Files: lifecycle.ApprovedBaseFiles()}
	weights := []byte("Explicit synthetic range fixture; not tensor or training evidence.")
	weightSHA := sha256.Sum256(weights)
	for i := range c.Files {
		if c.Files[i].Path == "model.safetensors" {
			c.Files[i].SizeBytes = int64(len(weights))
			c.Files[i].SHA256 = hex.EncodeToString(weightSHA[:])
		}
	}
	accessPrepare(t, x, c, false)
	// Exact existing-file replay obtains the package's private sealed token. It
	// verifies complete files again; no test constructs or forges token internals.
	sources := map[string]*os.File{}
	defer func() {
		for _, f := range sources {
			f.Close()
		}
	}()
	for _, f := range c.Files {
		input, e := os.Open(filepath.Join(x.s.RootDirectory, "derived", id, f.Path))
		if e != nil {
			t.Fatal(e)
		}
		sources[f.Path] = input
	}
	token, e := x.s.PrepareDerivedFiles(x.ctx, c, sources, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	return registrationStory{x: x, c: c, w: workspaces.Workspace{ID: x.f.Workspace, TenantID: x.f.Tenant}, token: token, review: uuid.NewString(), name: "Synthetic registration \u4e16\u754c"}
}
func (g registrationStory) reviewRow(t *testing.T, q transaction.Executor) {
	t.Helper()
	retirementSeed(t, g.x, q, "model_preparation_reviews", map[string]any{"id": g.review, "preparation_id": g.c.Scope.PreparationID, "attempt_id": g.c.AttemptID, "artifact_id": g.c.ArtifactID, "report_sha256": g.c.ReportSHA256, "expected_revision": 1, "decision": "approve", "model_id": g.c.ModelID, "model_name": g.name, "reviewed_by": g.x.f.User, "audience": "admin", "reason": "Synthetic SQL approval, no human or quality evidence", "idempotency_key": uuid.NewString(), "fingerprint": bytes.Repeat([]byte{2}, 32)})
}
func (g registrationStory) approve(ctx context.Context, q transaction.Executor) error {
	n, e := q.Exec(ctx, g.x.f.Statement("UPDATE model_preparations SET state='approved',review_id=$1,derived_model_id=$2,revision=revision+1 WHERE id=$3 AND state='awaiting_review'", "UPDATE model_preparations SET state='approved',review_id=?,derived_model_id=?,revision=revision+1 WHERE id=? AND state='awaiting_review'", g.review, g.c.ModelID, g.c.Scope.PreparationID))
	if e != nil {
		return e
	}
	if n != 1 {
		return errors.New("EXPLICIT_TEST_APPROVAL_NOT_BOUND")
	}
	return nil
}
func (g registrationStory) publish(t *testing.T, ctx context.Context, q transaction.Executor) (string, error) {
	t.Helper()
	id, e := g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
	if e != nil {
		return "", e
	}
	if id != g.c.ModelID {
		return "", errors.New("EXPLICIT_TEST_STAGE_ID_CHANGED")
	}
	var exists bool
	if e = q.QueryRow(ctx, g.x.f.Statement("SELECT EXISTS(SELECT 1 FROM model_preparation_reviews WHERE id=$1)", "SELECT EXISTS(SELECT 1 FROM model_preparation_reviews WHERE id=?)", g.review)).Scan(&exists); e != nil {
		return "", e
	}
	if !exists {
		g.reviewRow(t, q)
	}
	id, e = g.x.s.RegisterDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
	if e != nil {
		return "", e
	}
	if !exists {
		if e = g.approve(ctx, q); e != nil {
			return "", e
		}
	}
	id, e = g.x.s.FinalizeDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
	if e != nil {
		return "", e
	}
	if !exists {
		_, e = q.Exec(ctx, g.x.f.Statement(`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,'admin','model.preparation_review_approved','success','model_holdout',$5,'Explicit synthetic owner audit',$6)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'admin','model.preparation_review_approved','success','model_holdout',?,'Explicit synthetic owner audit',?)`, uuid.NewString(), g.x.f.User, g.w.TenantID, g.w.ID, g.c.Scope.PreparationID, uuid.NewString()))
	}
	return id, e
}
func registrationOwner(t *testing.T, g registrationStory, store transaction.Store, abort error) (string, error) {
	t.Helper()
	var id string
	e := store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		id, e = g.publish(t, ctx, q)
		if e != nil {
			return e
		}
		return abort
	})
	if e != nil {
		return "", e
	}
	return id, nil
}
func registrationIDError(t *testing.T, id string, e error, code string) {
	t.Helper()
	modelReadError(t, e, code)
	if id != "" {
		t.Fatal("failed borrowed registration leaked ID")
	}
}
func (g registrationStory) counts(t *testing.T, versions, derivations, reviews, audits int) {
	t.Helper()
	for _, c := range []struct {
		pg, my string
		want   int
	}{{"SELECT COUNT(*) FROM model_versions WHERE id=$1", "SELECT COUNT(*) FROM model_versions WHERE id=?", versions}, {"SELECT COUNT(*) FROM model_derivations WHERE model_id=$1", "SELECT COUNT(*) FROM model_derivations WHERE model_id=?", derivations}, {"SELECT COUNT(*) FROM model_preparation_reviews WHERE model_id=$1", "SELECT COUNT(*) FROM model_preparation_reviews WHERE model_id=?", reviews}, {"SELECT COUNT(*) FROM audit_events WHERE action='model.preparation_review_approved' AND target_id=$1", "SELECT COUNT(*) FROM audit_events WHERE action='model.preparation_review_approved' AND target_id=?", audits}} {
		key := g.c.ModelID
		if c.want == audits && c.pg == "SELECT COUNT(*) FROM audit_events WHERE action='model.preparation_review_approved' AND target_id=$1" {
			key = g.c.Scope.PreparationID
		}
		var n int
		if e := g.x.f.Store.QueryRow(g.x.ctx, g.x.f.Statement(c.pg, c.my, key)).Scan(&n); e != nil || n != c.want {
			t.Fatal("actual durable registration graph count", n, c.want, e)
		}
	}
}

func TestActualBorrowedDerivedRegistration(t *testing.T) {
	t.Run("current_admin_workspace_review_asset", func(t *testing.T) {
		g := registrationSetup(t)
		r := accessBaseFile(t, g.x)
		if e := g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			f, a, e := g.x.s.OpenReviewAssetWithin(ctx, q, g.x.admin, g.w, g.x.model, r.File)
			if e != nil {
				return e
			}
			defer f.Close()
			if a.Path != r.File || a.SizeBytes != r.Length {
				return errors.New("EXPLICIT_TEST_REVIEW_ASSET_CHANGED")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		_, _, e := g.x.s.OpenReviewAssetWithin(g.x.ctx, g.x.f.Store, g.x.portal, g.w, g.x.model, r.File)
		modelReadError(t, e, "ACCESS_DENIED")
	})
	t.Run("review_asset_failure_closes_descriptor", func(t *testing.T) {
		g := registrationSetup(t)
		r := accessBaseFile(t, g.x)
		path := filepath.Join(g.x.s.RootDirectory, "objects", g.x.model, r.File)
		if e := os.Chmod(path, 0600); e != nil {
			t.Fatal(e)
		}
		if e := g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			f, a, e := g.x.s.OpenReviewAssetWithin(ctx, q, g.x.admin, g.w, g.x.model, r.File)
			modelReadError(t, e, "MODEL_FILE_INVALID")
			if f != nil || a.Path != "" {
				if f != nil {
					f.Close()
				}
				t.Fatal("failed review file transfer leaked projection")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		if e := os.Chmod(path, 0444); e != nil {
			t.Fatal(e)
		}
		foreign := g.w
		foreign.TenantID = uuid.NewString()
		if e := g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			f, a, e := g.x.s.OpenReviewAssetWithin(ctx, q, g.x.admin, foreign, g.x.model, r.File)
			modelReadError(t, e, "ACCESS_DENIED")
			if f != nil || a.Path != "" {
				if f != nil {
					f.Close()
				}
				t.Fatal("foreign review asset projected")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("file_token_scope_name_and_base_fence", func(t *testing.T) {
		g := registrationSetup(t)
		if e := g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			id, e := g.x.s.StageDerivedWithin(ctx, q, g.x.portal, g.w, g.token, g.review, g.name)
			registrationIDError(t, id, e, "ACCESS_DENIED")
			id, e = g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, " "+g.name)
			registrationIDError(t, id, e, "VALIDATION_FAILED")
			foreign := g.w
			foreign.TenantID = uuid.NewString()
			id, e = g.x.s.StageDerivedWithin(ctx, q, g.x.admin, foreign, g.token, g.review, g.name)
			registrationIDError(t, id, e, "ACCESS_DENIED")
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		g.x.exec(t, "UPDATE model_versions SET state='deleted' WHERE id=$1", "UPDATE model_versions SET state='deleted' WHERE id=?", g.x.model)
		if e := g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			id, e := g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
			registrationIDError(t, id, e, "RESOURCE_NOT_FOUND")
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		g.counts(t, 0, 0, 0, 0)
	})
	t.Run("stage_actual_vendor_identity_and_visibility", func(t *testing.T) {
		g := registrationSetup(t)
		abort := errors.New("EXPLICIT_TEST_STAGE_ROLLBACK")
		e := g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			id, e := g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
			if e != nil || id != g.c.ModelID {
				t.Fatal(e)
			}
			var state string
			if e = q.QueryRow(ctx, g.x.f.Statement("SELECT state FROM model_versions WHERE id=$1", "SELECT state FROM model_versions WHERE id=?", id)).Scan(&state); e != nil {
				return e
			}
			want := "ready"
			if q.Dialect() == transaction.MySQL {
				want = "staged"
			}
			if state != want {
				t.Fatal("actual vendor stage", state, want)
			}
			again, e := g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
			if e != nil || again != id {
				t.Fatal("stage exact replay", e)
			}
			id, e = g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, "Different identity")
			registrationIDError(t, id, e, "MODEL_DERIVED_IDENTITY_CONFLICT")
			return abort
		})
		if !errors.Is(e, abort) {
			t.Fatal(e)
		}
		g.counts(t, 0, 0, 0, 0)
	})
	t.Run("register_requires_exact_current_review", func(t *testing.T) {
		g := registrationSetup(t)
		abort := errors.New("EXPLICIT_TEST_REVIEW_ROLLBACK")
		e := g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			id, e := g.x.s.RegisterDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
			registrationIDError(t, id, e, "MODEL_DERIVED_REVIEW_REQUIRED")
			if _, e = g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name); e != nil {
				return e
			}
			g.reviewRow(t, q)
			id, e = g.x.s.RegisterDerivedWithin(ctx, q, g.x.admin, g.w, g.token, uuid.NewString(), g.name)
			registrationIDError(t, id, e, "MODEL_DERIVED_REVIEW_REQUIRED")
			id, e = g.x.s.RegisterDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, "Changed review name")
			registrationIDError(t, id, e, "MODEL_DERIVED_REVIEW_REQUIRED")
			return abort
		})
		if !errors.Is(e, abort) {
			t.Fatal(e)
		}
		g.counts(t, 0, 0, 0, 0)
	})
	t.Run("register_actual_lineage_and_exact_replay", func(t *testing.T) {
		g := registrationSetup(t)
		id, e := registrationOwner(t, g, g.x.f.Store, nil)
		if e != nil || id != g.c.ModelID {
			t.Fatal("actual staged review publication", e)
		}
		id, e = registrationOwner(t, g, g.x.f.Store, nil)
		if e != nil || id != g.c.ModelID {
			t.Fatal("exact reviewed identity replay", e)
		}
		g.counts(t, 1, 1, 1, 1)
		catalog, e := g.x.s.DecodeDerivedVersionWithin(g.x.ctx, g.x.f.Store, g.w.TenantID, g.w.ID, id)
		if e != nil {
			t.Fatal(e)
		}
		a, _ := catalog.Digest()
		if a != g.token.Digest() {
			t.Fatal("published typed catalog changed")
		}
		g.x.exec(t, "UPDATE model_versions SET state='deleted',deleted_at=clock_timestamp() WHERE id=$1", "UPDATE model_versions SET state='deleted',deleted_at=UTC_TIMESTAMP(6) WHERE id=?", g.c.ModelID)
		if e := g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			id, e := g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
			registrationIDError(t, id, e, "MODEL_DERIVED_ALREADY_RETIRED")
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("finalize_requires_approved_exact_parent", func(t *testing.T) {
		g := registrationSetup(t)
		abort := errors.New("EXPLICIT_TEST_UNAPPROVED_ROLLBACK")
		e := g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			if _, e := g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name); e != nil {
				return e
			}
			g.reviewRow(t, q)
			if _, e := g.x.s.RegisterDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name); e != nil {
				return e
			}
			id, e := g.x.s.FinalizeDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
			registrationIDError(t, id, e, "MODEL_DERIVED_REVIEW_REQUIRED")
			return abort
		})
		if !errors.Is(e, abort) {
			t.Fatal(e)
		}
		g.counts(t, 0, 0, 0, 0)
	})
	t.Run("rollback_and_unknown_suppress_owner_projection", func(t *testing.T) {
		g := registrationSetup(t)
		id, e := registrationOwner(t, g, g.x.f.Store, errors.New("EXPLICIT_TEST_OWNER_AUDIT_ROLLBACK"))
		registrationIDError(t, id, e, "EXPLICIT_TEST_OWNER_AUDIT_ROLLBACK")
		g.counts(t, 0, 0, 0, 0)
		// Actual owned database trigger rejects the owner audit INSERT, proving
		// model/review/lineage/publication all roll back on that real failure.
		if g.x.f.Store.Dialect() == transaction.PostgreSQL {
			g.x.exec(t, `CREATE FUNCTION reject_registration_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_review_approved' THEN RAISE EXCEPTION 'EXPLICIT_TEST_AUDIT_FAILURE';END IF;RETURN NEW;END;$$`, "")
		}
		g.x.exec(t, `CREATE TRIGGER reject_registration_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_registration_audit()`, `CREATE TRIGGER reject_registration_audit BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action='model.preparation_review_approved' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='EXPLICIT_TEST_AUDIT_FAILURE';END IF;END`)
		id, e = registrationOwner(t, g, g.x.f.Store, nil)
		registrationIDError(t, id, e, string(transaction.Unavailable))
		g.counts(t, 0, 0, 0, 0)
		g.x.exec(t, `DROP TRIGGER reject_registration_audit ON audit_events`, `DROP TRIGGER reject_registration_audit`)
		id, e = registrationOwner(t, g, modelUnknownStore{g.x.f.Store}, nil)
		registrationIDError(t, id, e, string(transaction.CommitUnknown))
		g.counts(t, 1, 1, 1, 1)
		id, e = registrationOwner(t, g, g.x.f.Store, nil)
		if e != nil || id != g.c.ModelID {
			t.Fatal("exact lookup/replay after unknown", e)
		}
		g.counts(t, 1, 1, 1, 1)
	})
	t.Run("concurrent_same_identity_serializes", func(t *testing.T) {
		g := registrationSetup(t)
		start := make(chan struct{})
		results := make(chan error, 2)
		var wait sync.WaitGroup
		for range 2 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				id, e := registrationOwner(t, g, g.x.f.Store, nil)
				if e == nil && id != g.c.ModelID {
					e = errors.New("EXPLICIT_TEST_CONCURRENT_ID_CHANGED")
				}
				results <- e
			}()
		}
		close(start)
		wait.Wait()
		close(results)
		for e := range results {
			if e != nil {
				t.Fatal("actual serialized owner", e)
			}
		}
		g.counts(t, 1, 1, 1, 1)
	})
	t.Run("tamper_link_and_foreign_token_refused", func(t *testing.T) {
		g := registrationSetup(t)
		path := filepath.Join(g.x.s.RootDirectory, "derived", g.c.ModelID, "model.safetensors")
		link := filepath.Join(g.x.s.RootDirectory, "explicit-link")
		if e := os.Link(path, link); e != nil {
			t.Fatal(e)
		}
		e := g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			id, e := g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
			registrationIDError(t, id, e, "MODEL_DERIVED_STORAGE_INVALID")
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		if e = os.Remove(link); e != nil {
			t.Fatal(e)
		}
		// Reproduce the missing publication guard: file attributes alone do
		// not establish the immutable directory and exact canonical catalog receipt.
		dir := filepath.Dir(path)
		if e = os.Chmod(dir, 0700); e != nil {
			t.Fatal(e)
		}
		e = g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			id, e := g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
			registrationIDError(t, id, e, "MODEL_DERIVED_STORAGE_INVALID")
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(dir, 0555); e != nil {
			t.Fatal(e)
		}
		receipt := filepath.Join(dir, "projectone-derived-catalog.json")
		before, e := os.Stat(receipt)
		if e != nil {
			t.Fatal(e)
		}
		original, e := os.ReadFile(receipt)
		if e != nil {
			t.Fatal(e)
		}
		corrupt := append([]byte(nil), original...)
		corrupt[0] ^= 1
		if e = os.Chmod(receipt, 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(receipt, corrupt, 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(receipt, 0444); e != nil {
			t.Fatal(e)
		}
		if e = os.Chtimes(receipt, before.ModTime(), before.ModTime()); e != nil {
			t.Fatal(e)
		}
		e = g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			id, e := g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
			registrationIDError(t, id, e, "MODEL_DERIVED_STORAGE_INVALID")
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(receipt, 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(receipt, original, 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(receipt, 0444); e != nil {
			t.Fatal(e)
		}
		if e = os.Chtimes(receipt, before.ModTime(), before.ModTime()); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(path, 0600); e != nil {
			t.Fatal(e)
		}
		e = g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			id, e := g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
			registrationIDError(t, id, e, "MODEL_DERIVED_STORAGE_INVALID")
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		body, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		body[0] ^= 1
		if e = os.WriteFile(path, body, 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(path, 0444); e != nil {
			t.Fatal(e)
		}
		e = g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			id, e := g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
			registrationIDError(t, id, e, "MODEL_DERIVED_STORAGE_INVALID")
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		clone := *g.x.s
		clone.RootDirectory = fileOperationScope(t)
		e = g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			id, e := clone.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
			registrationIDError(t, id, e, "MODEL_DERIVED_STORAGE_INVALID")
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		g.counts(t, 0, 0, 0, 0)
	})
	t.Run("staged_never_serves_and_retires_closed", func(t *testing.T) {
		g := registrationSetup(t)
		e := g.x.f.Store.Within(g.x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			_, e := g.x.s.StageDerivedWithin(ctx, q, g.x.admin, g.w, g.token, g.review, g.name)
			return e
		})
		if g.x.f.Store.Dialect() == transaction.PostgreSQL {
			if e == nil {
				t.Fatal("incomplete deferred graph committed")
			}
			g.counts(t, 0, 0, 0, 0)
		} else {
			if e != nil {
				t.Fatal(e)
			}
			g.counts(t, 1, 0, 0, 0)
		}
		_, e = g.x.s.Get(g.x.ctx, g.x.admin, g.w.ID, g.c.ModelID)
		modelReadError(t, e, "RESOURCE_NOT_FOUND")
		c, e := g.x.s.RetainedDerivedVersionWithin(g.x.ctx, g.x.f.Store, g.w.TenantID, g.w.ID, g.c.ModelID)
		accessCatalogError(t, c, e, "RESOURCE_NOT_FOUND")
		g.x.s.Referenced = retirementReferences
		r, e := g.x.s.Retire(g.x.ctx, g.x.admin, g.c.ModelID, "Explicit unpublished fixture must stay closed", uuid.NewString())
		retirementRefused(t, r, e, "RESOURCE_NOT_FOUND")
	})
	t.Run("nil_mixed_context_and_error_boundaries", func(t *testing.T) {
		g := registrationSetup(t)
		var missing *models.Service
		id, e := missing.StageDerivedWithin(g.x.ctx, g.x.f.Store, g.x.admin, g.w, g.token, g.review, g.name)
		registrationIDError(t, id, e, "STORAGE_UNAVAILABLE")
		id, e = g.x.s.StageDerivedWithin(nil, g.x.f.Store, g.x.admin, g.w, g.token, g.review, g.name)
		registrationIDError(t, id, e, "STORAGE_UNAVAILABLE")
		other := transaction.MySQL
		if g.x.f.Store.Dialect() == other {
			other = transaction.PostgreSQL
		}
		id, e = g.x.s.StageDerivedWithin(g.x.ctx, accessDialect{g.x.f.Store, other}, g.x.admin, g.w, g.token, g.review, g.name)
		registrationIDError(t, id, e, "MODEL_STORAGE_MISMATCH")
		ctx, cancel := context.WithCancel(g.x.ctx)
		cancel()
		id, e = g.x.s.StageDerivedWithin(ctx, g.x.f.Store, g.x.admin, g.w, g.token, g.review, g.name)
		registrationIDError(t, id, e, string(transaction.Cancelled))
		id, e = g.x.s.RegisterDerivedInTransaction(g.x.ctx, nil, g.x.admin, g.w, g.token, g.review, g.name)
		registrationIDError(t, id, e, "STORAGE_UNAVAILABLE")
		var a identity.Auth
		id, e = g.x.s.StageDerivedWithin(g.x.ctx, g.x.f.Store, a, g.w, g.token, g.review, g.name)
		registrationIDError(t, id, e, "ACCESS_DENIED")
	})
}
