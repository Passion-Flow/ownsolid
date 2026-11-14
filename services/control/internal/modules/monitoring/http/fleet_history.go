package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

// FleetHistoryPath is the cell-relative Admin history route.
// The original dispatcher supplies authentication, Origin checks and cell scope.
const FleetHistoryPath = Path + "/fleet-history"

const maximumHistoryQueryBytes = 512

// HandleFleetHistory prepares the complete bounded response after the caller's
// Admin authentication and Origin checks. The service rechecks authority and
// commits its audit before releasing the prepared body.
func (h *Handler) HandleFleetHistory(r *http.Request, a identity.Auth, requestID string) (json.RawMessage, int, error) {
	if err := h.checkRead(r, a, FleetHistoryPath); err != nil {
		return nil, 0, err
	}
	bad := errors.New("VALIDATION_FAILED")
	if len(r.URL.RawQuery) > maximumHistoryQueryBytes {
		return nil, 0, bad
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 3 || len(q["workspace_id"]) != 1 || len(q["from"]) != 1 || len(q["to"]) != 1 {
		return nil, 0, bad
	}
	from, startOK := historyTime(q.Get("from"))
	to, endOK := historyTime(q.Get("to"))
	if !startOK || !endOK || !from.Before(to) {
		return nil, 0, bad
	}
	body, err := h.Service.GetFleetHistoryJSON(r.Context(), a, q.Get("workspace_id"), from, to, requestID)
	if err != nil {
		return nil, 0, err
	}
	return body, http.StatusOK, nil
}

func historyTime(raw string) (time.Time, bool) {
	const format = "2006-01-02T15:04:05Z"
	at, err := time.Parse(format, raw)
	return at, err == nil && at.Unix() >= 0 && at.Format(format) == raw
}
