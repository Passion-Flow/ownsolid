package workflows

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"strconv"
	"strings"
	"time"
)

const maximumStoredGraphBytes = 1048576
const maximumStoredInstructionsBytes = 16384
const maximumStoredTopK = 100
const runFields = `r.id::text,r.workflow_id::text,r.conversation_id::text,r.version_id::text,r.draft_revision,r.graph_sha256,r.state,r.question,r.answer,r.error_code,r.cancellation_requested_at,r.created_at,r.started_at,r.finished_at,r.deadline_at,r.lease_expires_at,r.owner_id::text,r.requested_audience,r.execution_authority::text,r.membership_authority::text,r.graph_snapshot,r.evidence,r.alias_snapshot,r.usage,r.process_owner::text,r.lease_id::text,r.result_kind,r.tool_result,r.tool_proof`
const visibleRunConversation = `(r.conversation_id IS NULL OR EXISTS(SELECT 1 FROM workflow_conversations c WHERE c.tenant_id=r.tenant_id AND c.workspace_id=r.workspace_id AND c.workflow_id=r.workflow_id AND c.id=r.conversation_id AND c.owner_id=r.owner_id AND c.deleted_at IS NULL))`

func scanRun(row transaction.Row) (runRecord, error) {
	var r runRecord
	r.Sources = []knowledge.AnswerSourceEvidence{}
	r.Nodes = []NodeTrace{}
	e := row.Scan(&r.ID, &r.WorkflowID, &r.ConversationID, &r.VersionID, &r.DraftRevision, &r.GraphSHA256, &r.State, &r.Question, &r.answer, &r.ErrorCode, &r.CancellationRequestedAt, &r.CreatedAt, &r.StartedAt, &r.FinishedAt, &r.DeadlineAt, &r.LeaseExpiresAt, &r.owner, &r.audience, &r.authority, &r.membership, &r.graph, &r.evidence, &r.alias, &r.usage, &r.processOwner, &r.leaseID, &r.ResultKind, &r.toolResult, &r.toolProof)
	if errors.Is(e, sql.ErrNoRows) {
		return runRecord{}, failure("WORKFLOW_RUN_NOT_FOUND")
	}
	if e != nil {
		return runRecord{}, e
	}
	r.CreatedAt = r.CreatedAt.UTC()
	r.DeadlineAt = r.DeadlineAt.UTC()
	for _, stamp := range []**time.Time{&r.CancellationRequestedAt, &r.StartedAt, &r.FinishedAt, &r.LeaseExpiresAt} {
		if *stamp != nil {
			v := (**stamp).UTC()
			*stamp = &v
		}
	}
	return r, nil
}
func (s *Service) hydrateRun(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, r runRecord) (Run, error) {
	out := r.Run
	limits := s.policy()
	limits.GraphBytes = maximumStoredGraphBytes
	limits.InstructionsBytes = maximumStoredInstructionsBytes
	graph, e := s.parseGraph(r.graph, limits, maximumStoredTopK)
	if e != nil || graph.SHA256 != r.GraphSHA256 {
		return Run{}, failure("WORKFLOW_RESULT_INVALID")
	}
	nodes := map[string]string{}
	for _, n := range graph.Graph.Nodes {
		nodes[n.ID] = n.Kind
	}
	rows, e := tx.Query(ctx, workflowStatement(tx, workflowTracePage, w.TenantID, w.ID, r.WorkflowID, r.ID))
	if e != nil {
		return Run{}, failure("STORAGE_UNAVAILABLE")
	}
	for rows.Next() {
		var n NodeTrace
		var metadata []byte
		if e = rows.Scan(&n.NodeID, &n.Kind, &n.State, &metadata, &n.ErrorCode, &n.StartedAt, &n.FinishedAt); e != nil {
			rows.Close()
			return Run{}, failure("STORAGE_UNAVAILABLE")
		}
		if nodes[n.NodeID] != n.Kind || wire.DecodeStrict(metadata, &n.Result) != nil {
			rows.Close()
			return Run{}, failure("WORKFLOW_RESULT_INVALID")
		}
		out.Nodes = append(out.Nodes, n)
	}
	rows.Close()
	if rows.Err() != nil {
		return Run{}, failure("STORAGE_UNAVAILABLE")
	}
	if len(out.Nodes) != len(graph.Graph.Nodes) {
		return Run{}, failure("WORKFLOW_RESULT_INVALID")
	}
	if r.ResultKind != graph.ResultKind() {
		return Run{}, failure("WORKFLOW_RESULT_INVALID")
	}
	if r.State != SucceededState {
		return out, nil
	}
	withhold := func(code string) (Run, error) { out.ResultUnavailableCode = &code; return out, nil }
	if graph.IsTool() {
		if s.Plugins == nil {
			return withhold("PLUGIN_UNAVAILABLE")
		}
		if e = s.Plugins.RecheckStoredModelListWithin(ctx, tx, a, w.ID, graph.Tool.PluginVersionID, r.toolProof, r.toolResult); e != nil {
			switch e.Error() {
			case "ACCESS_DENIED", "AUTH_SESSION_EXPIRED", "AUTH_REQUIRED", "SESSION_INVALID", "AUTHENTICATION_REQUIRED", "SSO_SOURCE_INVALID":
				return Run{}, e
			}
			if strings.HasPrefix(e.Error(), "PLUGIN_") || e.Error() == "MODEL_FILE_INVALID" || e.Error() == "MODEL_DERIVED_CATALOG_INVALID" {
				return withhold(e.Error())
			}
			return Run{}, failure("STORAGE_UNAVAILABLE")
		}
		var result plugins.ModelList
		if json.Unmarshal(r.toolResult, &result) != nil {
			return withhold("PLUGIN_RESULT_INVALID")
		}
		out.ToolResult = &result
		out.ResultAvailable = true
		return out, nil
	}
	if !s.Identity.Config.Knowledge.Enabled || s.Knowledge == nil || s.Distribution == nil {
		return withhold("KNOWLEDGE_UNAVAILABLE")
	}
	if r.answer == nil || len(*r.answer) > s.policy().AnswerBytes {
		return withhold("WORKFLOW_RESULT_LIMIT_EXCEEDED")
	}
	var evidence knowledge.StoredAnswerEvidence
	var alias modelReference
	var usage knowledge.Usage
	if wire.DecodeStrict(r.evidence, &evidence) != nil || wire.DecodeStrict(r.alias, &alias) != nil || wire.DecodeStrict(r.usage, &usage) != nil || usage.PromptTokens < 0 || usage.CompletionTokens < 0 || usage.TotalTokens < 0 || usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		return withhold("WORKFLOW_RESULT_INVALID")
	}
	e = s.Knowledge.RecheckStoredAnswerEvidenceWithin(ctx, tx, a, w.ID, evidence, alias.binding())
	if e != nil {
		switch e.Error() {
		case "KNOWLEDGE_ANSWER_SOURCE_CHANGED", "KNOWLEDGE_BASE_DISABLED", "KNOWLEDGE_BASE_NOT_FOUND", "KNOWLEDGE_INDEX_INCOMPATIBLE", "KNOWLEDGE_INVALID_SOURCE", "MODEL_ALIAS_UNAVAILABLE", "MODEL_ALIAS_NOT_FOUND", "RESOURCE_NOT_FOUND":
			return withhold(e.Error())
		case "ACCESS_DENIED", "AUTH_SESSION_EXPIRED", "AUTH_REQUIRED", "SESSION_INVALID", "AUTHENTICATION_REQUIRED", "SSO_SOURCE_INVALID":
			return Run{}, e
		default:
			return Run{}, failure("STORAGE_UNAVAILABLE")
		}
	}
	cited, e := knowledge.CitedStoredEvidence(*r.answer, evidence)
	if e != nil {
		return withhold("WORKFLOW_RESULT_INVALID")
	}
	out.Answer = r.answer
	out.Usage = &usage
	out.Sources = cited
	out.ResultAvailable = true
	return out, nil
}
func (s *Service) GetRun(ctx context.Context, a identity.Auth, workspace, workflow, id string) (Run, error) {
	var out Run
	if !wire.ValidID(id) {
		return out, failure("WORKFLOW_RUN_NOT_FOUND")
	}
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if w.Role == nil {
			return failure("ACCESS_DENIED")
		}
		if _, e := lockedWorkflow(ctx, tx, w, workflow); e != nil {
			return e
		}
		r, e := scanRun(tx.QueryRow(ctx, workflowStatement(tx, workflowRunVisible, w.TenantID, w.ID, workflow, a.Me.User.ID, id)))
		if e != nil {
			return e
		}
		out, e = s.hydrateRun(ctx, tx, a, w, r)
		return e
	})
	if e != nil {
		return Run{}, e
	}
	return out, nil
}
func (s *Service) RunPage(ctx context.Context, a identity.Auth, workspace, workflow string, conversation *string, values url.Values) (Page[Run], error) {
	out := Page[Run]{Items: []Run{}}
	scopeID := "debug-and-conversations"
	if conversation != nil {
		if !wire.ValidID(*conversation) {
			return out, failure("WORKFLOW_CONVERSATION_NOT_FOUND")
		}
		scopeID = *conversation
	}
	options := url.Values{}
	for k, v := range values {
		options[k] = append([]string(nil), v...)
	}
	if _, ok := options["limit"]; !ok {
		options.Set("limit", strconv.Itoa(s.Identity.Config.Policy().UI.PageSize))
	}
	limit, cursor, scope, e := pagination.Parse(options, "workflow-runs:"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace+":"+workflow+":"+scopeID)
	if e != nil {
		return out, e
	}
	e = s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if w.Role == nil {
			return failure("ACCESS_DENIED")
		}
		if _, e := lockedWorkflow(ctx, tx, w, workflow); e != nil {
			return e
		}
		if conversation != nil {
			if _, e := lockedConversation(ctx, tx, a, w, workflow, *conversation); e != nil {
				return e
			}
		}
		sql := "SELECT " + runFieldsWithin(tx) + " FROM workflow_runs r WHERE r.tenant_id=$1 AND r.workspace_id=$2 AND r.workflow_id=$3 AND r.owner_id=$4 AND " + visibleRunConversation
		if tx.Dialect() == transaction.MySQL {
			sql = "SELECT " + runFieldsWithin(tx) + " FROM workflow_runs r WHERE r.tenant_id=? AND r.workspace_id=? AND r.workflow_id=? AND r.owner_id=? AND " + visibleRunConversation
		}
		args := []any{w.TenantID, w.ID, workflow, a.Me.User.ID}
		if conversation != nil {
			args = append(args, *conversation)
			if tx.Dialect() == transaction.MySQL {
				sql += " AND r.conversation_id=?"
			} else {
				sql += " AND r.conversation_id=$" + strconv.Itoa(len(args)) + "::uuid"
			}
		}
		if cursor.ID != "" {
			args = append(args, cursor.Time, cursor.ID)
			if tx.Dialect() == transaction.MySQL {
				sql += " AND (r.created_at,r.id)<(?,?)"
			} else {
				sql += " AND (r.created_at,r.id)<($" + strconv.Itoa(len(args)-1) + ",$" + strconv.Itoa(len(args)) + "::uuid)"
			}
		}
		args = append(args, limit+1)
		if tx.Dialect() == transaction.MySQL {
			sql += " ORDER BY r.created_at DESC,r.id DESC LIMIT ?"
		} else {
			sql += " ORDER BY r.created_at DESC,r.id DESC LIMIT $" + strconv.Itoa(len(args))
		}
		statement := transaction.SQL(tx.Dialect(), sql, args...)
		if tx.Dialect() == transaction.Dameng {
			base := "SELECT " + runFieldsDameng + " FROM workflow_runs r WHERE r.tenant_id=? AND r.workspace_id=? AND r.workflow_id=? AND r.owner_id=? AND " + visibleRunConversation
			nativeArgs := []any{w.TenantID, w.ID, workflow, a.Me.User.ID}
			if conversation != nil {
				base += " AND r.conversation_id=?"
				nativeArgs = append(nativeArgs, *conversation)
			}
			var e error
			statement, e = workflowNativePage(base, "r.created_at", "r.id", nativeArgs, cursor, limit)
			if e != nil {
				return e
			}
		} else if tx.Dialect() != transaction.PostgreSQL && tx.Dialect() != transaction.MySQL {
			return failure("STORAGE_UNAVAILABLE")
		}
		rows, e := tx.Query(ctx, statement)
		if e != nil {
			return failure("STORAGE_UNAVAILABLE")
		}
		var records []runRecord
		for rows.Next() {
			r, e := scanRun(rows)
			if e != nil {
				rows.Close()
				return e
			}
			records = append(records, r)
		}
		rows.Close()
		if rows.Err() != nil {
			return failure("STORAGE_UNAVAILABLE")
		}
		if len(records) > limit {
			records = records[:limit]
			last := records[limit-1]
			out.NextCursor = pagination.Encode(last.CreatedAt, last.ID, scope)
		}
		for _, r := range records {
			value, e := s.hydrateRun(ctx, tx, a, w, r)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, value)
		}
		return nil
	})
	if e != nil {
		return Page[Run]{}, e
	}
	return out, nil
}
