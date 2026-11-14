package training

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

// Two final passes are an algorithm bound: detect a newly committed registry,
// perform one immutable analysis, then validate against all current reservations.
const trainingAdmissionPasses = 2

var trainingAdmissionScanRequired = errors.New("TRAINING_ADMISSION_SCAN_REQUIRED")

func trainingRepositoryError(e error) error {
	if e == nil {
		return nil
	}
	// A shutdown cancellation may accompany a lost COMMIT acknowledgement. Keep
	// the durable-outcome requirement visible; cancellation must not invite replay.
	if outcome := trainingCommitOutcome(e, 0); outcome != "" {
		return fail(string(outcome))
	}
	if transaction.IsCancellationOnly(e) && errors.Is(e, context.Canceled) {
		return fail(string(transaction.Cancelled))
	}
	if transaction.IsCancellationOnly(e) && errors.Is(e, context.DeadlineExceeded) {
		return fail(string(transaction.Timeout))
	}
	code := e.Error()
	if len(code) > 120 {
		return fail("STORAGE_UNAVAILABLE")
	}
	for _, c := range code {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return fail("STORAGE_UNAVAILABLE")
		}
	}
	for _, prefix := range []string{"TRAINING_", "MODEL_", "DATASET_", "NODE_", "AGENT_", "AUTH_", "STORAGE_"} {
		if strings.HasPrefix(code, prefix) {
			return fail(code)
		}
	}
	switch code {
	case "ACCESS_DENIED", "VALIDATION_FAILED", "IDEMPOTENCY_CONFLICT", "RESOURCE_NOT_FOUND":
		return fail(code)
	}
	return fail("STORAGE_UNAVAILABLE")
}

