package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func (h *Handler) preparationInput(r *http.Request, parts []string, credential string) (any, int, error) {
	if h.PreparationInputFile == nil {
		return nil, 0, errors.New("MODEL_PREPARATION_UNAVAILABLE")
	}
	var raw json.RawMessage
	if e := httpserver.DecodeJSON(r, &raw); e != nil {
		return nil, 0, e
	}
	var input wire.InputFileRequest
	if wire.DecodeStrict(raw, &input) != nil || input.Validate() != nil {
		return nil, 0, errors.New("MODEL_PREPARATION_TRANSFER_INVALID")
	}
	out, e := h.PreparationInputFile(r.Context(), parts[1], credential, parts[3], input)
	return out, 200, e
}

func (h *Handler) preparationEvents(r *http.Request, parts []string, credential string) (any, int, error) {
	if h.PreparationEvents == nil {
		return nil, 0, errors.New("MODEL_PREPARATION_UNAVAILABLE")
	}
	var raw json.RawMessage
	if e := httpserver.DecodeJSON(r, &raw); e != nil {
		return nil, 0, e
	}
	var input wire.EventSubmission
	if wire.DecodeStrict(raw, &input) != nil || len(input.Events) < 1 || len(input.Events) > wire.MaximumEventBatch {
		return nil, 0, errors.New("MODEL_PREPARATION_EVENT_INVALID")
	}
	var fields map[string]json.RawMessage
	var events []json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 || json.Unmarshal(fields["events"], &events) != nil {
		return nil, 0, errors.New("MODEL_PREPARATION_EVENT_INVALID")
	}
	values := make([][]byte, len(events))
	for index, event := range events {
		values[index] = event
	}
	out, e := h.PreparationEvents(r.Context(), parts[1], credential, parts[3], input.LeaseToken, values)
	return map[string]any{"items": out}, 200, e
}
