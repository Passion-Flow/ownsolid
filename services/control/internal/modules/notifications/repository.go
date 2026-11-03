package notifications

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func notificationError(e error) error {
	if e == nil {
		return nil
	}
	// An uncertain COMMIT must survive a simultaneous cancellation or shutdown.
	// Check the bounded graph before errors.Is/As can follow malformed wrappers.
	if kind, ok := transaction.CommitOutcome(e); ok {
		if direct, ok := e.(*transaction.Error); ok && direct.Kind == kind {
			return direct
		}
		return &transaction.Error{Kind: kind}
	}
	if transaction.IsCancellationOnly(e) {
		if errors.Is(e, context.Canceled) {
			return &transaction.Error{Kind: transaction.Cancelled}
		}
		return &transaction.Error{Kind: transaction.Timeout}
	}
	var safe *transaction.Error
	if errors.As(e, &safe) && safe.Kind != transaction.Cancelled && safe.Kind != transaction.Timeout {
		return safe
	}
	code := e.Error()
	if len(code) > 120 {
		return failure("STORAGE_UNAVAILABLE")
	}
	for _, c := range code {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return failure("STORAGE_UNAVAILABLE")
		}
	}
	for _, prefix := range []string{"NOTIFICATION_", "AUTH_", "STORAGE_"} {
		if strings.HasPrefix(code, prefix) {
			return failure(code)
		}
	}
	switch code {
	case "ACCESS_DENIED", "VALIDATION_FAILED", "INVALID_CURSOR":
		return failure(code)
	}
	return failure("STORAGE_UNAVAILABLE")
}
func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil {
		return nil, failure("NOTIFICATION_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB {
		return nil, failure("NOTIFICATION_STORAGE_MISMATCH")
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return nil, notificationError(e)
	}
	return store, nil
}
func notificationSQL(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, args...)
	default:
		return transaction.SQL(q.Dialect(), "")
	}
}
func (s *Service) storageTransaction(ctx context.Context, fn func(context.Context, transaction.Executor) error) error {
	if ctx == nil {
		return failure("STORAGE_UNAVAILABLE")
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	if s.policy().Validate() != nil {
		return failure("NOTIFICATION_UNAVAILABLE")
	}
	operation, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	return notificationError(store.Within(operation, false, fn))
}
func (s *Service) transaction(ctx context.Context, a identity.Auth, fn func(context.Context, transaction.Executor, string) error) error {
	if !s.Available() {
		return failure("NOTIFICATION_UNAVAILABLE")
	}
	if a.Me.Audience != "portal" && (a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin) {
		return failure("ACCESS_DENIED")
	}
	return s.storageTransaction(ctx, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		var tenant string
		e := q.QueryRow(ctx, notificationSQL(q, `SELECT i.tenant_id::text FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' FOR SHARE OF i,t`, `SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' FOR SHARE`, `SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' FOR UPDATE OF i.tenant_id,t.id`)).Scan(&tenant)
		if errors.Is(e, sql.ErrNoRows) {
			return failure("ACCESS_DENIED")
		}
		if e != nil {
			return e
		}
		return fn(ctx, q, tenant)
	})
}

const fields = `n.id::text,n.workspace_id::text,n.workflow_id::text,n.run_id::text,n.outcome,n.error_code,n.occurred_at,n.read_at`
const mysqlFields = `n.id,n.workspace_id,n.workflow_id,n.run_id,n.outcome,n.error_code,n.occurred_at,n.read_at`
const damengFields = `n.id,n.workspace_id,n.workflow_id,n.run_id,n.outcome,n.error_code,n.occurred_at,n.read_at`

// Recipient/run owner and current membership are always server-derived. Notice
// metadata grants no authority to private workflow result content.
const visible = ` FROM terminal_notifications n
 JOIN workspaces w ON w.tenant_id=n.tenant_id AND w.id=n.workspace_id AND w.status='active'
 JOIN workspace_memberships m ON m.tenant_id=w.tenant_id AND m.workspace_id=w.id AND m.user_id=$1
 JOIN workflow_runs r ON r.tenant_id=n.tenant_id AND r.workspace_id=n.workspace_id AND r.workflow_id=n.workflow_id AND r.id=n.run_id AND r.owner_id=$1
 WHERE n.recipient_id=$1 AND n.tenant_id=$2 AND n.occurred_at>clock_timestamp()-make_interval(days=>$3)
 AND (r.conversation_id IS NULL OR EXISTS(SELECT 1 FROM workflow_conversations c WHERE c.tenant_id=r.tenant_id AND c.workspace_id=r.workspace_id AND c.workflow_id=r.workflow_id AND c.id=r.conversation_id AND c.owner_id=$1 AND c.deleted_at IS NULL))`
