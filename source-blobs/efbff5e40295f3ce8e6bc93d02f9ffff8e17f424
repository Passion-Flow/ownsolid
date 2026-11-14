package training

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	"github.com/jackc/pgx/v5"
)

const jobFields = `id::text,workspace_id::text,node_id::text,current_node_id::text,model_id::text,dataset_id::text,dataset_version_id::text,gpu_uuid,configuration,configuration_sha256,execution_recipe,dataset_manifest_sha256,model_catalog_sha256,created_by::text,reason,state,wait_code,revision,attempt_number,current_attempt_id::text,accepted_artifact_id::text,cancel_requested_at,error_code,created_at,updated_at,finished_at,pending_recovery_id::text`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	var cfg, recipe []byte
	e := row.Scan(&j.ID, &j.WorkspaceID, &j.NodeID, &j.CurrentNodeID, &j.ModelID, &j.DatasetID, &j.DatasetVersionID, &j.GPUUUID, &cfg, &j.ConfigurationSHA256, &recipe, &j.DatasetManifestSHA256, &j.ModelCatalogSHA256, &j.CreatedBy, &j.Reason, &j.State, &j.WaitCode, &j.Revision, &j.AttemptNumber, &j.CurrentAttemptID, &j.AcceptedArtifactID, &j.CancelRequestedAt, &j.ErrorCode, &j.CreatedAt, &j.UpdatedAt, &j.FinishedAt, &j.PendingRecoveryID)
	if errors.Is(e, pgx.ErrNoRows) {
		return Job{}, fail("TRAINING_NOT_FOUND")
	}
	if e != nil {
		return Job{}, e
	}
	if wire.DecodeStrict(cfg, &j.Configuration) != nil || j.Configuration.Validate() != nil || wire.DecodeStrict(recipe, &j.Recipe) != nil || j.Recipe.Validate() != nil {
		return Job{}, fail("TRAINING_DATA_INVALID")
	}
	digest, e := j.Configuration.Digest()
	if e != nil || digest != j.ConfigurationSHA256 {
		return Job{}, fail("TRAINING_DATA_INVALID")
	}
	switch j.State {
	case "queued", "preparing", "running", "cancelling", "succeeded", "cancelled", "failed", "recovery_required":
	default:
		return Job{}, fail("TRAINING_DATA_INVALID")
	}
	// Older jobs could reach a terminal state with the queued recovery pointer
	// still set. The immutable recovery record remains available in history.
	if terminal(j.State) {
		j.PendingRecoveryID = nil
	}
	j.CreatedAt, j.UpdatedAt = j.CreatedAt.UTC(), j.UpdatedAt.UTC()
	for _, stamp := range []**time.Time{&j.CancelRequestedAt, &j.FinishedAt} {
		if *stamp != nil {
			value := (*stamp).UTC()
			*stamp = &value
		}
	}
	return j, nil
}
func lockedJob(ctx context.Context, tx pgx.Tx, w workspaces.Workspace, id string) (Job, error) {
	if !datawire.ValidID(id) {
		return Job{}, fail("TRAINING_NOT_FOUND")
	}
	return scanJob(tx.QueryRow(ctx, "SELECT "+jobFields+" FROM training_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", w.TenantID, w.ID, id))
}
func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, id string) (Job, error) {
	var out Job
	e := s.trainingActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, e = lockedJobWithin(ctx, tx, w, id)
		return e
	})
	if e != nil {
		return Job{}, e
	}
	return out, nil
}
func (s *Service) List(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[Job], error) {
	out := Page[Job]{Items: []Job{}}
	if _, e := s.trainingRepository(); e != nil {
		return out, trainingRepositoryError(e)
	}
	query := url.Values{}
	for key, values := range values {
		query[key] = append([]string{}, values...)
	}
	state := ""
	if states, ok := query["state"]; ok {
		if len(states) != 1 {
			return out, fail("VALIDATION_FAILED")
		}
		state = states[0]
		switch state {
		case "queued", "preparing", "running", "cancelling", "succeeded", "cancelled", "failed", "recovery_required":
		default:
			return out, fail("VALIDATION_FAILED")
		}
		query.Del("state")
	}
	if _, present := query["limit"]; !present {
		query.Set("limit", strconv.Itoa(s.Identity.Config.Policy().UI.PageSize))
	}
	limit, cursor, scope, e := pagination.Parse(query, "training:jobs:"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace+":"+state)
	if e != nil {
		return out, e
	}
	e = s.trainingActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		args := []any{w.TenantID, w.ID}
		sql := "SELECT " + jobFields + " FROM training_jobs WHERE tenant_id=$1 AND workspace_id=$2"
		my := "SELECT " + mysqlAcceptedJobFields + " FROM training_jobs WHERE tenant_id=? AND workspace_id=?"
		if state != "" {
			args = append(args, state)
			sql += " AND state=$" + strconv.Itoa(len(args))
			my += " AND state=?"
		}
		if cursor.ID != "" {
			args = append(args, cursor.Time, cursor.ID)
			sql += " AND (created_at,id)<($" + strconv.Itoa(len(args)-1) + ",$" + strconv.Itoa(len(args)) + "::uuid)"
			my += " AND (created_at,id)<(?,?)"
		}
		args = append(args, limit+1)
		sql += " ORDER BY created_at DESC,id DESC LIMIT $" + strconv.Itoa(len(args))
		my += " ORDER BY created_at DESC,id DESC LIMIT ?"
		rows, e := tx.Query(ctx, acceptedAdapterSQL(tx, sql, my, args...))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			j, e := scanJob(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, j)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			last := out.Items[len(out.Items)-1]
			out.NextCursor = pagination.Encode(last.CreatedAt, last.ID, scope)
		}
		return nil
	})
	if e != nil {
		return Page[Job]{Items: []Job{}}, e
	}
	return out, nil
}
