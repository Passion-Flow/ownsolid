package modelpreparations

import (
	"database/sql"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type PreparationCapabilities struct {
	CanCancelOwn          bool               `json:"can_cancel_own"`
	ExecutionConfigured   bool               `json:"execution_configured"`
	CanCreate             bool               `json:"can_create"`
	CanManage             bool               `json:"can_manage"`
	CanReview             bool               `json:"can_review"`
	NodeProfile           string             `json:"node_profile"`
	ProtocolVersion       string             `json:"protocol_version"`
	ConfigurationDefaults wire.Configuration `json:"configuration_defaults"`
	MaximumPreparations   int                `json:"maximum_preparations"`
	MaximumActive         int                `json:"maximum_active"`
	MaximumAttempts       int                `json:"maximum_attempts"`
}

func (s *Service) PreparationCapabilities(ctx context.Context, a identity.Auth, workspace string) (PreparationCapabilities, error) {
	var out PreparationCapabilities
	e := s.preparationActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		_, recipeError := s.executionRecipe()
		out.ExecutionConfigured = recipeError == nil
		out.CanManage = owner(a, w)
		out.CanCancelOwn = out.CanManage || a.Me.Audience == "portal" && w.Role != nil && *w.Role == "member"
		out.CanCreate = out.ExecutionConfigured && out.CanManage
		out.CanReview = admin(a)
		out.NodeProfile = wire.NodeProfile
		out.ProtocolVersion = wire.NodeProtocolVersion
		out.ConfigurationDefaults = wire.DefaultConfiguration()
		out.MaximumPreparations = s.policy().MaxPreparationsPerWorkspace
		out.MaximumActive = s.policy().MaxActivePerWorkspace
		out.MaximumAttempts = s.policy().MaxAttempts
		return nil
	})
	if e != nil {
		return PreparationCapabilities{}, e
	}
	return out, e
}

type PreparationAttempt struct {
	ID                 string     `json:"id"`
	PreparationID      string     `json:"preparation_id"`
	NodeID             string     `json:"node_id"`
	TaskID             string     `json:"task_id"`
	AttemptNumber      int        `json:"attempt_number"`
	CommandSHA256      string     `json:"command_sha256"`
	State              string     `json:"state"`
	LastEventSequence  int64      `json:"last_event_sequence"`
	OutcomeSHA256      *string    `json:"outcome_sha256"`
	PhysicalStopSHA256 *string    `json:"physical_stop_sha256"`
	ErrorCode          *string    `json:"error_code"`
	CreatedAt          time.Time  `json:"created_at"`
	StartedAt          *time.Time `json:"started_at"`
	FinishedAt         *time.Time `json:"finished_at"`
	PhysicalStoppedAt  *time.Time `json:"physical_stopped_at"`
}

const attemptFields = `id::text,preparation_id::text,node_id::text,task_id::text,attempt_number,command_sha256,state,last_event_sequence,outcome_sha256,physical_stop_sha256,error_code,created_at,started_at,finished_at,physical_stopped_at`

