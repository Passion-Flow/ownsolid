package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
)

// SeriesCardinalityPath is the unregistered cell-relative Admin candidate route.
// Publication must coordinate Owns, the dispatcher, schema and Problem registry.
const SeriesCardinalityPath = Path + "/series-cardinality"

// The largest metric's percent encoding plus scope, date and rank options fits
// this bound. Limit the raw input before allocating a decoded query map.
const maximumCardinalityQueryBytes = 4 << 10

// HandleSeriesCardinality prepares a bounded read behind the caller's original
// Admin authentication and Origin checks. It is deliberately outside Owns until
// the public API contract is coordinated.
func (h *Handler) HandleSeriesCardinality(r *http.Request, a identity.Auth, requestID string) (json.RawMessage, int, error) {
	if err := h.checkRead(r, a, SeriesCardinalityPath); err != nil {
		return nil, 0, err
	}
	workspace, selection, err := parseSeriesCardinalityQuery(r.URL.RawQuery)
	if err != nil {
		return nil, 0, err
	}
	body, err := h.Service.GetSeriesCardinalityJSON(r.Context(), a, workspace, selection, requestID)
	if err != nil {
		return nil, 0, err
	}
	return body, http.StatusOK, nil
}

func parseSeriesCardinalityQuery(raw string) (string, monitoring.SeriesCardinalityQuery, error) {
	empty := monitoring.SeriesCardinalityQuery{}
	bad := errors.New("VALIDATION_FAILED")
	if len(raw) > maximumCardinalityQueryBytes {
		return "", empty, bad
	}
	values, err := url.ParseQuery(raw)
	if err != nil || len(values["workspace_id"]) != 1 || values.Get("workspace_id") == "" || len(values["date"]) != 1 {
		return "", empty, bad
	}
	for key, entries := range values {
		if len(entries) != 1 || key != "workspace_id" && key != "date" && key != "metric" && key != "limit" {
			return "", empty, bad
		}
	}
	at, err := time.Parse(time.DateOnly, values.Get("date"))
	if err != nil || at.Format(time.DateOnly) != values.Get("date") {
		return "", empty, bad
	}
	selection := monitoring.SeriesCardinalityQuery{Date: at}
	if metric, present := values["metric"]; present {
		if metric[0] == "" {
			return "", empty, bad
		}
		selection.Metric = metric[0]
	}
	if limit, present := values["limit"]; present {
		value, err := strconv.Atoi(limit[0])
		if err != nil || value <= 0 || strconv.Itoa(value) != limit[0] {
			return "", empty, bad
		}
		selection.Limit = value
	}
	if selection.Validate() != nil {
		return "", empty, bad
	}
	return values.Get("workspace_id"), selection, nil
}
