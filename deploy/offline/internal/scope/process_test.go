package scope

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// All SDK/process effects below are explicit doubles; protected snapshots/journal are real.
type processEngineDouble struct {
	*runtimeEngineDouble
	worker     *processWorkerDouble
	startedAt  string
	admissions int
	refuse     bool
}

func (d *processEngineDouble) InspectRuntime(_ context.Context, _ string, name string) (engine.RuntimeContainer, bool, error) {
	var object engine.RuntimeContainer
	s, found := d.worker.state.Services[name]
	object.ID = s.ID
	object.State.Status = s.State
	object.State.ExitCode = s.ExitCode
	object.State.StartedAt = d.startedAt
	return object, found, nil
}
func (d *processEngineDouble) AdmitRuntimeResources(_ context.Context, need engine.ResourceRequirement, reserve int) (engine.ResourceAdmission, error) {
	d.admissions++
	if d.refuse {
		return engine.ResourceAdmission{}, errors.New("EXPLICIT_TEST_RESOURCE_REFUSAL")
	}
	return engine.ResourceAdmission{Status: "passed_point_in_time_resource_admission", ObservedAt: "2026-10-17T00:00:00Z", Requirement: need, ReservePercent: reserve, EngineCapacity: engine.ResourceRequirement{MemoryBytes: 8 << 30, MilliCPU: 10000}, RunningReservations: engine.ResourceRequirement{MemoryBytes: 512 << 20, MilliCPU: 1000}, Host: engine.HostResources{MemoryTotalBytes: 16 << 30, MemoryAvailableBytes: 12 << 30, LogicalCPUs: 10, LoadOneMinute: 1, Source: "explicit_test_counter"}}, nil
}

type processWorkerDouble struct {
	*runtimeWorkerDouble
	engine        *processEngineDouble
	starts, stops int
	fault         string
}

func (w *processWorkerDouble) StartRuntime(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model, compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	w.starts++
	if w.fault == "before-start" {
		return w.state, errors.New("EXPLICIT_TEST_START_RESPONSE_UNKNOWN")
	}
	w.engine.startedAt = "2026-10-17T00:00:01Z"
	s := w.state.Services["control"]
	s.State = "running"
	s.ExitCode = 0
	if w.fault == "exit-start" {
		s.State = "exited"
		s.ExitCode = 1
	}
	w.state.Services["control"] = s
	if w.fault == "after-start" {
		return w.state, errors.New("EXPLICIT_TEST_START_RESPONSE_LOST")
	}
	return w.state, nil
}
func (w *processWorkerDouble) StopRuntime(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model, compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	w.stops++
	if w.fault == "before-stop" {
		return w.state, errors.New("EXPLICIT_TEST_STOP_RESPONSE_UNKNOWN")
	}
	s := w.state.Services["control"]
	s.State = "exited"
	s.ExitCode = 0
	w.state.Services["control"] = s
	if w.fault == "after-stop" {
		return w.state, errors.New("EXPLICIT_TEST_STOP_RESPONSE_LOST")
	}
	return w.state, nil
}
func processFixture(t *testing.T) (configuration.Config, *processEngineDouble, *processWorkerDouble) {
	t.Helper()
	c, l, p := preparedInputFixtureConfigured(t, func(c *configuration.Config) {
		c.Compose.Process = &wire.ProcessSettings{StopSeconds: 2, ObserveSeconds: 1, PollMilliseconds: 50, ReservePercent: 30}
	})
	if _, e := prepareInputs(context.Background(), l, p); e != nil {
		l.close()
		t.Fatal(e)
	}
	state := imageState(l.events)
	image := state.completed["image"]
	base := &imageDouble{observation: *state.engine, found: true}
	base.image.ID = image.ManifestDigest
	base.image.OS = "linux"
	base.image.Architecture = "amd64"
	base.image.RootFS.Type = "layers"
	base.image.RootFS.Layers = image.DiffIDs
	l.close()
	storage := &storageDouble{imageDouble: base}
	if _, e := initializeStorage(context.Background(), c, storage, false); e != nil {
		t.Fatal(e)
	}
	worker := &processWorkerDouble{runtimeWorkerDouble: &runtimeWorkerDouble{state: compose.RuntimeStatus{Status: "observed_absent_compose_resources", Services: map[string]compose.RuntimeServiceStatus{}}}}
	client := &processEngineDouble{runtimeEngineDouble: &runtimeEngineDouble{storageDouble: storage}, worker: worker, startedAt: "0001-01-01T00:00:00Z"}
	worker.engine = client
	if _, e := createRuntime(context.Background(), c, client, worker, false); e != nil {
		t.Fatal(e)
	}
	return c, client, worker
}