func scanPreparationAttempt(row transaction.Row) (PreparationAttempt, error) {
	row = preparationIDRow(row, 0, 1, 2, 3)
	var out PreparationAttempt
	e := row.Scan(&out.ID, &out.PreparationID, &out.NodeID, &out.TaskID, &out.AttemptNumber, &out.CommandSHA256, &out.State, &out.LastEventSequence, &out.OutcomeSHA256, &out.PhysicalStopSHA256, &out.ErrorCode, &out.CreatedAt, &out.StartedAt, &out.FinishedAt, &out.PhysicalStoppedAt)

	if e == nil {
		out.CreatedAt = out.CreatedAt.UTC()
		for _, stamp := range []**time.Time{&out.StartedAt, &out.FinishedAt, &out.PhysicalStoppedAt} {
			if *stamp != nil {
				value := (**stamp).UTC()
				*stamp = &value
			}
		}
	}
	return out, e
}
func (s *Service) Attempts(ctx context.Context, a identity.Auth, workspace, id string, query url.Values) (Page[PreparationAttempt], error) {
	out := Page[PreparationAttempt]{Items: []PreparationAttempt{}}
	limit, cursor, scope, e := pagination.Parse(query, "model:preparation-attempts:"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace+":"+id)
	if e != nil {
		return out, e
	}
	e = s.preparationActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if e := preparationSelectedCursor(tx, cursor); e != nil {
			return e
		}
		if _, e := lockedPreparationWithin(ctx, tx, w, id); e != nil {
			return e
		}
		args := []any{w.TenantID, w.ID, id}
		querySQL := "SELECT " + attemptFields + " FROM model_preparation_attempts WHERE tenant_id=$1 AND workspace_id=$2 AND preparation_id=$3"
		mysqlQuery := "SELECT id,preparation_id,node_id,task_id,attempt_number,command_sha256,state,last_event_sequence,outcome_sha256,physical_stop_sha256,error_code,created_at,started_at,finished_at,physical_stopped_at FROM model_preparation_attempts WHERE tenant_id=? AND workspace_id=? AND preparation_id=?"
		if cursor.ID != "" {
			args = append(args, cursor.Time, cursor.ID)
			querySQL += " AND (created_at,id)<($4,$5::uuid)"
			mysqlQuery += " AND (created_at,id)<(?,?)"
		}
		args = append(args, limit+1)
		querySQL += " ORDER BY created_at DESC,id DESC LIMIT $" + strconv.Itoa(len(args))
		mysqlQuery += " ORDER BY created_at DESC,id DESC LIMIT ?"
		rows, e := preparationQuery(ctx, tx, preparationSelectedSQL(tx, preparationNativeAttemptPage(cursor.ID != ""), querySQL, mysqlQuery, args...))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			item, e := scanPreparationAttempt(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, item)
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
		return Page[PreparationAttempt]{Items: []PreparationAttempt{}}, e
	}
	return out, e
}

type PreparationEventPage struct {
	Items     []wire.Event `json:"items"`
	NextAfter *int64       `json:"next_after"`
}

func (s *Service) Events(ctx context.Context, a identity.Auth, workspace, id, attempt string, query url.Values) (PreparationEventPage, error) {
	out := PreparationEventPage{Items: []wire.Event{}}
	limit, after := s.Identity.Config.Policy().UI.PageSize, int64(0)
	for name, values := range query {
		if len(values) != 1 || name != "limit" && name != "after" {
			return out, fail("VALIDATION_FAILED")
		}
		value, e := strconv.ParseInt(values[0], 10, 64)
		if e != nil || strconv.FormatInt(value, 10) != values[0] {
			return out, fail("VALIDATION_FAILED")
		}
		if name == "limit" {
			if value < 1 || value > 100 {
				return out, fail("VALIDATION_FAILED")
			}
			limit = int(value)
		} else {
			if value < 0 || value > wire.MaximumEvents {
				return out, fail("VALIDATION_FAILED")
			}
			after = value
		}
	}
	if !datawire.ValidID(attempt) {
		return out, fail("MODEL_PREPARATION_NOT_FOUND")
	}
	e := s.preparationActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		p, e := lockedPreparationWithin(ctx, tx, w, id)
		if e != nil {
			return e
		}
		var exists bool
		if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT CASE WHEN EXISTS(SELECT 1 FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "PREPARATION_ID"=? AND "ID"=?) THEN 1 ELSE 0 END FROM DUAL`), "SELECT EXISTS(SELECT 1 FROM model_preparation_attempts WHERE tenant_id=$1 AND workspace_id=$2 AND preparation_id=$3 AND id=$4)", `SELECT EXISTS(SELECT 1 FROM model_preparation_attempts WHERE tenant_id=? AND workspace_id=? AND preparation_id=? AND id=?)`, w.TenantID, w.ID, id, attempt)).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return fail("MODEL_PREPARATION_NOT_FOUND")
		}
		rows, e := preparationQuery(ctx, tx, preparationSelectedSQL(tx, preparationDMPage(`SELECT "SEQUENCE","KIND","PAYLOAD","CANONICAL_SHA256" FROM "MODEL_PREPARATION_EVENTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "PREPARATION_ID"=? AND "ATTEMPT_ID"=? AND "SEQUENCE">? ORDER BY "SEQUENCE"`, 101), "SELECT sequence,kind,payload,canonical_sha256 FROM model_preparation_events WHERE tenant_id=$1 AND workspace_id=$2 AND preparation_id=$3 AND attempt_id=$4 AND sequence>$5 ORDER BY sequence LIMIT $6", `SELECT sequence,kind,payload,canonical_sha256 FROM model_preparation_events WHERE tenant_id=? AND workspace_id=? AND preparation_id=? AND attempt_id=? AND sequence>? ORDER BY sequence LIMIT ?`, w.TenantID, w.ID, id, attempt, after, limit+1))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var sequence int64
			var kind, sha string
			var raw []byte
			if e = rows.Scan(&sequence, &kind, &raw, &sha); e != nil {
				return e
			}
			event, e := wire.DecodeEvent(raw, id, attempt, p.inputs.Heldout.RowCount)
			if e != nil {
				return e
			}
			canonical, _ := json.Marshal(event)
			if event.Sequence != sequence || event.Kind != kind || hash(canonical) != sha {
				return fail("MODEL_PREPARATION_DATA_INVALID")
			}
			out.Items = append(out.Items, event)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			value := out.Items[len(out.Items)-1].Sequence
			out.NextAfter = &value
		}
		return nil
	})
	if e != nil {
		return PreparationEventPage{Items: []wire.Event{}}, e
	}
	return out, e
}

