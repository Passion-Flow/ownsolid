package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func sourceSessionDeadline(value time.Time) bool {
	_, offset := value.Zone()
	return !value.IsZero() && value.Year() >= 1970 && value.Year() <= 9999 && offset == 0 && value.Nanosecond()%int(time.Microsecond) == 0
}

// IssueSessionUntilWithin issues an externally sourced session with a hard source
// deadline, retaining the ordinary source, account, MFA, rotation and audit owners.
// The caller owns the transaction and must roll it back on any returned error.
// This method never lengthens the configured absolute lifetime or commits itself.
func (s *Service) IssueSessionUntilWithin(ctx context.Context, q transaction.Executor, user string, epoch int64, audience, previous, request string, source SessionSource, until time.Time) (IssuedSession, error) {
	if ctx == nil || !validSource(source) || !sourceSessionDeadline(until) {
		return IssuedSession{}, failure("AUTH_SOURCE_DEADLINE_INVALID")
	}
	if e := s.sessionExecutor(q); e != nil {
		return IssuedSession{}, e
	}
	var fresh bool
	e := q.QueryRow(ctx, identitySQL(q, `SELECT clock_timestamp()<$1`, `SELECT UTC_TIMESTAMP(6)<?`, `SELECT CASE WHEN CURRENT_TIMESTAMP(6)<? THEN 1 ELSE 0 END FROM DUAL`, until)).Scan(&fresh)
	if e != nil {
		return IssuedSession{}, e
	}
	if !fresh {
		return IssuedSession{}, failure("AUTH_SESSION_EXPIRED")
	}
	issued, e := s.IssueSessionWithin(ctx, q, user, epoch, audience, previous, request, &source)
	if e != nil {
		return IssuedSession{}, e
	}
	tokenHash := security.Digest(issued.Token)
	n, e := q.Exec(ctx, identitySQL(q,
		`UPDATE sessions SET absolute_expires_at=LEAST(absolute_expires_at,$1) WHERE id=$2 AND user_id=$3 AND token_hash=$4 AND audience=$5 AND auth_epoch=$6 AND revoked_at IS NULL AND created_at<$1`,
		`UPDATE sessions SET absolute_expires_at=LEAST(absolute_expires_at,?) WHERE id=? AND user_id=? AND token_hash=? AND audience=? AND auth_epoch=? AND revoked_at IS NULL AND created_at<?`,
		`UPDATE SESSIONS SET ABSOLUTE_EXPIRES_AT=LEAST(ABSOLUTE_EXPIRES_AT,?) WHERE ID=? AND USER_ID=? AND TOKEN_HASH=? AND AUDIENCE=? AND AUTH_EPOCH=? AND REVOKED_AT IS NULL AND CREATED_AT<?`,
		sourceDeadlineArguments(q, until, issued, user, epoch, audience, tokenHash)...))
	if e != nil {
		return IssuedSession{}, e
	}
	if n < 0 || n > 1 {
		return IssuedSession{}, failure("IDENTITY_WRITE_UNCONFIRMED")
	}
	// MySQL can report zero changed rows when its configured lifetime was shorter.
	// Confirm the exact postimage rather than interpreting that as a lost write.
	var expires time.Time
	var tenant, connector, binding, flow string
	var version int64
	e = q.QueryRow(ctx, identitySQL(q,
		`SELECT absolute_expires_at,sso_tenant_id::text,sso_connector_id::text,sso_binding_id::text,sso_flow_id::text,sso_auth_version FROM sessions WHERE id=$1 AND user_id=$2 AND token_hash=$3 AND audience=$4 AND auth_epoch=$5 AND revoked_at IS NULL AND absolute_expires_at>clock_timestamp()`,
		`SELECT absolute_expires_at,sso_tenant_id,sso_connector_id,sso_binding_id,sso_flow_id,sso_auth_version FROM sessions WHERE id=? AND user_id=? AND token_hash=? AND audience=? AND auth_epoch=? AND revoked_at IS NULL AND absolute_expires_at>UTC_TIMESTAMP(6)`,
		`SELECT ABSOLUTE_EXPIRES_AT,SSO_TENANT_ID,SSO_CONNECTOR_ID,SSO_BINDING_ID,SSO_FLOW_ID,SSO_AUTH_VERSION FROM SESSIONS WHERE ID=? AND USER_ID=? AND TOKEN_HASH=? AND AUDIENCE=? AND AUTH_EPOCH=? AND REVOKED_AT IS NULL AND ABSOLUTE_EXPIRES_AT>CURRENT_TIMESTAMP(6)`, issued.SessionID, user, tokenHash, audience, epoch)).Scan(&expires, &tenant, &connector, &binding, &flow, &version)
	if errors.Is(e, sql.ErrNoRows) {
		return IssuedSession{}, failure("AUTH_SESSION_EXPIRED")
	}
	if e != nil {
		return IssuedSession{}, e
	}
	expires = expires.UTC()
	if !sourceSessionDeadline(expires) || expires.After(until) || expires.After(issued.ExpiresAt) || tenant != source.TenantID || connector != source.ConnectorID || binding != source.BindingID || flow != source.FlowID || version != source.AuthVersion {
		return IssuedSession{}, failure("IDENTITY_WRITE_UNCONFIRMED")
	}
	issued.ExpiresAt = expires
	return issued, nil
}

func sourceDeadlineArguments(q transaction.Executor, until time.Time, issued IssuedSession, user string, epoch int64, audience string, tokenHash []byte) []any {
	args := []any{until, issued.SessionID, user, tokenHash, audience, epoch}
	if q.Dialect() != transaction.PostgreSQL {
		args = append(args, until)
	}
	return args
}
