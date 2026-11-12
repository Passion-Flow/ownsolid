package audit

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

const (
	maxScopedCodeBytes   = 64
	maxScopedReasonBytes = 500
)

var (
	// ErrScopedEventInvalid means the trusted event violates the append contract.
	ErrScopedEventInvalid = errors.New("AUDIT_SCOPED_EVENT_INVALID")
	// ErrScopeDenied does not distinguish a missing scope from foreign attribution.
	ErrScopeDenied = errors.New("AUDIT_SCOPE_DENIED")
	scopedCode     = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)
)

// ScopedEvent contains attribution resolved by the authenticated server, never
// decoded from an HTTP request. The caller holds current session/epoch and
// operation authority in the same transaction. The first supported actor is an
// active platform Admin; workspace status does not restrict historical reads.
// SiteID and CellID must identify the database's current local cell. Optional
// target fields form a pair. Reason is bounded server-generated selection data,
// not raw query text, credentials or returned content.
type ScopedEvent struct {
	ActorUserID string
	TenantID    string
	WorkspaceID string
	SiteID      string
	CellID      string
	Audience    string
	Action      string
	Outcome     string
	TargetType  string
	TargetID    string
	Reason      string
	RequestID   string
}

// WriteScopedWithin appends one human Admin event with exact workspace and local
// cell attribution. It borrows a PostgreSQL transaction, rechecks database scope,
// and leaves session/operation authorization and commit authority with its caller.
// Every error must abort the caller's response or mutation. It neither commits
// nor retries; a successful append cannot acknowledge the eventual commit.
func WriteScopedWithin(ctx context.Context, tx transaction.Executor, event ScopedEvent) error {
	_, pool := tx.(transaction.Store)
	if tx == nil || pool {
		return errors.New("AUDIT_TRANSACTION_REQUIRED")
	}
	if tx.Dialect() != transaction.PostgreSQL {
		return errors.New("AUDIT_DIALECT_UNSUPPORTED")
	}
	if ctx == nil || !validScopedEvent(event) {
		return ErrScopedEventInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// This guard supplies attribution, not a second authentication mechanism.
	// Current authority locks belong to the consumer. No private chain-head or
	// mutation privilege on the authority tables is needed here.
	const insert = `
INSERT INTO audit_events (
    id, occurred_at, actor_user_id, tenant_id, workspace_id, site_id, cell_id,
    audience, action, outcome, target_type, target_id, reason, request_id
)
SELECT $1, clock_timestamp(), u.id, w.tenant_id, w.id, c.site_id, c.id,
    $7::text, $8, $9, NULLIF($10, ''), NULLIF($11, '')::uuid, NULLIF($12, ''), $13
FROM workspaces w
JOIN installation_state i ON i.id = 1 AND i.bootstrap_complete AND i.tenant_id = w.tenant_id
JOIN users u ON u.id = $4 AND u.status = 'active' AND u.is_platform_admin
JOIN cells c ON c.is_local AND c.site_id = $5 AND c.id = $6
WHERE w.tenant_id = $2 AND w.id = $3`
	rows, err := tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, insert,
		uuid.NewString(), event.TenantID, event.WorkspaceID, event.ActorUserID,
		event.SiteID, event.CellID, event.Audience, event.Action, event.Outcome,
		event.TargetType, event.TargetID, event.Reason, event.RequestID))
	if err != nil {
		return err
	}
	switch rows {
	case 0:
		return ErrScopeDenied
	case 1:
		return nil
	default:
		return errors.New("AUDIT_WRITE_UNCONFIRMED")
	}
}

func validScopedEvent(event ScopedEvent) bool {
	for _, value := range [...]string{event.ActorUserID, event.TenantID, event.WorkspaceID, event.RequestID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return false
		}
	}
	if !(agentwire.Scope{SiteID: event.SiteID, CellID: event.CellID}).Valid() || event.Audience != "admin" {
		return false
	}
	if len(event.Action) > maxScopedCodeBytes || !scopedCode.MatchString(event.Action) ||
		strings.HasPrefix(event.Action, productAssistantActionPrefix) {
		return false
	}
	switch event.Outcome {
	case "success", "failure", "accepted":
	default:
		return false
	}
	if event.TargetType != "" || event.TargetID != "" {
		id, err := uuid.Parse(event.TargetID)
		if len(event.TargetType) > maxScopedCodeBytes || !scopedCode.MatchString(event.TargetType) ||
			err != nil || id == uuid.Nil || id.String() != event.TargetID {
			return false
		}
	}
	if len(event.Reason) > maxScopedReasonBytes || !utf8.ValidString(event.Reason) {
		return false
	}
	return !strings.ContainsFunc(event.Reason, unicode.IsControl)
}
