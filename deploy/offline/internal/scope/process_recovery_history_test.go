package scope

import (
	"maps"
	"strings"
	"testing"
)

// These explicit synthetic history graphs test only the prefix selector. The
// production journal/runtime validators and actual recovery are separate gates.
func stoppedRecoveryGraph() ([]Event, ProcessRecord, ProcessRecord) {
	creation, failedID, stopID := strings.Repeat("c", 64), strings.Repeat("1", 32), strings.Repeat("2", 32)
	failed := map[string]ProcessSnapshot{"control": {ID: strings.Repeat("a", 64), State: "exited", ExitCode: 137, StartedAt: "2026-10-18T00:00:02Z"}, "postgres": {ID: strings.Repeat("b", 64), State: "running", StartedAt: "2026-10-18T00:00:01Z"}}
	clean := maps.Clone(failed)
	control := clean["control"]
	control.ExitCode, control.StartedAt = 0, "2026-10-18T00:00:00Z"
	clean["control"] = control
	stopped := maps.Clone(failed)
	pg := stopped["postgres"]
	pg.State = "exited"
	stopped["postgres"] = pg
	running := maps.Clone(stopped)
	pg.State, pg.StartedAt = "running", "2026-10-18T00:01:00Z"
	running["postgres"] = pg
	dependency := ProcessRecord{AttemptVersion: processRecoveryAttemptVersion, RecoveryOfOperationID: failedID, RecoveryStopOperationID: stopID, RecoveryReason: "Explicit synthetic stopped-control recovery", CreationSHA256: creation, Target: "running", SelectedServices: []string{"postgres"}, Before: stopped, After: running, RestoredDependency: &RestoredDependencyAdmission{}}
	application := ProcessRecord{AttemptVersion: processRecoveryAttemptVersion, RecoveryOfOperationID: failedID, RecoveryStopOperationID: stopID, RecoveryReason: dependency.RecoveryReason, RetryOfOperationID: failedID, CreationSHA256: creation, Target: "running", SelectedServices: []string{"control"}, Before: running, RestoredApplication: &RestoredApplicationAdmission{}}
	events := []Event{{OperationID: failedID, Action: "process_start", Outcome: "failed", Process: &ProcessRecord{AttemptVersion: 1, CreationSHA256: creation, Target: "running", SelectedServices: []string{"control"}, Before: clean, After: failed, RestoredApplication: &RestoredApplicationAdmission{}}}, {OperationID: stopID, Action: "process_stop", Outcome: "completed", Process: &ProcessRecord{AttemptVersion: 1, CreationSHA256: creation, Target: "stopped", Before: failed, After: stopped}}}
	return events, dependency, application
}

func TestProcessStoppedControlRecoveryHistoryBindsExactSafeStopAndDependency(t *testing.T) {
	events, dependency, application := stoppedRecoveryGraph()
	if !validStoppedRecoveryDependency(events, dependency) || validStoppedRecoveryApplication(events, application) {
		t.Fatal("only explicit dependency may follow a stopped failure")
	}
	events = append(events, Event{OperationID: strings.Repeat("3", 32), Action: "process_start", Outcome: "completed", Process: &dependency})
	if !validStoppedRecoveryApplication(events, application) {
		t.Fatal("current complete dependency should admit a bound control retry")
	}
	for _, change := range []func(*ProcessRecord){func(r *ProcessRecord) { r.RecoveryOfOperationID = strings.Repeat("4", 32) }, func(r *ProcessRecord) { r.RecoveryStopOperationID = strings.Repeat("5", 32) }, func(r *ProcessRecord) { r.RecoveryReason = "" }, func(r *ProcessRecord) { r.AttemptVersion = 1 }, func(r *ProcessRecord) { r.RetryOfOperationID = strings.Repeat("6", 32) }, func(r *ProcessRecord) {
		r.Before = maps.Clone(r.Before)
		p := r.Before["postgres"]
		p.StartedAt = "2026-10-18T00:03:00Z"
		r.Before["postgres"] = p
	}} {
		r := application
		change(&r)
		if validStoppedRecoveryApplication(events, r) {
			t.Fatal("forged or stale application context admitted")
		}
	}
}

