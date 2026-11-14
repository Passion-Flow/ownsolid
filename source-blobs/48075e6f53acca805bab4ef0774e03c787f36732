//go:build repository_integration

package models_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/google/uuid"
)

type accessDerivedSource struct {
	report []byte
	files  []datawire.Asset
	events int64
}

func readActualDerivedSource(t *testing.T, path string) *accessDerivedSource {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report lifecycle.PreparedResult
	if lifecycle.DecodeStrict(raw, &report) != nil || report.Status != "prepared" ||
		report.Device != "cpu_verification" || len(report.ModelFiles) != len(lifecycle.ApprovedBaseFiles()) {
		t.Fatal("retained native CPU preparation report required")
	}
	return &accessDerivedSource{report: raw, files: report.ModelFiles, events: report.EventCount}
}

// The native case binds the original report bytes and inventory. The enclosing
// SQL graph still uses explicit synthetic identities and review metadata.
func accessDerived(t *testing.T, x modelReadFixture, native bool, corrupt string) lifecycle.DerivedCatalog {
	t.Helper()
	p := retirementTraining(t, x, x.model)
	retirementPreparation(t, x, p, x.model)
	var source *accessDerivedSource
	if native {
		source = readActualDerivedSource(t, os.Getenv("PROJECT_ONE_REPOSITORY_NATIVE_DERIVED_REPORT"))
	}
	return accessDerivedFromPreparation(t, x, p, source, corrupt)
}

