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
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"strconv"
	"time"
)

type Conversation struct {
	ID         string    `json:"id"`
	WorkflowID string    `json:"workflow_id"`
	VersionID  string    `json:"version_id"`
	Revision   int64     `json:"revision"`
	TurnCount  int       `json:"turn_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
type ConversationRequest struct {
	VersionID string `json:"version_id"`
	Reason    string `json:"reason"`
}

const conversationFields = `id::text,workflow_id::text,version_id::text,revision,turn_count,created_at,updated_at`

func scanConversation(row transaction.Row) (Conversation, error) {
	var c Conversation
	e := row.Scan(&c.ID, &c.WorkflowID, &c.VersionID, &c.Revision, &c.TurnCount, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return Conversation{}, failure("WORKFLOW_CONVERSATION_NOT_FOUND")
	}
	if e != nil {
		return Conversation{}, e
	}
	c.CreatedAt = c.CreatedAt.UTC()
	c.UpdatedAt = c.UpdatedAt.UTC()
	return c, nil
}
func lockedConversation(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, workflow, id string) (Conversation, error) {
	if !wire.ValidID(id) {
		return Conversation{}, failure("WORKFLOW_CONVERSATION_NOT_FOUND")
	}
	return scanConversation(tx.QueryRow(ctx, workflowStatement(tx, workflowConversationLock, w.TenantID, w.ID, workflow, id, a.Me.User.ID)))
}
func (s *Service) CreateConversation(ctx context.Context, a identity.Auth, workspace, id string, input ConversationRequest, key, request string) (Conversation, error) {
	var out Conversation
	if !s.Identity.Config.Workflows.Enabled {
		return out, failure("WORKFLOW_UNAVAILABLE")
	}
	if !validReason(input.Reason) || !wire.ValidID(input.VersionID) {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if w.Role == nil {
			return failure("ACCESS_DENIED")
		}
		previous, digest, e := s.intent(ctx, tx, a, w, "conversation:"+id, key, input)
		if e != nil {
			return e
		}
		b, e := lockedWorkflow(ctx, tx, w, id)
		if e != nil {
			return e
		}
		if previous != "" {
			out, e = lockedConversation(ctx, tx, a, w, id, previous)
			return e
		}
		if !b.Enabled {
			return failure("WORKFLOW_DISABLED")
		}
		if b.CurrentVersionID == nil || *b.CurrentVersionID != input.VersionID {
			return failure("WORKFLOW_VERSION_UNAVAILABLE")
		}
		version, e := scanVersion(tx.QueryRow(ctx, workflowStatement(tx, workflowVersionScoped, w.TenantID, w.ID, id, input.VersionID)))
		if e != nil {
			return e
		}
		graph, e := s.parseGraph(version.Graph, s.policy(), s.Identity.Config.Policy().Knowledge.MaximumTopK)
		if e != nil {
			return e
		}
		if graph.SHA256 != version.GraphSHA256 || !graph.ValidateExecutable().Valid {
			return failure("WORKFLOW_GRAPH_CORRUPT")
		}
		if _, e = s.references(ctx, tx, a, w, graph); e != nil {
			return e
		}
		var count int
		if e = tx.QueryRow(ctx, workflowStatement(tx, workflowConversationCount, w.TenantID, w.ID, a.Me.User.ID)).Scan(&count); e != nil {
			return e
		}
		if count >= s.policy().MaximumConversations {
			return failure("WORKFLOW_LIMIT_EXCEEDED")
		}
		conversation := uuid.NewString()
		if e = workflowExecOne(ctx, tx, workflowStatement(tx, workflowConversationCreate, conversation, w.TenantID, w.ID, id, input.VersionID, a.Me.User.ID)); e != nil {
			return e
		}
		if e = saveIntent(ctx, tx, a, w, "conversation:"+id, key, conversation, digest); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "workflow.conversation_created", "workflow_conversation", conversation, input.Reason, request); e != nil {
			return e
		}
		out, e = lockedConversation(ctx, tx, a, w, id, conversation)
		return e
	})
	if e != nil {
		return Conversation{}, e
	}
	return out, nil
}
func (s *Service) Conversation(ctx context.Context, a identity.Auth, workspace, id, conversation string) (Conversation, error) {
	var out Conversation
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if w.Role == nil {
			return failure("ACCESS_DENIED")
		}
		if _, e := lockedWorkflow(ctx, tx, w, id); e != nil {
			return e
		}
		var e error
		out, e = lockedConversation(ctx, tx, a, w, id, conversation)
		return e
	})
	if e != nil {
		return Conversation{}, e
	}
	return out, nil
}
func (s *Service) Conversations(ctx context.Context, a identity.Auth, workspace, id string, values url.Values) (Page[Conversation], error) {
	out := Page[Conversation]{Items: []Conversation{}}
	options := url.Values{}
	for k, v := range values {
		options[k] = append([]string(nil), v...)
	}
	if _, ok := options["limit"]; !ok {
		options.Set("limit", strconv.Itoa(s.Identity.Config.Policy().UI.PageSize))
	}
	limit, cursor, scope, e := pagination.Parse(options, "workflow-conversations:"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace+":"+id)
	if e != nil {
		return out, e
	}
	e = s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if w.Role == nil {
			return failure("ACCESS_DENIED")
		}
		if _, e := lockedWorkflow(ctx, tx, w, id); e != nil {
			return e
		}
		sql := "SELECT " + conversationFieldsWithin(tx) + " FROM workflow_conversations WHERE tenant_id=$1 AND workspace_id=$2 AND workflow_id=$3 AND owner_id=$4 AND deleted_at IS NULL"
		if tx.Dialect() == transaction.MySQL {
			sql = "SELECT " + conversationFieldsWithin(tx) + " FROM workflow_conversations WHERE tenant_id=? AND workspace_id=? AND workflow_id=? AND owner_id=? AND deleted_at IS NULL"
		}
		args := []any{w.TenantID, w.ID, id, a.Me.User.ID}
		if cursor.ID != "" {
			args = append(args, cursor.Time, cursor.ID)
			if tx.Dialect() == transaction.MySQL {
				sql += " AND (created_at,id)<(?,?)"
			} else {
				sql += " AND (created_at,id)<($5,$6::uuid)"
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
			base := "SELECT " + conversationFieldsDameng + " FROM workflow_conversations WHERE tenant_id=? AND workspace_id=? AND workflow_id=? AND owner_id=? AND deleted_at IS NULL"
			nativeArgs := []any{w.TenantID, w.ID, id, a.Me.User.ID}
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
			c, e := scanConversation(rows)
			if e != nil {
				rows.Close()
				return e
			}
			out.Items = append(out.Items, c)
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
		return Page[Conversation]{}, e
	}
	return out, nil
}
func (s *Service) DeleteConversation(ctx context.Context, a identity.Auth, workspace, id, conversation string, input RevisionReason, request string) error {
	if !validReason(input.Reason) || input.ExpectedRevision < 1 || input.ExpectedRevision >= MaximumRevision {
		return failure("VALIDATION_FAILED")
	}
	return s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if w.Role == nil {
			return failure("ACCESS_DENIED")
		}
		if _, e := lockedWorkflow(ctx, tx, w, id); e != nil {
			return e
		}
		c, e := lockedConversation(ctx, tx, a, w, id, conversation)
		if e != nil {
			return e
		}
		if c.Revision != input.ExpectedRevision {
			return failure("WORKFLOW_DRAFT_CHANGED")
		}
		if e = workflowExecOne(ctx, tx, workflowStatement(tx, workflowConversationDelete, conversation)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, workflowStatement(tx, workflowConversationCancel, w.TenantID, w.ID, id, conversation)); e != nil {
			return e
		}
		return s.audit(ctx, tx, a, w, "workflow.conversation_deleted", "workflow_conversation", conversation, input.Reason, request)
	})
}

// DecodePublishedGraph checks stored immutable graph bytes at execution admission.
func (s *Service) DecodePublishedGraph(raw json.RawMessage, digest string) (ParsedGraph, error) {
	p, e := s.parseGraph(raw, s.policy(), s.Identity.Config.Policy().Knowledge.MaximumTopK)
	if e != nil {
		return p, e
	}
	if p.SHA256 != digest || !p.ValidateExecutable().Valid {
		return p, failure("WORKFLOW_GRAPH_CORRUPT")
	}
	return p, nil
}
