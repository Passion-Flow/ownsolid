package identity

import (
	"context"
	"fmt"
	"net/url"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
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

func recordAuditReadWithin(ctx context.Context, tx transaction.Executor, a Auth, workspaceID string, limit, returned int, assistant bool, requestID string) error {
	if limit < 1 || limit > maximumListLimit || returned < 0 || returned > limit {
		return failure("STORAGE_UNAVAILABLE")
	}
	if workspaceID != "" {
		var tenantID, siteID, cellID string
		const scope = `SELECT i.tenant_id::text,c.site_id,c.id
FROM installation_state i CROSS JOIN cells c
WHERE i.id=1 AND i.bootstrap_complete AND c.is_local`
		if err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, scope)).Scan(&tenantID, &siteID, &cellID); err != nil {
			return err
		}
		if requestID == "" {
			requestID = uuid.NewString()
		}
		return audit.WriteScopedWithin(ctx, tx, audit.ScopedEvent{
			ActorUserID: a.Me.User.ID, TenantID: tenantID, WorkspaceID: workspaceID,
			SiteID: siteID, CellID: cellID, Audience: "admin", Action: "audit.events_read", Outcome: "success",
			TargetType: "workspace", TargetID: workspaceID, RequestID: requestID,
			Reason: fmt.Sprintf("workspace_audit_read;limit=%d;returned=%d;assistant=%t", limit, returned, assistant),
		})
	}
	// The installation writer resolves its own default workspace attribution.
	reason := fmt.Sprintf("installation_audit_read;limit=%d;returned=%d;assistant=%t", limit, returned, assistant)
	return audit.WriteWithin(ctx, tx, a.Me.User.ID, "", "admin", "audit.events_read", "success", "installation", "", reason, requestID)
}