const mysqlVisible = ` FROM terminal_notifications n
 JOIN workspaces w ON w.tenant_id=n.tenant_id AND w.id=n.workspace_id AND w.status='active'
 JOIN workspace_memberships m ON m.tenant_id=w.tenant_id AND m.workspace_id=w.id AND m.user_id=?
 JOIN workflow_runs r ON r.tenant_id=n.tenant_id AND r.workspace_id=n.workspace_id AND r.workflow_id=n.workflow_id AND r.id=n.run_id AND r.owner_id=?
 WHERE n.recipient_id=? AND n.tenant_id=? AND n.occurred_at>TIMESTAMPADD(DAY,?,UTC_TIMESTAMP(6))
 AND (r.conversation_id IS NULL OR EXISTS(SELECT 1 FROM workflow_conversations c WHERE c.tenant_id=r.tenant_id AND c.workspace_id=r.workspace_id AND c.workflow_id=r.workflow_id AND c.id=r.conversation_id AND c.owner_id=? AND c.deleted_at IS NULL))`
const damengVisible = ` FROM terminal_notifications n
 JOIN workspaces w ON w.tenant_id=n.tenant_id AND w.id=n.workspace_id AND w.status='active'
 JOIN workspace_memberships m ON m.tenant_id=w.tenant_id AND m.workspace_id=w.id AND m.user_id=?
 JOIN workflow_runs r ON r.tenant_id=n.tenant_id AND r.workspace_id=n.workspace_id AND r.workflow_id=n.workflow_id AND r.id=n.run_id AND r.owner_id=?
 WHERE n.recipient_id=? AND n.tenant_id=? AND n.occurred_at>TIMESTAMPADD(SQL_TSI_DAY,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)))
 AND (r.conversation_id IS NULL OR EXISTS(SELECT 1 FROM workflow_conversations c WHERE c.tenant_id=r.tenant_id AND c.workspace_id=r.workspace_id AND c.workflow_id=r.workflow_id AND c.id=r.conversation_id AND c.owner_id=? AND c.deleted_at IS NULL))`

