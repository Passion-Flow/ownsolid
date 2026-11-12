package http

import (
	"net/http"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

const auditActorHeader = "X-Ownsolid-Audit-Actor"

func (h *Handler) listAuditEvents(w http.ResponseWriter, r *http.Request, actor identity.Auth, requestID string) (any, int, error) {
	w.Header().Add("Vary", auditActorHeader)
	q := r.URL.Query()
	values := r.Header.Values(auditActorHeader)
	page, err := h.Service.ListAuditEventsForRequest(r.Context(), actor, q, requestID, len(values) == 1 && values[0] == "true")
	if err != nil {
		return nil, 0, err
	}
	for _, item := range page.Items {
		id, _ := item["id"].(string)
		stamp, _ := item["occurred_at"].(string)
		if assistant, ok := page.AssistantActors[identity.AuditEventKey{ID: id, OccurredAt: stamp}]; ok {
			item["actor_kind"] = assistant.Kind
			item["actor_id"] = assistant.ID
		}
	}
	return page.List, http.StatusOK, nil
}