func accessDerivedFromPreparation(t *testing.T, x modelReadFixture, p retirementProvenance, source *accessDerivedSource, corrupt string) lifecycle.DerivedCatalog {
	t.Helper()
	attempt, task, review, derived := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	report := []byte(`{"status":"prepared","gate_passed":true,"event_count":1}`)
	reportEvents := int64(1)
	files := lifecycle.ApprovedBaseFiles()
	if source != nil {
		report, files, reportEvents = source.report, source.files, source.events
	} else {
		weights := []byte("Explicit synthetic range fixture; not tensor or training evidence.")
		weightSHA := sha256.Sum256(weights)
		for i := range files {
			if files[i].Path == "model.safetensors" {
				files[i].SizeBytes = int64(len(weights))
				files[i].SHA256 = hex.EncodeToString(weightSHA[:])
			}
		}
	}
	sum := sha256.Sum256(report)
	reportSHA := hex.EncodeToString(sum[:])
	c := lifecycle.DerivedCatalog{SchemaVersion: 1, Kind: "merged_lora", Scope: lifecycle.Scope{TenantID: x.f.Tenant, WorkspaceID: x.f.Workspace, PreparationID: p.preparation}, ModelID: derived, ArtifactID: derived, BaseModelID: x.model, AttemptID: attempt, ReportSHA256: reportSHA, RecipeSHA256: lifecycle.RecipeDigest(), BaseCatalogSHA256: lifecycle.ApprovedModelCatalogSHA256, BaseRepository: lifecycle.ApprovedBaseRepository, BaseRevision: lifecycle.ApprovedBaseRevision, License: lifecycle.ApprovedBaseLicense, Format: "safetensors", Architecture: lifecycle.ApprovedBaseArchitecture, ModelType: lifecycle.ApprovedBaseModelType, Precision: "BF16", MaximumContextTokens: lifecycle.MaximumContextTokens, Files: files}
	if e := c.Validate(); e != nil {
		t.Fatal("valid synthetic catalog required", e)
	}
	if corrupt == "scope" {
		c.Scope.WorkspaceID = uuid.NewString()
	}
	if corrupt == "lineage_fields" {
		c.AttemptID = uuid.NewString()
	}
	raw, e := c.CanonicalBytes()
	if e != nil {
		t.Fatal(e)
	}
	if corrupt == "unknown_field" {
		raw = append([]byte(`{"unknown":true,`), raw[1:]...)
	}
	digestBytes := sha256.Sum256(raw)
	digest := hex.EncodeToString(digestBytes[:])
	lineage := raw
	put := func(table string, v map[string]any) { retirementSeed(t, x, x.f.Store, table, v) }
	put("node_tasks", map[string]any{"id": task, "node_id": p.node, "operation": "collect_facts.v1", "state": "succeeded", "idempotency_key": uuid.NewString(), "requested_by": x.f.User, "deadline_at": time.Now().UTC(), "completed_at": time.Now().UTC()})
	put("model_preparation_attempts", map[string]any{"id": attempt, "preparation_id": p.preparation, "node_id": p.node, "task_id": task, "attempt_number": 1, "state": "prepared", "requested_by": x.f.User, "requested_audience": "admin", "requested_epoch": 1, "command_sha256": reportSHA, "physical_stop_receipt": retirementJSON(`{}`), "physical_stop_sha256": reportSHA, "physical_stopped_at": time.Now().UTC(), "outcome": retirementJSON(report), "outcome_bytes": report, "outcome_sha256": reportSHA, "last_event_sequence": reportEvents})
	put("model_preparation_uploads", map[string]any{"id": derived, "preparation_id": p.preparation, "attempt_id": attempt, "manifest_sha256": reportSHA, "manifest": retirementJSON(`{}`), "manifest_bytes": []byte(`{}`), "descriptor": retirementJSON(`{}`)})
	put("model_preparation_artifacts", map[string]any{"id": derived, "preparation_id": p.preparation, "attempt_id": attempt, "upload_id": derived, "manifest_sha256": reportSHA, "descriptor": retirementJSON(`{}`), "report": retirementJSON(report), "report_bytes": report, "storage_path": "artifacts/" + p.preparation + "/" + attempt + "/" + derived})
	x.exec(t, "UPDATE model_preparations SET state='awaiting_review',attempt_number=1,current_attempt_id=$1 WHERE id=$2", "UPDATE model_preparations SET state='awaiting_review',attempt_number=1,current_attempt_id=? WHERE id=?", attempt, p.preparation)
	var gpu string
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT gpu_uuid FROM model_preparations WHERE id=$1", "SELECT gpu_uuid FROM model_preparations WHERE id=?", p.preparation)).Scan(&gpu); e != nil {
		t.Fatal(e)
	}
	put("gpu_leases", map[string]any{"id": uuid.NewString(), "node_id": p.node, "gpu_uuid": gpu, "model_preparation_id": p.preparation, "revision": 1, "state": "released"})
	if e = x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		state := "ready"
		if q.Dialect() == transaction.MySQL {
			state = "staged"
		}
		seed := func(table string, v map[string]any) { retirementSeed(t, x, q, table, v) }
		seed("model_versions", map[string]any{"id": derived, "name": "Synthetic scoped file fixture", "model_key": "qwen3-0.6b", "catalog_digest": digest, "manifest": retirementJSON(raw), "state": state, "kind": "merged_lora", "created_by": x.f.User})
		seed("model_preparation_reviews", map[string]any{"id": review, "preparation_id": p.preparation, "attempt_id": attempt, "artifact_id": derived, "report_sha256": reportSHA, "expected_revision": 1, "decision": "approve", "model_id": derived, "model_name": "Synthetic scoped file fixture", "reviewed_by": x.f.User, "audience": "admin", "reason": "Synthetic relational approval; no human review", "idempotency_key": uuid.NewString(), "fingerprint": bytes.Repeat([]byte{9}, 32)})
		seed("model_derivations", map[string]any{"model_id": derived, "preparation_id": p.preparation, "attempt_id": attempt, "artifact_id": derived, "base_model_id": x.model, "review_id": review, "catalog_digest": digest, "catalog": retirementJSON(lineage), "report_sha256": reportSHA})
		n, e := q.Exec(ctx, x.f.Statement("UPDATE model_preparations SET state='approved',review_id=$1,derived_model_id=$2,revision=revision+1 WHERE id=$3 AND state='awaiting_review'", "UPDATE model_preparations SET state='approved',review_id=?,derived_model_id=?,revision=revision+1 WHERE id=? AND state='awaiting_review'", review, derived, p.preparation))
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.New("SYNTHETIC_APPROVAL_NOT_BOUND")
		}
		if q.Dialect() == transaction.MySQL && corrupt != "staged" {
			seed("model_publications", map[string]any{"model_id": derived})
		}
		return nil
	}); e != nil {
		t.Fatal("actual constrained synthetic access lineage", e)
	}
	return c
}