func (s *Service) visibilityArgs(q transaction.Executor, a identity.Auth, tenant string) []any {
	if q.Dialect() == transaction.MySQL || q.Dialect() == transaction.Dameng {
		return []any{a.Me.User.ID, a.Me.User.ID, a.Me.User.ID, tenant, -s.policy().NotificationRetentionDays, a.Me.User.ID}
	}
	return []any{a.Me.User.ID, tenant, s.policy().NotificationRetentionDays}
}
func notificationProjection(q transaction.Executor, pg, my, dm string, args []any) transaction.Statement {
	return notificationSQL(q, pg, my, dm, args...)
}
func scan(row transaction.Row) (Notification, error) {
	var n Notification
	e := row.Scan(&n.ID, &n.WorkspaceID, &n.WorkflowID, &n.RunID, &n.Outcome, &n.ErrorCode, &n.OccurredAt, &n.ReadAt)
	if errors.Is(e, sql.ErrNoRows) {
		return Notification{}, failure("NOTIFICATION_NOT_FOUND")
	}
	if e != nil {
		return Notification{}, notificationError(e)
	}
	n.OccurredAt = n.OccurredAt.UTC()
	if n.ReadAt != nil {
		at := n.ReadAt.UTC()
		n.ReadAt = &at
	}
	return n, nil
}
func (s *Service) Configuration(ctx context.Context, a identity.Auth) (Configuration, error) {
	var out Configuration
	e := s.transaction(ctx, a, func(ctx context.Context, q transaction.Executor, tenant string) error {
		out = Configuration{Available: true, RetentionDays: s.policy().NotificationRetentionDays}
		return q.QueryRow(ctx, notificationProjection(q, "SELECT COUNT(*)"+visible+" AND n.read_at IS NULL", "SELECT COUNT(*)"+mysqlVisible+" AND n.read_at IS NULL", "SELECT COUNT(*)"+damengVisible+" AND n.read_at IS NULL", s.visibilityArgs(q, a, tenant))).Scan(&out.UnreadCount)
	})
	if e != nil {
		return Configuration{}, e
	}
	return out, nil
}
func (s *Service) List(ctx context.Context, a identity.Auth, values url.Values) (Page, error) {
	empty := Page{Items: []Notification{}}
	if s == nil || s.Identity == nil {
		return empty, failure("NOTIFICATION_IDENTITY_REQUIRED")
	}
	limit, cursor, scope, e := pagination.ParseWithDefault(values, "terminal-notifications:"+a.Me.Audience+":"+a.Me.User.ID, s.Identity.Config.Policy().UI.PageSize)
	if e != nil {
		return empty, e
	}
	out := Page{Items: []Notification{}}
	e = s.transaction(ctx, a, func(ctx context.Context, q transaction.Executor, tenant string) error {
		query, e := s.pageQuery(q, a, tenant, cursor, limit)
		if e != nil {
			return e
		}
		rows, e := q.Query(ctx, query)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			n, e := scan(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, n)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			last := out.Items[limit-1]
			out.NextCursor = pagination.Encode(last.OccurredAt, last.ID, scope)
		}
		return nil
	})
	if e != nil {
		return empty, e
	}
	return out, nil
}
func (s *Service) Get(ctx context.Context, a identity.Auth, id string) (Notification, error) {
	var out Notification
	if !strict.ValidID(id) {
		return out, failure("NOTIFICATION_NOT_FOUND")
	}
	e := s.transaction(ctx, a, func(ctx context.Context, q transaction.Executor, tenant string) error {
		args := append(s.visibilityArgs(q, a, tenant), id)
		var e error
		out, e = scan(q.QueryRow(ctx, notificationProjection(q, "SELECT "+fields+visible+" AND n.id=$4", "SELECT "+mysqlFields+mysqlVisible+" AND n.id=?", "SELECT "+damengFields+damengVisible+" AND n.id=?", args)))
		return e
	})
	if e != nil {
		return Notification{}, e
	}
	return out, nil
}
func (s *Service) Read(ctx context.Context, a identity.Auth, id, request string) (Notification, error) {
	var out Notification
	if !strict.ValidID(id) {
		return out, failure("NOTIFICATION_NOT_FOUND")
	}
	e := s.transaction(ctx, a, func(ctx context.Context, q transaction.Executor, tenant string) error {
		var e error
		out, e = s.readWithin(ctx, q, a, tenant, id, request)
		return e
	})
	if e != nil {
		return Notification{}, e
	}
	return out, nil
}

