package identity

import (
	"context"
	"fmt"
	"net/url"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ListAuditEventsForRequest reads the current installation-wide Admin view and
// accounts for access using the transport's server-generated request ID. The
// representation flag selects assistant attribution, never access accounting.
func (s *Service) ListAuditEventsForRequest(ctx context.Context, a Auth, q url.Values, requestID string, includeAssistant bool) (AuditEventList, error) {
	if ctx == nil || !sessionID(requestID) {
		return AuditEventList{List: List{Items: []map[string]any{}}}, failure("VALIDATION_FAILED")
	}
	if includeAssistant {
		return s.listAuditEventsForActor(ctx, a, q, nil, requestID)
	}
	list, err := s.listForActor(ctx, a, "audit-events", q, nil, requestID)
	return AuditEventList{List: list}, err
}

func recordAuditReadWithin(ctx context.Context, tx transaction.Executor, a Auth, limit, returned int, assistant bool, requestID string) error {
	if limit < 1 || limit > maximumListLimit || returned < 0 || returned > limit {
		return failure("STORAGE_UNAVAILABLE")
	}
	// The writer resolves installation scope. This path accounts for the global
	// Admin view; a workspace-specific view needs its own scoped writer.
	reason := fmt.Sprintf("installation_audit_read;limit=%d;returned=%d;assistant=%t", limit, returned, assistant)
	return audit.WriteWithin(ctx, tx, a.Me.User.ID, "", "admin", "audit.events_read", "success", "installation", "", reason, requestID)
}