// Error graphs come from transaction owners, never from SQL text. Bound malformed
// graphs and prefer unknown over rollback when multiple shutdown failures exist.
func trainingCommitOutcome(e error, depth int) transaction.ErrorKind {
	if e == nil {
		return ""
	}
	if depth >= 64 {
		return transaction.Unavailable
	}
	if outcome, ok := e.(*transaction.Error); ok {
		if outcome.Kind == transaction.CommitUnknown || outcome.Kind == transaction.CommitRolledBack {
			return outcome.Kind
		}
	}
	if joined, ok := e.(interface{ Unwrap() []error }); ok {
		var selected transaction.ErrorKind
		for _, child := range joined.Unwrap() {
			outcome := trainingCommitOutcome(child, depth+1)
			if outcome == transaction.CommitUnknown {
				return outcome
			}
			if outcome != "" {
				selected = outcome
			}
		}
		return selected
	}
	if wrapped, ok := e.(interface{ Unwrap() error }); ok {
		return trainingCommitOutcome(wrapped.Unwrap(), depth+1)
	}
	return ""
}
func (s *Service) trainingRepository() (transaction.Store, error) {
	if s == nil || s.Identity == nil || s.Workspaces == nil || s.Workspaces.Identity != s.Identity {
		return nil, fail("TRAINING_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return nil, fail("TRAINING_STORAGE_MISMATCH")
	}
	return s.Identity.Repository()
}
func (s *Service) trainingActorTransaction(ctx context.Context, a identity.Auth, workspace string, write bool, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	if ctx == nil || !datawire.ValidID(workspace) || a.Me.Audience != "portal" && !admin(a) {
		return fail("ACCESS_DENIED")
	}
	store, e := s.trainingRepository()
	if e != nil {
		return trainingRepositoryError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().TransactionSeconds))
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if w.Status != "active" || write && !owner(a, w) {
			return fail("ACCESS_DENIED")
		}
		return fn(ctx, q, w)
	})
	if errors.Is(e, trainingAdmissionScanRequired) {
		return trainingAdmissionScanRequired
	}
	return trainingRepositoryError(e)
}
func lockedJobWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, id string) (Job, error) {
	if !datawire.ValidID(id) {
		return Job{}, fail("TRAINING_NOT_FOUND")
	}
	return scanJob(q.QueryRow(ctx, acceptedAdapterSQL(q, "SELECT "+jobFields+" FROM training_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND site_id=local_site_id() AND cell_id=local_cell_id() FOR UPDATE", "SELECT "+mysqlAcceptedJobFields+" FROM training_jobs WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE", w.TenantID, w.ID, id)))
}
func (s *Service) replayTrainingWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, key string, wanted []byte) (Job, bool, error) {
	var id string
	var previous []byte
	e := q.QueryRow(ctx, acceptedAdapterSQL(q, "SELECT id::text,creation_fingerprint FROM training_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND created_by=$3 AND creation_key=$4 FOR SHARE", "SELECT id,creation_fingerprint FROM training_jobs WHERE tenant_id=? AND workspace_id=? AND created_by=? AND creation_key=? FOR SHARE", w.TenantID, w.ID, a.Me.User.ID, key)).Scan(&id, &previous)
	if errors.Is(e, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if e != nil {
		return Job{}, false, e
	}
	if subtle.ConstantTimeCompare(wanted, previous) != 1 {
		return Job{}, false, fail("IDEMPOTENCY_CONFLICT")
	}
	j, e := lockedJobWithin(ctx, q, w, id)
	if e != nil {
		return Job{}, false, e
	}
	return j, true, nil
}
func (s *Service) trainingSourcesWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, input CreateRequest) (datasets.TrainingInputs, error) {
	if s.Nodes == nil || s.Models == nil || s.Datasets == nil {
		return datasets.TrainingInputs{}, fail("TRAINING_UNAVAILABLE")
	}
	if s.Nodes.Identity != s.Identity || s.Models.Identity != s.Identity || s.Datasets.Identity != s.Identity {
		return datasets.TrainingInputs{}, fail("TRAINING_STORAGE_MISMATCH")
	}
	if _, e := s.Nodes.TrainingTargetWithin(ctx, q, w.TenantID, w.ID, input.NodeID); e != nil {
		return datasets.TrainingInputs{}, e
	}
	if e := s.Models.ReadyWithin(ctx, q, w.TenantID, w.ID, input.ModelID); e != nil {
		return datasets.TrainingInputs{}, e
	}
	return s.Datasets.TrainingInputSnapshotWithin(ctx, q, w.TenantID, w.ID, input.DatasetID, input.DatasetVersionID, false)
}
func trainingInputLimits(input datasets.TrainingInputs, recipe ExecutionRecipe) error {
	if input.Train.SizeBytes > recipe.DataLimits.MaximumBytes || input.Validation.SizeBytes > recipe.DataLimits.MaximumBytes || input.Version.Manifest.TrainRows+input.Version.Manifest.ValidationRows > recipe.DataLimits.MaximumRows {
		return fail("TRAINING_INPUT_LIMIT_REACHED")
	}
	return nil
}
func trainingAuditWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, id, reason, request string) error {
	n, e := q.Exec(ctx, acceptedAdapterSQL(q, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,'training.created','success','training_job',$6,$7,$8)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,?,'training.created','success','training_job',?,?,?)`, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, id, reason, request))
	if e == nil && n != 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	return e
}

// Create freezes metadata briefly, verifies real files outside business locks,
// then rechecks current authority and every source before the single intent commit.
func (s *Service) Create(ctx context.Context, a identity.Auth, input CreateRequest, key, request string) (Job, error) {
	if !datawire.ValidID(key) || !datawire.ValidID(input.NodeID) || !datawire.ValidID(input.ModelID) || !datawire.ValidID(input.DatasetID) || !datawire.ValidID(input.DatasetVersionID) || !gpuPattern.MatchString(input.GPUUUID) || !validReason(input.Reason) || input.Configuration.Validate() != nil || input.Configuration.Device != wire.ProductionProfile {
		return Job{}, fail("VALIDATION_FAILED")
	}
	wanted := fingerprint(a, input)
	var out Job
	var recipe ExecutionRecipe
	var sources datasets.TrainingInputs
	replayed := false
	e := s.trainingActorTransaction(ctx, a, input.WorkspaceID, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, replayed, e = s.replayTrainingWithin(ctx, q, a, w, key, wanted)
		if e != nil || replayed {
			return e
		}
		recipe, e = s.recipe()
		if e != nil {
			return e
		}
		sources, e = s.trainingSourcesWithin(ctx, q, w, input)
		if e != nil {
			return e
		}
		if e = trainingInputLimits(sources, recipe); e != nil {
			return e
		}
		if s.Holdouts == nil {
			return s.requireEmptyHoldoutsWithin(ctx, q, w)
		}
		return nil
	})
	if e != nil {
		return Job{}, e
	}
	if replayed {
		return out, nil
	}
	if e = s.Datasets.VerifyTrainingInputSnapshot(ctx, sources); e != nil {
		return Job{}, trainingRepositoryError(e)
	}
	var admission AdmissionCheck
	// A committed registry is monotonic. One outside-lock analysis is enough:
	// its final borrowed validation checks every current reservation.
	for pass := 0; pass < trainingAdmissionPasses; pass++ {
		e = s.trainingActorTransaction(ctx, a, input.WorkspaceID, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
			var e error
			out, replayed, e = s.replayTrainingWithin(ctx, q, a, w, key, wanted)
			if e != nil || replayed {
				return e
			}
			currentRecipe, e := s.recipe()
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(recipe, currentRecipe) {
				return fail("TRAINING_RECIPE_UNAVAILABLE")
			}
			current, e := s.trainingSourcesWithin(ctx, q, w, input)
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(current, sources) {
				return fail("TRAINING_INPUT_UNAVAILABLE")
			}
			if s.Holdouts != nil {
				present, e := s.Holdouts.HasReservationsWithin(ctx, q, w)
				if e != nil {
					return e
				}
				if present && admission == nil {
					return trainingAdmissionScanRequired
				}
			}
			if admission != nil {
				if e = admission.ValidateWithin(ctx, q, w, current); e != nil {
					return e
				}
			} else if s.Holdouts == nil {
				if e = s.requireEmptyHoldoutsWithin(ctx, q, w); e != nil {
					return e
				}
			}
			var total, active int
			if e = q.QueryRow(ctx, acceptedAdapterSQL(q, `SELECT count(*),count(*) FILTER(WHERE state IN ('queued','preparing','running','cancelling','recovery_required')) FROM training_jobs WHERE tenant_id=$1 AND workspace_id=$2`, `SELECT COUNT(*),COUNT(CASE WHEN state IN ('queued','preparing','running','cancelling','recovery_required') THEN 1 END) FROM training_jobs WHERE tenant_id=? AND workspace_id=?`, w.TenantID, w.ID)).Scan(&total, &active); e != nil {
				return e
			}
			if total >= s.policy().MaxJobsPerWorkspace || active >= s.policy().MaxActivePerWorkspace {
				return fail("TRAINING_JOB_LIMIT_REACHED")
			}
			var membership *string
			if !admin(a) {
				authority, e := s.Workspaces.MembershipAuthorityWithin(ctx, q, w.TenantID, w.ID, a.Me.User.ID)
				if e != nil {
					return e
				}
				membership = &authority
			}
			cfg, _ := json.Marshal(input.Configuration)
			frozen, _ := json.Marshal(recipe)
			digest, _ := input.Configuration.Digest()
			id := uuid.NewString()
			n, e := q.Exec(ctx, acceptedAdapterSQL(q, `INSERT INTO training_jobs(id,tenant_id,workspace_id,node_id,model_id,dataset_id,dataset_version_id,gpu_uuid,configuration,configuration_sha256,execution_recipe,dataset_manifest_sha256,model_catalog_sha256,created_by,requested_by,requested_audience,requested_epoch,membership_authority,reason,creation_key,creation_fingerprint,state,current_node_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11::jsonb,$12,$13,$14,$14,$15,$16,$17,$18,$19,$20,'queued',$4)`, `INSERT INTO training_jobs(id,tenant_id,workspace_id,node_id,model_id,dataset_id,dataset_version_id,gpu_uuid,configuration,configuration_sha256,execution_recipe,dataset_manifest_sha256,model_catalog_sha256,created_by,requested_by,requested_audience,requested_epoch,membership_authority,reason,creation_key,creation_fingerprint,state,current_node_id) VALUES(?,?,?,?,?,?,?,?,CAST(? AS JSON),?,CAST(? AS JSON),?,?,?,?,?,?,?,?,?,?,'queued',?)`, trainingInsertArguments(q, id, w, a, input, cfg, digest, frozen, sources.Version.ManifestSHA256, key, membership, wanted)...))
			if e != nil {
				return e
			}
			if n != 1 {
				return fail("STORAGE_UNAVAILABLE")
			}
			if e = trainingAuditWithin(ctx, q, a, w, id, input.Reason, request); e != nil {
				return e
			}
			out, e = lockedJobWithin(ctx, q, w, id)
			return e
		})
		if errors.Is(e, trainingAdmissionScanRequired) {
			// This transaction rolled back before any intent write. It is not an
			// uncertain-commit retry. Release locks before taking the bounded scan slot.
			admission, e = s.Holdouts.PrepareTrainingAdmission(ctx, a, input)
			if e != nil {
				return Job{}, trainingRepositoryError(e)
			}
			if admission == nil {
				return Job{}, fail("MODEL_HOLDOUT_GUARD_REQUIRED")
			}
			continue
		}
		if e != nil {
			return Job{}, e
		}
		return out, nil
	}
	return Job{}, fail("MODEL_HOLDOUT_GUARD_REQUIRED")
}
func trainingInsertArguments(q transaction.Executor, id string, w workspaces.Workspace, a identity.Auth, input CreateRequest, cfg []byte, digest string, recipe []byte, manifest, key string, membership *string, fingerprint []byte) []any {
	args := []any{id, w.TenantID, w.ID, input.NodeID, input.ModelID, input.DatasetID, input.DatasetVersionID, input.GPUUUID, cfg, digest, recipe, manifest, agentwire.ModelCatalogDigest(), a.Me.User.ID}
	if q.Dialect() == transaction.MySQL {
		args = append(args, a.Me.User.ID)
	}
	args = append(args, a.Me.Audience, a.Epoch, membership, input.Reason, key, fingerprint)
	if q.Dialect() == transaction.MySQL {
		args = append(args, input.NodeID)
	}
	return args
}
