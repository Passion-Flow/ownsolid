package workflows

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"strconv"
)

const workflowFields = `id::text,workspace_id::text,name,enabled,revision,draft_graph,draft_sha256,current_version_id::text,execution_authority::text,created_by::text,created_at,updated_at`
const InitialNodeSpacing = 220

func scanWorkflow(row transaction.Row) (workflowRecord, error) {
	var b workflowRecord
	e := row.Scan(&b.ID, &b.WorkspaceID, &b.Name, &b.Enabled, &b.Revision, &b.graph, &b.digest, &b.CurrentVersionID, &b.authority, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return workflowRecord{}, failure("WORKFLOW_NOT_FOUND")
	}
	if e != nil {
		return workflowRecord{}, e
	}
	b.CreatedAt = b.CreatedAt.UTC()
	b.UpdatedAt = b.UpdatedAt.UTC()
	return b, nil
}
func lockedWorkflow(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, id string) (workflowRecord, error) {
	if !wire.ValidID(id) {
		return workflowRecord{}, failure("WORKFLOW_NOT_FOUND")
	}
	return scanWorkflow(tx.QueryRow(ctx, workflowStatement(tx, workflowLock, w.TenantID, w.ID, id)))
}
func visibleWorkflow(a identity.Auth, w workspaces.Workspace, b workflowRecord) (Workflow, error) {
	out := b.Workflow
	out.CanEdit = editable(a, w)
	if !out.CanEdit && (!out.Enabled || out.CurrentVersionID == nil) {
		return Workflow{}, failure("WORKFLOW_NOT_FOUND")
	}
	if out.CanEdit {
		out.DraftGraph = append(json.RawMessage(nil), b.graph...)
		out.DraftSHA256 = b.digest
	}
	return out, nil
}
func (s *Service) initialGraph() (ParsedGraph, error) {
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	retrieval, _ := json.Marshal(RetrievalConfiguration{TopK: s.Identity.Config.Policy().Knowledge.TopK, MinimumScore: s.Identity.Config.Policy().Knowledge.MinimumScore})
	answer, _ := json.Marshal(AnswerConfiguration{})
	configurations := []json.RawMessage{json.RawMessage(`{}`), retrieval, answer, json.RawMessage(`{}`)}
	kinds := []string{StartNode, RetrievalNode, AnswerNode, EndNode}
	g := Graph{SchemaVersion: GraphSchemaVersion, Nodes: []Node{}, Edges: []Edge{}}
	if s.toolOnly() {
		tool, _ := json.Marshal(ToolConfiguration{})
		kinds = []string{StartNode, ToolNode, EndNode}
		configurations = []json.RawMessage{json.RawMessage(`{}`), tool, json.RawMessage(`{}`)}
		g.SchemaVersion = ToolGraphSchemaVersion
	}
	for i, kind := range kinds {
		g.Nodes = append(g.Nodes, Node{ID: ids[i], Kind: kind, Position: Position{X: float64(i * InitialNodeSpacing)}, Configuration: configurations[i]})
		if i > 0 {
			g.Edges = append(g.Edges, Edge{ID: uuid.NewString(), Source: ids[i-1], Target: ids[i]})
		}
	}
	raw, _ := json.Marshal(g)
	return s.parseGraph(raw, s.policy(), s.Identity.Config.Policy().Knowledge.MaximumTopK)
}
func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, id string) (Workflow, error) {
	var out Workflow
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		b, e := lockedWorkflow(ctx, tx, w, id)
		if e != nil {
			return e
		}
		out, e = visibleWorkflow(a, w, b)
		return e
	})
	if e != nil {
		return Workflow{}, e
	}
	return out, nil
}
func (s *Service) ResolveWorkspace(ctx context.Context, a identity.Auth, id string) (string, error) {
	if !admin(a) || !wire.ValidID(id) {
		return "", failure("WORKFLOW_NOT_FOUND")
	}
	store, e := s.repository()
	if e != nil {
		return "", workflowRepositoryError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	var workspace string
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		if e := q.QueryRow(ctx, workflowStatement(q, workflowResolve, id)).Scan(&workspace); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return failure("WORKFLOW_NOT_FOUND")
			}
			return e
		}
		w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if w.Status != "active" {
			return failure("ACCESS_DENIED")
		}
		return nil
	})
	if e != nil {
		return "", workflowRepositoryError(e)
	}
	return workspace, nil
}
func (s *Service) List(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[Workflow], error) {
	out := Page[Workflow]{Items: []Workflow{}}
	queryValues := url.Values{}
	for k, v := range values {
		queryValues[k] = append([]string(nil), v...)
	}
	if _, ok := queryValues["limit"]; !ok {
		queryValues.Set("limit", strconv.Itoa(s.Identity.Config.Policy().UI.PageSize))
	}
	limit, cursor, scope, e := pagination.Parse(queryValues, "workflows:"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace)
	if e != nil {
		return out, e
	}
	e = s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		sql := "SELECT " + workflowFieldsWithin(tx) + " FROM workflows WHERE tenant_id=$1 AND workspace_id=$2 AND ($3 OR (enabled AND current_version_id IS NOT NULL))"
		if tx.Dialect() == transaction.MySQL {
			sql = "SELECT " + workflowFieldsWithin(tx) + " FROM workflows WHERE tenant_id=? AND workspace_id=? AND (? OR (enabled AND current_version_id IS NOT NULL))"
		}
		args := []any{w.TenantID, w.ID, editable(a, w)}
		if cursor.ID != "" {
			args = append(args, cursor.Time, cursor.ID)
			if tx.Dialect() == transaction.MySQL {
				sql += " AND (created_at,id)<(?,?)"
			} else {
				sql += " AND (created_at,id)<($4,$5::uuid)"
			}
		}
		args = append(args, limit+1)
		if tx.Dialect() == transaction.MySQL {
			sql += " ORDER BY created_at DESC,id DESC LIMIT ?"
		} else {
			sql += " ORDER BY created_at DESC,id DESC LIMIT $" + strconv.Itoa(len(args))
		}
		statement := transaction.SQL(tx.Dialect(), sql, args...)
		if tx.Dialect() == transaction.Dameng {
			base := "SELECT " + workflowFieldsDameng + " FROM workflows WHERE tenant_id=? AND workspace_id=? AND (?=1 OR (enabled=1 AND current_version_id IS NOT NULL))"
			nativeArgs := []any{w.TenantID, w.ID, editable(a, w)}
			var e error
			statement, e = workflowNativePage(base, "created_at", "id", nativeArgs, cursor, limit)
			if e != nil {
				return e
			}
		} else if tx.Dialect() != transaction.PostgreSQL && tx.Dialect() != transaction.MySQL {
			return failure("STORAGE_UNAVAILABLE")
		}
		rows, e := tx.Query(ctx, statement)
		if e != nil {
			return e
		}
		for rows.Next() {
			b, e := scanWorkflow(rows)
			if e != nil {
				rows.Close()
				return e
			}
			v, e := visibleWorkflow(a, w, b)
			if e != nil {
				rows.Close()
				return e
			}
			out.Items = append(out.Items, v)
		}
		rows.Close()
		if rows.Err() != nil {
			return rows.Err()
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			last := out.Items[limit-1]
			out.NextCursor = pagination.Encode(last.CreatedAt, last.ID, scope)
		}
		return nil
	})
	if e != nil {
		return Page[Workflow]{}, e
	}
	return out, nil
}
func (s *Service) Create(ctx context.Context, a identity.Auth, input CreateRequest, key, request string) (Workflow, error) {
	var out Workflow
	if !s.Identity.Config.Workflows.Enabled {
		return out, failure("WORKFLOW_UNAVAILABLE")
	}
	if !validReason(input.Reason) {
		return out, failure("VALIDATION_FAILED")
	}
	name, normalized, e := nameKey(input.Name, s.policy().NameCharacters)
	if e != nil {
		return out, e
	}
	input.Name = name
	initial, e := s.initialGraph()
	if e != nil {
		return out, e
	}
	e = s.transaction(ctx, a, input.WorkspaceID, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		previous, digest, e := s.intent(ctx, tx, a, w, "create_workflow", key, input)
		if e != nil {
			return e
		}
		if previous != "" {
			b, e := lockedWorkflow(ctx, tx, w, previous)
			if e != nil {
				return e
			}
			out, e = visibleWorkflow(a, w, b)
			return e
		}
		var count int
		if e = tx.QueryRow(ctx, workflowStatement(tx, workflowCount, w.TenantID, w.ID)).Scan(&count); e != nil {
			return e
		}
		if count >= s.policy().MaximumWorkflows {
			return failure("WORKFLOW_LIMIT_EXCEEDED")
		}
		id := uuid.NewString()
		if e = workflowNameAvailable(ctx, tx, w, normalized, id); e != nil {
			return e
		}
		if e = workflowExecOne(ctx, tx, workflowStatement(tx, workflowCreate, id, w.TenantID, w.ID, name, normalized, uuid.NewString(), initial.CanonicalJSON, initial.SHA256, a.Me.User.ID)); e != nil {
			return e
		}
		if e = saveIntent(ctx, tx, a, w, "create_workflow", key, id, digest); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "workflow.created", "workflow", id, input.Reason, request); e != nil {
			return e
		}
		b, e := lockedWorkflow(ctx, tx, w, id)
		if e != nil {
			return e
		}
		out, e = visibleWorkflow(a, w, b)
		return e
	})
	if e != nil {
		return Workflow{}, e
	}
	return out, nil
}
func (s *Service) Update(ctx context.Context, a identity.Auth, workspace, id string, input UpdateRequest, request string) (Workflow, error) {
	var out Workflow
	if !validReason(input.Reason) || input.ExpectedRevision < 1 || input.ExpectedRevision >= MaximumRevision {
		return out, failure("VALIDATION_FAILED")
	}
	name, key, e := nameKey(input.Name, s.policy().NameCharacters)
	if e != nil {
		return out, e
	}
	raw, e := json.Marshal(input.Graph)
	if e != nil {
		return out, graphFailure()
	}
	graph, e := s.parseGraph(raw, s.policy(), s.Identity.Config.Policy().Knowledge.MaximumTopK)
	if e != nil {
		return out, e
	}
	e = s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		b, e := lockedWorkflow(ctx, tx, w, id)
		if e != nil {
			return e
		}
		if b.Revision != input.ExpectedRevision {
			return failure("WORKFLOW_DRAFT_CHANGED")
		}
		if graph.IsTool() && graph.Tool.PluginVersionID != "" {
			if s.Plugins == nil {
				return failure("PLUGIN_UNAVAILABLE")
			}
			if e = s.Plugins.CheckToolWithin(ctx, tx, a, w.ID, graph.Tool.PluginVersionID); e != nil {
				return e
			}
		}
		if e = workflowNameAvailable(ctx, tx, w, key, id); e != nil {
			return e
		}
		authority := b.authority
		if b.Enabled != input.Enabled {
			authority = uuid.NewString()
		}
		if e = workflowExecOne(ctx, tx, workflowStatement(tx, workflowUpdate, id, name, key, input.Enabled, authority, graph.CanonicalJSON, graph.SHA256)); e != nil {
			return e
		}
		if !input.Enabled {
			if _, e = tx.Exec(ctx, workflowStatement(tx, workflowCancelAll, w.TenantID, w.ID, id)); e != nil {
				return e
			}
		}
		if e = s.audit(ctx, tx, a, w, "workflow.updated", "workflow", id, input.Reason, request); e != nil {
			return e
		}
		b, e = lockedWorkflow(ctx, tx, w, id)
		if e != nil {
			return e
		}
		out, e = visibleWorkflow(a, w, b)
		return e
	})
	if e != nil {
		return Workflow{}, e
	}
	return out, nil
}
