package taskdeliveries

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

func (s *Service) humanWithin(ctx context.Context, a identity.Auth, workspace, node string, fn func(context.Context, transaction.Executor, nodes.Node) error) error {
	if ctx == nil || !taskbus.ValidID(node) || a.Me.Audience != "admin" && a.Me.Audience != "portal" {
		return fail("ACCESS_DENIED")
	}
	store, err := s.repository()
	if err != nil {
		return err
	}
	seconds := s.Configuration.RequestSeconds
	if ordinary := s.Identity.Config.Policy().HTTP.RequestSeconds; ordinary < seconds {
		seconds = ordinary
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	err = store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error {
		current, err := s.Nodes.ComponentNodeWithin(ctx, q, a, workspace, node)
		if err != nil {
			return err
		}
		if current.TenantID != s.lifecycle.tenant {
			return fail("RESOURCE_NOT_FOUND")
		}
		return fn(ctx, q, current)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fail("RESOURCE_NOT_FOUND")
	}
	return safe(err)
}
func (s *Service) historyScope(ctx context.Context, q transaction.Executor, a identity.Auth, n nodes.Node) (string, error) {
	lifetime := "admin"
	if !admin(a) {
		var err error
		lifetime, err = s.Workspaces.MembershipAuthorityWithin(ctx, q, n.TenantID, n.WorkspaceID, a.Me.User.ID)
		if err != nil {
			return "", err
		}
	}
	return strings.Join([]string{"task-deliveries", n.TenantID, n.WorkspaceID, n.ID, a.Me.Audience, a.Me.User.ID, a.SessionID, strconv.FormatInt(a.Epoch, 10), lifetime}, ":"), nil
}
func (s *Service) List(ctx context.Context, a identity.Auth, workspace, node string, values url.Values) (Page[Delivery], error) {
	run, done, err := s.begin(ctx)
	if err != nil {
		return Page[Delivery]{}, err
	}
	defer done()
	var out Page[Delivery]
	err = s.humanWithin(run, a, workspace, node, func(ctx context.Context, q transaction.Executor, n nodes.Node) error {
		scope, err := s.historyScope(ctx, q, a, n)
		if err != nil {
			return err
		}
		limit, cursor, fingerprint, err := pagination.ParseWithDefault(values, scope, s.Identity.Config.Policy().UI.PageSize)
		if err != nil {
			return err
		}
		if raw := values.Get("limit"); raw != "" && raw != strconv.Itoa(limit) {
			return fail("VALIDATION_FAILED")
		}
		pg, my := "SELECT "+pgFields+" FROM task_event_outbox WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3", "SELECT "+myFields+" FROM task_event_outbox WHERE tenant_id=? AND workspace_id=? AND node_id=?"
		pgArgs, myArgs := []any{n.TenantID, n.WorkspaceID, n.ID}, []any{n.TenantID, n.WorkspaceID, n.ID}
		if cursor.ID != "" {
			pg += " AND (created_at,id)<($4,$5::uuid)"
			my += " AND (created_at<? OR (created_at=? AND id<?))"
			pgArgs = append(pgArgs, cursor.Time, cursor.ID)
			myArgs = append(myArgs, cursor.Time, cursor.Time, cursor.ID)
		}
		pg += " ORDER BY created_at DESC,id DESC LIMIT $" + strconv.Itoa(len(pgArgs)+1)
		my += " ORDER BY created_at DESC,id DESC LIMIT ?"
		query := transaction.SQL(transaction.PostgreSQL, pg, append(pgArgs, limit+1)...)
		if q.Dialect() == transaction.MySQL {
			query = transaction.SQL(transaction.MySQL, my, append(myArgs, limit+1)...)
		}
		rows, err := q.Query(ctx, query)
		if err != nil {
			return err
		}
		stored := []storedDelivery{}
		for rows.Next() {
			value, e := scanDelivery(rows)
			if e != nil {
				rows.Close()
				return e
			}
			stored = append(stored, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		now, err := nowWithin(ctx, q)
		if err != nil {
			return err
		}
		out.Items = []Delivery{}
		for _, value := range stored {
			task, err := s.sourceWithin(ctx, q, value)
			if err != nil {
				return err
			}
			value.TaskState = task.State
			value.CanRetry = admin(a) && n.Status == "active" && (task.State == "pending" || task.State == "running") && value.State == "dead" && value.ClaimID == nil && now.Before(value.ExpiresAt)
			out.Items = append(out.Items, value.Delivery)
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			last := out.Items[limit-1]
			out.NextCursor = pagination.Encode(last.CreatedAt, last.ID, fingerprint)
		}
		return nil
	})
	if err != nil {
		return Page[Delivery]{}, err
	}
	return out, nil
}
func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, node, event string) (Delivery, error) {
	run, done, err := s.begin(ctx)
	if err != nil {
		return Delivery{}, err
	}
	defer done()
	if !taskbus.ValidID(event) {
		return Delivery{}, fail("RESOURCE_NOT_FOUND")
	}
	var out Delivery
	err = s.humanWithin(run, a, workspace, node, func(ctx context.Context, q transaction.Executor, n nodes.Node) error {
		value, err := deliveryWithin(ctx, q, n.TenantID, n.WorkspaceID, n.ID, event, false)
		if err != nil {
			return err
		}
		task, err := s.sourceWithin(ctx, q, value)
		if err != nil {
			return err
		}
		now, err := nowWithin(ctx, q)
		if err != nil {
			return err
		}
		value.TaskState = task.State
		value.CanRetry = admin(a) && n.Status == "active" && (task.State == "pending" || task.State == "running") && value.State == "dead" && value.ClaimID == nil && now.Before(value.ExpiresAt)
		out = value.Delivery
		return nil
	})
	if err != nil {
		return Delivery{}, err
	}
	return out, nil
}
