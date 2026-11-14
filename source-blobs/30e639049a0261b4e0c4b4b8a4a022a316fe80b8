package taskdeliveries

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

type Quarantine struct {
	ID        string    `json:"id"`
	ErrorCode string    `json:"error_code"`
	CreatedAt time.Time `json:"created_at"`
}

// Quarantines is installation Admin metadata only. Untrusted message scope never
// enters Portal task history or grants human access to a node or Workspace.
func (s *Service) Quarantines(ctx context.Context, a identity.Auth, values url.Values) (Page[Quarantine], error) {
	if !admin(a) {
		return Page[Quarantine]{}, fail("ACCESS_DENIED")
	}
	run, done, err := s.begin(ctx)
	if err != nil {
		return Page[Quarantine]{}, err
	}
	defer done()
	store, err := s.repository()
	if err != nil {
		return Page[Quarantine]{}, err
	}
	bounded, cancel := context.WithTimeout(run, time.Duration(s.Configuration.RequestSeconds)*time.Second)
	defer cancel()
	var out Page[Quarantine]
	err = store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		var tenant string
		if e := q.QueryRow(ctx, statement(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete FOR SHARE", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1 FOR SHARE")).Scan(&tenant); e != nil {
			return e
		}
		if tenant != s.lifecycle.tenant {
			return fail("TASK_BUS_IDENTITY_CHANGED")
		}
		scope := strings.Join([]string{"task-delivery-quarantine", tenant, a.Me.User.ID, a.SessionID, strconv.FormatInt(a.Epoch, 10)}, ":")
		limit, cursor, fingerprint, e := pagination.ParseWithDefault(values, scope, s.Identity.Config.Policy().UI.PageSize)
		if e != nil {
			return e
		}
		if raw := values.Get("limit"); raw != "" && raw != strconv.Itoa(limit) {
			return fail("VALIDATION_FAILED")
		}
		pg, my := "SELECT id::text,error_code,created_at FROM task_event_dead_letters WHERE tenant_id=$1 AND kind='poison' AND event_id IS NULL", "SELECT id,error_code,created_at FROM task_event_dead_letters WHERE tenant_id=? AND kind='poison' AND event_id IS NULL"
		pa, ma := []any{tenant}, []any{tenant}
		if cursor.ID != "" {
			pg += " AND (created_at,id)<($2,$3::uuid)"
			my += " AND (created_at<? OR (created_at=? AND id<?))"
			pa = append(pa, cursor.Time, cursor.ID)
			ma = append(ma, cursor.Time, cursor.Time, cursor.ID)
		}
		pg += " ORDER BY created_at DESC,id DESC LIMIT $" + strconv.Itoa(len(pa)+1)
		my += " ORDER BY created_at DESC,id DESC LIMIT ?"
		query := transaction.SQL(transaction.PostgreSQL, pg, append(pa, limit+1)...)
		if q.Dialect() == transaction.MySQL {
			query = transaction.SQL(transaction.MySQL, my, append(ma, limit+1)...)
		}
		rows, e := q.Query(ctx, query)
		if e != nil {
			return e
		}
		defer rows.Close()
		out.Items = []Quarantine{}
		for rows.Next() {
			var v Quarantine
			if e = rows.Scan(&v.ID, &v.ErrorCode, &v.CreatedAt); e != nil {
				return e
			}
			if !taskbus.ValidID(v.ID) || !storedErrorCode(v.ErrorCode) {
				return fail("TASK_EVENT_INVALID")
			}
			v.CreatedAt = v.CreatedAt.UTC()
			out.Items = append(out.Items, v)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			last := out.Items[limit-1]
			out.NextCursor = pagination.Encode(last.CreatedAt, last.ID, fingerprint)
		}
		return nil
	})
	if err != nil {
		return Page[Quarantine]{}, safe(err)
	}
	return out, nil
}

func (s *Service) cleanupPoisonNext(ctx context.Context) (worked bool, err error) {
	defer func() {
		if err != nil {
			worked = false
		}
	}()
	store, err := s.repository()
	if err != nil {
		return false, err
	}
	err = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var tenant string
		if e := q.QueryRow(ctx, statement(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete FOR SHARE", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1 FOR SHARE")).Scan(&tenant); e != nil {
			return e
		}
		if tenant != s.lifecycle.tenant {
			return fail("TASK_BUS_IDENTITY_CHANGED")
		}
		var id string
		e := q.QueryRow(ctx, statement(q, "SELECT id::text FROM task_event_dead_letters WHERE tenant_id=$1 AND kind='poison' AND event_id IS NULL AND NOT outcome_unknown AND created_at<clock_timestamp()-make_interval(secs=>$2) ORDER BY created_at,id LIMIT 1 FOR UPDATE", "SELECT id FROM task_event_dead_letters WHERE tenant_id=? AND kind='poison' AND event_id IS NULL AND outcome_unknown=0 AND created_at<TIMESTAMPADD(SECOND,-?,UTC_TIMESTAMP(6)) ORDER BY created_at,id LIMIT 1 FOR UPDATE", tenant, s.Configuration.HistoryRetentionSeconds)).Scan(&id)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if !taskbus.ValidID(id) {
			return fail("TASK_EVENT_INVALID")
		}
		count, e := q.Exec(ctx, statement(q, "DELETE FROM task_event_dead_letters WHERE tenant_id=$1 AND id=$2 AND kind='poison' AND event_id IS NULL", "DELETE FROM task_event_dead_letters WHERE tenant_id=? AND id=? AND kind='poison' AND event_id IS NULL", tenant, id))
		if e != nil {
			return e
		}
		if count != 1 {
			return fail("TASK_EVENT_SOURCE_CHANGED")
		}
		_, e = q.Exec(ctx, statement(q, `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,'system','task_event.quarantine_removed','success','task_event_dead_letter',$3,'Removed exact aged poison coordinate after configured proof retention',$4)`,
			`INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,'system','task_event.quarantine_removed','success','task_event_dead_letter',?,'Removed exact aged poison coordinate after configured proof retention',?)`, uuid.NewString(), tenant, id, uuid.NewString()))
		if e != nil {
			return e
		}
		worked = true
		return nil
	})
	return worked, safe(err)
}

// Cleanup bounds the entire incremental pass by both row policy and request
// deadline. Earlier confirmed per-row transactions remain audited if a later
// transaction fails; callers receive zero count on every error/unknown outcome.
func (s *Service) Cleanup(ctx context.Context) (int, error) {
	run, done, err := s.begin(ctx)
	if err != nil {
		return 0, err
	}
	defer done()
	bounded, cancel := context.WithTimeout(run, time.Duration(s.Configuration.RequestSeconds)*time.Second)
	defer cancel()
	total := 0
	for total < s.Configuration.CleanupBatch {
		changed, e := s.CleanupNext(bounded)
		if e != nil {
			return 0, e
		}
		if !changed {
			changed, e = s.cleanupPoisonNext(bounded)
			if e != nil {
				return 0, e
			}
		}
		if !changed {
			return total, nil
		}
		total++
	}
	return total, nil
}
