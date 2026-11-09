package identity

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

// BrowserSession describes a retained sign-in without exposing its credential or source identifiers.
type BrowserSession struct {
	ID                   string    `json:"id"`
	Current              bool      `json:"current"`
	CreatedAt            time.Time `json:"created_at"`
	LastSeenAt           time.Time `json:"last_seen_at"`
	AbsoluteExpiresAt    time.Time `json:"absolute_expires_at"`
	AuthenticationMethod string    `json:"authentication_method"`
}

// BrowserSessionPage contains one page of the caller's sessions in this entrance.
type BrowserSessionPage struct {
	Items      []BrowserSession `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

func browserSessionPlan(a Auth, idle time.Duration, limit int, cursor pagination.Cursor) listPlan {
	const fields = "s.created_at,s.last_seen_at,s.absolute_expires_at,CASE WHEN s.sso_connector_id IS NULL THEN 'password' ELSE 'sso' END"
	p := listPlan{
		alias: "s", order: "created_at", limit: limit,
		pgFrom: "sessions s", myFrom: "sessions s", dmFrom: "SESSIONS s",
		pgProjection: "s.id::text," + fields,
		myProjection: "s.id," + fields,
		dmProjection: "s.id," + fields,
		pgWhere:      []string{"s.revoked_at IS NULL", "s.absolute_expires_at>statement_timestamp()", "s.last_seen_at>statement_timestamp()-$1::bigint*interval '1 microsecond'"},
		myWhere:      []string{"s.revoked_at IS NULL", "s.absolute_expires_at>UTC_TIMESTAMP(6)", "s.last_seen_at>TIMESTAMPADD(MICROSECOND,-?,UTC_TIMESTAMP(6))"},
		dmWhere:      []string{"s.revoked_at IS NULL", "s.absolute_expires_at>CURRENT_TIMESTAMP(6)", "s.last_seen_at>CURRENT_TIMESTAMP(6)-NUMTODSINTERVAL(?/1000000.0,'SECOND')"},
		pgArgs:       []any{idle.Microseconds()}, myArgs: []any{idle.Microseconds()}, dmArgs: []any{idle.Microseconds()},
	}
	p.compare("s.user_id", "=", a.Me.User.ID, "")
	p.compare("s.audience", "=", a.Me.Audience, "")
	p.compare("s.auth_epoch", "=", a.Epoch, "")
	if cursor.ID != "" {
		p.seek(cursor.Time, cursor.ID)
	}
	return p
}

// ListBrowserSessions reads the caller's unrevoked, unexpired sessions for this entrance.
func (s *Service) ListBrowserSessions(ctx context.Context, a Auth, query url.Values) (BrowserSessionPage, error) {
	empty := BrowserSessionPage{Items: []BrowserSession{}}
	if !sessionActor(a) {
		return empty, failure("AUTH_SESSION_EXPIRED")
	}
	scope := "browser-sessions:" + a.Me.User.ID + ":" + a.Me.Audience
	limit, cursor, fingerprint, err := pagination.ParseWithDefault(query, scope, s.Config.Policy().UI.PageSize)
	if err != nil {
		return empty, err
	}
	store, err := s.Repository()
	if err != nil {
		return empty, err
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	p := browserSessionPlan(a, s.Idle(a.Me.Audience), limit, cursor)
	if store.Dialect() == transaction.Dameng {
		if err := p.validateDamengTimes(); err != nil {
			return empty, err
		}
	}
	page := empty
	err = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if err := s.RecheckWithin(ctx, tx, a); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, p.statement(tx.Dialect()))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item BrowserSession
			if err := rows.Scan(&item.ID, &item.CreatedAt, &item.LastSeenAt, &item.AbsoluteExpiresAt, &item.AuthenticationMethod); err != nil {
				return err
			}
			if len(page.Items) == limit {
				last := page.Items[limit-1]
				page.NextCursor = pagination.Encode(last.CreatedAt, last.ID, fingerprint)
				break
			}
			item.Current = item.ID == a.SessionID
			item.CreatedAt, item.LastSeenAt, item.AbsoluteExpiresAt = item.CreatedAt.UTC(), item.LastSeenAt.UTC(), item.AbsoluteExpiresAt.UTC()
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return empty, sessionError(err)
	}
	return page, nil
}

// RevokeBrowserSession ends one of the caller's sessions in this entrance.
// An already revoked session of the same account and entrance is a successful retry.
func (s *Service) RevokeBrowserSession(ctx context.Context, a Auth, id, request string) error {
	if !sessionActor(a) {
		return failure("AUTH_SESSION_EXPIRED")
	}
	if !sessionID(id) || !sessionID(request) {
		return failure("VALIDATION_FAILED")
	}
	store, err := s.Repository()
	if err != nil {
		return err
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	reason, auditReason := "session_revoked", "session_revoked"
	if id == a.SessionID {
		reason, auditReason = "logout", ""
	}
	err = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if err := s.RecheckWithin(ctx, tx, a); err != nil {
			return err
		}
		n, err := tx.Exec(ctx, identitySQL(tx,
			"UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason=$1 WHERE id=$2 AND user_id=$3 AND audience=$4 AND revoked_at IS NULL",
			"UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason=? WHERE id=? AND user_id=? AND audience=? AND revoked_at IS NULL",
			"UPDATE SESSIONS SET REVOKED_AT=CURRENT_TIMESTAMP(6),REVOKE_REASON=? WHERE ID=? AND USER_ID=? AND AUDIENCE=? AND REVOKED_AT IS NULL",
			reason, id, a.Me.User.ID, a.Me.Audience))
		if err != nil {
			return err
		}
		switch n {
		case 1:
			return audit.WriteWithin(ctx, tx, a.Me.User.ID, a.Me.User.ID, a.Me.Audience, "auth.logout", "success", "session", id, auditReason, request)
		case 0:
			var ended string
			err := tx.QueryRow(ctx, identitySQL(tx,
				"SELECT id::text FROM sessions WHERE id=$1 AND user_id=$2 AND audience=$3 AND revoked_at IS NOT NULL",
				"SELECT id FROM sessions WHERE id=? AND user_id=? AND audience=? AND revoked_at IS NOT NULL",
				"SELECT ID FROM SESSIONS WHERE ID=? AND USER_ID=? AND AUDIENCE=? AND REVOKED_AT IS NOT NULL",
				id, a.Me.User.ID, a.Me.Audience)).Scan(&ended)
			if errors.Is(err, sql.ErrNoRows) {
				return failure("RESOURCE_NOT_FOUND")
			}
			return err
		default:
			return failure("IDENTITY_WRITE_UNCONFIRMED")
		}
	})
	return sessionError(err)
}