func accessRoot(t *testing.T, x modelReadFixture) {
	t.Helper()
	x.s.RootDirectory = fileOperationScope(t)
	t.Cleanup(func() {
		_ = filepath.WalkDir(x.s.RootDirectory, func(path string, d os.DirEntry, e error) error {
			if e == nil && d.IsDir() {
				return os.Chmod(path, 0700)
			}
			return e
		})
	})
}
func accessPrepare(t *testing.T, x modelReadFixture, c lifecycle.DerivedCatalog, native bool) {
	t.Helper()
	sources := map[string]*os.File{}
	defer func() {
		for _, f := range sources {
			f.Close()
		}
	}()
	for _, a := range c.Files {
		path := filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_MODEL_FILES_DIRECTORY"), a.Path)
		if native {
			path = filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_NATIVE_DERIVED_DIRECTORY"), a.Path)
		} else if a.Path == "model.safetensors" {
			path = filepath.Join(fileOperationScope(t), a.Path)
			if e := os.WriteFile(path, []byte("Explicit synthetic range fixture; not tensor or training evidence."), 0600); e != nil {
				t.Fatal(e)
			}
		}
		f, e := os.Open(path)
		if e != nil {
			t.Fatal(e)
		}
		sources[a.Path] = f
	}
	// Approved cache aliases can be linked; copy non-weight assets into private
	// ordinary inputs rather than changing retained immutable source permissions.
	if !native {
		for _, a := range c.Files {
			if a.Path == "model.safetensors" {
				continue
			}
			src := sources[a.Path]
			dest := filepath.Join(fileOperationScope(t), a.Path)
			out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				t.Fatal(e)
			}
			_, e = io.Copy(out, src)
			closeError := out.Close()
			src.Close()
			if e != nil || closeError != nil {
				t.Fatal(e, closeError)
			}
			sources[a.Path], e = os.Open(dest)
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e := x.s.PrepareDerivedFiles(x.ctx, c, sources, 1<<20); e != nil {
		t.Fatal("real private copy/hash/durable publication", e)
	}
}
func accessBaseFile(t *testing.T, x modelReadFixture) wire.ModelFileRequest {
	t.Helper()
	asset, _ := wire.ModelFileForName("config.json")
	dir := filepath.Join(x.s.RootDirectory, "objects", x.model)
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	source, e := os.Open(filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_MODEL_FILES_DIRECTORY"), asset.Path))
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	out, e := os.OpenFile(filepath.Join(dir, asset.Path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, copyError := io.Copy(out, source)
	modeError := out.Chmod(0444)
	closeError := out.Close()
	if copyError != nil || modeError != nil || closeError != nil {
		t.Fatal(copyError, modeError, closeError)
	}
	return wire.ModelFileRequest{File: asset.Path, Length: asset.SizeBytes}
}
func accessCommand(c lifecycle.DerivedCatalog) wire.InferenceCommand {
	digest, _ := c.Digest()
	return wire.InferenceCommand{Action: "deploy", DeploymentID: uuid.NewString(), ModelID: c.ModelID, CatalogDigest: digest, GPUUUID: "GPU-" + uuid.NewString(), Revision: 1, Derived: &wire.DerivedInferenceIdentity{SchemaVersion: 1, Catalog: c, ServingRecipeSHA256: wire.DerivedServingRecipeDigest()}}
}
func accessRequest(c lifecycle.DerivedCatalog, name string, offset int64) wire.ModelFileRequest {
	for _, f := range c.Files {
		if f.Path == name {
			return wire.ModelFileRequest{File: name, Offset: offset, Length: min(wire.ModelTransferChunkBytes, f.SizeBytes-offset)}
		}
	}
	return wire.ModelFileRequest{File: name}
}
func accessCatalogError(t *testing.T, c lifecycle.DerivedCatalog, e error, code string) {
	t.Helper()
	modelReadError(t, e, code)
	if !reflect.DeepEqual(c, lifecycle.DerivedCatalog{}) {
		t.Fatal("failed read leaked catalog")
	}
}
func accessDownloadError(t *testing.T, d httpserver.Download, e error, code string) {
	t.Helper()
	modelReadError(t, e, code)
	if d != (httpserver.Download{}) {
		if d.File != nil {
			d.File.Close()
		}
		t.Fatal("failed range leaked descriptor/metadata")
	}
}
func accessRange(t *testing.T, d httpserver.Download, r wire.ModelFileRequest) {
	t.Helper()
	if d.File == nil {
		t.Fatal("missing owned descriptor")
	}
	defer d.File.Close()
	b, e := io.ReadAll(io.NewSectionReader(d.File, d.Offset, d.Size))
	sum := sha256.Sum256(b)
	if e != nil || d.Offset != r.Offset || d.Size != r.Length || int64(len(b)) != r.Length || d.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("independent pinned range readback", e)
	}
	info, e := d.File.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0444 {
		t.Fatal("private immutable descriptor", e)
	}
}

// This test owner models the production caller obligation: authenticate/fence
// first, recheck after IO, close and suppress on rollback/unknown outcome.
func accessOwner(x modelReadFixture, store transaction.Store, a identity.Auth, scope string, abort error, open func(context.Context, transaction.Executor) (httpserver.Download, error)) (httpserver.Download, error) {
	owner := *x.owner
	owner.Storage = store
	ws := *x.s.Workspaces
	ws.Identity = &owner
	var d httpserver.Download
	e := store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := owner.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		w, e := ws.ReadScopeWithin(ctx, q, a, scope)
		if e != nil {
			return e
		}
		if w.TenantID != x.f.Tenant {
			return errors.New("RESOURCE_NOT_FOUND")
		}
		d, e = open(ctx, q)
		if e != nil {
			return e
		}
		if e = owner.RecheckWithin(ctx, q, a); e != nil {
			return e
		}
		return abort
	})
	if e != nil {
		if d.File != nil {
			d.File.Close()
		}
		return httpserver.Download{}, e
	}
	return d, nil
}

type accessDialect struct {
	transaction.Executor
	dialect transaction.Dialect
}

func (q accessDialect) Dialect() transaction.Dialect { return q.dialect }

// Explicit cancellation injection after a real successful SQL read, before IO.
type accessCancelExecutor struct {
	transaction.Executor
	cancel context.CancelFunc
}
type accessCancelRow struct {
	transaction.Row
	cancel context.CancelFunc
}

func (q accessCancelExecutor) QueryRow(ctx context.Context, s transaction.Statement) transaction.Row {
	return accessCancelRow{q.Executor.QueryRow(ctx, s), q.cancel}
}
func (r accessCancelRow) Scan(dest ...any) error {
	e := r.Row.Scan(dest...)
	if e == nil {
		r.cancel()
	}
	return e
}

// An explicit result-corruption double around a real SQL row exercises the
// decoder branch. Real immutable graph guards refuse this catalog mismatch at
// write/commit; no production guard is disabled to persist such a row.
type accessLineageExecutor struct {
	transaction.Executor
	record []byte
}
type accessLineageRow struct {
	transaction.Row
	record []byte
}

func (q accessLineageExecutor) QueryRow(ctx context.Context, s transaction.Statement) transaction.Row {
	return accessLineageRow{q.Executor.QueryRow(ctx, s), q.record}
}
func (r accessLineageRow) Scan(dest ...any) error {
	if e := r.Row.Scan(dest...); e != nil {
		return e
	}
	if len(dest) != 9 {
		return errors.New("EXPLICIT_TEST_LINEAGE_SHAPE_INVALID")
	}
	*dest[3].(*[]byte) = r.record
	return nil
}

func TestActualBorrowedModelAccess(t *testing.T) {
	t.Run("base_ready_scope_state_and_kind", func(t *testing.T) {
		x := modelReadSetup(t)
		if e := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			if e := x.s.ReadyWithin(ctx, q, x.f.Tenant, x.f.Workspace, x.model); e != nil {
				return e
			}
			for _, ids := range [][3]string{{uuid.NewString(), x.f.Workspace, x.model}, {x.f.Tenant, uuid.NewString(), x.model}, {x.f.Tenant, x.f.Workspace, uuid.NewString()}, {x.f.Tenant, x.f.Workspace, "invalid"}} {
				modelReadError(t, x.s.ReadyWithin(ctx, q, ids[0], ids[1], ids[2]), "RESOURCE_NOT_FOUND")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(wire.ApprovedModel())
		bad := append([]byte(`{"unknown":true,`), raw[1:]...)
		x.exec(t, "UPDATE model_versions SET manifest=$1::jsonb WHERE id=$2", "UPDATE model_versions SET manifest=CAST(? AS JSON) WHERE id=?", bad, x.model)
		modelReadError(t, x.s.ReadyWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, x.model), "MODEL_FILE_INVALID")
		x.exec(t, "UPDATE model_versions SET manifest=$1::jsonb WHERE id=$2", "UPDATE model_versions SET manifest=CAST(? AS JSON) WHERE id=?", raw, x.model)
		c := accessDerived(t, x, false, "")
		modelReadError(t, x.s.ReadyWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, c.ModelID), "RESOURCE_NOT_FOUND")
		x.exec(t, "UPDATE model_versions SET state='deleted' WHERE id=$1", "UPDATE model_versions SET state='deleted' WHERE id=?", x.model)
		modelReadError(t, x.s.ReadyWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, x.model), "RESOURCE_NOT_FOUND")
	})
	t.Run("base_range_hash_and_owned_descriptor", func(t *testing.T) {
		x := modelReadSetup(t)
		accessRoot(t, x)
		r := accessBaseFile(t, x)
		d, e := accessOwner(x, x.f.Store, x.admin, x.f.Workspace, nil, func(ctx context.Context, q transaction.Executor) (httpserver.Download, error) {
			return x.s.OpenTransferWithin(ctx, q, x.f.Tenant, x.f.Workspace, x.model, r)
		})
		if e != nil {
			t.Fatal(e)
		}
		accessRange(t, d, r)
		if _, e = d.File.Stat(); !errors.Is(e, os.ErrClosed) {
			t.Fatal("consumer must close range", e)
		}
		f, a, e := x.s.OpenVerificationAssetWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, x.model, r.File, "")
		if e != nil || a.Path != r.File || f == nil {
			t.Fatal(e)
		}
		f.Close()
		path := filepath.Join(x.s.RootDirectory, "objects", x.model, r.File)
		link := filepath.Join(x.s.RootDirectory, "owned-hardlink")
		if e = os.Link(path, link); e != nil {
			t.Fatal(e)
		}
		d, e = x.s.OpenTransferWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, x.model, r)
		accessDownloadError(t, d, e, "MODEL_FILE_INVALID")
	})
	t.Run("derived_ready_and_retained_lineage", func(t *testing.T) {
		x := modelReadSetup(t)
		c := accessDerived(t, x, false, "")
		got, e := x.s.DecodeDerivedVersionWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, c.ModelID)
		if e != nil || !reflect.DeepEqual(got, c) {
			t.Fatal("actual typed lineage", e)
		}
		x.exec(t, "UPDATE model_versions SET state='deleted' WHERE id=$1", "UPDATE model_versions SET state='deleted' WHERE id=?", c.ModelID)
		got, e = x.s.DecodeDerivedVersionWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, c.ModelID)
		accessCatalogError(t, got, e, "RESOURCE_NOT_FOUND")
		got, e = x.s.RetainedDerivedVersionWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, c.ModelID)
		if e != nil || !reflect.DeepEqual(got, c) {
			t.Fatal("immutable retained lineage", e)
		}
		if x.f.Store.Dialect() == transaction.MySQL {
			staged := accessDerived(t, x, false, "staged")
			got, e = x.s.RetainedDerivedVersionWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, staged.ModelID)
			accessCatalogError(t, got, e, "RESOURCE_NOT_FOUND")
		}
	})
	t.Run("invalid_catalog_scope_and_lineage_zero_projection", func(t *testing.T) {
		x := modelReadSetup(t)
		for _, mode := range []string{"scope", "unknown_field", "lineage_fields"} {
			c := accessDerived(t, x, false, mode)
			got, e := x.s.DecodeDerivedVersionWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, c.ModelID)
			accessCatalogError(t, got, e, "MODEL_DERIVED_CATALOG_INVALID")
		}
		c := accessDerived(t, x, false, "")
		other := c
		other.ReportSHA256 = strings.Repeat("e", 64)
		record, _ := other.CanonicalBytes()
		got, e := x.s.DecodeDerivedVersionWithin(x.ctx, accessLineageExecutor{x.f.Store, record}, x.f.Tenant, x.f.Workspace, c.ModelID)
		accessCatalogError(t, got, e, "MODEL_DERIVED_CATALOG_INVALID")
		got, e = x.s.DecodeDerivedVersionWithin(x.ctx, x.f.Store, x.f.Tenant, uuid.NewString(), x.model)
		accessCatalogError(t, got, e, "RESOURCE_NOT_FOUND")
	})
	t.Run("derived_command_and_range_refusal", func(t *testing.T) {
		x := modelReadSetup(t)
		c := accessDerived(t, x, false, "")
		cmd := accessCommand(c)
		r := accessRequest(c, "model.safetensors", 0)
		for _, change := range []func(*wire.ModelFileRequest){func(r *wire.ModelFileRequest) { r.File = "../model.safetensors" }, func(r *wire.ModelFileRequest) { r.Offset = 1 }, func(r *wire.ModelFileRequest) { r.Length++ }} {
			bad := r
			change(&bad)
			d, e := x.s.OpenDerivedTransferWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, cmd, bad)
			accessDownloadError(t, d, e, "VALIDATION_FAILED")
		}
		stale := cmd
		stale.CatalogDigest = strings.Repeat("f", 64)
		d, e := x.s.OpenDerivedTransferWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, stale, r)
		accessDownloadError(t, d, e, "VALIDATION_FAILED")
		wrong := cmd
		copyIdentity := *cmd.Derived
		wrong.Derived = &copyIdentity
		wrong.Derived.Catalog = c
		wrong.Derived.Catalog.Scope.WorkspaceID = uuid.NewString()
		d, e = x.s.OpenDerivedTransferWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, wrong, r)
		accessDownloadError(t, d, e, "RESOURCE_NOT_FOUND")
		// A valid, different command catalog passes protocol validation but cannot
		// substitute for the exact immutable DB digest.
		changed := c
		changed.ReportSHA256 = strings.Repeat("f", 64)
		d, e = x.s.OpenDerivedTransferWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, accessCommand(changed), r)
		accessDownloadError(t, d, e, "MODEL_DERIVED_CATALOG_INVALID")
	})
	t.Run("derived_metadata_file_mode_link_missing_refusal", func(t *testing.T) {
		x := modelReadSetup(t)
		accessRoot(t, x)
		c := accessDerived(t, x, false, "")
		accessPrepare(t, x, c, false)
		r := accessRequest(c, "model.safetensors", 0)
		cmd := accessCommand(c)
		d, e := x.s.OpenDerivedTransferWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, cmd, r)
		if e != nil {
			t.Fatal(e)
		}
		accessRange(t, d, r)
		dir := filepath.Join(x.s.RootDirectory, "derived", c.ModelID)
		metadata := filepath.Join(dir, "projectone-derived-catalog.json")
		original, e := os.ReadFile(metadata)
		if e != nil {
			t.Fatal(e)
		}
		refuse := func() {
			d, e := x.s.OpenDerivedTransferWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, cmd, r)
			accessDownloadError(t, d, e, "MODEL_DERIVED_STORAGE_INVALID")
		}
		if e = os.Chmod(metadata, 0600); e != nil {
			t.Fatal(e)
		}
		refuse()
		if e = os.Chmod(metadata, 0444); e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(dir, r.File)
		link := filepath.Join(x.s.RootDirectory, "owned-hardlink")
		if e = os.Link(path, link); e != nil {
			t.Fatal(e)
		}
		refuse()
		if e = os.Remove(link); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(dir, 0700); e != nil {
			t.Fatal(e)
		}
		refuse()
		if e = os.Remove(metadata); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(dir, 0555); e != nil {
			t.Fatal(e)
		}
		refuse()
		if e = os.Chmod(dir, 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(metadata, bytes.Repeat([]byte("x"), len(original)), 0444); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(dir, 0555); e != nil {
			t.Fatal(e)
		}
		refuse()
	})
	t.Run("real_actor_scope_and_context_boundaries", func(t *testing.T) {
		x := modelReadSetup(t)
		baseline := x.audits(t)
		accessRoot(t, x)
		r := accessBaseFile(t, x)
		open := func(ctx context.Context, q transaction.Executor) (httpserver.Download, error) {
			return x.s.OpenTransferWithin(ctx, q, x.f.Tenant, x.f.Workspace, x.model, r)
		}
		d, e := accessOwner(x, x.f.Store, x.portal, uuid.NewString(), nil, open)
		accessDownloadError(t, d, e, "RESOURCE_NOT_FOUND")
		x.exec(t, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_access_test' WHERE id=$1", "UPDATE sessions SET revoked_at=CURRENT_TIMESTAMP(6),revoke_reason='synthetic_access_test' WHERE id=?", x.admin.SessionID)
		d, e = accessOwner(x, x.f.Store, x.admin, x.f.Workspace, nil, open)
		accessDownloadError(t, d, e, "AUTH_SESSION_EXPIRED")
		ctx, cancel := context.WithCancel(x.ctx)
		cancel()
		got, e := x.s.DecodeDerivedVersionWithin(ctx, x.f.Store, x.f.Tenant, x.f.Workspace, x.model)
		accessCatalogError(t, got, e, string(transaction.Cancelled))
		if n := x.audits(t); n != baseline {
			t.Fatal("read path changed audit baseline", baseline, n)
		}
	})
	t.Run("shared_model_lock_fences_state_change", func(t *testing.T) {
		x := modelReadSetup(t)
		entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			finished <- x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
				if e := x.s.ReadyWithin(ctx, q, x.f.Tenant, x.f.Workspace, x.model); e != nil {
					return e
				}
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		}()
		select {
		case <-entered:
		case <-x.ctx.Done():
			t.Fatal("reader did not acquire lock")
		}
		writer := make(chan error, 1)
		go func() {
			_, e := x.f.Store.Exec(x.ctx, x.f.Statement("UPDATE model_versions SET state='deleted' WHERE id=$1", "UPDATE model_versions SET state='deleted' WHERE id=?", x.model))
			writer <- e
		}()
		select {
		case e := <-writer:
			close(release)
			<-finished
			t.Fatal("shared lock failed to fence writer", e)
		case <-time.After(150 * time.Millisecond):
		}
		close(release)
		if e := <-finished; e != nil {
			t.Fatal(e)
		}
		if e := <-writer; e != nil {
			t.Fatal(e)
		}
		modelReadError(t, x.s.ReadyWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, x.model), "RESOURCE_NOT_FOUND")
	})
	t.Run("cancelled_IO_returns_no_download", func(t *testing.T) {
		x := modelReadSetup(t)
		accessRoot(t, x)
		r := accessBaseFile(t, x)
		ctx, cancel := context.WithCancel(x.ctx)
		cancel()
		d, e := x.s.OpenTransferWithin(ctx, x.f.Store, x.f.Tenant, x.f.Workspace, x.model, r)
		accessDownloadError(t, d, e, string(transaction.Cancelled))
		c := accessDerived(t, x, false, "")
		d, e = x.s.OpenDerivedTransferWithin(ctx, x.f.Store, x.f.Tenant, x.f.Workspace, accessCommand(c), accessRequest(c, "model.safetensors", 0))
		accessDownloadError(t, d, e, string(transaction.Cancelled))
		accessPrepare(t, x, c, false)
		for _, derived := range []bool{false, true} {
			ctx, cancel := context.WithCancel(x.ctx)
			q := accessCancelExecutor{x.f.Store, cancel}
			if derived {
				d, e = x.s.OpenDerivedTransferWithin(ctx, q, x.f.Tenant, x.f.Workspace, accessCommand(c), accessRequest(c, "model.safetensors", 0))
			} else {
				d, e = x.s.OpenTransferWithin(ctx, q, x.f.Tenant, x.f.Workspace, x.model, r)
			}
			cancel()
			code := "MODEL_IO_FAILED"
			if derived {
				code = context.Canceled.Error()
			}
			accessDownloadError(t, d, e, code)
		}
	})
	t.Run("owner_rollback_and_unknown_closes_projection", func(t *testing.T) {
		x := modelReadSetup(t)
		accessRoot(t, x)
		r := accessBaseFile(t, x)
		var opened *os.File
		open := func(ctx context.Context, q transaction.Executor) (httpserver.Download, error) {
			d, e := x.s.OpenTransferWithin(ctx, q, x.f.Tenant, x.f.Workspace, x.model, r)
			opened = d.File
			return d, e
		}
		for _, scenario := range []struct {
			store transaction.Store
			abort error
			code  string
		}{{x.f.Store, errors.New("EXPLICIT_TEST_OWNER_ROLLBACK"), "EXPLICIT_TEST_OWNER_ROLLBACK"}, {modelUnknownStore{x.f.Store}, nil, string(transaction.CommitUnknown)}} {
			d, e := accessOwner(x, scenario.store, x.admin, x.f.Workspace, scenario.abort, open)
			accessDownloadError(t, d, e, scenario.code)
			if opened == nil {
				t.Fatal("must reach real file before outcome loss")
			}
			if _, e = opened.Stat(); !errors.Is(e, os.ErrClosed) {
				t.Fatal("failed owner retained descriptor", e)
			}
		}
	})
	t.Run("actual_CPU_files_range_restart", func(t *testing.T) {
		x := modelReadSetup(t)
		accessRoot(t, x)
		c := accessDerived(t, x, true, "")
		accessPrepare(t, x, c, true)
		cmd := accessCommand(c)
		for _, offset := range []int64{0, wire.ModelTransferChunkBytes} {
			r := accessRequest(c, "model.safetensors", offset)
			d, e := x.s.OpenDerivedTransferWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, cmd, r)
			if e != nil {
				t.Fatal(e)
			}
			accessRange(t, d, r)
		}
		// A new service instance/transaction reopens the retained private files. This
		// is object/connection recovery, not an OS process or database restart.
		restarted := *x.s
		x.s = &restarted
		if e := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			got, e := x.s.DecodeDerivedVersionWithin(ctx, q, x.f.Tenant, x.f.Workspace, c.ModelID)
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(got, c) {
				return errors.New("RESTARTED_CATALOG_CHANGED")
			}
			r := accessRequest(c, "model.safetensors", wire.ModelTransferChunkBytes)
			d, e := x.s.OpenDerivedTransferWithin(ctx, q, x.f.Tenant, x.f.Workspace, cmd, r)
			if e != nil {
				return e
			}
			accessRange(t, d, r)
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("nil_and_mixed_dialect_boundaries", func(t *testing.T) {
		x := modelReadSetup(t)
		var missing *models.Service
		modelReadError(t, missing.ReadyWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, x.model), "STORAGE_UNAVAILABLE")
		modelReadError(t, x.s.ReadyWithin(nil, x.f.Store, x.f.Tenant, x.f.Workspace, x.model), "STORAGE_UNAVAILABLE")
		modelReadError(t, x.s.ReadyWithin(x.ctx, nil, x.f.Tenant, x.f.Workspace, x.model), "STORAGE_UNAVAILABLE")
		other := transaction.MySQL
		if x.f.Store.Dialect() == other {
			other = transaction.PostgreSQL
		}
		modelReadError(t, x.s.ReadyWithin(x.ctx, accessDialect{x.f.Store, other}, x.f.Tenant, x.f.Workspace, x.model), "MODEL_STORAGE_MISMATCH")
		got, e := x.s.DecodeDerivedVersionInTransaction(x.ctx, nil, x.f.Tenant, x.f.Workspace, x.model)
		accessCatalogError(t, got, e, "STORAGE_UNAVAILABLE")
		modelReadError(t, x.s.ReadyInTransaction(x.ctx, nil, x.f.Tenant, x.f.Workspace, x.model), "STORAGE_UNAVAILABLE")
		d, e := x.s.OpenTransfer(x.ctx, nil, x.f.Tenant, x.f.Workspace, x.model, wire.ModelFileRequest{})
		accessDownloadError(t, d, e, "STORAGE_UNAVAILABLE")
		d, e = x.s.OpenDerivedTransfer(x.ctx, nil, x.f.Tenant, x.f.Workspace, wire.InferenceCommand{}, wire.ModelFileRequest{})
		accessDownloadError(t, d, e, "STORAGE_UNAVAILABLE")
		f, _, e := x.s.OpenVerificationAsset(x.ctx, nil, x.f.Tenant, x.f.Workspace, x.model, "config.json", "")
		modelReadError(t, e, "STORAGE_UNAVAILABLE")
		if f != nil {
			f.Close()
			t.Fatal("nil tx opened file")
		}
	})
}
