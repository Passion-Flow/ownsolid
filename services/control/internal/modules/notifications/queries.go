package notifications

import (
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func (s *Service) pageQuery(q transaction.Executor, a identity.Auth, tenant string, cursor pagination.Cursor, limit int) (transaction.Statement, error) {
	if limit < 1 || limit > pagination.MaximumLimit {
		return transaction.Statement{}, failure("VALIDATION_FAILED")
	}
	args := s.visibilityArgs(q, a, tenant)
	var query string
	switch q.Dialect() {
	case transaction.PostgreSQL:
		query = "SELECT " + fields + visible
		if cursor.ID != "" {
			query += " AND (n.occurred_at,n.id)<($4,$5::uuid)"
			args = append(args, cursor.Time, cursor.ID)
		}
		query += " ORDER BY n.occurred_at DESC,n.id DESC LIMIT $" + strconv.Itoa(len(args)+1)
		args = append(args, limit+1)
	case transaction.MySQL:
		query = "SELECT " + mysqlFields + mysqlVisible
		if cursor.ID != "" {
			query += " AND (n.occurred_at,n.id)<(?,?)"
			args = append(args, cursor.Time, cursor.ID)
		}
		query += " ORDER BY n.occurred_at DESC,n.id DESC LIMIT ?"
		args = append(args, limit+1)
	case transaction.Dameng:
		query = "SELECT " + damengFields + damengVisible
		if cursor.ID != "" {
			at := cursor.Time.UTC()
			if !strict.ValidID(cursor.ID) || !nativeCursorTime(at) {
				return transaction.Statement{}, failure("INVALID_CURSOR")
			}
			query += " AND (n.occurred_at<? OR (n.occurred_at=? AND n.id<?))"
			args = append(args, at, at, cursor.ID)
		}
		// The limit was validated above. Native LIMIT bindings are not assumed.
		query += " ORDER BY n.occurred_at DESC,n.id DESC LIMIT " + strconv.Itoa(limit+1)
	default:
		return transaction.Statement{}, failure("STORAGE_UNAVAILABLE")
	}
	return transaction.SQL(q.Dialect(), query, args...), nil
}

func (s *Service) cleanupQuery(q transaction.Executor, tenant string) (transaction.Statement, error) {
	p := s.policy()
	if e := p.Validate(); e != nil {
		return transaction.Statement{}, e
	}
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, "SELECT id::text FROM terminal_notifications WHERE tenant_id=$1 AND occurred_at<clock_timestamp()-make_interval(days=>$2) ORDER BY occurred_at,id LIMIT $3 FOR UPDATE SKIP LOCKED", tenant, p.NotificationRetentionDays, p.NotificationMaintenanceBatch), nil
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, "SELECT id FROM terminal_notifications WHERE tenant_id=? AND occurred_at<TIMESTAMPADD(DAY,?,UTC_TIMESTAMP(6)) ORDER BY occurred_at,id LIMIT ? FOR UPDATE SKIP LOCKED", tenant, -p.NotificationRetentionDays, p.NotificationMaintenanceBatch), nil
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, "SELECT id FROM terminal_notifications WHERE tenant_id=? AND occurred_at<TIMESTAMPADD(SQL_TSI_DAY,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) ORDER BY occurred_at,id LIMIT "+strconv.Itoa(p.NotificationMaintenanceBatch)+" FOR UPDATE SKIP LOCKED", tenant, -p.NotificationRetentionDays), nil
	default:
		return transaction.Statement{}, failure("STORAGE_UNAVAILABLE")
	}
}

func nativeCursorTime(at time.Time) bool {
	return !at.IsZero() && at.Location() == time.UTC && at.Year() >= 1970 && at.Year() <= 9999 && at.Nanosecond()%1000 == 0
}
