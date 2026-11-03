package workflows

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"strconv"
	"time"
)

type Version struct {
	ID            string          `json:"id"`
	WorkflowID    string          `json:"workflow_id"`
	VersionNumber int             `json:"version_number"`
	DraftRevision int64           `json:"draft_revision"`
	Graph         json.RawMessage `json:"graph,omitempty"`
	GraphSHA256   string          `json:"graph_sha256"`
	PublishedBy   string          `json:"published_by"`
	PublishedAt   time.Time       `json:"published_at"`
}

const versionFields = `id::text,workflow_id::text,version_number,draft_revision,graph,graph_sha256,published_by::text,published_at`

func scanVersion(row transaction.Row) (Version, error) {
	var v Version
	e := row.Scan(&v.ID, &v.WorkflowID, &v.VersionNumber, &v.DraftRevision, &v.Graph, &v.GraphSHA256, &v.PublishedBy, &v.PublishedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return Version{}, failure("WORKFLOW_VERSION_UNAVAILABLE")
	}
	if e != nil {
		return Version{}, e
	}
	v.PublishedAt = v.PublishedAt.UTC()
	return v, nil
}
func (s *Service) parseStored(b workflowRecord) (ParsedGraph, error) {
	p, e := s.parseGraph(b.graph, s.policy(), s.Identity.Config.Policy().Knowledge.MaximumTopK)
	if e != nil {
		return p, e
	}
	if p.SHA256 != b.digest {
		return p, failure("WORKFLOW_GRAPH_CORRUPT")
	}
	return p, nil
}
func (s *Service) references(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, p ParsedGraph) (distribution.Alias, error) {
	var zero distribution.Alias
	if p.IsTool() {
		if s.Plugins == nil {
			return zero, failure("PLUGIN_UNAVAILABLE")
		}
		return zero, s.Plugins.CheckToolWithin(ctx, tx, a, w.ID, p.Tool.PluginVersionID)
	}
	if !s.Identity.Config.Knowledge.Enabled || s.Knowledge == nil || s.Distribution == nil {
		return zero, failure("KNOWLEDGE_UNAVAILABLE")
	}
	alias, e := s.Distribution.CurrentAnswerAliasWithin(ctx, tx, a, w.ID, p.Answer.AliasID)
	if e != nil {
		return zero, e
	}
	trusted := false
	for _, destination := range s.Identity.Config.Knowledge.Gateways {
		if destination.Origin == alias.ClientOrigin {
			trusted = true
			break
		}
	}
	if !trusted {
		return zero, failure("KNOWLEDGE_GATEWAY_UNAVAILABLE")
	}
	if _, e = s.Knowledge.CheckWorkflowBaseWithin(ctx, tx, a, w.ID, p.Retrieval.BaseID); e != nil {
		return zero, e
	}
	return alias, nil
}

