package taskdeliveries

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

// DeadLetter exposes current readable task delivery history without broker
// coordinates, message bodies, credentials or another actor's private retry text.
type DeadLetter struct {
	ID             string     `json:"id"`
	EventID        string     `json:"event_id"`
	Generation     int        `json:"generation"`
	Kind           string     `json:"kind"`
	OutcomeUnknown bool       `json:"outcome_unknown"`
	ErrorCode      string     `json:"error_code"`
	CreatedAt      time.Time  `json:"created_at"`
	RetriedAt      *time.Time `json:"retried_at"`
	NextGeneration *int       `json:"next_generation"`
}

func (s *Service) DeadLetters(ctx context.Context, a identity.Auth, workspace, node, event string, values url.Values) (Page[DeadLetter], error) {
	run, done, err := s.begin(ctx)
	if err != nil {
		return Page[DeadLetter]{}, err
	}
	defer done()
	if !taskbus.ValidID(event) {
		return Page[DeadLetter]{}, fail("RESOURCE_NOT_FOUND")
	}
	var out Page[DeadLetter]
	err = s.humanWithin(run, a, workspace, node, func(ctx context.Context, q transaction.Executor, n nodes.Node) error {
		value, err := deliveryWithin(ctx, q, n.TenantID, n.WorkspaceID, n.ID, event, false)
		if err != nil {
			return err
		}
		if _, err = s.sourceWithin(ctx, q, value); err != nil {
			return err
		}
		scope, err := s.historyScope(ctx, q, a, n)
		if err != nil {
			return err
		}
		limit, cursor, fingerprint, err := pagination.ParseWithDefault(values, scope+":dead-letters:"+event, s.Identity.Config.Policy().UI.PageSize)
		if err != nil {
			return err
		}
		if raw := values.Get("limit"); raw != "" && raw != strconv.Itoa(limit) {
			return fail("VALIDATION_FAILED")
		}
		pg, my := `SELECT id::text,event_id::text,generation,kind,outcome_unknown,error_code,created_at,retried_at,retry_generation FROM task_event_dead_letters WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND task_id=$4 AND event_id=$5`,
			`SELECT id,event_id,generation,kind,outcome_unknown,error_code,created_at,retried_at,retry_generation FROM task_event_dead_letters WHERE tenant_id=? AND workspace_id=? AND node_id=? AND task_id=? AND event_id=?`
		pgArgs, myArgs := []any{n.TenantID, n.WorkspaceID, n.ID, value.TaskID, event}, []any{n.TenantID, n.WorkspaceID, n.ID, value.TaskID, event}
		if cursor.ID != "" {
			pg += " AND (created_at,id)<($6,$7::uuid)"
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
		defer rows.Close()
		out.Items = []DeadLetter{}
		for rows.Next() {
			var item DeadLetter
			if err = rows.Scan(&item.ID, &item.EventID, &item.Generation, &item.Kind, &item.OutcomeUnknown, &item.ErrorCode, &item.CreatedAt, &item.RetriedAt, &item.NextGeneration); err != nil {
				return err
			}
			if !taskbus.ValidID(item.ID) || item.EventID != event || item.Generation < 1 || item.Generation > value.Generation || !storedErrorCode(item.ErrorCode) || item.Kind != "publish" && item.Kind != "consume" && item.Kind != "expiry" || (item.RetriedAt == nil) != (item.NextGeneration == nil) || item.NextGeneration != nil && *item.NextGeneration != item.Generation+1 {
				return fail("TASK_EVENT_INVALID")
			}
			item.CreatedAt = item.CreatedAt.UTC()
			if item.RetriedAt != nil {
				v := item.RetriedAt.UTC()
				item.RetriedAt = &v
			}
			out.Items = append(out.Items, item)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			last := out.Items[limit-1]
			out.NextCursor = pagination.Encode(last.CreatedAt, last.ID, fingerprint)
		}
		return nil
	})
	if err != nil {
		return Page[DeadLetter]{}, err
	}
	return out, nil
}
