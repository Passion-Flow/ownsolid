package datasets

import (
	"context"
	"encoding/json"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
)

var errDatasetJobPolicy = failure("DATASET_POLICY_INVALID")

type jobPolicy struct {
	Limits        wire.Limits    `json:"limits"`
	Resources     wire.Resources `json:"resources"`
	Deduplication string         `json:"deduplication,omitempty"`
}

const jobFields = `error_line_number,reason,id::text,dataset_id::text,draft_id::text,workspace_id::text,kind,requested_by::text,expected_revision,state,version_id::text,input_sha256,split_seed,validation_basis_points,attempt_count,attempt_id::text,lease_expires_at,cancel_requested_at,error_code,retryable,created_at,updated_at,completed_at`

func scanJob(row transaction.Row) (Job, error) {
	var j Job
	e := row.Scan(&j.ErrorLineNumber, &j.Reason, &j.ID, &j.DatasetID, &j.DraftID, &j.WorkspaceID, &j.Kind, &j.RequestedBy, &j.ExpectedRevision, &j.State, &j.VersionID, &j.InputSHA256, &j.Seed, &j.ValidationBasisPoints, &j.AttemptCount, &j.AttemptID, &j.LeaseExpiresAt, &j.CancelRequestedAt, &j.ErrorCode, &j.Retryable, &j.CreatedAt, &j.UpdatedAt, &j.CompletedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return Job{}, failure("DATASET_NOT_FOUND")
	}
	if e != nil {
		return Job{}, e
	}
	j.CreatedAt = j.CreatedAt.UTC()
	j.UpdatedAt = j.UpdatedAt.UTC()
	if j.LeaseExpiresAt != nil {
		stamp := j.LeaseExpiresAt.UTC()
		j.LeaseExpiresAt = &stamp
	}
	if j.CancelRequestedAt != nil {
		stamp := j.CancelRequestedAt.UTC()
		j.CancelRequestedAt = &stamp
	}
	if j.CompletedAt != nil {
		stamp := j.CompletedAt.UTC()
		j.CompletedAt = &stamp
	}

	return committed(j, e)
}
func lockedJob(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, dataset, id string) (Job, error) {
	if !wire.ValidID(id) {
		return Job{}, failure("DATASET_NOT_FOUND")
	}
	return scanJob(tx.QueryRow(ctx, datasetSQL(tx, "SELECT "+jobFields+" FROM dataset_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND id=$4 FOR UPDATE", `SELECT error_line_number,reason,id,dataset_id,draft_id,workspace_id,kind,requested_by,expected_revision,state,version_id,input_sha256,split_seed,validation_basis_points,attempt_count,attempt_id,lease_expires_at,cancel_requested_at,error_code,retryable,created_at,updated_at,completed_at FROM dataset_jobs WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, dataset, id}, []any{w.TenantID, w.ID, dataset, id}, transaction.SQL(transaction.Dameng, dmJobLock, w.TenantID, w.ID, dataset, id))))
}
func (s *Service) GetJob(ctx context.Context, a identity.Auth, workspace, dataset, id string) (Job, error) {
	var out Job
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedDataset(ctx, tx, w, dataset); e != nil {
			return e
		}
		var draft string
		if e := tx.QueryRow(ctx, datasetSQL(tx, "SELECT draft_id::text FROM dataset_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND id=$4", `SELECT draft_id FROM dataset_jobs WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=?`, []any{w.TenantID, w.ID, dataset, id}, []any{w.TenantID, w.ID, dataset, id}, transaction.SQL(transaction.Dameng, dmJobDraft, w.TenantID, w.ID, dataset, id))).Scan(&draft); e != nil {
			return failure("DATASET_NOT_FOUND")
		}
		d, e := lockedDraft(ctx, tx, w, dataset, draft)
		if e != nil {
			return e
		}
		if e = draftAccess(a, w, d, false); e != nil {
			return e
		}
		out, e = lockedJob(ctx, tx, w, dataset, id)
		return e
	})
	return committed(out, e)
}
func (s *Service) jobPolicyBytes(deduplication string) []byte {
	p := s.policy()
	raw, _ := json.Marshal(jobPolicy{Limits: p.Limits, Resources: p.Resources, Deduplication: deduplication})
	return raw
}

func datasetJobInput(ctx context.Context, tx transaction.Executor, id string) (jobPolicy, string, int64, error) {
	var raw []byte
	var sourceObject string
	var sourceBytes int64
	var policy jobPolicy
	e := tx.QueryRow(ctx, datasetSQL(tx, "SELECT input_policy,source_object_id::text,expected_bytes FROM dataset_jobs JOIN dataset_drafts ON dataset_drafts.id=dataset_jobs.draft_id WHERE dataset_jobs.id=$1", `SELECT input_policy,source_object_id,expected_bytes FROM dataset_jobs JOIN dataset_drafts ON dataset_drafts.id=dataset_jobs.draft_id WHERE dataset_jobs.id=?`, []any{id}, []any{id}, transaction.SQL(transaction.Dameng, dmWorkerInput, id))).Scan(&raw, &sourceObject, &sourceBytes)
	if e != nil {
		return policy, "", 0, e
	}
	if wire.DecodeStrict(raw, &policy) != nil || policy.Limits.Validate() != nil || policy.Resources.Validate() != nil ||
		policy.Deduplication != "" && policy.Deduplication != NearPromptV1 {
		return jobPolicy{}, "", 0, errDatasetJobPolicy
	}
	return policy, sourceObject, sourceBytes, nil
}
