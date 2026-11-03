package scope

import (
	"reflect"
	"slices"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type ProcessSnapshot struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	ExitCode  int    `json:"exit_code"`
	StartedAt string `json:"started_at"`
}
type ProcessRecord struct {
	RetryOfOperationID      string                        `json:"retry_of_operation_id,omitempty"`
	RecoveryOfOperationID   string                        `json:"recovery_of_operation_id,omitempty"`
	RecoveryStopOperationID string                        `json:"recovery_stop_operation_id,omitempty"`
	RecoveryReason          string                        `json:"recovery_reason,omitempty"`
	AttemptVersion          int                           `json:"attempt_version,omitempty"`
	Submission              string                        `json:"submission,omitempty"`
	RestoredApplication     *RestoredApplicationAdmission `json:"restored_application,omitempty"`
	RestoredDependency      *RestoredDependencyAdmission  `json:"restored_dependency,omitempty"`
	SelectedServices        []string                      `json:"selected_services,omitempty"`
	CreationSHA256          string                        `json:"creation_sha256"`
	Target                  string                        `json:"target"`
	Before                  map[string]ProcessSnapshot    `json:"before"`
	After                   map[string]ProcessSnapshot    `json:"after,omitempty"`
	Admission               *engine.ResourceAdmission     `json:"admission,omitempty"`
}
type processRecords struct {
	pending string
	record  *ProcessRecord
	outcome string
}

const processSubmissionNotSubmitted = "not_submitted"

const processAttemptVersion = 1

// Safe stops do not erase a failed start's explicit-retry obligation.
func retryableStartID(events []Event, creationSHA256 string, selected []string) string {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		r := e.Process
		if r == nil || r.Target != "running" || r.CreationSHA256 != creationSHA256 || !slices.Equal(r.SelectedServices, selected) {
			continue
		}
		if e.Outcome == "failed" || e.Outcome == "unsubmitted" {
			if e.Action == "process_reconcile" {
				return e.ReferenceOperationID
			}
			return e.OperationID
		}
		return ""
	}
	return ""
}

func validProcessRetry(events []Event, r ProcessRecord) bool {
	if r.AttemptVersion == processLateFailureAttemptVersion {
		return validLateControlRetry(events, r)
	}
	if r.AttemptVersion != processAttemptVersion && r.AttemptVersion != processRecoveryAttemptVersion || !recoveryReferenceValid(r) || r.RetryOfOperationID == "" || r.Target != "running" || len(r.SelectedServices) != 1 || retryableStartID(events, r.CreationSHA256, r.SelectedServices) != r.RetryOfOperationID {
		return false
	}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Process == nil || e.Outcome != "failed" && e.Outcome != "unsubmitted" || e.OperationID != r.RetryOfOperationID && e.ReferenceOperationID != r.RetryOfOperationID {
			continue
		}
		prior := e.Process
		if prior.Target != r.Target || prior.CreationSHA256 != r.CreationSHA256 || !slices.Equal(prior.SelectedServices, r.SelectedServices) || len(prior.After) == 0 {
			return false
		}
		if r.AttemptVersion == processRecoveryAttemptVersion {
			if r.SelectedServices[0] == "control" {
				return prior.After["control"] == r.Before["control"] && validStoppedRecoveryApplication(events, r)
			}
			return reflect.DeepEqual(prior.After, r.Before) && validStoppedRecoveryDependency(events, r)
		}
		return reflect.DeepEqual(prior.After, r.Before)
	}
	return false
}