func (s *Service) Validate(ctx context.Context, a identity.Auth, workspace, id string, input RevisionReason) (Validation, error) {
	out := Validation{Issues: []GraphIssue{}}
	if !validReason(input.Reason) || input.ExpectedRevision < 1 {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		b, e := lockedWorkflow(ctx, tx, w, id)
		if e != nil {
			return e
		}
		if b.Revision != input.ExpectedRevision {
			return failure("WORKFLOW_DRAFT_CHANGED")
		}
		p, e := s.parseStored(b)
		if e != nil {
			return e
		}
		out = p.ValidateExecutable()
		if !out.Valid {
			return nil
		}
		_, e = s.references(ctx, tx, a, w, p)
		return e
	})
	if e != nil {
		return Validation{}, e
	}
	return out, nil
}
func (s *Service) Publish(ctx context.Context, a identity.Auth, workspace, id string, input RevisionReason, key, request string) (Version, error) {
	var out Version
	if !s.Identity.Config.Workflows.Enabled {
		return out, failure("WORKFLOW_UNAVAILABLE")
	}
	if !validReason(input.Reason) || input.ExpectedRevision < 1 || input.ExpectedRevision >= MaximumRevision || !wire.ValidID(id) {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		previous, digest, e := s.intent(ctx, tx, a, w, "publish:"+id, key, input)
		if e != nil {
			return e
		}
		b, e := lockedWorkflow(ctx, tx, w, id)
		if e != nil {
			return e
		}
		if previous != "" {
			out, e = scanVersion(tx.QueryRow(ctx, workflowStatement(tx, workflowVersionScoped, w.TenantID, w.ID, id, previous)))
			return e
		}
		if !b.Enabled {
			return failure("WORKFLOW_DISABLED")
		}
		if b.Revision != input.ExpectedRevision {
			return failure("WORKFLOW_DRAFT_CHANGED")
		}
		p, e := s.parseStored(b)
		if e != nil {
			return e
		}
		if !p.ValidateExecutable().Valid {
			return graphFailure()
		}
		if _, e = s.references(ctx, tx, a, w, p); e != nil {
			return e
		}
		var count, number int
		if e = tx.QueryRow(ctx, workflowStatement(tx, workflowVersionCount, w.TenantID, w.ID, id)).Scan(&count, &number); e != nil {
			return e
		}
		if count >= s.policy().MaximumVersions {
			return failure("WORKFLOW_LIMIT_EXCEEDED")
		}
		version := uuid.NewString()
		if e = workflowExecOne(ctx, tx, workflowStatement(tx, workflowVersionCreate, version, w.TenantID, w.ID, id, number, b.Revision, p.CanonicalJSON, p.SHA256, a.Me.User.ID)); e != nil {
			return e
		}
		if p.IsTool() {
			if e = s.Plugins.RecordPublicationWithin(ctx, tx, a, w.ID, p.Tool.PluginVersionID, id, version); e != nil {
				return e
			}
		}
		if e = workflowExecOne(ctx, tx, workflowStatement(tx, workflowPublish, id, version)); e != nil {
			return e
		}
		if e = saveIntent(ctx, tx, a, w, "publish:"+id, key, version, digest); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "workflow.published", "workflow_version", version, input.Reason, request); e != nil {
			return e
		}
		out, e = scanVersion(tx.QueryRow(ctx, workflowStatement(tx, workflowVersionByID, version)))
		return e
	})
	if e != nil {
		return Version{}, e
	}
	return out, nil
}
func (s *Service) Versions(ctx context.Context, a identity.Auth, workspace, id string, values url.Values) (Page[Version], error) {
	out := Page[Version]{Items: []Version{}}
	options := url.Values{}
	for k, v := range values {
		options[k] = append([]string(nil), v...)
	}
	if _, ok := options["limit"]; !ok {
		options.Set("limit", strconv.Itoa(s.Identity.Config.Policy().UI.PageSize))
	}
	limit, cursor, scope, e := pagination.Parse(options, "workflow-versions:"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace+":"+id)
	if e != nil {
		return out, e
	}
	e = s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		b, e := lockedWorkflow(ctx, tx, w, id)
		if e != nil {
			return e
		}
		if _, e = visibleWorkflow(a, w, b); e != nil {
			return e
		}
		sql := "SELECT " + versionFieldsWithin(tx) + " FROM workflow_versions WHERE tenant_id=$1 AND workspace_id=$2 AND workflow_id=$3"
		if tx.Dialect() == transaction.MySQL {
			sql = "SELECT " + versionFieldsWithin(tx) + " FROM workflow_versions WHERE tenant_id=? AND workspace_id=? AND workflow_id=?"
		}
		args := []any{w.TenantID, w.ID, id}
		if cursor.ID != "" {
			args = append(args, cursor.Time, cursor.ID)
			if tx.Dialect() == transaction.MySQL {
				sql += " AND (published_at,id)<(?,?)"
			} else {
				sql += " AND (published_at,id)<($4,$5::uuid)"
			}
		}
		args = append(args, limit+1)
		if tx.Dialect() == transaction.MySQL {
			sql += " ORDER BY published_at DESC,id DESC LIMIT ?"
		} else {
			sql += " ORDER BY published_at DESC,id DESC LIMIT $" + strconv.Itoa(len(args))
		}
		statement := transaction.SQL(tx.Dialect(), sql, args...)
		if tx.Dialect() == transaction.Dameng {
			base := "SELECT " + versionFieldsDameng + " FROM workflow_versions WHERE tenant_id=? AND workspace_id=? AND workflow_id=?"
			nativeArgs := []any{w.TenantID, w.ID, id}
			var e error
			statement, e = workflowNativePage(base, "published_at", "id", nativeArgs, cursor, limit)
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
			v, e := scanVersion(rows)
			if e != nil {
				rows.Close()
				return e
			}
			if !editable(a, w) {
				v.Graph = nil
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
			out.NextCursor = pagination.Encode(last.PublishedAt, last.ID, scope)
		}
		return nil
	})
	if e != nil {
		return Page[Version]{}, e
	}
	return out, nil
}
