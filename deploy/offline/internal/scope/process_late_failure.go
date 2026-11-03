package scope

import (
	"context"
	"reflect"
	"slices"
)

// Version3 records an explicit retry of a known activated Control that later
// exited nonzero. The completed activation stays completed; Before records the
// separately observed late failure. Historical versions1/2 retain their rules.
const processLateFailureAttemptVersion = 3

func activatedControlStart(events []Event, creation string) (string, *ProcessRecord) {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		r := e.Process
		if r == nil || r.CreationSHA256 != creation || r.Target != "running" || !slices.Equal(r.SelectedServices, []string{"control"}) {
			continue
		}
		if e.Outcome != "completed" || r.RestoredApplication == nil || !selectedProcessTarget(r.After, "running", []string{"control"}) || !nonselectedProcessUnchanged(*r) {
			return "", nil
		}
		if e.Action == "process_reconcile" {
			return e.ReferenceOperationID, r
		}
		return e.OperationID, r
	}
	return "", nil
}

func (l *locked) observedLateControlRetry(ctx context.Context, client processEngine, creation string) (string, error) {
	id, prior := activatedControlStart(l.events, creation)
	if id == "" || prior == nil {
		return "", nil
	}
	observed, found, err := client.InspectRuntime(ctx, l.marker.ScopeID, "control")
	if err != nil {
		return "", err
	}
	old := prior.After["control"]
	state := observed.State
	if !found || observed.ID != old.ID || state.StartedAt != old.StartedAt || state.Status != "exited" || state.ExitCode < 1 || state.ExitCode > 255 || state.Running || state.PID != 0 || state.OOMKilled || state.Paused || state.Restarting || state.Dead {
		return "", nil
	}
	return id, nil
}

func validLateControlRetry(events []Event, r ProcessRecord) bool {
	if r.AttemptVersion != processLateFailureAttemptVersion || !recoveryReferenceValid(r) || r.RetryOfOperationID == "" || r.Target != "running" || r.RestoredApplication == nil || r.RestoredDependency != nil || !slices.Equal(r.SelectedServices, []string{"control"}) {
		return false
	}
	id, prior := activatedControlStart(events, r.CreationSHA256)
	if id != r.RetryOfOperationID || prior == nil || len(r.Before) != len(prior.After) || !reflect.DeepEqual(r.RestoredApplication, prior.RestoredApplication) {
		return false
	}
	for name, old := range prior.After {
		current, found := r.Before[name]
		if !found || !validProcessSnapshot(current) {
			return false
		}
		if name == "control" {
			if old.State != "running" || old.ExitCode != 0 || current.State != "exited" || current.ExitCode == 0 || current.ID != old.ID || current.StartedAt != old.StartedAt {
				return false
			}
		} else if current != old {
			return false
		}
	}
	return true
}