func processState(events []Event) processRecords {
	state := processRecords{}
	for _, event := range events {
		if event.Action == "process_start" || event.Action == "process_stop" {
			state.record = event.Process
			state.outcome = event.Outcome
			if event.Outcome == "started" || event.Outcome == "uncertain" {
				state.pending = event.OperationID
			} else {
				state.pending = ""
			}
		}
		if event.Action == "process_reconcile" {
			state.record = event.Process
			state.outcome = event.Outcome
			if event.Outcome == "completed" || event.Outcome == "failed" {
				state.pending = ""
			}
		}
	}
	return state
}
func validProcessSnapshot(s ProcessSnapshot) bool {
	timestamp, e := time.Parse(time.RFC3339Nano, s.StartedAt)
	if e != nil || !utilityIDPattern.MatchString(s.ID) || s.ExitCode < 0 || s.ExitCode > 255 {
		return false
	}
	switch s.State {
	case "created":
		return timestamp.IsZero() && s.ExitCode == 0
	case "running":
		return !timestamp.IsZero() && s.ExitCode == 0
	case "exited":
		return !timestamp.IsZero()
	}
	return false
}
func validProcessMap(values map[string]ProcessSnapshot, creation RuntimeRecord) bool {
	if len(values) != len(creation.Services) {
		return false
	}
	for name, snapshot := range values {
		expected, ok := creation.Services[name]
		if !ok || snapshot.ID != expected.ID || !validProcessSnapshot(snapshot) {
			return false
		}
	}
	return true
}
func processImmutable(a, b ProcessRecord) bool {
	return a.RetryOfOperationID == b.RetryOfOperationID && a.RecoveryOfOperationID == b.RecoveryOfOperationID && a.RecoveryStopOperationID == b.RecoveryStopOperationID && a.RecoveryReason == b.RecoveryReason && a.AttemptVersion == b.AttemptVersion && a.CreationSHA256 == b.CreationSHA256 && a.Target == b.Target && reflect.DeepEqual(a.Before, b.Before) && reflect.DeepEqual(a.Admission, b.Admission) && reflect.DeepEqual(a.SelectedServices, b.SelectedServices) && reflect.DeepEqual(a.RestoredDependency, b.RestoredDependency) && reflect.DeepEqual(a.RestoredApplication, b.RestoredApplication)
}
func selectedProcessTarget(values map[string]ProcessSnapshot, target string, selected []string) bool {
	if len(selected) == 0 {
		return processTarget(values, target)
	}
	if len(selected) != 1 {
		return false
	}
	value, found := values[selected[0]]
	return found && target == "running" && value.State == "running"
}
func nonselectedProcessUnchanged(r ProcessRecord) bool {
	if len(r.SelectedServices) == 0 {
		return true
	}
	for name, before := range r.Before {
		if len(r.SelectedServices) != 1 || name != r.SelectedServices[0] && r.After[name] != before {
			return false
		}
	}
	return true
}
func validProcessSelection(r *ProcessRecord) bool {
	if len(r.SelectedServices) == 0 {
		return true
	}
	if len(r.SelectedServices) != 1 {
		return false
	}
	_, found := r.Before[r.SelectedServices[0]]
	return (r.SelectedServices[0] == "postgres" || r.SelectedServices[0] == "control" && r.RestoredApplication != nil) && r.Target == "running" && found
}
func processTarget(values map[string]ProcessSnapshot, target string) bool {
	if len(values) == 0 {
		return false
	}
	for _, s := range values {
		if target == "running" {
			if s.State != "running" {
				return false
			}
		} else if s.State != "created" && s.State != "exited" {
			return false
		}
	}
	return true
}
func observedStartFailure(r ProcessRecord, model *compose.Model) bool {
	if r.Target != "running" || len(r.After) == 0 || !nonselectedProcessUnchanged(r) {
		return false
	}
	failed := false
	for name, before := range r.Before {
		if len(r.SelectedServices) > 0 && name != r.SelectedServices[0] {
			continue
		}
		after, ok := r.After[name]
		if !ok {
			return false
		}
		if before.State == "running" {
			if after.State == "exited" {
				if model != nil && model.Services[name].Restart != "no" {
					return false
				}
				failed = true
			} else if after.State != "running" {
				return false
			}
			continue
		}
		if after.State == "running" {
			continue
		}
		if after.State != "exited" || after.StartedAt == before.StartedAt {
			return false
		}
		if model != nil && model.Services[name].Restart != "no" {
			return false
		}
		failed = true
	}
	return failed
}
func validateProcessEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_PROCESS_JOURNAL_TRANSITION_INVALID") }
	creation := effectiveRuntimeState(events)
	if len(imageState(events).pending) > 0 || len(volumeState(events).pending) > 0 {
		return failure("OFFLINE_SCOPE_PROCESS_JOURNAL_TRANSITION_INVALID")
	}
	r := e.Process
	if r == nil || !recoveryReferenceValid(*r) || !validProcessSelection(r) || !validDependencyProcess(events, *r) || e.Runtime != nil || e.Inputs != nil || e.Volume != nil || e.Engine != nil || e.Image != nil || !creation.completed || creation.record == nil || r.CreationSHA256 != wire.Digest(encode(*creation.record)) || r.Target != "running" && r.Target != "stopped" || !validProcessMap(r.Before, *creation.record) || len(r.After) > 0 && !validProcessMap(r.After, *creation.record) {
		return bad()
	}
	if r.Target == "running" {
		if r.Admission == nil || r.Admission.ValidateRecorded() != nil {
			return bad()
		}
	} else if r.Admission != nil {
		return bad()
	}
	if r.AttemptVersion < 0 || r.AttemptVersion > processLateFailureAttemptVersion || r.Submission != "" && (r.AttemptVersion < processAttemptVersion || r.Submission != processSubmissionNotSubmitted || e.Outcome != "unsubmitted") {
		return bad()
	}
	if r.AttemptVersion == processLateFailureAttemptVersion && !validRecordedProcessRetry(events, *r) {
		return bad()
	}
	if r.AttemptVersion == processRecoveryAttemptVersion && !validRecordedRecoveryContext(events, *r) {
		return bad()
	}
	if r.RetryOfOperationID != "" && !validProcessRetry(events, *r) {
		// Terminal/reconciliation records retain the intent's original prefix.
		prefix := events
		for i, prior := range events {
			if e.Outcome != "started" && prior.Process != nil && prior.Process.RetryOfOperationID == r.RetryOfOperationID && prior.Outcome == "started" && (prior.OperationID == e.OperationID || prior.OperationID == e.ReferenceOperationID) {
				prefix = events[:i]
				break
			}
		}
		if !validProcessRetry(prefix, *r) {
			return bad()
		}
	}
	state := processState(events)
	if e.Action == "process_start" || e.Action == "process_stop" {
		if e.ReferenceOperationID != "" || e.Action == "process_start" && r.Target != "running" || e.Action == "process_stop" && r.Target != "stopped" {
			return bad()
		}
		if e.Outcome == "started" {
			if state.pending != "" || e.Code != "" || len(r.After) > 0 || selectedProcessTarget(r.Before, r.Target, r.SelectedServices) {
				return bad()
			}
			if r.AttemptVersion >= processAttemptVersion && r.Target == "running" && retryableStartID(events, r.CreationSHA256, r.SelectedServices) != "" && !validProcessRetry(events, *r) {
				return bad()
			}
			for _, prior := range events {
				if prior.OperationID == e.OperationID {
					return bad()
				}
			}
			return nil
		}
		if state.pending != e.OperationID || state.record == nil || state.outcome != "started" || !processImmutable(*state.record, *r) {
			return bad()
		}
	} else if e.Action == "process_reconcile" {
		if state.pending == "" || e.ReferenceOperationID != state.pending || state.record == nil || !processImmutable(*state.record, *r) {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
	} else {
		return bad()
	}
	switch e.Outcome {
	case "unsubmitted":
		if e.Action != "process_reconcile" && e.Code == "OFFLINE_SCOPE_PROCESS_ACTION_NOT_SUBMITTED" && r.Submission == processSubmissionNotSubmitted && reflect.DeepEqual(r.Before, r.After) {
			return nil
		}
	case "completed":
		if e.Code == "" && selectedProcessTarget(r.After, r.Target, r.SelectedServices) && nonselectedProcessUnchanged(*r) {
			return nil
		}
	case "failed":
		if codePattern.MatchString(e.Code) && observedStartFailure(*r, nil) {
			return nil
		}
	case "uncertain":
		if e.Action != "process_reconcile" && codePattern.MatchString(e.Code) {
			return nil
		}
	case "unresolved":
		if e.Action == "process_reconcile" && codePattern.MatchString(e.Code) {
			return nil
		}
	}
	return bad()
}

