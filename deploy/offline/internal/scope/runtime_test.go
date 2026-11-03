package scope

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

// These explicit metadata/effect doubles exercise real protected scope records.
// The tagged journey separately executes actual Compose and Unix API operations.
type runtimeEngineDouble struct {
	*storageDouble
	consumers int
}

func (d *runtimeEngineDouble) InspectRuntime(context.Context, string, string) (engine.RuntimeContainer, bool, error) {
	return engine.RuntimeContainer{}, false, errors.New("EXPLICIT_TEST_WORKER_MUST_SUPPLY_METADATA")
}
func (d *runtimeEngineDouble) InspectRuntimeNetwork(context.Context, string) (engine.RuntimeNetwork, bool, error) {
	return engine.RuntimeNetwork{}, false, errors.New("EXPLICIT_TEST_WORKER_MUST_SUPPLY_METADATA")
}
func (d *runtimeEngineDouble) InspectRuntimeIngress(context.Context, string) (engine.RuntimeNetwork, bool, error) {
	return engine.RuntimeNetwork{}, false, errors.New("EXPLICIT_TEST_WORKER_MUST_SUPPLY_METADATA")
}
func (d *runtimeEngineDouble) RuntimeCandidates(context.Context, string, bool) ([]engine.RuntimeObject, error) {
	return nil, errors.New("EXPLICIT_TEST_WORKER_MUST_SUPPLY_METADATA")
}
func (d *runtimeEngineDouble) VerifyVolumeConsumers(context.Context, engine.Ownership, []string) error {
	d.consumers++
	return nil
}

type runtimeWorkerDouble struct {
	state compose.RuntimeStatus
	calls int
	fault string
}

func (w *runtimeWorkerDouble) ObserveRuntime(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model) (compose.RuntimeStatus, error) {
	return w.state, nil
}
func (w *runtimeWorkerDouble) CreateRuntime(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model) (compose.RuntimeStatus, error) {
	w.calls++
	if w.fault != "before" {
		w.state = createdRuntimeFixture()
	}
	if w.fault != "" {
		return w.state, errors.New("EXPLICIT_TEST_CREATE_RESPONSE_LOST")
	}
	return w.state, nil
}
func createdRuntimeFixture() compose.RuntimeStatus {
	return compose.RuntimeStatus{Status: "observed_complete_compose_resources", NetworkID: strings.Repeat("a", 64), Services: map[string]compose.RuntimeServiceStatus{"control": {ID: strings.Repeat("b", 64), State: "created"}}}
}
func runtimeScopeFixture(t *testing.T) (configuration.Config, *runtimeEngineDouble, *runtimeWorkerDouble) {
	t.Helper()
	c, s := storageFixture(t)
	if _, e := initializeStorage(context.Background(), c, s, false); e != nil {
		t.Fatal(e)
	}
	return c, &runtimeEngineDouble{storageDouble: s}, &runtimeWorkerDouble{state: compose.RuntimeStatus{Status: "observed_absent_compose_resources", Services: map[string]compose.RuntimeServiceStatus{}}}
}
func TestScopedRuntimeCreationRecordsIdentityAndRepeatObservesOnly(t *testing.T) {
	c, client, worker := runtimeScopeFixture(t)
	result, e := createRuntime(context.Background(), c, client, worker, false)
	if e != nil || result.Pending || worker.calls != 1 {
		t.Fatal(result, e)
	}
	if _, e = createRuntime(context.Background(), c, client, worker, false); e != nil || worker.calls != 1 {
		t.Fatal("create replayed", e)
	}
	status, e := Read(context.Background(), c)
	if e != nil || status.Status != "isolated_scope_runtime_resources_recorded" {
		t.Fatal(status, e)
	}
	worker.state.Services["control"] = compose.RuntimeServiceStatus{ID: strings.Repeat("c", 64), State: "created"}
	if _, e = createRuntime(context.Background(), c, client, worker, false); e == nil || worker.calls != 1 {
		t.Fatal("changed recorded container adopted", e)
	}
}
func TestScopedRuntimeUnknownOutcomeNeverResendsAndRequiresExplicitResolution(t *testing.T) {
	for _, fault := range []string{"before", "after"} {
		t.Run(fault, func(t *testing.T) {
			c, client, worker := runtimeScopeFixture(t)
			worker.fault = fault
			result, e := createRuntime(context.Background(), c, client, worker, false)
			if e == nil || !result.Pending || worker.calls != 1 {
				t.Fatal(result, e)
			}
			if _, e = createRuntime(context.Background(), c, client, worker, false); e == nil || worker.calls != 1 {
				t.Fatal("pending create resent", e)
			}
			result, e = createRuntime(context.Background(), c, client, worker, true)
			if fault == "before" {
				if e == nil || !result.Pending {
					t.Fatal("absence resolved unknown create", result, e)
				}
				worker.state = createdRuntimeFixture()
				result, e = createRuntime(context.Background(), c, client, worker, true)
			}
			if e != nil || result.Pending || worker.calls != 1 {
				t.Fatal(result, e)
			}
			if _, e = createRuntime(context.Background(), c, client, worker, false); e != nil || worker.calls != 1 {
				t.Fatal("reconciled create resent", e)
			}
		})
	}
}
func TestScopedRuntimeRejectsUnrecordedAndChangedPartialResources(t *testing.T) {
	t.Run("unrecorded", func(t *testing.T) {
		c, client, worker := runtimeScopeFixture(t)
		worker.state = createdRuntimeFixture()
		if _, e := createRuntime(context.Background(), c, client, worker, false); e == nil || worker.calls != 0 {
			t.Fatal("unrecorded adopted", e)
		}
	})
	t.Run("partial-identity", func(t *testing.T) {
		c, client, worker := runtimeScopeFixture(t)
		worker.fault = "before"
		if _, e := createRuntime(context.Background(), c, client, worker, false); e == nil {
			t.Fatal("fixture")
		}
		worker.state = compose.RuntimeStatus{Status: "observed_partial_compose_resources", NetworkID: strings.Repeat("a", 64)}
		if result, e := createRuntime(context.Background(), c, client, worker, true); e == nil || !result.Pending {
			t.Fatal(result, e)
		}
		worker.state = createdRuntimeFixture()
		worker.state.NetworkID = strings.Repeat("c", 64)
		if result, e := createRuntime(context.Background(), c, client, worker, true); e == nil || !result.Pending || worker.calls != 1 {
			t.Fatal("changed partial network adopted", result, e)
		}
	})
}