func TestDatabaseProcessSelectionResourceAndSnapshotIntegrity(t *testing.T) {
	model := compose.Model{Services: map[string]compose.Service{"control": {MemoryBytes: 64 << 20, MilliCPU: 250, Restart: "no"}, "postgres": {MemoryBytes: 128 << 20, MilliCPU: 300, Restart: "no"}}}
	created := "0001-01-01T00:00:00Z"
	started := "2026-10-17T00:00:01Z"
	before := map[string]ProcessSnapshot{"control": {ID: strings.Repeat("a", 64), State: "created", StartedAt: created}, "postgres": {ID: strings.Repeat("b", 64), State: "created", StartedAt: created}}
	after := map[string]ProcessSnapshot{"control": before["control"], "postgres": {ID: before["postgres"].ID, State: "running", StartedAt: started}}
	r := ProcessRecord{Target: "running", Before: before, After: after, SelectedServices: []string{"postgres"}}
	if !validProcessSelection(&r) || !selectedProcessTarget(after, r.Target, r.SelectedServices) || !nonselectedProcessUnchanged(r) {
		t.Fatal("valid database selector refused")
	}
	need, e := additionalSelectedProcessResources(model, before, r.SelectedServices)
	if e != nil || need.MemoryBytes != 128<<20 || need.MilliCPU != 300 {
		t.Fatal(need, e)
	}
	whole, e := additionalProcessResources(model, before)
	if e != nil || whole.MemoryBytes != 192<<20 || whole.MilliCPU != 550 {
		t.Fatal("legacy resource accounting", whole, e)
	}
	if outcome, _ := processOutcome(r, model); outcome != "completed" {
		t.Fatal(outcome)
	}
	changed := after["control"]
	changed.StartedAt = started
	changed.State = "running"
	after["control"] = changed
	if outcome, _ := processOutcome(r, model); outcome != "unresolved" {
		t.Fatal("nonselected process changed", outcome)
	}
	after["control"] = before["control"]
	pg := after["postgres"]
	pg.State = "exited"
	pg.ExitCode = 1
	after["postgres"] = pg
	if outcome, _ := processOutcome(r, model); outcome != "failed" {
		t.Fatal("selected exit failure not observed", outcome)
	}
	copy := r
	copy.SelectedServices = nil
	if processImmutable(copy, r) {
		t.Fatal("selector changed in terminal/reconcile")
	}
	for _, selection := range [][]string{{"control"}, {"postgres", "postgres"}, {"gateway"}} {
		r.SelectedServices = selection
		if validProcessSelection(&r) {
			t.Fatal("invalid selection accepted")
		}
	}
	r.SelectedServices = []string{"postgres"}
	r.Target = "stopped"
	if validProcessSelection(&r) {
		t.Fatal("invalid selector/target pair")
	}
}
func TestDurableProcessStartStopAndDesiredStateRepeat(t *testing.T) {
	c, client, worker := processFixture(t)
	result, e := processRuntime(context.Background(), c, client, worker, "running", false)
	if e != nil || result.Pending || worker.starts != 1 || client.admissions != 1 {
		t.Fatal(result, e)
	}
	if _, e = processRuntime(context.Background(), c, client, worker, "running", false); e != nil || worker.starts != 1 || client.admissions != 1 {
		t.Fatal("running effect repeated", e)
	}
	client.refuse = true
	result, e = processRuntime(context.Background(), c, client, worker, "stopped", false)
	if e != nil || result.Pending || worker.stops != 1 || client.admissions != 1 {
		t.Fatal("stop blocked by resource pressure", result, e)
	}
	if _, e = processRuntime(context.Background(), c, client, worker, "stopped", false); e != nil || worker.stops != 1 {
		t.Fatal("stop repeated", e)
	}
	if result, e = processRuntime(context.Background(), c, client, worker, "", true); e != nil || result.Status != "observed_no_pending_process_action" {
		t.Fatal(result, e)
	}
	status, e := Read(context.Background(), c)
	if e != nil || status.Status != "isolated_scope_process_outcome_recorded" {
		t.Fatal(status, e)
	}
}
func TestUncertainProcessEffectsRequireExplicitObservedResolution(t *testing.T) {
	for _, fault := range []string{"before-start", "after-start", "before-stop", "after-stop"} {
		t.Run(fault, func(t *testing.T) {
			c, client, worker := processFixture(t)
			target := "running"
			if strings.HasSuffix(fault, "stop") {
				if _, e := processRuntime(context.Background(), c, client, worker, "running", false); e != nil {
					t.Fatal(e)
				}
				target = "stopped"
			}
			worker.fault = fault
			result, e := processRuntime(context.Background(), c, client, worker, target, false)
			if e == nil || !result.Pending {
				t.Fatal(result, e)
			}
			starts, stops := worker.starts, worker.stops
			if _, e = processRuntime(context.Background(), c, client, worker, target, false); e == nil || worker.starts != starts || worker.stops != stops {
				t.Fatal("uncertain effect resent", e)
			}
			result, e = processRuntime(context.Background(), c, client, worker, "", true)
			if strings.HasPrefix(fault, "before") {
				if e == nil || !result.Pending {
					t.Fatal("unchanged state resolved uncertainty", result, e)
				}
				// Explicit later outcome in this test double, never a production resend.
				s := worker.state.Services["control"]
				if target == "running" {
					s.State = "running"
					client.startedAt = "2026-10-17T00:00:01Z"
				} else {
					s.State = "exited"
				}
				worker.state.Services["control"] = s
				result, e = processRuntime(context.Background(), c, client, worker, "", true)
			}
			if e != nil || result.Pending || worker.starts != starts || worker.stops != stops {
				t.Fatal("resolution resent effect", result, e)
			}
		})
	}
}
func TestProcessResourceRefusalAndDeterminateStartFailure(t *testing.T) {
	t.Run("admission", func(t *testing.T) {
		c, client, worker := processFixture(t)
		client.refuse = true
		if _, e := processRuntime(context.Background(), c, client, worker, "running", false); e == nil || worker.starts != 0 {
			t.Fatal("admission bypassed", e)
		}
		l, e := open(c, false)
		if e != nil {
			t.Fatal(e)
		}
		defer l.close()
		if processState(l.events).record != nil {
			t.Fatal("failed admission submitted intent")
		}
	})
	t.Run("failed-start", func(t *testing.T) {
		c, client, worker := processFixture(t)
		worker.fault = "exit-start"
		result, e := processRuntime(context.Background(), c, client, worker, "running", false)
		if e == nil || result.Pending || result.Status != "recorded_process_failed" {
			t.Fatal("new exit falsely successful/unknown", result, e)
		}
		l, e := open(c, false)
		if e != nil {
			t.Fatal(e)
		}
		defer l.close()
		if processState(l.events).outcome != "failed" {
			t.Fatal("original failure absent")
		}
	})
}