// readWithin requires the current identity and installation locks from transaction.
func (s *Service) readWithin(ctx context.Context, q transaction.Executor, a identity.Auth, tenant, id, request string) (Notification, error) {
	var out Notification
	e := func() error {
		args := append(s.visibilityArgs(q, a, tenant), id)
		var e error
		out, e = scan(q.QueryRow(ctx, notificationProjection(q, "SELECT "+fields+visible+" AND n.id=$4 FOR UPDATE OF n", "SELECT "+mysqlFields+mysqlVisible+" AND n.id=? FOR UPDATE", "SELECT "+damengFields+damengVisible+" AND n.id=? FOR UPDATE", args)))
		if e != nil {
			return e
		}
		if out.ReadAt != nil {
			return nil
		}
		changed, e := q.Exec(ctx, notificationSQL(q, "UPDATE terminal_notifications SET read_at=clock_timestamp() WHERE tenant_id=$1 AND recipient_id=$2 AND id=$3 AND read_at IS NULL", "UPDATE terminal_notifications SET read_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND recipient_id=? AND id=? AND read_at IS NULL", "UPDATE terminal_notifications SET read_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND recipient_id=? AND id=? AND read_at IS NULL", tenant, a.Me.User.ID, id))
		if e != nil {
			return e
		}
		if changed != 1 {
			return failure("STORAGE_UNAVAILABLE")
		}
		var at time.Time
		if e = q.QueryRow(ctx, notificationSQL(q, "SELECT read_at FROM terminal_notifications WHERE tenant_id=$1 AND recipient_id=$2 AND id=$3", "SELECT read_at FROM terminal_notifications WHERE tenant_id=? AND recipient_id=? AND id=?", "SELECT read_at FROM terminal_notifications WHERE tenant_id=? AND recipient_id=? AND id=?", tenant, a.Me.User.ID, id)).Scan(&at); e != nil {
			return e
		}
		at = at.UTC()
		out.ReadAt = &at
		changed, e = q.Exec(ctx, notificationSQL(q, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,'notification.read','success','terminal_notification',$6,'Recipient read internal task notice',$7)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,?,'notification.read','success','terminal_notification',?,'Recipient read internal task notice',?)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,?,?,?,'notification.read','success','terminal_notification',?,'Recipient read internal task notice',?)`, uuid.NewString(), a.Me.User.ID, tenant, out.WorkspaceID, a.Me.Audience, id, request))
		if e == nil && changed != 1 {
			return failure("STORAGE_UNAVAILABLE")
		}
		return e
	}()
	if e != nil {
		return Notification{}, e
	}
	return out, nil
}

func (s *Service) VerifyStored(ctx context.Context) error {
	if e := s.Readiness(); e != nil {
		return e
	}
	return s.storageTransaction(ctx, func(ctx context.Context, q transaction.Executor) error {
		var invalid bool
		e := q.QueryRow(ctx, notificationSQL(q, `SELECT EXISTS(SELECT 1 FROM terminal_notifications n JOIN installation_state i ON i.tenant_id=n.tenant_id LEFT JOIN workflow_runs r ON r.tenant_id=n.tenant_id AND r.workspace_id=n.workspace_id AND r.workflow_id=n.workflow_id AND r.id=n.run_id AND r.owner_id=n.recipient_id WHERE i.id=1 AND (r.id IS NULL OR r.state<>n.outcome OR r.error_code IS DISTINCT FROM n.error_code OR r.finished_at IS NULL))`, `SELECT EXISTS(SELECT 1 FROM terminal_notifications n JOIN installation_state i ON i.tenant_id=n.tenant_id LEFT JOIN workflow_runs r ON r.tenant_id=n.tenant_id AND r.workspace_id=n.workspace_id AND r.workflow_id=n.workflow_id AND r.id=n.run_id AND r.owner_id=n.recipient_id WHERE i.id=1 AND (r.id IS NULL OR r.state<>n.outcome OR NOT (r.error_code <=> n.error_code) OR r.finished_at IS NULL))`, `SELECT CASE WHEN EXISTS(SELECT 1 FROM terminal_notifications n JOIN installation_state i ON i.tenant_id=n.tenant_id LEFT JOIN workflow_runs r ON r.tenant_id=n.tenant_id AND r.workspace_id=n.workspace_id AND r.workflow_id=n.workflow_id AND r.id=n.run_id AND r.owner_id=n.recipient_id WHERE i.id=1 AND (r.id IS NULL OR r.state<>n.outcome OR r.error_code<>n.error_code OR (r.error_code IS NULL AND n.error_code IS NOT NULL) OR (r.error_code IS NOT NULL AND n.error_code IS NULL) OR r.finished_at IS NULL)) THEN 1 ELSE 0 END FROM DUAL`)).Scan(&invalid)
		if e != nil {
			return e
		}
		if invalid {
			return failure("NOTIFICATION_PROVENANCE_INVALID")
		}
		return nil
	})
}
func (s *Service) Cleanup(ctx context.Context) (int, error) {
	count := 0
	e := s.storageTransaction(ctx, func(ctx context.Context, q transaction.Executor) error {
		var tenant string
		e := q.QueryRow(ctx, notificationSQL(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete FOR SHARE", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1 FOR SHARE", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1 FOR UPDATE")).Scan(&tenant)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		query, e := s.cleanupQuery(q, tenant)
		if e != nil {
			return e
		}
		rows, e := q.Query(ctx, query)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			changed, e := q.Exec(ctx, notificationSQL(q, "DELETE FROM terminal_notifications WHERE tenant_id=$1 AND id=$2", "DELETE FROM terminal_notifications WHERE tenant_id=? AND id=?", "DELETE FROM terminal_notifications WHERE tenant_id=? AND id=?", tenant, id))
			if e != nil {
				return e
			}
			if changed != 1 {
				return failure("STORAGE_UNAVAILABLE")
			}
			count++
		}
		if count == 0 {
			return nil
		}
		changed, e := q.Exec(ctx, notificationSQL(q, `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,'system','notification.retention','success','installation',$3,'Expired bounded internal terminal notices',$4)`, `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,'system','notification.retention','success','installation',?,'Expired bounded internal terminal notices',?)`, `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,'system','notification.retention','success','installation',?,'Expired bounded internal terminal notices',?)`, uuid.NewString(), tenant, tenant, uuid.NewString()))
		if e == nil && changed != 1 {
			return failure("STORAGE_UNAVAILABLE")
		}
		return e
	})
	if e != nil {
		return 0, e
	}
	return count, nil
}
