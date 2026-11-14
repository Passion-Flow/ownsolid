package identity

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// SessionSource records the exact local authority established by an external login.
// It never contains a provider token, role, email, or other provider-controlled authority.
type SessionSource struct {
	TenantID, ConnectorID, BindingID, FlowID string
	AuthVersion                              int64
}

// SourceGuard is injected once at startup. A nil transaction permits a read check;
// a nonnil transaction must hold shared source-authority locks through its commit.
type SourceGuard interface {
	CheckSessionSource(context.Context, pgx.Tx, SessionSource, string) error
	CheckSessionSourceWithin(context.Context, transaction.Executor, SessionSource, string) error
}

type IssuedSession struct {
	Me        Me
	Token     string
	SessionID string
	ExpiresAt time.Time
}

func validSource(source SessionSource) bool {
	for _, value := range []string{source.TenantID, source.ConnectorID, source.BindingID, source.FlowID} {
		id, e := uuid.Parse(value)
		if e != nil || id == uuid.Nil || id.String() != value {
			return false
		}
	}
	return source.AuthVersion > 0 && source.AuthVersion <= MaximumAuthVersion
}

// LockSessionUsers borrows a real PostgreSQL caller transaction.
func (s *Service) LockSessionUsers(ctx context.Context, tx pgx.Tx, userID, previous, audience string) error {
	q, e := s.sessionTransaction(tx)
	if e != nil {
		return e
	}
	return s.LockSessionUsersWithin(ctx, q, userID, previous, audience)
}

// IssueSession leaves commit/rollback authority with the actual pgx caller.
func (s *Service) IssueSession(ctx context.Context, tx pgx.Tx, userID string, epoch int64, audience, previous, requestID string, source *SessionSource) (IssuedSession, error) {
	q, e := s.sessionTransaction(tx)
	if e != nil {
		return IssuedSession{}, e
	}
	return s.IssueSessionWithin(ctx, q, userID, epoch, audience, previous, requestID, source)
}

// PasswordConfirmation is an opaque proof of verification against one current local
// credential and browser session. It is not a login or reusable bearer credential.
type PasswordConfirmation struct {
	user, session, audience string
	tokenHash               []byte
	epoch, version          int64
}

func (p PasswordConfirmation) Version() int64 { return p.version }

func (s *Service) RecheckConfirmation(ctx context.Context, tx pgx.Tx, a Auth, proof PasswordConfirmation) error {
	q, e := s.sessionTransaction(tx)
	if e != nil {
		return e
	}
	return s.RecheckConfirmationWithin(ctx, q, a, proof)
}

// RecheckCredentialVersion requires an earlier ordinary-session/user write fence.
func (s *Service) RecheckCredentialVersion(ctx context.Context, tx pgx.Tx, a Auth, version int64) error {
	q, e := s.sessionTransaction(tx)
	if e != nil {
		return e
	}
	return s.RecheckCredentialVersionWithin(ctx, q, a, version)
}