type PreparationReport struct {
	PreparationID     string          `json:"preparation_id"`
	AttemptID         *string         `json:"attempt_id"`
	ArtifactID        *string         `json:"artifact_id"`
	ReportSHA256      *string         `json:"report_sha256"`
	PhysicalStoppedAt *time.Time      `json:"physical_stopped_at"`
	Result            json.RawMessage `json:"result"`
	Review            *ModelReview    `json:"review"`
}

func (s *Service) Report(ctx context.Context, a identity.Auth, workspace, id string) (PreparationReport, error) {
	var out PreparationReport
	e := s.preparationActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		p, e := lockedPreparationWithin(ctx, tx, w, id)
		if e != nil {
			return e
		}
		out.PreparationID = p.ID
		out.AttemptID = p.CurrentAttemptID
		if p.CurrentAttemptID == nil {
			return nil
		}
		var raw []byte
		var task, attemptNode string
		if e = preparationIDRow(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "OUTCOME_BYTES","OUTCOME_SHA256","PHYSICAL_STOPPED_AT","TASK_ID","NODE_ID" FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "PREPARATION_ID"=? AND "ID"=?`), "SELECT outcome_bytes,outcome_sha256,physical_stopped_at,task_id::text,node_id::text FROM model_preparation_attempts WHERE tenant_id=$1 AND workspace_id=$2 AND preparation_id=$3 AND id=$4", `SELECT outcome_bytes,outcome_sha256,physical_stopped_at,task_id,node_id FROM model_preparation_attempts WHERE tenant_id=? AND workspace_id=? AND preparation_id=? AND id=?`, w.TenantID, w.ID, id, *p.CurrentAttemptID)), 3, 4).Scan(&raw, &out.ReportSHA256, &out.PhysicalStoppedAt, &task, &attemptNode); e != nil {
			return e
		}
		if len(raw) > 0 {
			if out.ReportSHA256 == nil || hash(raw) != *out.ReportSHA256 || int64(len(raw)) > p.Recipe.Resources.MaximumResultBytes {
				return fail("MODEL_PREPARATION_REPORT_INVALID")
			}
			var status struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(raw, &status) != nil {
				return fail("MODEL_PREPARATION_REPORT_INVALID")
			}
			if status.Status == "prepared" {
				var report wire.PreparedResult
				if wire.DecodeStrict(raw, &report) != nil || report.TenantID != w.TenantID || report.WorkspaceID != w.ID || report.PreparationID != id || report.AttemptID != *p.CurrentAttemptID || report.ConfigurationSHA256 != p.ConfigurationSHA256 {
					return fail("MODEL_PREPARATION_REPORT_INVALID")
				}
				var artifact, sha string
				e = preparationIDRow(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "ID","MANIFEST_SHA256" FROM "MODEL_PREPARATION_ARTIFACTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "PREPARATION_ID"=? AND "ATTEMPT_ID"=?`), "SELECT id::text,manifest_sha256 FROM model_preparation_artifacts WHERE tenant_id=$1 AND workspace_id=$2 AND preparation_id=$3 AND attempt_id=$4", `SELECT id,manifest_sha256 FROM model_preparation_artifacts WHERE tenant_id=? AND workspace_id=? AND preparation_id=? AND attempt_id=?`, w.TenantID, w.ID, id, *p.CurrentAttemptID)), 0).Scan(&artifact, &sha)
				if e != nil || sha != *out.ReportSHA256 {
					return fail("MODEL_PREPARATION_ARTIFACT_INVALID")
				}
				out.ArtifactID = &artifact
			} else {
				n, e := s.Nodes.PreparationHistoryWithin(ctx, tx, w.TenantID, w.ID, attemptNode)
				if e != nil {
					return e
				}
				_, command, e := s.Nodes.PreparationStopTaskWithin(ctx, tx, n, task)
				if e != nil {
					return e
				}
				request, e := command.ContainerRequest()
				if e != nil {
					return e
				}
				if _, e = wire.DecodeFailedResult(raw, request); e != nil {
					return e
				}
			}
			out.Result = append(json.RawMessage(nil), raw...)
		} else if out.ReportSHA256 != nil {
			return fail("MODEL_PREPARATION_REPORT_INVALID")
		}
		review, e := scanReview(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM("SELECT "+nativeReviewFields+` FROM "MODEL_PREPARATION_REVIEWS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "PREPARATION_ID"=? AND "ATTEMPT_ID"=?`), "SELECT "+reviewFields+" FROM model_preparation_reviews WHERE tenant_id=$1 AND workspace_id=$2 AND preparation_id=$3 AND attempt_id=$4", `SELECT id,workspace_id,preparation_id,attempt_id,artifact_id,report_sha256,expected_revision,decision,model_id,model_name,reviewed_by,reason,created_at FROM model_preparation_reviews WHERE tenant_id=? AND workspace_id=? AND preparation_id=? AND attempt_id=?`, w.TenantID, w.ID, id, *p.CurrentAttemptID)))
		if e == nil {
			out.Review = &review
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		return nil
	})
	if e != nil {
		return PreparationReport{}, e
	}
	return out, e
}

// Target exposes only a fresh current-scoped compatible accelerator inventory.
func (s *Service) Target(ctx context.Context, a identity.Auth, workspace, node string) (training.Target, error) {
	var out training.Target
	e := s.preparationActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		n, e := s.Nodes.ModelPreparationTargetWithin(ctx, tx, w.TenantID, w.ID, node)
		if e != nil {
			return e
		}
		if s.Inference == nil {
			return fail("MODEL_PREPARATION_UNAVAILABLE")
		}
		observation, e := s.Inference.HardwareWithin(ctx, tx, n)
		if e != nil {
			return e
		}
		out.NodeID = n.ID
		out.GPUs = observation.GPUs
		if out.GPUs == nil {
			out.GPUs = []agentwire.NVIDIAGPU{}
		}
		out.ObservedAt = observation.ObservedAt
		if observation.ProbeErrorCode != "" {
			code := observation.ProbeErrorCode
			out.ProbeErrorCode = &code
		}
		return nil
	})
	if e != nil {
		return training.Target{}, e
	}
	return out, e
}