func validRecordedProcessRetry(events []Event, r ProcessRecord) bool {
	if validProcessRetry(events, r) {
		return true
	}
	for i, e := range events {
		if e.Action == "process_start" && e.Outcome == "started" && e.Process != nil && e.Process.RetryOfOperationID != "" && processImmutable(*e.Process, r) {
			return validProcessRetry(events[:i], r)
		}
	}
	return false
}
func validateProcessInputs(events []Event, p preparedInputs, c configuration.Config) error {
	model, e := compose.DecodeModel(p.model, int64(len(p.model)))
	if e != nil {
		return e
	}
	creation := runtimeState(events)
	for _, event := range events {
		if event.Process == nil {
			continue
		}
		r := event.Process
		if !creation.completed || c.Compose == nil || c.Compose.Process == nil {
			return failure("OFFLINE_SCOPE_PROCESS_INPUT_CONTEXT_INVALID")
		}
		if r.Target == "running" {
			need, e := additionalSelectedProcessResources(model, r.Before, r.SelectedServices)
			if e != nil {
				return e
			}
			if r.Admission == nil || r.Admission.Requirement != need || r.Admission.ReservePercent != c.Compose.Process.ReservePercent {
				return failure("OFFLINE_SCOPE_PROCESS_ADMISSION_CONTEXT_INVALID")
			}
		}
		if event.Outcome == "failed" && !observedStartFailure(*r, &model) {
			return failure("OFFLINE_SCOPE_PROCESS_FAILURE_PROOF_INVALID")
		}
	}
	return nil
}
