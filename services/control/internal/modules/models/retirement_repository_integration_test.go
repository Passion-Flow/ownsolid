//go:build repository_integration

package models_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// These are explicit relational metadata fixtures, not training/GPU/creator
// execution or approved preparation artifacts. All real constraints stay enabled.
type retirementJSON string

func retirementSeed(t *testing.T, x modelReadFixture, q transaction.Executor, table string, values map[string]any) {
	t.Helper()
	values["tenant_id"], values["workspace_id"] = x.f.Tenant, x.f.Workspace
	columns := make([]string, 0, len(values))
	for k := range values {
		columns = append(columns, k)
	}
	sort.Strings(columns)
	args := make([]any, 0, len(columns))
	marks := make([]string, 0, len(columns))
	for _, k := range columns {
		mark := "?"
		if q.Dialect() == transaction.PostgreSQL {
			mark = fmt.Sprintf("$%d", len(args)+1)
		}
		v := values[k]
		if j, ok := v.(retirementJSON); ok {
			v = string(j)
			if q.Dialect() == transaction.MySQL {
				mark = "CAST(" + mark + " AS JSON)"
			} else {
				mark += "::jsonb"
			}
		}
		args = append(args, v)
		marks = append(marks, mark)
	}
	statement := "INSERT INTO " + table + "(" + strings.Join(columns, ",") + ") VALUES(" + strings.Join(marks, ",") + ")"
	if _, e := q.Exec(x.ctx, transaction.SQL(q.Dialect(), statement, args...)); e != nil {
		t.Fatal("synthetic constrained retirement fixture", table, e)
	}
}
func retirementReferences(ctx context.Context, q transaction.Executor, tenant, workspace, model string) (bool, error) {
	d := &distribution.Service{Deployments: &deployments.Service{}}
	if found, e := d.HasModelReferences(ctx, q, tenant, workspace, model); e != nil || found {
		return found, e
	}
	if found, e := (&training.Service{}).Referenced(ctx, q, tenant, workspace, model); e != nil || found {
		return found, e
	}
	return (&modelpreparations.Service{}).Referenced(ctx, q, tenant, workspace, model)
}
func retirementSetup(t *testing.T) modelReadFixture {
	x := modelReadSetup(t)
	x.s.Referenced = retirementReferences
	return x
}
func retirementState(t *testing.T, x modelReadFixture, id, state string, audits int) {
	t.Helper()
	var actual string
	var n int
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT state FROM model_versions WHERE id=$1", "SELECT state FROM model_versions WHERE id=?", id)).Scan(&actual); e != nil || actual != state {
		t.Fatal("durable retirement state", actual, e)
	}
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT COUNT(*) FROM audit_events WHERE action='model.version_retired' AND target_type='model_version' AND target_id=$1 AND tenant_id=$2 AND workspace_id=$3", "SELECT COUNT(*) FROM audit_events WHERE action='model.version_retired' AND target_type='model_version' AND target_id=? AND tenant_id=? AND workspace_id=?", id, x.f.Tenant, x.f.Workspace)).Scan(&n); e != nil || n != audits {
		t.Fatal("atomic retirement audit", n, e)
	}
}
func retirementRefused(t *testing.T, v models.Retirement, e error, code string) {
	t.Helper()
	modelReadError(t, e, code)
	if v != (models.Retirement{}) {
		t.Fatal("failed retirement published DTO")
	}
}
func retirementRetire(t *testing.T, x modelReadFixture, id string) {
	t.Helper()
	v, e := x.s.Retire(x.ctx, x.admin, id, "Synthetic reviewed retirement \u4e16\u754c", uuid.NewString())
	if e != nil || v.ID != id || v.WorkspaceID != x.f.Workspace || v.State != "deleted" || v.ArtifactRetention != "immutable_files_retained" {
		t.Fatal("actual authorized retirement", v, e)
	}
	retirementState(t, x, id, "deleted", 1)
}
func retirementNode(t *testing.T, x modelReadFixture) string {
	t.Helper()
	id := uuid.NewString()
	credential := sha256.Sum256([]byte(id))
	retirementSeed(t, x, x.f.Store, "managed_nodes", map[string]any{"id": id, "name": id, "status": "active", "credential_hash": credential[:], "protocol_version": "1.0", "facts": retirementJSON(`{}`), "last_heartbeat_at": time.Now().UTC()})
	return id
}
func retirementDeployment(t *testing.T, x modelReadFixture, q transaction.Executor, node, model string, removed bool) string {
	t.Helper()
	id := uuid.NewString()
	v := map[string]any{"id": id, "node_id": node, "model_id": model, "gpu_uuid": "GPU-" + uuid.NewString(), "desired_state": "stopped", "revision": 1, "created_by": x.f.User, "model_kind": "approved_base", "catalog_digest": wire.ModelCatalogDigest()}
	if removed {
		v["removed_at"] = time.Now().UTC()
		v["desired_state"] = "removed"
	}
	retirementSeed(t, x, q, "deployments", v)
	return id
}
func retirementAlias(t *testing.T, x modelReadFixture, history bool) string {
	t.Helper()
	node := retirementNode(t, x)
	dep := retirementDeployment(t, x, x.f.Store, node, x.model, true)
	gateway, alias := uuid.NewString(), uuid.NewString()
	retirementSeed(t, x, x.f.Store, "gateway_instances", map[string]any{"id": gateway, "node_id": node, "name": gateway, "client_origin": "https://synthetic.invalid", "control_origin_sha256": strings.Repeat("1", 64), "credential_hash": bytes.Repeat([]byte{4}, 32), "created_by": x.f.User, "requested_epoch": 1, "idempotency_key": uuid.NewString(), "fingerprint": bytes.Repeat([]byte{4}, 32)})
	v := map[string]any{"id": alias, "node_id": node, "gateway_id": gateway, "name": "synthetic-" + alias, "deployment_id": dep, "deployment_revision": 1, "revision": 1, "created_by": x.f.User, "idempotency_key": uuid.NewString(), "fingerprint": bytes.Repeat([]byte{5}, 32)}
	if history {
		v["disabled_at"] = time.Now().UTC()
	}
	retirementSeed(t, x, x.f.Store, "model_aliases", v)
	if history {
		retirementSeed(t, x, x.f.Store, "model_alias_history", map[string]any{"alias_id": alias, "revision": 1, "action": "published", "actor_user_id": x.f.User, "reason": "Synthetic retained target"})
		retirementSeed(t, x, x.f.Store, "model_alias_target_versions", map[string]any{"id": uuid.NewString(), "alias_id": alias, "revision": 1, "action": "published", "deployment_id": dep, "deployment_revision": 1, "model_id": x.model, "model_kind": "approved_base", "catalog_digest": wire.ModelCatalogDigest()})
	}
	return alias
}

