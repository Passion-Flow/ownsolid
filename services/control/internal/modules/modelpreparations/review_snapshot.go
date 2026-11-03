package modelpreparations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type reviewSnapshot struct {
	expected                 outputExpectation
	descriptor               UploadDescriptor
	raw                      []byte
	version                  datasets.Version
	stopSHA                  string
	tokenizer, configuration *os.File
}

func (s *reviewSnapshot) Close() {
	for _, file := range []*os.File{s.tokenizer, s.configuration, s.expected.BaseWeights} {
		if file != nil {
			file.Close()
		}
	}
}

func (s *Service) reviewSnapshot(ctx context.Context, tx pgx.Tx, a identity.Auth, w workspaces.Workspace, id string, input ReviewRequest, files bool) (reviewSnapshot, error) {
	return s.reviewSnapshotWithin(ctx, transaction.PostgreSQLTransaction(tx), a, w, id, input, files)
}

func (s *Service) reviewSnapshotWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, id string, input ReviewRequest, files bool) (reviewSnapshot, error) {
	var out reviewSnapshot
	if s == nil || s.Nodes == nil || q == nil {
		return out, fail("MODEL_PREPARATION_UNAVAILABLE")
	}
	p, e := lockedPreparationWithin(ctx, q, w, id)
	if e != nil {
		return out, e
	}
	if p.Revision != input.ExpectedRevision {
		return out, fail("MODEL_PREPARATION_REVISION_CONFLICT")
	}
	if p.State != "awaiting_review" || p.CurrentAttemptID == nil || p.PendingRecoveryID != nil {
		return out, fail("MODEL_PREPARATION_REVIEW_NOT_ALLOWED")
	}
	var task, node, commandSHA, attemptState string
	var number int
	var outcome, stop []byte
	var outcomeSHA, stopSHA *string
	var events int64
	e = preparationIDRow(preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationDM(`SELECT "TASK_ID","NODE_ID","COMMAND_SHA256","ATTEMPT_NUMBER","STATE","OUTCOME_BYTES","OUTCOME_SHA256","PHYSICAL_STOP_RECEIPT","PHYSICAL_STOP_SHA256","LAST_EVENT_SEQUENCE" FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "PREPARATION_ID"=? AND "ID"=? AND "PHYSICAL_STOPPED_AT" IS NOT NULL FOR UPDATE`), `SELECT task_id::text,node_id::text,command_sha256,attempt_number,state,outcome_bytes,outcome_sha256,physical_stop_receipt,physical_stop_sha256,last_event_sequence FROM model_preparation_attempts WHERE tenant_id=$1 AND workspace_id=$2 AND preparation_id=$3 AND id=$4 AND physical_stopped_at IS NOT NULL FOR UPDATE`, `SELECT task_id,node_id,command_sha256,attempt_number,state,outcome_bytes,outcome_sha256,physical_stop_receipt,physical_stop_sha256,last_event_sequence FROM model_preparation_attempts WHERE tenant_id=? AND workspace_id=? AND preparation_id=? AND id=? AND physical_stopped_at IS NOT NULL FOR UPDATE`, w.TenantID, w.ID, p.ID, *p.CurrentAttemptID)), 0, 1).Scan(&task, &node, &commandSHA, &number, &attemptState, &outcome, &outcomeSHA, &stop, &stopSHA, &events)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, fail("MODEL_PREPARATION_PHYSICAL_STOP_REQUIRED")
	}
	if e != nil {
		return out, e
	}
	n, e := s.Nodes.PreparationHistoryWithin(ctx, q, w.TenantID, w.ID, node)
	if e != nil {
		return out, e
	}
	_, command, e := s.Nodes.PreparationStopTaskWithin(ctx, q, n, task)
	if e != nil {
		return out, e
	}
	digest, e := command.Digest()
	if e != nil || bindFrozenCommand(p, command) != nil || digest != commandSHA || number != p.AttemptNumber || node != p.CurrentNodeID || attemptState != "prepared" || outcomeSHA == nil || *outcomeSHA != input.ReportSHA256 || hash(outcome) != *outcomeSHA || stopSHA == nil {
		return out, fail("MODEL_PREPARATION_REVIEW_BINDING_INVALID")
	}
	var receipt wire.StopReceipt
	if wire.DecodeStrict(stop, &receipt) != nil || receipt.ValidateFor(command, node, task) != nil || receipt.Proof != "owned_container_removed" || receipt.AgentErrorCode != "" || receipt.OutcomeSHA256 == nil || *receipt.OutcomeSHA256 != *outcomeSHA {
		return out, fail("MODEL_PREPARATION_PHYSICAL_STOP_REQUIRED")
	}
	canonicalStop, _ := json.Marshal(receipt)
	if hash(canonicalStop) != *stopSHA {
		return out, fail("MODEL_PREPARATION_REVIEW_BINDING_INVALID")
	}
	var gpuNode, gpu, state string
	var revision int
	if e = preparationIDRow(preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationDM(`SELECT "NODE_ID","GPU_UUID","STATE","REVISION" FROM "GPU_LEASES" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "MODEL_PREPARATION_ID"=? FOR UPDATE`), "SELECT node_id::text,gpu_uuid,state,revision FROM gpu_leases WHERE tenant_id=$1 AND workspace_id=$2 AND model_preparation_id=$3 FOR UPDATE", "SELECT node_id,gpu_uuid,state,revision FROM gpu_leases WHERE tenant_id=? AND workspace_id=? AND model_preparation_id=? FOR UPDATE", w.TenantID, w.ID, p.ID)), 0).Scan(&gpuNode, &gpu, &state, &revision); e != nil {
		return out, e
	}
	if gpuNode != node || gpu != command.GPUUUID || revision != command.AttemptNumber || state != "released" {
		return out, fail("MODEL_PREPARATION_PHYSICAL_STOP_REQUIRED")
	}
	var descriptor []byte
	var artifact, manifest string
	e = preparationIDRow(preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationDM(`SELECT "ID","DESCRIPTOR","REPORT_BYTES","MANIFEST_SHA256" FROM "MODEL_PREPARATION_ARTIFACTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "PREPARATION_ID"=? AND "ATTEMPT_ID"=? FOR UPDATE`), `SELECT id::text,descriptor,report_bytes,manifest_sha256 FROM model_preparation_artifacts WHERE tenant_id=$1 AND workspace_id=$2 AND preparation_id=$3 AND attempt_id=$4 FOR SHARE`, `SELECT id,descriptor,report_bytes,manifest_sha256 FROM model_preparation_artifacts WHERE tenant_id=? AND workspace_id=? AND preparation_id=? AND attempt_id=? FOR SHARE`, w.TenantID, w.ID, p.ID, command.AttemptID)), 0).Scan(&artifact, &descriptor, &out.raw, &manifest)
	if e != nil {
		return out, fail("MODEL_PREPARATION_ARTIFACT_INVALID")
	}
	if artifact != input.ArtifactID || manifest != input.ReportSHA256 || !bytes.Equal(out.raw, outcome) || wire.DecodeStrict(descriptor, &out.descriptor) != nil || out.descriptor.Validate(wire.MaximumModelBytes, wire.MaximumModelFiles) != nil || out.descriptor.UploadID != artifact || out.descriptor.Scope != command.Scope || out.descriptor.AttemptID != command.AttemptID || out.descriptor.ManifestSHA256 != manifest {
		return out, fail("MODEL_PREPARATION_REVIEW_BINDING_INVALID")
	}
	out.expected = outputExpectation{Preparation: p, Command: command}
	if wire.DecodeStrict(out.raw, &out.expected.Report) != nil {
		return out, fail("MODEL_PREPARATION_REPORT_INVALID")
	}
	report := out.expected.Report
	gate, gateError := wire.QualityGate(report.Base.Correct, report.Candidate.Correct, report.RowCount, p.Configuration.MinimumAccuracyBasisPoints, p.Configuration.MaximumRegressionBasisPoints)
	if report.Status != "prepared" || !report.GatePassed || gateError != nil || !gate || report.EventCount != events || report.PreparationID != p.ID || report.AttemptID != command.AttemptID || report.ConfigurationSHA256 != p.ConfigurationSHA256 {
		return out, fail("MODEL_PREPARATION_QUALITY_FAILED")
	}
	out.stopSHA = *stopSHA
	if !files {
		return out, nil
	}
	sources, version, e := s.retainedPreparationSourcesWithin(ctx, q, w, p.TrainingJobID, p.HeldoutID)
	if e != nil {
		return out, e
	}
	if sources.digest() != p.InputSHA256 {
		return out, fail("MODEL_PREPARATION_INPUT_CHANGED")
	}
	out.version = version
	out.tokenizer, _, e = s.Models.OpenReviewAssetWithin(ctx, q, a, w, p.ModelID, "tokenizer_config.json")
	if e != nil {
		out.Close()
		return out, e
	}
	out.configuration, _, e = s.Models.OpenReviewAssetWithin(ctx, q, a, w, p.ModelID, "config.json")
	if e != nil {
		out.Close()
		return out, e
	}
	out.expected.BaseWeights, _, e = s.Models.OpenReviewAssetWithin(ctx, q, a, w, p.ModelID, "model.safetensors")
	if e != nil {
		out.Close()
		return out, e
	}
	return out, nil
}