func TestObservedStartFailureNeedsNewStartProofAndNoAutoRestart(t *testing.T) {
	before := ProcessSnapshot{ID: strings.Repeat("a", 64), State: "exited", ExitCode: 1, StartedAt: "2026-10-17T00:00:00Z"}
	after := before
	r := ProcessRecord{Target: "running", Before: map[string]ProcessSnapshot{"control": before}, After: map[string]ProcessSnapshot{"control": after}}
	model := compose.Model{Services: map[string]compose.Service{"control": {Restart: "no"}}}
	if observedStartFailure(r, &model) {
		t.Fatal("unchanged exit proved start failure")
	}
	after.StartedAt = "2026-10-17T00:00:01Z"
	r.After["control"] = after
	if !observedStartFailure(r, &model) {
		t.Fatal("new start/exit proof ignored")
	}
	s := model.Services["control"]
	s.Restart = "unless-stopped"
	model.Services["control"] = s
	if observedStartFailure(r, &model) {
		t.Fatal("auto-restarting service prematurely closed")
	}
}

func TestProcessJournalRejectsForgedIdentityAdmissionAndReferences(t *testing.T) {
	c, client, worker := processFixture(t)
	if _, e := processRuntime(context.Background(), c, client, worker, "running", false); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	index := -1
	for i, event := range l.events {
		if event.Action == "process_start" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("missing real protected intent")
	}
	input, _, _ := inputsState(l.events)
	p, e := l.readInputs(context.Background(), *input)
	if e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		name   string
		offset int
		mutate func(*Event)
	}{
		{"creation-binding", 0, func(e *Event) { e.Process.CreationSHA256 = strings.Repeat("f", 64) }},
		{"target", 0, func(e *Event) { e.Process.Target = "stopped" }},
		{"missing-before", 0, func(e *Event) { e.Process.Before = nil }},
		{"foreign-id", 0, func(e *Event) {
			s := e.Process.Before["control"]
			s.ID = strings.Repeat("f", 64)
			e.Process.Before["control"] = s
		}},
		{"invalid-created-time", 0, func(e *Event) {
			s := e.Process.Before["control"]
			s.StartedAt = "2026-10-17T00:00:01Z"
			e.Process.Before["control"] = s
		}},
		{"early-after", 0, func(e *Event) { e.Process.After = e.Process.Before }},
		{"missing-admission", 0, func(e *Event) { e.Process.Admission = nil }},
		{"forged-host-capacity", 0, func(e *Event) { e.Process.Admission.Host.MemoryAvailableBytes = 0 }},
		{"wrong-requested-resources", 0, func(e *Event) { e.Process.Admission.Requirement.MemoryBytes++ }},
		{"wrong-reserve-policy", 0, func(e *Event) { e.Process.Admission.ReservePercent = 40 }},
		{"invalid-admission-time", 0, func(e *Event) { e.Process.Admission.ObservedAt = "not-a-timestamp" }},
		{"reused-operation", 0, func(e *Event) { e.OperationID = l.events[0].OperationID }},
		{"false-completion", 1, func(e *Event) { e.Process.After = e.Process.Before }},
		{"changed-before", 1, func(e *Event) {
			s := e.Process.Before["control"]
			s.State = "running"
			s.StartedAt = "2026-10-17T00:00:01Z"
			e.Process.Before["control"] = s
		}},
		{"changed-admission", 1, func(e *Event) { e.Process.Admission.Host.LoadOneMinute = 2 }},
		{"wrong-reference", 1, func(e *Event) {
			e.Action = "process_reconcile"
			e.OperationID = strings.Repeat("f", 32)
			e.ReferenceOperationID = strings.Repeat("a", 32)
		}},
		{"reused-reconciliation", 1, func(e *Event) { e.Action = "process_reconcile"; e.ReferenceOperationID = e.OperationID }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var event Event
			if wire.StrictJSON(encode(l.events[index+test.offset]), &event) != nil {
				t.Fatal("deep copy")
			}
			test.mutate(&event)
			prefix := append([]Event(nil), l.events[:index+test.offset]...)
			structural := validateImageEvent(prefix, event)
			if structural == nil {
				prefix = append(prefix, event)
				structural = validateProcessInputs(prefix, p, c)
			}
			if structural == nil {
				t.Fatal("forged process journal accepted")
			}
		})
	}
}

