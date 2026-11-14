//go:build training_artifact_integration && model_lifecycle_admission_integration && model_lifecycle_output_acceptance_integration

package training_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"syscall"
)

// Actual model/base/adapter/heldout files and all acceptance/copy/decision transactions
// are real. Production identity/case/physical-stop/hardware envelopes remain explicit
// test doubles; no preparation container or NVIDIA execution is asserted.
func TestActualCpuModelPreparationFullArtifactAdminApprovalCopyAuditRetryAndDerivedInventory(t *testing.T) {
	if os.Getenv("PROJECT_ONE_BROWSER") == "1" {
		for _, locale := range []string{"en", "zh-CN"} {
			t.Run(locale, func(t *testing.T) {
				t.Setenv("PROJECT_ONE_BROWSER_LOCALE", locale)
				testActualCpuPreparationFullArtifactReview(t)
			})
		}
		return
	}
	testActualCpuPreparationFullArtifactReview(t)
}
func testActualCpuPreparationFullArtifactReview(t *testing.T) {
	if os.Getenv("PROJECT_ONE_PREPARATION_PAIRED_RESTORE") == "1" {
		fixture.PreflightOwnedPostgres(t)
	}
	g, p, l, metadata := preparationOutputFixtureBeforeClaim(t, 3000, func(g preparationScenario, p modelpreparations.Preparation) {
		if count(t, g.f, "SELECT count(*) FROM node_tasks WHERE state='running'") != 0 {
			t.Fatal("fixture base copy must precede live agent claim")
		}
		copyPreparationBase(t, g, p.ModelID)
	})
	duration := 270 * time.Second
	if os.Getenv("PROJECT_ONE_PREPARATION_PAIRED_RESTORE") == "1" {
		duration = 450 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	// Restore writability only for this newly created isolated test tree's cleanup.
	t.Cleanup(func() {
		filepath.WalkDir(filepath.Join(g.f.s.Models.RootDirectory, "derived"), func(path string, entry os.DirEntry, e error) error {
			if e == nil && entry.IsDir() {
				os.Chmod(path, 0700)
			}
			return nil
		})
	})
	heartbeat, stopHeartbeat := context.WithCancel(ctx)
	var group sync.WaitGroup
	renewalErrors := make(chan error, 1)
	group.Add(1)
	go func() {
		defer group.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeat.Done():
				return
			case <-ticker.C:
				if _, e := g.f.s.Nodes.Renew(heartbeat, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken); e != nil {
					if heartbeat.Err() == nil {
						renewalErrors <- e
					}
					return
				}
			}
		}
	}()
	defer func() { stopHeartbeat(); group.Wait() }()
	if _, e := g.f.s.Nodes.Renew(heartbeat, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken); e != nil {
		t.Fatal("initial actual lease renewal before large file IO", e)
	}
	upload, e := g.service.BeginUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, metadata)
	if e != nil {
		var state string
		var leaseRemaining, deadlineRemaining float64
		diagnostic := g.f.s.DB.QueryRow(ctx, "SELECT state,COALESCE(EXTRACT(epoch FROM lease_expires_at-clock_timestamp()),0)::float8,EXTRACT(epoch FROM deadline_at-clock_timestamp())::float8 FROM node_tasks WHERE id=$1", l.TaskID).Scan(&state, &leaseRemaining, &deadlineRemaining)
		select {
		case renewal := <-renewalErrors:
			t.Fatal("begin actual artifact", e, "renewal", renewal, "state", state, "lease_seconds", leaseRemaining, "deadline_seconds", deadlineRemaining, "diagnostic", diagnostic)
		default:
			t.Fatal("begin actual artifact", e, "state", state, "lease_seconds", leaseRemaining, "deadline_seconds", deadlineRemaining, "diagnostic", diagnostic)
		}
	}
	var total int64
	chunks := 0
	for _, asset := range upload.Files {
		file, e := os.Open(filepath.Join(os.Getenv("PROJECT_ONE_ACTUAL_PREPARATION_OUTPUT"), "model", asset.Path))
		if e != nil {
			t.Fatal(e)
		}
		for offset := int64(0); offset < asset.SizeBytes; offset += wire.TransferChunkBytes {
			length := min(wire.TransferChunkBytes, asset.SizeBytes-offset)
			raw, e := io.ReadAll(io.NewSectionReader(file, offset, length))
			if e != nil || int64(len(raw)) != length {
				file.Close()
				t.Fatal(e)
			}
			if _, e = g.service.UploadChunk(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, upload.ID, wire.ChunkRequest{Asset: asset.Path, Offset: offset, Length: length, SHA256: digest(raw)}, bytes.NewReader(raw)); e != nil {
				file.Close()
				t.Fatal(e)
			}
			total += length
			chunks++
		}
		file.Close()
	}
	if total != 1208042441 || chunks != 152 {
		t.Fatal("actual native workload mismatch", total, chunks)
	}
	if _, e = g.service.FinishUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, upload.ID); e != nil {
		select {
		case renewal := <-renewalErrors:
			t.Fatal("independent actual artifact acceptance", e, "concurrent renewal", renewal)
		default:
			t.Fatal("independent actual artifact acceptance", e)
		}
	}
	stopHeartbeat()
	group.Wait()
	select {
	case e := <-renewalErrors:
		t.Fatal(e)
	default:
	}
	receipt := preparationStopReceipt(t, g, l)
	receipt.AgentErrorCode = ""
	receipt.Proof = "owned_container_removed"
	cid := strings.Repeat("a", 64)
	receipt.ContainerID = &cid
	sha := digest(metadata)
	receipt.OutcomeSHA256 = &sha
	accepted, e := g.service.RecordStop(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, receipt, nil)
	if e != nil || accepted.State != "awaiting_review" || !accepted.GPUReleased {
		t.Fatal("explicit30% gate actual1/3 score plus synthetic positive stop", e)
	}
	p, e = g.service.Get(ctx, g.f.admin, p.WorkspaceID, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	input := modelpreparations.ReviewRequest{ExpectedRevision: p.Revision, ArtifactID: upload.ID, ReportSHA256: sha, Decision: "approve", Name: "Actual CPU artifact review fixture", Reason: "Explicit Admin review of actual native files; synthetic production and stop envelopes"}
	key := uuid.NewString()
	if _, e = g.service.Review(ctx, g.f.portal, p.WorkspaceID, p.ID, input, key, uuid.NewString()); e == nil {
		t.Fatal("owner approved")
	}
	if _, e = g.f.s.DB.Exec(ctx, `CREATE FUNCTION reject_actual_preparation_approval() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_review_approved' THEN RAISE EXCEPTION 'SYNTHETIC_APPROVAL_AUDIT_FAILURE';END IF;RETURN NEW;END;$$;CREATE TRIGGER reject_actual_preparation_approval BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_actual_preparation_approval()`); e != nil {
		t.Fatal(e)
	}
	if _, e = g.service.Review(ctx, g.f.admin, p.WorkspaceID, p.ID, input, key, uuid.NewString()); e == nil {
		t.Fatal("approval audit failure committed")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_reviews") != 0 || count(t, g.f, "SELECT count(*) FROM model_derivations") != 0 || count(t, g.f, "SELECT count(*) FROM model_versions WHERE kind='merged_lora'") != 0 || count(t, g.f, "SELECT count(*) FROM model_preparations WHERE id=$1 AND state='awaiting_review' AND review_id IS NULL", p.ID) != 1 {
		t.Fatal("post-copy rollback retained partial DB decision/model")
	}
	var report wire.PreparedResult
	if json.Unmarshal(metadata, &report) != nil {
		t.Fatal("report")
	}
	catalog := wire.DerivedCatalog{SchemaVersion: 1, Kind: "merged_lora", Scope: l.Preparation.Scope, ModelID: upload.ID, BaseModelID: l.Preparation.ModelID, BaseCatalogSHA256: wire.ApprovedModelCatalogSHA256, AttemptID: l.Preparation.AttemptID, ArtifactID: upload.ID, ReportSHA256: sha, RecipeSHA256: wire.RecipeDigest(), BaseRepository: wire.ApprovedBaseRepository, BaseRevision: wire.ApprovedBaseRevision, License: wire.ApprovedBaseLicense, Format: "safetensors", Architecture: wire.ApprovedBaseArchitecture, ModelType: wire.ApprovedBaseModelType, Precision: "BF16", MaximumContextTokens: wire.MaximumContextTokens, Files: report.ModelFiles}
	if e = g.f.s.Models.VerifyDerivedFiles(ctx, catalog); e != nil {
		t.Fatal("exact private copy after DB rollback was not retained", e)
	}
	if _, e = g.f.s.DB.Exec(ctx, "DROP TRIGGER reject_actual_preparation_approval ON audit_events"); e != nil {
		t.Fatal(e)
	}
	var review modelpreparations.ModelReview
	if os.Getenv("PROJECT_ONE_BROWSER") == "1" {
		aliasFields, verifyAlias := prepareBrowserAlias(t, g, upload.ID)
		fields := map[string]any{"preparation_id": p.ID, "artifact_id": upload.ID, "report_sha256": sha, "name": input.Name, "reason": input.Reason, "scope": "Real9-file1208042441-byte152-chunk acceptance plus full1503300328-byte approved base and actual Admin review/file verification/private copy. Native predictions1/3 pass a separate30% intent. Production/case/hardware/physical-stop envelopes are explicit doubles; no CUDA/vLLM qualification."}
		for name, value := range aliasFields {
			fields[name] = value
		}
		raw := runPreparationBrowser(t, g, "approval-flow.mjs", "actual-file-approval-browser", fields, func() func() { return derivedBrowserPhysicalDouble(t, g, upload.ID) })
		verifyAlias(raw)
		var result struct {
			Review modelpreparations.ModelReview `json:"review"`
			Key    string                        `json:"idempotency_key"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Key == "" {
			t.Fatal("actual approval browser result invalid")
		}
		review = result.Review
		key = result.Key
	} else {
		adminClient, _ := preparationHumanClients(t, g)
		adminClient.Headers = map[string]string{"Idempotency-Key": key}
		humanReview := adminClient.Write("POST", "/api/v1/admin/model-preparations/"+p.ID+"/reviews?workspace_id="+p.WorkspaceID, input, 201, "ModelPreparationReviewResponse")
		reviewRaw, _ := json.Marshal(humanReview)
		e = json.Unmarshal(reviewRaw, &review)
	}
	if e != nil || review.Decision != "approve" || review.ModelID == nil || *review.ModelID != upload.ID {
		t.Fatal("actual full-file approval retry", e)
	}
	replay, e := g.service.Review(ctx, g.f.admin, p.WorkspaceID, p.ID, input, key, uuid.NewString())
	if e != nil || replay.ID != review.ID {
		t.Fatal("exact approved decision replay", e)
	}
	version, e := g.f.s.Models.Get(ctx, g.f.portal, p.WorkspaceID, *review.ModelID)
	if e != nil || version.Kind != "merged_lora" {
		t.Fatal("scoped typed derived inventory", e)
	}
	encoded, _ := json.Marshal(version)
	var value any
	json.Unmarshal(encoded, &value)
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/models/openapi.json")
	if e = schemas["ModelVersion"].Validate(value); e != nil {
		t.Fatal("actual derived inventory schema", e)
	}
	decoded, e := wire.DecodeDerivedCatalog(version.Manifest)
	if e != nil || decoded.ReportSHA256 != sha || decoded.ModelID != upload.ID {
		t.Fatal("actual local derivation was decoded as a remote base", e)
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparations WHERE id=$1 AND state='approved' AND review_id=$2 AND derived_model_id=$3", p.ID, review.ID, upload.ID) != 1 || count(t, g.f, "SELECT count(*) FROM model_derivations WHERE model_id=$1 AND review_id=$2", upload.ID, review.ID) != 1 || count(t, g.f, "SELECT count(*) FROM audit_events WHERE action='model.preparation_review_approved'") != 1 {
		t.Fatal("immutable approved graph cardinality")
	}
	if _, e = g.f.s.DB.Exec(ctx, "UPDATE model_derivations SET report_sha256=$2 WHERE model_id=$1", upload.ID, strings.Repeat("a", 64)); e == nil {
		t.Fatal("derived provenance changed")
	}
	if _, e = g.f.s.DB.Exec(ctx, "UPDATE model_versions SET manifest='{}'::jsonb WHERE id=$1", upload.ID); e == nil {
		t.Fatal("derived identity changed")
	}
	actualDerivedTransfer(t, g, catalog)
	actualDerivedDeployment(t, g, catalog)
	if e = g.service.VerifyStored(ctx); e != nil {
		t.Fatal("actual preparation accepted-file readiness", e)
	}
	if e = g.f.s.Models.VerifyStoredVersions(ctx); e != nil {
		t.Fatal("approved derived startup verification", e)
	}
	privateRoot := func() string {
		t.Helper()
		root := t.TempDir()
		os.Chmod(root, 0700)
		t.Cleanup(func() {
			filepath.WalkDir(root, func(path string, entry os.DirEntry, e error) error {
				if e == nil && entry.IsDir() {
					os.Chmod(path, 0700)
				}
				return nil
			})
		})
		return root
	}
	snapshot := filepath.Join(privateRoot(), "model-backup")
	if e = g.f.s.Models.ExportFiles(ctx, snapshot); e != nil {
		t.Fatal("actual base and derived export", e)
	}
	rawSnapshot, e := os.ReadFile(filepath.Join(snapshot, "model-files.json"))
	if e != nil {
		t.Fatal(e)
	}
	var inventory models.FileSnapshot
	if json.Unmarshal(rawSnapshot, &inventory) != nil || inventory.SchemaVersion != 2 || len(inventory.Models) != 2 {
		t.Fatal("exact two-model snapshot2 inventory")
	}
	restored := *g.f.s.Models
	restored.RootDirectory = privateRoot()
	if e = restored.ImportFiles(ctx, snapshot); e != nil {
		t.Fatal("actual derived import", e)
	}
	if e = restored.VerifyStoredVersions(ctx); e != nil {
		t.Fatal("actual restored model restart", e)
	}
	if e = restored.ImportFiles(ctx, snapshot); e == nil {
		t.Fatal("populated model store overwritten")
	}
	original := filepath.Join(g.f.s.Models.RootDirectory, "derived", upload.ID, "config.json")
	copied := filepath.Join(restored.RootDirectory, "derived", upload.ID, "config.json")
	before, e := os.Stat(original)
	if e != nil {
		t.Fatal(e)
	}
	after, e := os.Stat(copied)
	if e != nil {
		t.Fatal(e)
	}
	if os.SameFile(before, after) || before.Sys().(*syscall.Stat_t).Nlink != 1 || after.Sys().(*syscall.Stat_t).Nlink != 1 {
		t.Fatal("snapshot import created a hardlink")
	}
	inventory.Models[0].CatalogDigest = strings.Repeat("b", 64)
	wrong, _ := json.Marshal(inventory)
	if e = os.WriteFile(filepath.Join(snapshot, "model-files.json"), wrong, 0600); e != nil {
		t.Fatal(e)
	}
	refused := *g.f.s.Models
	refused.RootDirectory = privateRoot()
	if e = refused.ImportFiles(ctx, snapshot); e == nil {
		t.Fatal("DB/catalog mismatch accepted")
	}
	entries, e := os.ReadDir(refused.RootDirectory)
	if e != nil || len(entries) != 0 {
		t.Fatal("invalid snapshot changed target", e)
	}
	if e = os.WriteFile(filepath.Join(snapshot, "model-files.json"), rawSnapshot, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(copied, 0600); e != nil {
		t.Fatal(e)
	}
	body, e := os.ReadFile(copied)
	if e != nil {
		t.Fatal(e)
	}
	changed := append([]byte(nil), body...)
	changed[0] ^= 1
	if e = os.WriteFile(copied, changed, 0600); e != nil {
		t.Fatal(e)
	}
	os.Chmod(copied, 0444)
	if e = restored.VerifyStoredVersions(ctx); e == nil {
		t.Fatal("corrupt restored derived file passed readiness")
	}
	os.Chmod(copied, 0600)
	if e = os.WriteFile(copied, body, 0600); e != nil {
		t.Fatal(e)
	}
	os.Chmod(copied, 0444)
	if e = restored.VerifyStoredVersions(ctx); e != nil {
		t.Fatal("explicit restored-file repair", e)
	}
	preparationSnapshot := filepath.Join(privateRoot(), "preparation-backup")
	if e = g.service.ExportFiles(ctx, preparationSnapshot); e != nil {
		t.Fatal("actual accepted preparation export", e)
	}
	preparationRoot := privateRoot()
	preparationRestored := *g.service
	preparationRestored.Store = nil
	if e = preparationRestored.ImportFiles(ctx, preparationSnapshot, preparationRoot); e != nil {
		t.Fatal("actual accepted preparation import", e)
	}
	preparationStore, e := modelpreparations.OpenStore(preparationRoot, p.WorkspaceID, 4096, wire.MaximumModelBytes, wire.MaximumModelFiles)
	if e == nil {
		preparationStore.Close()
		t.Fatal("foreign tenant opened preparation snapshot")
	}
	preparationStore, e = modelpreparations.OpenStore(preparationRoot, l.Preparation.Scope.TenantID, 4096, wire.MaximumModelBytes, wire.MaximumModelFiles)
	if e != nil {
		t.Fatal(e)
	}
	defer preparationStore.Close()
	preparationRestored.Store = preparationStore
	if e = preparationRestored.VerifyStored(ctx); e != nil {
		t.Fatal("actual restored preparation startup", e)
	}
	if preparationStore.Namespace() != g.store.Namespace() {
		t.Fatal("accepted snapshot changed namespace")
	}
	metadataSnapshot, e := os.ReadFile(filepath.Join(preparationSnapshot, "snapshot.json"))
	if e != nil {
		t.Fatal(e)
	}
	var actualFiles struct {
		Files []modelpreparations.SnapshotFile `json:"files"`
	}
	if json.Unmarshal(metadataSnapshot, &actualFiles) != nil || len(actualFiles.Files) != 12 {
		t.Fatal("actual9model/report/publication/namespace snapshot count")
	}
	t.Log("Actual accepted preparation12-file snapshot with9 model files1208042441bytes plus original report/publication/namespace, exact namespace import/full hashes/restart. Upload chunks excluded.")

	if os.Getenv("PROJECT_ONE_PREPARATION_PAIRED_RESTORE") == "1" {
		if preparationPairedRestoreProfile == nil {
			t.Fatal("native operator profile build tag required")
		}
		preparationPairedRestoreProfile(t, g)
	}
	actualDerivedOperatorReceipt(t, g, catalog)
	t.Log("Actual Admin TLS approval, accepted preparation hashes, mixed base/derived schema2 ordinary-file export/import/restart, no hardlinks/overwrite, DB mismatch before mutation and corruption/repair. No physical target qualification.")

	t.Log("Real9-file1208042441-byte152-chunk full independent artifact acceptance, explicit30% gate, actual private copy, post-copy audit rollback/retry, Admin decision, deferred graph and schema-valid scoped inventory. Production/case/physical-stop/hardware observations remain test doubles; no CUDA/serving qualification.")
}
