package scope

import (
	"reflect"
	"slices"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

const processRecoveryAttemptVersion = 2

type stoppedControlRecovery struct {
	failure           *ProcessRecord
	stopID            string
	current           map[string]ProcessSnapshot
	dependency        *ProcessRecord
	dependencyOutcome string
}

func recoveryReferenceValid(r ProcessRecord) bool {
	if r.AttemptVersion != processRecoveryAttemptVersion {
		return r.RecoveryOfOperationID == "" && r.RecoveryStopOperationID == "" && r.RecoveryReason == ""
	}
	request := configuration.ProcessRecoveryRequest{FormatVersion: 1, FailedControlStartOperationID: r.RecoveryOfOperationID, Reason: r.RecoveryReason}
	return request.Validate() == nil && identityPattern.MatchString(r.RecoveryStopOperationID) && r.Target == "running" && len(r.SelectedServices) == 1 && (r.SelectedServices[0] == "postgres" || r.SelectedServices[0] == "control")
}

// These helpers consume an already validated immutable event prefix. They never
// infer a zero exit or accept a caller-supplied runtime snapshot as observation.
func stoppedControlRecoveryHistory(events []Event, creation, failedID string) (stoppedControlRecovery, bool) {
	var out stoppedControlRecovery
	if !identityPattern.MatchString(failedID) {
		return out, false
	}
	start := -1
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		p := e.Process
		if p == nil || e.Outcome != "failed" || p.CreationSHA256 != creation || p.Target != "running" || !slices.Equal(p.SelectedServices, []string{"control"}) || e.OperationID != failedID && e.ReferenceOperationID != failedID {
			continue
		}
		control, found := p.After["control"]
		if !found || control.State != "exited" || control.ExitCode < 1 || control.ExitCode > 255 || !validProcessSnapshot(control) || p.RestoredApplication == nil {
			return out, false
		}
		out.failure, out.current, start = p, p.After, i+1
		break
	}
	if start < 0 {
		return out, false
	}
	for _, e := range events[start:] {
		p := e.Process
		if p == nil || e.Outcome == "started" || e.Outcome == "uncertain" || e.Outcome == "unresolved" {
			continue
		}
		if p.CreationSHA256 != creation || p.After["control"] != out.failure.After["control"] || !reflect.DeepEqual(p.Before, out.current) {
			return stoppedControlRecovery{}, false
		}
		if (e.Action == "process_stop" || e.Action == "process_reconcile") && e.Outcome == "completed" && p.Target == "stopped" && len(p.SelectedServices) == 0 && processTarget(p.After, "stopped") {
			out.stopID, out.current, out.dependency, out.dependencyOutcome = e.OperationID, p.After, nil, ""
			if e.Action == "process_reconcile" {
				out.stopID = e.ReferenceOperationID
			}
			continue
		}
		if (e.Action == "process_start" || e.Action == "process_reconcile") && (e.Outcome == "completed" || e.Outcome == "failed" || e.Outcome == "unsubmitted") && p.AttemptVersion == processRecoveryAttemptVersion && p.RecoveryOfOperationID == failedID && p.RecoveryStopOperationID == out.stopID && out.stopID != "" && slices.Equal(p.SelectedServices, []string{"postgres"}) && p.RestoredDependency != nil && recoveryReferenceValid(*p) {
			out.current, out.dependency, out.dependencyOutcome = p.After, p, e.Outcome
			continue
		}
		// Any selected control action or unbound dependency effect consumes or
		// changes the context. Later retries need their own original failure.
		return stoppedControlRecovery{}, false
	}
	return out, out.stopID != ""
}

func validStoppedRecoveryDependency(events []Event, r ProcessRecord) bool {
	if r.AttemptVersion != processRecoveryAttemptVersion || r.RestoredDependency == nil || r.RestoredApplication != nil || !slices.Equal(r.SelectedServices, []string{"postgres"}) || !recoveryReferenceValid(r) || retryableStartID(events, r.CreationSHA256, []string{"control"}) != r.RecoveryOfOperationID {
		return false
	}
	context, ok := stoppedControlRecoveryHistory(events, r.CreationSHA256, r.RecoveryOfOperationID)
	return ok && context.stopID == r.RecoveryStopOperationID && reflect.DeepEqual(context.current, r.Before) && r.Before["control"] == context.failure.After["control"]
}

func validStoppedRecoveryApplication(events []Event, r ProcessRecord) bool {
	if r.AttemptVersion != processRecoveryAttemptVersion || r.RestoredApplication == nil || r.RestoredDependency != nil || !slices.Equal(r.SelectedServices, []string{"control"}) || r.RetryOfOperationID != r.RecoveryOfOperationID || !recoveryReferenceValid(r) {
		return false
	}
	context, ok := stoppedControlRecoveryHistory(events, r.CreationSHA256, r.RecoveryOfOperationID)
	return ok && context.dependency != nil && context.dependencyOutcome == "completed" && context.stopID == r.RecoveryStopOperationID && reflect.DeepEqual(context.current, r.Before) && r.Before["control"] == context.failure.After["control"] && r.Before["postgres"].State == "running" && r.Before["postgres"].ExitCode == 0
}

func stoppedRecoveryAtSnapshot(events []Event, creation string, current map[string]ProcessSnapshot, failedID string, requireRunningDependency bool) (stoppedControlRecovery, bool) {
	if failedID == "" {
		// Read-only SQL/store admission may reuse only an actually completed
		// explicit dependency recovery matching the complete live snapshot.
		for i := len(events) - 1; i >= 0; i-- {
			p := events[i].Process
			if p != nil && p.AttemptVersion == processRecoveryAttemptVersion && p.RestoredDependency != nil && events[i].Outcome == "completed" && slices.Equal(p.SelectedServices, []string{"postgres"}) && reflect.DeepEqual(p.After, current) {
				failedID = p.RecoveryOfOperationID
				break
			}
		}
	}
	if failedID == "" || retryableStartID(events, creation, []string{"control"}) != failedID {
		return stoppedControlRecovery{}, false
	}
	context, ok := stoppedControlRecoveryHistory(events, creation, failedID)
	if !ok || !reflect.DeepEqual(context.current, current) || current["control"] != context.failure.After["control"] {
		return stoppedControlRecovery{}, false
	}
	if requireRunningDependency && (context.dependency == nil || context.dependencyOutcome != "completed" || current["postgres"].State != "running" || current["postgres"].ExitCode != 0) {
		return stoppedControlRecovery{}, false
	}
	return context, true
}

func validRecordedRecoveryContext(events []Event, record ProcessRecord) bool {
	valid := func(prefix []Event) bool {
		return validStoppedRecoveryDependency(prefix, record) || validStoppedRecoveryApplication(prefix, record)
	}
	if valid(events) {
		return true
	}
	// Terminal observations must retain their own intent's admitted prefix;
	// a later completed observation is never a new activation authority.
	for i, event := range events {
		if event.Action == "process_start" && event.Outcome == "started" && event.Process != nil && processImmutable(*event.Process, record) {
			return valid(events[:i])
		}
	}
	return false
}
