package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type MetricValues struct {
	Loss         *float64 `json:"loss,omitempty"`
	LearningRate *float64 `json:"learning_rate,omitempty"`
	GradientNorm *float64 `json:"grad_norm,omitempty"`
	Epoch        *float64 `json:"epoch,omitempty"`
}

type EventSubmission struct {
	LeaseToken string  `json:"lease_token"`
	Events     []Event `json:"events"`
}
type EventAcceptance struct {
	JobID     string `json:"job_id"`
	AttemptID string `json:"attempt_id"`
	Sequence  int64  `json:"sequence"`
	Duplicate bool   `json:"duplicate"`
	JobState  string `json:"job_state"`
}

func (v MetricValues) Validate() error {
	count := 0
	for index, value := range []*float64{v.Loss, v.LearningRate, v.GradientNorm, v.Epoch} {
		if value == nil {
			continue
		}
		count++
		if math.IsNaN(*value) || math.IsInf(*value, 0) || index > 0 && *value < 0 {
			return errors.New("TRAINING_METRIC_INVALID")
		}
	}
	if count == 0 {
		return errors.New("TRAINING_METRIC_INVALID")
	}
	return nil
}
func metricNulls(raw []byte) error {
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return errors.New("TRAINING_METRIC_INVALID")
	}
	for _, value := range values {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("TRAINING_METRIC_INVALID")
		}
	}
	return nil
}

type Metric struct {
	Kind       string       `json:"kind"`
	GlobalStep int64        `json:"global_step"`
	Values     MetricValues `json:"values"`
}
type Event struct {
	Sequence      int64         `json:"sequence"`
	JobID         string        `json:"job_id"`
	AttemptID     string        `json:"attempt_id"`
	Kind          string        `json:"kind"`
	GlobalStep    int64         `json:"global_step"`
	Phase         *string       `json:"phase,omitempty"`
	Values        *MetricValues `json:"values,omitempty"`
	ReceiptSHA256 *string       `json:"receipt_sha256,omitempty"`
	Path          *string       `json:"path,omitempty"`
}

func (e Event) Validate(job, attempt string, maximumSteps int64) error {
	if !datawire.ValidID(job) || !datawire.ValidID(attempt) || e.JobID != job || e.AttemptID != attempt || e.Sequence < 1 || e.Sequence > MaximumEventSequence || e.GlobalStep < 0 || e.GlobalStep > maximumSteps {
		return errors.New("TRAINING_EVENT_INVALID")
	}
	switch e.Kind {
	case "phase":
		if e.Phase == nil || *e.Phase != "loading_model" && *e.Phase != "training" && *e.Phase != "finalizing_checkpoint" || e.Values != nil || e.Path != nil || e.ReceiptSHA256 != nil {
			return errors.New("TRAINING_EVENT_INVALID")
		}
	case "metric":
		if e.Values == nil || e.Values.Validate() != nil || e.GlobalStep < 1 || e.Phase != nil || e.Path != nil || e.ReceiptSHA256 != nil {
			return errors.New("TRAINING_EVENT_INVALID")
		}
	case "checkpoint":
		if e.Path == nil || *e.Path != "checkpoint-"+strconv.FormatInt(e.GlobalStep, 10) || e.ReceiptSHA256 == nil || !datawire.ValidDigest(*e.ReceiptSHA256) || e.GlobalStep < 1 || e.Phase != nil || e.Values != nil {
			return errors.New("TRAINING_EVENT_INVALID")
		}
	default:
		return errors.New("TRAINING_EVENT_INVALID")
	}
	return nil
}
func DecodeEvent(raw []byte, job, attempt string, maximumSteps int64) (Event, error) {
	var event Event
	if len(raw) > MaximumEventBytes || DecodeStrict(raw, &event) != nil {
		return event, errors.New("TRAINING_EVENT_INVALID")
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	allowed := map[string]bool{"sequence": true, "job_id": true, "attempt_id": true, "kind": true, "global_step": true}
	switch event.Kind {
	case "phase":
		allowed["phase"] = true
	case "metric":
		allowed["values"] = true
		if metricNulls(fields["values"]) != nil {
			return event, errors.New("TRAINING_METRIC_INVALID")
		}
	case "checkpoint":
		allowed["path"] = true
		allowed["receipt_sha256"] = true
	}
	if len(fields) != len(allowed) {
		return event, errors.New("TRAINING_EVENT_INVALID")
	}
	for name := range fields {
		if !allowed[name] {
			return event, errors.New("TRAINING_EVENT_INVALID")
		}
	}
	return event, event.Validate(job, attempt, maximumSteps)
}