func TestRestartUsesSeparateStopStartWithFailureBarriers(t *testing.T) {
	for _, fault := range []string{"none", "before-stop", "start-admission"} {
		t.Run(fault, func(t *testing.T) {
			c, client, worker := processFixture(t)
			if _, e := processRuntime(context.Background(), c, client, worker, "running", false); e != nil {
				t.Fatal(e)
			}
			if fault == "before-stop" {
				worker.fault = fault
			}
			if fault == "start-admission" {
				client.refuse = true
			}
			result, e := restartRuntime(context.Background(), c, client, worker)
			switch fault {
			case "none":
				if e != nil || result.Status != "recorded_restart_sequence_completed" || result.Started == nil || worker.stops != 1 || worker.starts != 2 {
					t.Fatal(result, e)
				}
			case "before-stop":
				if e == nil || result.Status != "restart_stop_incomplete" || result.Started != nil || worker.starts != 1 {
					t.Fatal("restart crossed failed stop", result, e)
				}
			case "start-admission":
				if e == nil || result.Status != "restart_start_incomplete" || result.Started == nil || worker.starts != 1 || worker.stops != 1 || worker.state.Services["control"].State != "exited" {
					t.Fatal("restart bypassed admission or restored running", result, e)
				}
			}
		})
	}
}