type retirementProvenance struct{ node, dataset, version, job, attempt, artifact, holdout, preparation string }

func retirementTraining(t *testing.T, x modelReadFixture, model string) retirementProvenance {
	t.Helper()
	p := retirementProvenance{node: retirementNode(t, x), dataset: uuid.NewString(), version: uuid.NewString(), job: uuid.NewString(), attempt: uuid.NewString(), artifact: uuid.NewString(), holdout: uuid.NewString(), preparation: uuid.NewString()}
	draft, job, attempt := uuid.NewString(), uuid.NewString(), uuid.NewString()
	sha := strings.Repeat("1", 64)
	fingerprint := bytes.Repeat([]byte{8}, 32)
	now := time.Now().UTC()
	put := func(table string, v map[string]any) { retirementSeed(t, x, x.f.Store, table, v) }
	put("datasets", map[string]any{"id": p.dataset, "name": "Synthetic provenance", "name_key": p.dataset, "origin": "synthetic", "license": "synthetic", "rights_asserted": true, "created_by": x.f.User, "creation_key": uuid.NewString(), "creation_fingerprint": fingerprint})
	put("dataset_drafts", map[string]any{"id": draft, "dataset_id": p.dataset, "created_by": x.f.User, "requested_audience": "admin", "requested_epoch": 1, "source_object_id": uuid.NewString(), "source_name": "synthetic.jsonl", "source_sha256": sha, "expected_bytes": 1, "source_received_at": now, "state": "published", "creation_key": uuid.NewString(), "creation_fingerprint": fingerprint})
	put("dataset_jobs", map[string]any{"id": job, "dataset_id": p.dataset, "draft_id": draft, "kind": "publish", "requested_by": x.f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic provenance", "expected_revision": 1, "state": "succeeded", "version_id": p.version, "input_policy": retirementJSON(`{}`), "split_seed": strings.Repeat("1", 32), "validation_basis_points": 1000, "completed_at": now, "creation_key": uuid.NewString(), "creation_fingerprint": fingerprint})
	put("dataset_job_attempts", map[string]any{"id": attempt, "dataset_id": p.dataset, "draft_id": draft, "job_id": job, "attempt_number": 1, "state": "succeeded", "finished_at": now})
	put("dataset_versions", map[string]any{"id": p.version, "dataset_id": p.dataset, "draft_id": draft, "job_id": job, "attempt_id": attempt, "ordinal": 1, "published_by": x.f.User, "raw_source_sha256": sha, "effective_source_sha256": sha, "manifest_sha256": sha, "manifest": retirementJSON(`{}`), "row_mapping_sha256": sha})
	put("training_jobs", map[string]any{"id": p.job, "node_id": p.node, "current_node_id": p.node, "model_id": model, "dataset_id": p.dataset, "dataset_version_id": p.version, "gpu_uuid": "GPU-" + uuid.NewString(), "configuration": retirementJSON(`{}`), "configuration_sha256": sha, "execution_recipe": retirementJSON(`{}`), "dataset_manifest_sha256": sha, "model_catalog_sha256": wire.ModelCatalogDigest(), "created_by": x.f.User, "requested_by": x.f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic retained provenance", "creation_key": uuid.NewString(), "creation_fingerprint": fingerprint, "state": "cancelled"})
	return p
}
func retirementPreparation(t *testing.T, x modelReadFixture, p retirementProvenance, model string) {
	t.Helper()
	sha := strings.Repeat("1", 64)
	put := func(table string, v map[string]any) { retirementSeed(t, x, x.f.Store, table, v) }
	put("training_attempts", map[string]any{"id": p.attempt, "job_id": p.job, "node_id": p.node, "attempt_number": 1, "state": "cancelled", "requested_by": x.f.User, "requested_audience": "admin", "requested_epoch": 1, "input_inventory": retirementJSON(`{}`)})
	put("training_uploads", map[string]any{"id": p.artifact, "job_id": p.job, "attempt_id": p.attempt, "kind": "artifact", "global_step": 0, "manifest_sha256": sha, "manifest": retirementJSON(`{}`), "manifest_bytes": []byte(`{}`), "state": "accepted"})
	put("training_artifacts", map[string]any{"id": p.artifact, "job_id": p.job, "attempt_id": p.attempt, "manifest_sha256": sha, "manifest": retirementJSON(`{}`), "file_inventory": retirementJSON(`{}`), "storage_key": "artifacts/" + p.job + "/" + p.attempt + "/" + p.artifact})
	put("model_holdouts", map[string]any{"id": p.holdout, "dataset_id": p.dataset, "dataset_version_id": p.version, "name": "Synthetic holdout", "manifest_sha256": sha, "effective_source_sha256": sha, "row_mapping_sha256": sha, "source_bytes": 3, "row_count": 3, "label_counts": retirementJSON(`{"account":1,"billing":1,"technical":1}`), "recipe_id": "synthetic", "recipe_sha256": sha, "instruction_sha256": sha, "fingerprint_algorithm": "synthetic", "created_by": x.f.User, "audience": "admin", "reason": "Synthetic provenance", "creation_key": uuid.NewString(), "creation_fingerprint": bytes.Repeat([]byte{8}, 32)})
	put("model_preparations", map[string]any{"id": p.preparation, "node_id": p.node, "current_node_id": p.node, "model_id": model, "training_job_id": p.job, "training_attempt_id": p.attempt, "training_artifact_id": p.artifact, "heldout_id": p.holdout, "gpu_uuid": "GPU-" + uuid.NewString(), "input_snapshot": retirementJSON(`{}`), "input_sha256": sha, "configuration": retirementJSON(`{}`), "configuration_sha256": sha, "execution_recipe": retirementJSON(`{}`), "created_by": x.f.User, "requested_by": x.f.User, "creation_audience": "admin", "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic retained preparation", "creation_key": uuid.NewString(), "creation_fingerprint": bytes.Repeat([]byte{9}, 32), "state": "cancelled"})
}
func retirementDerivation(t *testing.T, x modelReadFixture, p retirementProvenance, base, derived string) {
	t.Helper()
	attempt, task, review := uuid.NewString(), uuid.NewString(), uuid.NewString()
	report := []byte(`{"status":"prepared","gate_passed":true,"event_count":1}`)
	reportDigest := sha256.Sum256(report)
	sha := hex.EncodeToString(reportDigest[:])
	put := func(table string, v map[string]any) { retirementSeed(t, x, x.f.Store, table, v) }
	put("node_tasks", map[string]any{"id": task, "node_id": p.node, "operation": "collect_facts.v1", "state": "succeeded", "idempotency_key": uuid.NewString(), "requested_by": x.f.User, "deadline_at": time.Now().UTC(), "completed_at": time.Now().UTC()})
	put("model_preparation_attempts", map[string]any{"id": attempt, "preparation_id": p.preparation, "node_id": p.node, "task_id": task, "attempt_number": 1, "state": "prepared", "requested_by": x.f.User, "requested_audience": "admin", "requested_epoch": 1, "command_sha256": sha, "physical_stop_receipt": retirementJSON(`{}`), "physical_stop_sha256": sha, "physical_stopped_at": time.Now().UTC(), "outcome": retirementJSON(report), "outcome_bytes": report, "outcome_sha256": sha, "last_event_sequence": 1})
	put("model_preparation_uploads", map[string]any{"id": derived, "preparation_id": p.preparation, "attempt_id": attempt, "manifest_sha256": sha, "manifest": retirementJSON(`{}`), "manifest_bytes": []byte(`{}`), "descriptor": retirementJSON(`{}`)})
	put("model_preparation_artifacts", map[string]any{"id": derived, "preparation_id": p.preparation, "attempt_id": attempt, "upload_id": derived, "manifest_sha256": sha, "descriptor": retirementJSON(`{}`), "report": retirementJSON(report), "report_bytes": report, "storage_path": "artifacts/" + p.preparation + "/" + attempt + "/" + derived})

	x.exec(t, "UPDATE model_preparations SET state='awaiting_review',attempt_number=1,current_attempt_id=$1 WHERE id=$2", "UPDATE model_preparations SET state='awaiting_review',attempt_number=1,current_attempt_id=? WHERE id=?", attempt, p.preparation)
	var gpu string
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT gpu_uuid FROM model_preparations WHERE id=$1", "SELECT gpu_uuid FROM model_preparations WHERE id=?", p.preparation)).Scan(&gpu); e != nil {
		t.Fatal(e)
	}
	lease := map[string]any{"node_id": p.node, "gpu_uuid": gpu, "model_preparation_id": p.preparation, "revision": 1, "state": "released"}
	if x.f.Store.Dialect() == transaction.MySQL {
		lease["id"] = uuid.NewString()
	}
	put("gpu_leases", lease)

	if x.f.Store.Dialect() == transaction.MySQL {
		// Explicit synthetic SQL approval graph; no training/physical/human proof.
		catalog, _ := json.Marshal(map[string]any{"model_id": derived, "synthetic_schema": true})
		sum := sha256.Sum256(catalog)
		digest := hex.EncodeToString(sum[:])
		if e := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			seed := func(table string, v map[string]any) { retirementSeed(t, x, q, table, v) }
			seed("model_versions", map[string]any{"id": derived, "name": "Synthetic metadata derivation", "model_key": "qwen3-0.6b", "catalog_digest": digest, "manifest": retirementJSON(catalog), "state": "staged", "kind": "merged_lora", "created_by": x.f.User})
			seed("model_preparation_reviews", map[string]any{"id": review, "preparation_id": p.preparation, "attempt_id": attempt, "artifact_id": derived, "report_sha256": sha, "expected_revision": 1, "decision": "approve", "model_id": derived, "model_name": "Synthetic metadata derivation", "reviewed_by": x.f.User, "audience": "admin", "reason": "Synthetic relational approval; no human review", "idempotency_key": uuid.NewString(), "fingerprint": bytes.Repeat([]byte{9}, 32)})
			seed("model_derivations", map[string]any{"model_id": derived, "preparation_id": p.preparation, "attempt_id": attempt, "artifact_id": derived, "base_model_id": base, "review_id": review, "catalog_digest": digest, "catalog": retirementJSON(catalog), "report_sha256": sha})
			n, e := q.Exec(ctx, transaction.SQL(transaction.MySQL, "UPDATE model_preparations SET state='approved',review_id=?,derived_model_id=?,revision=revision+1 WHERE id=? AND state='awaiting_review'", review, derived, p.preparation))
			if e != nil {
				return e
			}
			if n != 1 {
				return errors.New("SYNTHETIC_APPROVAL_NOT_BOUND")
			}
			seed("model_publications", map[string]any{"model_id": derived})
			// The real publication AFTER INSERT trigger owns the ready transition.
			var state string
			if e = q.QueryRow(ctx, transaction.SQL(transaction.MySQL, "SELECT state FROM model_versions WHERE id=?", derived)).Scan(&state); e != nil {
				return e
			}
			if state != "ready" {
				return errors.New("SYNTHETIC_DERIVATION_NOT_READY")
			}
			return nil
		}); e != nil {
			t.Fatal("real constrained MySQL staged publication metadata fixture", e)
		}
		return
	}
	put("model_preparation_reviews", map[string]any{"id": review, "preparation_id": p.preparation, "attempt_id": attempt, "artifact_id": derived, "report_sha256": sha, "expected_revision": 1, "decision": "approve", "model_id": derived, "model_name": "Synthetic metadata derivation", "reviewed_by": x.f.User, "audience": "admin", "reason": "Synthetic retained derivation", "idempotency_key": uuid.NewString(), "fingerprint": bytes.Repeat([]byte{9}, 32)})
	put("model_derivations", map[string]any{"model_id": derived, "preparation_id": p.preparation, "attempt_id": attempt, "artifact_id": derived, "base_model_id": base, "review_id": review, "catalog_digest": retirementMetadataDigest(derived), "catalog": retirementJSON(`{}`), "report_sha256": sha})
}
func retirementApprovedManifest(t *testing.T) string {
	t.Helper()
	raw, e := json.Marshal(wire.ApprovedModel())
	if e != nil {
		t.Fatal(e)
	}
	return string(raw)
}
func retirementMetadataDigest(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// Deliberately metadata-only, distinct catalogs: never approved/imported/served.
func retirementExtraModel(t *testing.T, x modelReadFixture) string {
	t.Helper()
	id := uuid.NewString()
	retirementSeed(t, x, x.f.Store, "model_versions", map[string]any{"id": id, "name": "Synthetic extra " + id, "model_key": "qwen3-0.6b", "catalog_digest": retirementMetadataDigest(id), "manifest": retirementJSON(retirementApprovedManifest(t)), "state": "ready", "created_by": x.f.User})
	return id
}
func retirementScopedChecks(t *testing.T, x modelReadFixture, checker func(context.Context, transaction.Executor, string, string, string) (bool, error), model string) {
	t.Helper()
	if e := x.f.Store.Within(x.ctx, true, func(ctx context.Context, q transaction.Executor) error {
		for _, scope := range [][3]string{{x.f.Tenant, x.f.Workspace, model}, {uuid.NewString(), x.f.Workspace, model}, {x.f.Tenant, uuid.NewString(), model}, {x.f.Tenant, x.f.Workspace, uuid.NewString()}} {
			found, e := checker(ctx, q, scope[0], scope[1], scope[2])
			want := scope[0] == x.f.Tenant && scope[1] == x.f.Workspace && scope[2] == model
			if e != nil || found != want {
				return errors.New("SCOPED_REFERENCE_RESULT_MISMATCH")
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestActualModelRetirement(t *testing.T) {
	for _, name := range []string{"retire_and_authorized_replay", "authority_scope_and_request_refusal", "missing_and_failing_reference_checker", "deployment_reference", "alias_history_and_live_alias_reference", "retained_training_reference", "preparation_and_derivation_references", "audit_rollback_and_unknown_projection", "reference_first_lock_order", "retirement_first_lock_order", "approved_files_retained_and_deleted_export"} {
		t.Run(name, func(t *testing.T) {
			if name == "approved_files_retained_and_deleted_export" {
				h := fileOperationSetup(t)
				h.s.Referenced = retirementReferences
				id := fileOperationReady(t, h)
				retirementRetire(t, h.modelReadFixture, id)
				if e := modelartifact.VerifyDirectory(h.ctx, filepath.Join(h.root, "objects", id)); e != nil {
					t.Fatal(e)
				}
				target := fileOperationTarget(t)
				if e := h.s.ExportFiles(h.ctx, target); e != nil {
					t.Fatal(e)
				}
				raw, e := os.ReadFile(filepath.Join(target, "model-files.json"))
				var v models.FileSnapshot
				if e != nil || json.Unmarshal(raw, &v) != nil || len(v.Models) != 1 || v.Models[0].ID != id || v.Models[0].State != "deleted" {
					t.Fatal("deleted inventory/file retention", e)
				}
				if e = modelartifact.VerifyDirectory(h.ctx, filepath.Join(target, "objects", id)); e != nil {
					t.Fatal(e)
				}
				return
			}
			x := retirementSetup(t)
			denied := func(a identity.Auth, id, reason, request, code string) {
				t.Helper()
				v, e := x.s.Retire(x.ctx, a, id, reason, request)
				retirementRefused(t, v, e, code)
			}
			switch name {
			case "retire_and_authorized_replay":
				retirementRetire(t, x, x.model)
				retirementRetire(t, x, x.model)
				if v, e := x.s.Get(x.ctx, x.admin, "", x.model); e == nil || v.ID != "" {
					t.Fatal("retired version remains visible")
				}
				denied(x.portal, x.model, "Portal denied", uuid.NewString(), "ACCESS_DENIED")
				retirementState(t, x, x.model, "deleted", 1)
			case "authority_scope_and_request_refusal":
				denied(x.portal, x.model, "Portal denied", uuid.NewString(), "ACCESS_DENIED")
				for _, v := range [][3]string{{strings.ToUpper(x.model), "Valid reason", uuid.NewString()}, {x.model, "", uuid.NewString()}, {x.model, strings.Repeat("\u754c", 501), uuid.NewString()}, {x.model, "Valid reason", "invalid"}} {
					denied(x.admin, v[0], v[1], v[2], "VALIDATION_FAILED")
				}
				denied(x.admin, uuid.NewString(), "Missing model", uuid.NewString(), "RESOURCE_NOT_FOUND")
				foreign, workspace, model := uuid.NewString(), uuid.NewString(), uuid.NewString()
				x.exec(t, `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Foreign','active')`, foreign)
				x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Foreign','active')`, workspace, foreign)
				x.exec(t, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Foreign','qwen3-0.6b',$4,$5::jsonb,'ready',$6)`, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,'Foreign','qwen3-0.6b',?,CAST(? AS JSON),'ready',?)`, model, foreign, workspace, wire.ModelCatalogDigest(), retirementApprovedManifest(t), x.f.User)
				denied(x.admin, model, "Foreign installation model", uuid.NewString(), "RESOURCE_NOT_FOUND")
				cancelled, cancel := context.WithCancel(x.ctx)
				cancel()
				v, e := x.s.Retire(cancelled, x.admin, x.model, "Cancelled retirement", uuid.NewString())
				retirementRefused(t, v, e, string(transaction.Cancelled))

				x.exec(t, "UPDATE workspaces SET status='disabled' WHERE id=$1", "UPDATE workspaces SET status='disabled' WHERE id=?", x.f.Workspace)
				denied(x.admin, x.model, "Disabled scope", uuid.NewString(), "ACCESS_DENIED")
				x.exec(t, "UPDATE workspaces SET status='active' WHERE id=$1", "UPDATE workspaces SET status='active' WHERE id=?", x.f.Workspace)
				x.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", x.f.User)
				denied(x.admin, x.model, "Revoked actor", uuid.NewString(), "AUTH_SESSION_EXPIRED")
				retirementState(t, x, x.model, "ready", 0)
			case "missing_and_failing_reference_checker":
				x.s.Referenced = nil
				denied(x.admin, x.model, "Missing checker", uuid.NewString(), "MODEL_REFERENCE_CHECK_UNAVAILABLE")
				x.s.Referenced = func(context.Context, transaction.Executor, string, string, string) (bool, error) {
					return false, errors.New("SYNTHETIC_QUERY_FAILURE_SECRET")
				}
				denied(x.admin, x.model, "Fail closed", uuid.NewString(), "STORAGE_UNAVAILABLE")
				x.s.Referenced = (&distribution.Service{}).HasModelReferences
				denied(x.admin, x.model, "Missing dependency", uuid.NewString(), "STORAGE_UNAVAILABLE")
				retirementState(t, x, x.model, "ready", 0)
			case "deployment_reference":
				dep := retirementDeployment(t, x, x.f.Store, retirementNode(t, x), x.model, false)
				retirementScopedChecks(t, x, (&deployments.Service{}).HasModelReferences, x.model)
				denied(x.admin, x.model, "Referenced", uuid.NewString(), "MODEL_VERSION_REFERENCED")
				retirementState(t, x, x.model, "ready", 0)
				x.exec(t, "UPDATE deployments SET desired_state='removed',removed_at=clock_timestamp() WHERE id=$1", "UPDATE deployments SET desired_state='removed',removed_at=UTC_TIMESTAMP(6) WHERE id=?", dep)
				retirementRetire(t, x, x.model)
			case "alias_history_and_live_alias_reference":
				alias := retirementAlias(t, x, false)
				retirementScopedChecks(t, x, (&distribution.Service{Deployments: &deployments.Service{}}).HasModelReferences, x.model)
				denied(x.admin, x.model, "Live alias", uuid.NewString(), "MODEL_VERSION_REFERENCED")
				x.exec(t, "UPDATE model_aliases SET disabled_at=clock_timestamp() WHERE id=$1", "UPDATE model_aliases SET disabled_at=UTC_TIMESTAMP(6) WHERE id=?", alias)
				retirementAlias(t, x, true)
				retirementScopedChecks(t, x, (&distribution.Service{Deployments: &deployments.Service{}}).HasModelReferences, x.model)
				denied(x.admin, x.model, "Retained alias history", uuid.NewString(), "MODEL_VERSION_REFERENCED")
				retirementState(t, x, x.model, "ready", 0)
			case "retained_training_reference":
				retirementTraining(t, x, x.model)
				retirementScopedChecks(t, x, (&training.Service{}).Referenced, x.model)
				denied(x.admin, x.model, "Cancelled training still owns provenance", uuid.NewString(), "MODEL_VERSION_REFERENCED")
				retirementState(t, x, x.model, "ready", 0)
			case "preparation_and_derivation_references":
				if x.f.Store.Dialect() == transaction.MySQL {
					p := retirementTraining(t, x, x.model)
					retirementPreparation(t, x, p, x.model)
					retirementScopedChecks(t, x, (&modelpreparations.Service{}).Referenced, x.model)
					denied(x.admin, x.model, "Retained coherent preparation/training provenance", uuid.NewString(), "MODEL_VERSION_REFERENCED")
					derived := uuid.NewString()
					retirementDerivation(t, x, p, x.model, derived)
					retirementScopedChecks(t, x, (&modelpreparations.Service{}).Referenced, derived)
					retirementScopedChecks(t, x, (&modelpreparations.Service{}).Referenced, x.model)
					denied(x.admin, derived, "Retained staged publication provenance", uuid.NewString(), "MODEL_VERSION_REFERENCED")
					retirementState(t, x, x.model, "ready", 0)
					retirementState(t, x, derived, "ready", 0)
					break
				}
				other := retirementExtraModel(t, x)
				p := retirementTraining(t, x, other)
				retirementPreparation(t, x, p, x.model)
				retirementScopedChecks(t, x, (&modelpreparations.Service{}).Referenced, x.model)
				denied(x.admin, x.model, "Retained preparation", uuid.NewString(), "MODEL_VERSION_REFERENCED")
				derived := retirementExtraModel(t, x)
				retirementDerivation(t, x, p, other, derived)
				retirementScopedChecks(t, x, (&modelpreparations.Service{}).Referenced, derived)
				retirementScopedChecks(t, x, (&modelpreparations.Service{}).Referenced, other)
				denied(x.admin, derived, "Retained derived provenance", uuid.NewString(), "MODEL_VERSION_REFERENCED")
				retirementState(t, x, x.model, "ready", 0)
				retirementState(t, x, derived, "ready", 0)
			case "audit_rollback_and_unknown_projection":
				repair := (cancellationFixture{modelReadFixture: x}).auditFault(t, "model.version_retired")
				active := true
				t.Cleanup(func() {
					if active {
						repair()
					}
				})
				denied(x.admin, x.model, "Audit refusal", uuid.NewString(), "STORAGE_UNAVAILABLE")
				retirementState(t, x, x.model, "ready", 0)
				repair()
				active = false
				h := humanImportFixture{modelReadFixture: x}
				s := fileOperationClone(h, &modelUnknownStore{Store: x.f.Store})
				v, e := s.Retire(x.ctx, x.admin, x.model, "Explicit lost-outcome double", uuid.NewString())
				retirementRefused(t, v, e, string(transaction.CommitUnknown))
				retirementState(t, x, x.model, "deleted", 1)
				retirementRetire(t, x, x.model)
			case "reference_first_lock_order":
				node := retirementNode(t, x)
				locked, release := make(chan struct{}), make(chan struct{})
				done := make(chan error, 1)
				go func() {
					done <- x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
						// Follow the actual creator order: actor, Workspace, model.
						if e := x.owner.RecheckOrderedWithin(ctx, q, x.admin); e != nil {
							return e
						}
						if _, e := x.s.Workspaces.RecheckScopeWithin(ctx, q, x.admin, x.f.Workspace); e != nil {
							return e
						}
						var state string
						if e := q.QueryRow(ctx, x.f.Statement("SELECT state FROM model_versions WHERE id=$1 FOR SHARE", "SELECT state FROM model_versions WHERE id=? FOR SHARE", x.model)).Scan(&state); e != nil {
							return e
						}
						if state != "ready" {
							return errors.New("REFERENCE_NOT_READY")
						}
						close(locked)
						select {
						case <-release:
						case <-ctx.Done():
							return ctx.Err()
						}
						retirementDeployment(t, x, q, node, x.model, false)
						return nil
					})
				}()
				select {
				case <-locked:
				case <-x.ctx.Done():
					t.Fatal(x.ctx.Err())
				}
				retired := make(chan error, 1)
				go func() {
					v, e := x.s.Retire(x.ctx, x.admin, x.model, "Concurrent reference first", uuid.NewString())
					if v != (models.Retirement{}) {
						e = errors.New("UNEXPECTED_RETIREMENT_DTO")
					}
					retired <- e
				}()
				select {
				case e := <-retired:
					close(release)
					<-done
					t.Fatal("retirement crossed shared lock", e)
				case <-time.After(75 * time.Millisecond):
				}
				close(release)
				if e := <-done; e != nil {
					t.Fatal(e)
				}
				modelReadError(t, <-retired, "MODEL_VERSION_REFERENCED")
				retirementState(t, x, x.model, "ready", 0)
			case "retirement_first_lock_order":
				locked, release := make(chan struct{}), make(chan struct{})
				x.s.Referenced = func(ctx context.Context, q transaction.Executor, a, b, c string) (bool, error) {
					close(locked)
					select {
					case <-release:
					case <-ctx.Done():
						return false, ctx.Err()
					}
					return retirementReferences(ctx, q, a, b, c)
				}
				retired := make(chan error, 1)
				go func() {
					_, e := x.s.Retire(x.ctx, x.admin, x.model, "Concurrent retirement first", uuid.NewString())
					retired <- e
				}()
				select {
				case <-locked:
				case <-x.ctx.Done():
					t.Fatal(x.ctx.Err())
				}
				referenced := make(chan error, 1)
				go func() {
					referenced <- x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
						// Follow the actual creator order: actor, Workspace, model.
						if e := x.owner.RecheckOrderedWithin(ctx, q, x.admin); e != nil {
							return e
						}
						if _, e := x.s.Workspaces.RecheckScopeWithin(ctx, q, x.admin, x.f.Workspace); e != nil {
							return e
						}
						var state string
						if e := q.QueryRow(ctx, x.f.Statement("SELECT state FROM model_versions WHERE id=$1 FOR SHARE", "SELECT state FROM model_versions WHERE id=? FOR SHARE", x.model)).Scan(&state); e != nil {
							return e
						}
						if state != "ready" {
							return errors.New("REFERENCE_NOT_READY")
						}
						return errors.New("RETIRED_MODEL_ADMITTED")
					})
				}()
				select {
				case e := <-referenced:
					close(release)
					<-retired
					t.Fatal("creator crossed update lock", e)
				case <-time.After(75 * time.Millisecond):
				}
				close(release)
				if e := <-retired; e != nil {
					t.Fatal(e)
				}
				modelReadError(t, <-referenced, "REFERENCE_NOT_READY")
				retirementState(t, x, x.model, "deleted", 1)
			}
		})
	}
}