func TestProcessStoppedControlRecoveryRejectsForeignUnrecordedAndConsumedHistory(t *testing.T) {
	for _, fault := range []string{"no-stop", "control-change", "stop-before-change", "foreign-creation", "unbound-dependency", "consumed-control"} {
		t.Run(fault, func(t *testing.T) {
			events, dependency, application := stoppedRecoveryGraph()
			switch fault {
			case "no-stop":
				events = events[:1]
			case "control-change":
				dependency.Before = maps.Clone(dependency.Before)
				p := dependency.Before["control"]
				p.StartedAt = "2026-10-18T00:04:00Z"
				dependency.Before["control"] = p
			case "stop-before-change":
				events[1].Process.Before = maps.Clone(events[1].Process.Before)
				p := events[1].Process.Before["postgres"]
				p.StartedAt = "2026-10-18T00:04:00Z"
				events[1].Process.Before["postgres"] = p
			case "foreign-creation":
				events[1].Process.CreationSHA256 = strings.Repeat("d", 64)
			case "unbound-dependency":
				dependency.AttemptVersion = 1
				events = append(events, Event{OperationID: strings.Repeat("3", 32), Action: "process_start", Outcome: "completed", Process: &dependency})
			case "consumed-control":
				events = append(events, Event{OperationID: strings.Repeat("3", 32), Action: "process_start", Outcome: "completed", Process: &dependency})
				application.After = maps.Clone(application.Before)
				p := application.After["control"]
				p.State, p.ExitCode = "running", 0
				application.After["control"] = p
				events = append(events, Event{OperationID: strings.Repeat("4", 32), Action: "process_start", Outcome: "completed", Process: &application})
			}
			if validStoppedRecoveryDependency(events, dependency) || validStoppedRecoveryApplication(events, application) {
				t.Fatal("foreign/unrecorded/consumed context admitted")
			}
		})
	}
}

func TestProcessStoppedControlRecoveryCanAnchorAnotherOwnedSafeShutdown(t *testing.T) {
	events, dependency, application := stoppedRecoveryGraph()
	events = append(events, Event{OperationID: strings.Repeat("3", 32), Action: "process_start", Outcome: "completed", Process: &dependency})
	stopped := maps.Clone(dependency.After)
	pg := stopped["postgres"]
	pg.State = "exited"
	stopped["postgres"] = pg
	stopID := strings.Repeat("4", 32)
	events = append(events, Event{OperationID: stopID, Action: "process_stop", Outcome: "completed", Process: &ProcessRecord{AttemptVersion: 1, CreationSHA256: dependency.CreationSHA256, Target: "stopped", Before: dependency.After, After: stopped}})
	if validStoppedRecoveryApplication(events, application) {
		t.Fatal("old running dependency survived safe shutdown")
	}
	next := dependency
	next.RecoveryStopOperationID, next.Before = stopID, stopped
	next.After = maps.Clone(stopped)
	pg.State, pg.StartedAt = "running", "2026-10-18T00:02:00Z"
	next.After["postgres"] = pg
	if !validStoppedRecoveryDependency(events, next) {
		t.Fatal("new exact stopped anchor refused")
	}
	events = append(events, Event{OperationID: strings.Repeat("5", 32), Action: "process_start", Outcome: "completed", Process: &next})
	application.RecoveryStopOperationID, application.Before = stopID, next.After
	if !validStoppedRecoveryApplication(events, application) {
		t.Fatal("new current dependency anchor refused")
	}
}

func TestProcessStoppedControlRecoveryReferencesAreImmutableAndLegacyFieldsRefuse(t *testing.T) {
	_, base, _ := stoppedRecoveryGraph()
	for _, change := range []func(*ProcessRecord){func(r *ProcessRecord) { r.RecoveryOfOperationID = strings.Repeat("4", 32) }, func(r *ProcessRecord) { r.RecoveryStopOperationID = strings.Repeat("5", 32) }, func(r *ProcessRecord) { r.RecoveryReason = "Changed audit reason" }} {
		changed := base
		change(&changed)
		if processImmutable(base, changed) {
			t.Fatal("recovery reference changed after intent")
		}
	}
	legacy := base
	legacy.AttemptVersion = 1
	if recoveryReferenceValid(legacy) {
		t.Fatal("legacy record accepted additive recovery fields")
	}
	legacy.RecoveryOfOperationID, legacy.RecoveryStopOperationID, legacy.RecoveryReason = "", "", ""
	if !recoveryReferenceValid(legacy) {
		t.Fatal("ordinary legacy shape changed")
	}
}
