package training

import (
	"context"
	"net/url"
	"strconv"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type Capabilities struct {
	Enabled               bool               `json:"enabled"`
	CanCreate             bool               `json:"can_create"`
	ConfigurationDefaults wire.Configuration `json:"configuration_defaults"`
	Resources             wire.Resources     `json:"resources"`
}

type Target struct {
	NodeID         string                `json:"node_id"`
	GPUs           []agentwire.NVIDIAGPU `json:"gpus"`
	ObservedAt     time.Time             `json:"observed_at"`
	ProbeErrorCode *string               `json:"probe_error_code"`
}

// Target exposes current owning-module accelerator observations within the reader's Workspace, never node credentials or commands.
func (s *Service) Target(ctx context.Context, a identity.Auth, workspace, node string) (Target, error) {
	var out Target
	e := s.trainingActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if s.Nodes == nil {
			return fail("TRAINING_UNAVAILABLE")
		}
		if s.Inference != nil && s.Inference.Nodes != s.Nodes {
			return fail("TRAINING_STORAGE_MISMATCH")
		}
		n, e := s.Nodes.TrainingTargetWithin(ctx, tx, w.TenantID, w.ID, node)
		if e != nil {
			return e
		}
		if s.Inference == nil {
			return fail("TRAINING_PREFLIGHT_REQUIRED")
		}
		o, e := s.Inference.HardwareWithin(ctx, tx, n)
		if e != nil {
			return e
		}
		out = Target{NodeID: n.ID, GPUs: append([]agentwire.NVIDIAGPU{}, o.GPUs...), ObservedAt: o.ObservedAt}
		if o.ProbeErrorCode != "" {
			out.ProbeErrorCode = &o.ProbeErrorCode
		}
		return nil
	})
	if e != nil {
		return Target{}, e
	}
	return out, nil
}

func (s *Service) Capabilities(ctx context.Context, a identity.Auth, workspace string) (Capabilities, error) {
	var out Capabilities
	e := s.trainingActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		enabled := s.Identity.Config.Training.Enabled() && s.Store != nil
		out = Capabilities{Enabled: enabled, CanCreate: enabled && owner(a, w), ConfigurationDefaults: wire.DefaultConfiguration(), Resources: s.policy().Resources}
		return nil
	})
	if e != nil {
		return Capabilities{}, e
	}
	return out, nil
}

type Attempt struct {
	ID                 string     `json:"id"`
	JobID              string     `json:"job_id"`
	NodeID             string     `json:"node_id"`
	TaskID             *string    `json:"task_id"`
	AttemptNumber      int        `json:"attempt_number"`
	State              string     `json:"state"`
	ResumeCheckpointID *string    `json:"resume_checkpoint_id"`
	LastEventSequence  int64      `json:"last_event_sequence"`
	LastGlobalStep     int64      `json:"last_global_step"`
	ErrorCode          *string    `json:"error_code"`
	PhysicalStoppedAt  *time.Time `json:"physical_stopped_at"`
	PhysicalStopProof  *string    `json:"physical_stop_proof"`
	CreatedAt          time.Time  `json:"created_at"`
	StartedAt          *time.Time `json:"started_at"`
	FinishedAt         *time.Time `json:"finished_at"`
}
type Checkpoint struct {
	ID            string    `json:"id"`
	JobID         string    `json:"job_id"`
	AttemptID     string    `json:"attempt_id"`
	GlobalStep    int64     `json:"global_step"`
	ReceiptSHA256 string    `json:"receipt_sha256"`
	CreatedAt     time.Time `json:"created_at"`
}
type Recovery struct {
	ID                string    `json:"id"`
	JobID             string    `json:"job_id"`
	PreviousAttemptID *string   `json:"previous_attempt_id"`
	NextAttemptID     *string   `json:"next_attempt_id"`
	CheckpointID      *string   `json:"checkpoint_id"`
	PreviousNodeID    string    `json:"previous_node_id"`
	NextNodeID        string    `json:"next_node_id"`
	Mode              string    `json:"mode"`
	RequestedBy       string    `json:"requested_by"`
	Reason            string    `json:"reason"`
	CreatedAt         time.Time `json:"created_at"`
}

