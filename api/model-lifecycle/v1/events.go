package v1

import (
	"encoding/json"
	"errors"

	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const MaximumEventBatch = 64

type Event struct {
	Sequence      int64  `json:"sequence"`
	PreparationID string `json:"preparation_id"`
	AttemptID     string `json:"attempt_id"`
	Kind          string `json:"kind"`
	Phase         string `json:"phase"`
	CompletedRows *int64 `json:"completed_rows,omitempty"`
	TotalRows     *int64 `json:"total_rows,omitempty"`
}

type EventSubmission struct {
	LeaseToken string  `json:"lease_token"`
	Events     []Event `json:"events"`
}

type EventAcceptance struct {
	PreparationID string `json:"preparation_id"`
	AttemptID     string `json:"attempt_id"`
	Sequence      int64  `json:"sequence"`
	Duplicate     bool   `json:"duplicate"`
	State         string `json:"state"`
}

// ValidatePosition binds every phase and row to the actual frozen computation order.
func (e Event) ValidatePosition(preparation, attempt string, rows int64) error {
	bad := errors.New("MODEL_PREPARATION_EVENT_INVALID")
	if !dataset.ValidID(preparation) || !dataset.ValidID(attempt) || e.PreparationID != preparation || e.AttemptID != attempt || rows < MinimumRows || rows > MaximumRows || e.Sequence < 1 || e.Sequence > MaximumEvents {
		return bad
	}
	if e.Kind == "phase" {
		if e.CompletedRows != nil || e.TotalRows != nil {
			return bad
		}
		if e.Sequence == 1 && e.Phase == "evaluating_base" || e.Sequence == rows+2 && e.Phase == "merging" || e.Sequence == rows+3 && e.Phase == "evaluating_candidate" {
			return nil
		}
	} else if e.Kind == "evaluation_progress" && e.CompletedRows != nil && e.TotalRows != nil && *e.TotalRows == rows && *e.CompletedRows >= 1 && *e.CompletedRows <= rows {
		if e.Phase == "base" && e.Sequence == 1+*e.CompletedRows || e.Phase == "candidate" && e.Sequence == rows+3+*e.CompletedRows {
			return nil
		}
	}
	return bad
}

func DecodeEvent(raw []byte, preparation, attempt string, rows int64) (Event, error) {
	var e Event
	bad := errors.New("MODEL_PREPARATION_EVENT_INVALID")
	if len(raw) > MaximumEventBytes || DecodeStrict(raw, &e) != nil {
		return e, bad
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return e, bad
	}
	allowed := map[string]bool{"sequence": true, "preparation_id": true, "attempt_id": true, "kind": true, "phase": true}
	if e.Kind == "evaluation_progress" {
		allowed["completed_rows"] = true
		allowed["total_rows"] = true
	}
	if len(fields) != len(allowed) {
		return e, bad
	}
	for key := range fields {
		if !allowed[key] {
			return e, bad
		}
	}
	return e, e.ValidatePosition(preparation, attempt, rows)
}
