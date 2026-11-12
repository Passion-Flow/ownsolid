// Package applications projects current publications and caller-private conversations.
package applications

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB         *pgxpool.Pool
	Identity   *identity.Service
	Workspaces *workspaces.Service
	Workflows  *workflows.Service
}
type Application struct {
	WorkflowID    string    `json:"workflow_id"`
	WorkspaceID   string    `json:"workspace_id"`
	Name          string    `json:"name"`
	VersionID     string    `json:"version_id"`
	VersionNumber int       `json:"version_number"`
	GraphSHA256   string    `json:"graph_sha256"`
	PublishedAt   time.Time `json:"published_at"`
	CanExecute    bool      `json:"can_execute"`
	QuestionBytes int       `json:"question_bytes"`
}
type Conversation struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	WorkflowID    string    `json:"workflow_id"`
	WorkflowName  string    `json:"workflow_name"`
	VersionID     string    `json:"version_id"`
	VersionNumber int       `json:"version_number"`
	TurnCount     int       `json:"turn_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CanExecute    bool      `json:"can_execute"`
	QuestionBytes int       `json:"question_bytes"`
}
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func failure(code string) error { return errors.New(code) }
func (s *Service) options(values url.Values) url.Values {
	out := url.Values{}
	for key, v := range values {
		out[key] = append([]string(nil), v...)
	}
	if _, ok := out["limit"]; !ok {
		out.Set("limit", strconv.Itoa(s.Identity.Config.Policy().UI.PageSize))
	}
	return out
}

// List returns the ordinary portal application projection.
func (s *Service) List(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[Application], error) {
	return s.list(ctx, a, workspace, values, nil)
}

// ListForAssistant returns applications after committing the scoped assistant audit.
// requestID must be assigned by the server.
func (s *Service) ListForAssistant(ctx context.Context, a identity.Auth, values url.Values, requestID string) (Page[Application], error) {
	return s.list(ctx, a, assistantWorkspace(a), values, &assistantRead{requestID: requestID, toolID: "list_applications"})
}

func (s *Service) list(ctx context.Context, a identity.Auth, workspace string, values url.Values, assistant *assistantRead) (Page[Application], error) {
	out := Page[Application]{Items: []Application{}}
	if _, e := s.repository(); e != nil {
		return out, applicationError(e)
	}
	if a.Me.Audience != "portal" || !strict.ValidID(workspace) {
		return out, failure("ACCESS_DENIED")
	}
	limit, cursor, scope, e := pagination.Parse(s.options(values), "applications:"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace)
	if e != nil {
		return out, e
	}
	if s.Workflows == nil {
		return out, failure("WORKFLOW_UNAVAILABLE")
	}
	e = s.transaction(ctx, a, workspace, assistant, func(operation context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		configuration, e := s.Workflows.ConfigurationWithin(operation, tx, a, workspace)
		if e != nil {
			return e
		}
		query, e := projectionQuery(tx.Dialect(), false, w.TenantID, w.ID, a.Me.User.ID, "", cursor, limit)
		if e != nil {
			return e
		}
		rows, e := tx.Query(operation, query)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			app, e := scanApplication(rows)
			if e != nil {
				return e
			}
			app.CanExecute = configuration.Enabled && configuration.CanExecute
			app.QuestionBytes = configuration.Limits.QuestionBytes
			out.Items = append(out.Items, app)
		}
		if rows.Err() != nil {
			return rows.Err()
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			last := out.Items[limit-1]
			out.NextCursor = pagination.Encode(last.PublishedAt, last.WorkflowID, scope)
		}
		return nil
	})
	if e != nil {
		return Page[Application]{}, e
	}
	return out, nil
}

// Conversations returns the ordinary caller-private portal projection.
func (s *Service) Conversations(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[Conversation], error) {
	return s.conversations(ctx, a, workspace, values, nil)
}

// ConversationsForAssistant returns caller-private conversations after committing
// the scoped assistant audit. requestID must be assigned by the server.
func (s *Service) ConversationsForAssistant(ctx context.Context, a identity.Auth, values url.Values, requestID string) (Page[Conversation], error) {
	if _, search := values[pagination.ListSearchParameter]; search {
		return Page[Conversation]{}, failure("VALIDATION_FAILED")
	}
	return s.conversations(ctx, a, assistantWorkspace(a), values, &assistantRead{requestID: requestID, toolID: "list_conversations"})
}

func (s *Service) conversations(ctx context.Context, a identity.Auth, workspace string, values url.Values, assistant *assistantRead) (Page[Conversation], error) {
	out := Page[Conversation]{Items: []Conversation{}}
	if _, e := s.repository(); e != nil {
		return out, applicationError(e)
	}
	if a.Me.Audience != "portal" || !strict.ValidID(workspace) {
		return out, failure("ACCESS_DENIED")
	}
	options := s.options(values)
	listScope := "application-conversations:" + a.Me.Audience + ":" + a.Me.User.ID + ":" + workspace
	search, e := prepareConversationSearch(options, listScope, a.SessionID, s.Identity.HMACKey)
	if e != nil {
		return out, e
	}
	limit, cursor, scope, e := pagination.Parse(options, listScope)
	if e != nil {
		return out, e
	}
	if s.Workflows == nil {
		return out, failure("WORKFLOW_UNAVAILABLE")
	}
	e = s.transaction(ctx, a, workspace, assistant, func(operation context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		configuration, e := s.Workflows.ConfigurationWithin(operation, tx, a, workspace)
		if e != nil {
			return e
		}
		query, e := projectionQuery(tx.Dialect(), true, w.TenantID, w.ID, a.Me.User.ID, search.term, cursor, limit)
		if e != nil {
			return e
		}
		rows, e := tx.Query(operation, query)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var item Conversation
			var enabled bool
			if e = rows.Scan(&item.ID, &item.WorkspaceID, &item.WorkflowID, &item.WorkflowName, &item.VersionID, &item.VersionNumber, &item.TurnCount, &item.CreatedAt, &item.UpdatedAt, &enabled); e != nil {
				return e
			}
			item.CreatedAt = item.CreatedAt.UTC()
			item.UpdatedAt = item.UpdatedAt.UTC()
			item.CanExecute = enabled && configuration.Enabled && configuration.CanExecute
			item.QuestionBytes = configuration.Limits.QuestionBytes
			out.Items = append(out.Items, item)
		}
		if rows.Err() != nil {
			return rows.Err()
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			last := out.Items[limit-1]
			out.NextCursor = pagination.Encode(last.UpdatedAt, last.ID, scope)
			if search.term != "" {
				position := *out.NextCursor
				*out.NextCursor = position + "." + search.tag(position)
			}
		}
		return nil
	})
	if e != nil {
		return Page[Conversation]{}, e
	}
	return out, nil
}

func assistantWorkspace(a identity.Auth) string {
	if a.Me.Workspace == nil {
		return ""
	}
	return a.Me.Workspace.ID
}