func TestRuntimeJournalRejectsForgedReferencesTransitionsAndInputs(t *testing.T) {
	c, client, worker := runtimeScopeFixture(t)
	if _, e := createRuntime(context.Background(), c, client, worker, false); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	index := -1
	for i, event := range l.events {
		if event.Action == "runtime_create" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("missing actual protected intent")
	}
	cases := []struct {
		name   string
		offset int
		mutate func(*Event)
	}{
		{"wrong-descriptor", 0, func(e *Event) { e.Runtime.DescriptorSHA256 = strings.Repeat("c", 64) }},
		{"wrong-model", 0, func(e *Event) { e.Runtime.ModelSHA256 = strings.Repeat("c", 64) }},
		{"early-resource", 0, func(e *Event) { e.Runtime.NetworkID = strings.Repeat("c", 64) }},
		{"old-operation", 0, func(e *Event) { e.OperationID = l.events[0].OperationID }},
		{"missing-network", 1, func(e *Event) { e.Runtime.NetworkID = "" }},
		{"invalid-resource-id", 1, func(e *Event) {
			v := e.Runtime.Services["control"]
			v.ID = "foreign"
			e.Runtime.Services["control"] = v
		}},
		{"false-running", 1, func(e *Event) {
			v := e.Runtime.Services["control"]
			v.State = "running"
			e.Runtime.Services["control"] = v
		}},
		{"unknown-service", 1, func(e *Event) {
			v := e.Runtime.Services["control"]
			e.Runtime.Services = map[string]compose.RuntimeServiceStatus{"foreign": v}
		}},
		{"wrong-operation", 1, func(e *Event) { e.OperationID = strings.Repeat("f", 32) }},
		{"unreferenced-reconcile", 1, func(e *Event) {
			e.Action = "runtime_reconcile"
			e.OperationID = strings.Repeat("f", 32)
			e.ReferenceOperationID = strings.Repeat("a", 32)
		}},
		{"reused-reconcile-operation", 1, func(e *Event) { e.Action = "runtime_reconcile"; e.ReferenceOperationID = e.OperationID }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			event := l.events[index+test.offset]
			r := *event.Runtime
			r.Services = map[string]compose.RuntimeServiceStatus{}
			for key, value := range event.Runtime.Services {
				r.Services[key] = value
			}
			event.Runtime = &r
			test.mutate(&event)
			if validateImageEvent(l.events[:index+test.offset], event) == nil {
				t.Fatal("forged runtime event accepted")
			}
		})
	}
	terminal := l.events[len(l.events)-1]
	terminal.OperationID = strings.Repeat("f", 32)
	terminal.Outcome = "started"
	if validateRuntimeEvent(l.events, terminal) == nil {
		t.Fatal("completed creation restarted")
	}
	input, _, _ := inputsState(l.events)
	p, e := l.readInputs(context.Background(), *input)
	if e != nil {
		t.Fatal(e)
	}
	state := runtimeState(l.events)
	copy := *state.record
	copy.Services = map[string]compose.RuntimeServiceStatus{"postgres": {ID: strings.Repeat("f", 64), State: "created"}}
	state.record = &copy
	if validateRuntimeInputs(state, p) == nil {
		t.Fatal("undeclared service adopted")
	}
}

func TestRuntimePartialIngressIdentityCannotBeReplacedOrLost(t *testing.T) {
	previous := RuntimeRecord{DescriptorSHA256: strings.Repeat("a", 64), ModelSHA256: strings.Repeat("b", 64), IngressNetworkID: strings.Repeat("c", 64)}
	next := previous
	next.NetworkID = strings.Repeat("d", 64)
	if !validRuntimeRecord(&previous) || !runtimeProgress(previous, next) {
		t.Fatal("observed partial ingress cannot progress")
	}
	next.IngressNetworkID = strings.Repeat("e", 64)
	if runtimeProgress(previous, next) {
		t.Fatal("ingress ID changed")
	}
	next.IngressNetworkID = ""
	if runtimeProgress(previous, next) {
		t.Fatal("ingress ID lost")
	}
}