// historyPage receives only fixed module-owned selectors/tables/scanners; all caller values remain SQL parameters.
func historyPage[T any](ctx context.Context, s *Service, a identity.Auth, workspace, job, kind string, values url.Values, fields, mysqlFields, table string, scan func(transaction.Row) (T, time.Time, string, error)) (Page[T], error) {
	out := Page[T]{Items: []T{}}
	if _, e := s.trainingRepository(); e != nil {
		return out, trainingRepositoryError(e)
	}
	query := url.Values{}
	for k, v := range values {
		query[k] = append([]string{}, v...)
	}
	if _, ok := query["limit"]; !ok {
		query.Set("limit", strconv.Itoa(s.Identity.Config.Policy().UI.PageSize))
	}
	limit, cursor, scope, e := pagination.Parse(query, "training:"+kind+":"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace+":"+job)
	if e != nil {
		return out, e
	}
	e = s.trainingActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedJobWithin(ctx, tx, w, job); e != nil {
			return e
		}
		args := []any{w.TenantID, w.ID, job}
		sql := "SELECT " + fields + " FROM " + table + " WHERE tenant_id=$1 AND workspace_id=$2 AND job_id=$3"
		my := "SELECT " + mysqlFields + " FROM " + table + " WHERE tenant_id=? AND workspace_id=? AND job_id=?"
		if cursor.ID != "" {
			sql += " AND (created_at,id)<($4,$5::uuid)"
			my += " AND (created_at,id)<(?,?)"
			args = append(args, cursor.Time, cursor.ID)
		}
		args = append(args, limit+1)
		sql += " ORDER BY created_at DESC,id DESC LIMIT $" + strconv.Itoa(len(args))
		my += " ORDER BY created_at DESC,id DESC LIMIT ?"
		rows, e := tx.Query(ctx, acceptedAdapterSQL(tx, sql, my, args...))
		if e != nil {
			return e
		}
		defer rows.Close()
		var lastTime time.Time
		var lastID string
		for rows.Next() {
			item, created, id, e := scan(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, item)
			if len(out.Items) <= limit {
				lastTime = created
				lastID = id
			}
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			out.NextCursor = pagination.Encode(lastTime, lastID, scope)
		}
		return nil
	})
	if e != nil {
		return Page[T]{Items: []T{}}, e
	}
	return out, nil
}

func (s *Service) Attempts(ctx context.Context, a identity.Auth, workspace, job string, query url.Values) (Page[Attempt], error) {
	return historyPage(ctx, s, a, workspace, job, "attempts", query, `id::text,job_id::text,node_id::text,task_id::text,attempt_number,state,resume_checkpoint_id::text,last_event_sequence,last_global_step,error_code,physical_stopped_at,physical_stop_receipt->>'proof',created_at,started_at,finished_at`, `id,job_id,node_id,task_id,attempt_number,state,resume_checkpoint_id,last_event_sequence,last_global_step,error_code,physical_stopped_at,JSON_UNQUOTE(JSON_EXTRACT(physical_stop_receipt,'$.proof')),created_at,started_at,finished_at`, "training_attempts", func(row transaction.Row) (Attempt, time.Time, string, error) {
		var item Attempt
		e := row.Scan(&item.ID, &item.JobID, &item.NodeID, &item.TaskID, &item.AttemptNumber, &item.State, &item.ResumeCheckpointID, &item.LastEventSequence, &item.LastGlobalStep, &item.ErrorCode, &item.PhysicalStoppedAt, &item.PhysicalStopProof, &item.CreatedAt, &item.StartedAt, &item.FinishedAt)
		for _, stamp := range []**time.Time{&item.PhysicalStoppedAt, &item.StartedAt, &item.FinishedAt} {
			if *stamp != nil {
				v := (*stamp).UTC()
				*stamp = &v
			}
		}
		item.CreatedAt = item.CreatedAt.UTC()
		return item, item.CreatedAt, item.ID, e
	})
}
func (s *Service) Checkpoints(ctx context.Context, a identity.Auth, workspace, job string, query url.Values) (Page[Checkpoint], error) {
	return historyPage(ctx, s, a, workspace, job, "checkpoints", query, `id::text,job_id::text,attempt_id::text,global_step,receipt_sha256,created_at`, `id,job_id,attempt_id,global_step,receipt_sha256,created_at`, "training_checkpoints", func(row transaction.Row) (Checkpoint, time.Time, string, error) {
		var item Checkpoint
		e := row.Scan(&item.ID, &item.JobID, &item.AttemptID, &item.GlobalStep, &item.ReceiptSHA256, &item.CreatedAt)
		item.CreatedAt = item.CreatedAt.UTC()
		return item, item.CreatedAt, item.ID, e
	})
}
func (s *Service) Recoveries(ctx context.Context, a identity.Auth, workspace, job string, query url.Values) (Page[Recovery], error) {
	return historyPage(ctx, s, a, workspace, job, "recoveries", query, `id::text,job_id::text,previous_attempt_id::text,next_attempt_id::text,checkpoint_id::text,previous_node_id::text,next_node_id::text,mode,requested_by::text,reason,created_at`, `id,job_id,previous_attempt_id,next_attempt_id,checkpoint_id,previous_node_id,next_node_id,mode,requested_by,reason,created_at`, "training_recoveries", func(row transaction.Row) (Recovery, time.Time, string, error) {
		var item Recovery
		e := row.Scan(&item.ID, &item.JobID, &item.PreviousAttemptID, &item.NextAttemptID, &item.CheckpointID, &item.PreviousNodeID, &item.NextNodeID, &item.Mode, &item.RequestedBy, &item.Reason, &item.CreatedAt)
		item.CreatedAt = item.CreatedAt.UTC()
		return item, item.CreatedAt, item.ID, e
	})
}