func (s *Service) verifyReviewSnapshot(ctx context.Context, snapshot reviewSnapshot) (outputExpectation, error) {
	return s.verifyRetainedOutput(ctx, snapshot.expected, snapshot.version, snapshot.raw, snapshot.tokenizer, snapshot.configuration)
}

func reviewCatalog(snapshot reviewSnapshot) wire.DerivedCatalog {
	p, d := snapshot.expected.Preparation, snapshot.descriptor
	files := append(d.Files[:0:0], d.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return wire.DerivedCatalog{SchemaVersion: 1, Kind: "merged_lora", Scope: d.Scope, ModelID: d.UploadID, BaseModelID: p.ModelID, BaseCatalogSHA256: wire.ApprovedModelCatalogSHA256, AttemptID: d.AttemptID, ArtifactID: d.UploadID, ReportSHA256: d.ManifestSHA256, RecipeSHA256: wire.RecipeDigest(), BaseRepository: wire.ApprovedBaseRepository, BaseRevision: wire.ApprovedBaseRevision, License: wire.ApprovedBaseLicense, Format: "safetensors", Architecture: wire.ApprovedBaseArchitecture, ModelType: wire.ApprovedBaseModelType, Precision: "BF16", MaximumContextTokens: wire.MaximumContextTokens, Files: files}
}
