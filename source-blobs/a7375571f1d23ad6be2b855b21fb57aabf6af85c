package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

// FleetHistoryPath is the cell-relative candidate path for DEV's dispatcher.
// Owns continues to identify only the currently registered snapshot route.
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

// FleetHistoryProblem preserves candidate-specific statuses while reusing
// the canonical registry for all existing problems. The public route owner
// must add these codes to its registry before mounting the candidate.
func FleetHistoryProblem(err error) (string, int) {
	if err != nil {
		code := err.Error()
		switch code {
		case "MONITORING_HISTORY_RANGE_INVALID":
			return code, http.StatusBadRequest
		case "MONITORING_HISTORY_RESPONSE_LIMIT":
			return code, http.StatusUnprocessableEntity
		case "MONITORING_HISTORY_NOT_CONFIGURED", "MONITORING_LONG_TIER_NOT_CONFIGURED",
			"MONITORING_RAW_TIER_UNAVAILABLE", "MONITORING_LONG_TIER_UNAVAILABLE":
			return code, http.StatusServiceUnavailable
		}
	}
	return httpserver.PublicProblem(err)
}
