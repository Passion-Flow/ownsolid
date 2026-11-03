package scope

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit process/SDK doubles; no SQL, store content or serving success is
// asserted by these tests. PKI, private plans and durable journal files are real.
type applicationEngineDouble struct {
	*currentStoresCheckDouble
	readFault bool
}

func (d *applicationEngineDouble) InspectRuntime(ctx context.Context, scope, name string) (engine.RuntimeContainer, bool, error) {
	r, found, e := d.currentStoresCheckDouble.InspectRuntime(ctx, scope, name)
	if name == "control" && found && e == nil {
		r.State.Running = r.State.Status == "running"
		if r.State.Running {
			r.State.PID = 789
		}
	}
	return r, found, e
}
func (d *applicationEngineDouble) InspectCurrentControlConfiguration(ctx context.Context, o engine.Ownership, id, target string, uid, gid int, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	if d.readFault {
		return engine.ConfigurationObservation{}, errors.New("SYNTHETIC_CURRENT_PAYLOAD_FAILURE")
	}
	return d.replacementEngineDouble.InspectStoppedControlConfiguration(ctx, o, id, target, uid, gid, files)
}

func (d *applicationEngineDouble) InspectFailedControlConfiguration(ctx context.Context, o engine.Ownership, id, target string, uid, gid int, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	// Explicit metadata double; the real SDK's failed/clean distinction has
	// separate Unix HTTP/archive tests and does not imply activation authority.
	return d.InspectCurrentControlConfiguration(ctx, o, id, target, uid, gid, files)
}

type applicationWorkerDouble struct {
	*dependencyWorkerDouble
	controlStarts int
	controlFault  string
}

func (w *applicationWorkerDouble) StartControlRuntime(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model, compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	w.controlStarts++
	if w.controlFault == "before" {
		return w.state, errors.New("SYNTHETIC_CONTROL_NOT_SUBMITTED")
	}
	s := w.state.Services["control"]
	s.State, s.ExitCode = "running", 0
	if w.controlFault == "exit" {
		s.State, s.ExitCode = "exited", 1
	}
	w.state.Services["control"] = s
	w.databaseEngine.times["control"] = fmt.Sprintf("2026-10-18T00:00:%02dZ", w.controlStarts)
	if w.controlFault == "after" {
		return w.state, errors.New("SYNTHETIC_CONTROL_RESPONSE_LOST")
	}
	return w.state, nil
}

var _ restoredApplicationEngine = (*applicationEngineDouble)(nil)
var _ controlProcessOperator = (*applicationWorkerDouble)(nil)

func applicationFixture(t *testing.T) (configuration.Config, configuration.TLSGenerationRequest, *applicationEngineDouble, *applicationWorkerDouble) {
	t.Helper()
	c, request, d, w := currentStoresControllerFixture(t)
	if _, e := runRestoredCurrentStores(context.Background(), c, "qualify-restored-current-stores", d, w); e != nil {
		t.Fatal(e)
	}
	return c, request, &applicationEngineDouble{currentStoresCheckDouble: d}, &applicationWorkerDouble{dependencyWorkerDouble: w}
}
func startApplicationDouble(t *testing.T, c configuration.Config, d *applicationEngineDouble, w *applicationWorkerDouble) (ProcessResult, error) {
	t.Helper()
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	return l.processSelectedWithAdmissionLocked(context.Background(), d, w, "running", false, []string{"control"}, restoredApplicationProcess)
}

func TestRestoredApplicationStartsOnlyControlRepeatsWithoutEffectAndBindsJournal(t *testing.T) {
	c, _, d, w := applicationFixture(t)
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	prefix := append([]Event{}, l.events...)
	l.close()
	pg := w.state.Services["postgres"]
	out, e := startApplicationDouble(t, c, d, w)
	if e != nil || out.Pending || w.controlStarts != 1 || w.databaseStarts != 1 || out.Resources.Services["control"].State != "running" || out.Resources.Services["postgres"] != pg {
		t.Fatal("selected activation", out, e)
	}
	before, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	again, e := startApplicationDouble(t, c, d, w)
	after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if e != nil || again.Status != "observed_already_running" || w.controlStarts != 1 || !bytes.Equal(before, after) {
		t.Fatal("completed repeat resent or changed journal", again, e)
	}
	for _, selected := range [][]string{nil, {"postgres"}, {"control"}} {
		if _, e = processRuntimeSelected(context.Background(), c, d, w, "running", false, selected); e == nil {
			t.Fatal("generic restored start admitted")
		}
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	intent := l.events[len(prefix)]
	if intent.Process == nil || !validApplicationProcess(prefix, *intent.Process) || validateImageEvent(prefix, intent) != nil {
		t.Fatal("application admission not recorded")
	}
	for _, fault := range []string{"missing", "SQL-proof", "SQL-plan", "store-proof", "store-plan", "TLS", "PID", "time", "selection", "mixed", "foreign-start"} {
		t.Run(fault, func(t *testing.T) {
			var forged Event
			if wire.StrictJSON(encode(intent), &forged) != nil {
				t.Fatal("clone")
			}
			r := forged.Process
			switch fault {
			case "missing":
				r.RestoredApplication = nil
			case "SQL-proof":
				r.RestoredApplication.DatabaseProofSHA256 = strings.Repeat("1", 64)
			case "SQL-plan":
				r.RestoredApplication.DatabasePlanSHA256 = strings.Repeat("2", 64)
			case "store-proof":
				r.RestoredApplication.StoresProofSHA256 = strings.Repeat("3", 64)
			case "store-plan":
				r.RestoredApplication.StoresPlanSHA256 = strings.Repeat("4", 64)
			case "TLS":
				r.RestoredApplication.Dependency.TLSProofSHA256 = strings.Repeat("5", 64)
			case "PID":
				r.RestoredApplication.PostgresPID = 0
			case "time":
				r.RestoredApplication.PostgresStartedAt = "2026-10-18T00:00:00Z"
			case "selection":
				r.SelectedServices = nil
			case "mixed":
				r.RestoredDependency = &r.RestoredApplication.Dependency
			case "foreign-start":
				s := r.Before["control"]
				s.State, s.StartedAt = "exited", "2026-10-18T00:00:00Z"
				r.Before["control"] = s
			}
			if validateProcessEvent(prefix, forged) == nil || validateImageEvent(prefix, forged) == nil {
				t.Fatal("forged application authority accepted")
			}
		})
	}
	for _, fault := range []string{"payload", "context", "positive-PID"} {
		var forged Event
		if wire.StrictJSON(encode(intent), &forged) != nil {
			t.Fatal("clone history")
		}
		switch fault {
		case "payload":
			forged.Process.RestoredApplication.PayloadSHA256 = strings.Repeat("a", 64)
		case "context":
			forged.Process.RestoredApplication.ContextSHA256 = strings.Repeat("b", 64)
		case "positive-PID":
			forged.Process.RestoredApplication.PostgresPID++
		}
		view := *l
		view.events = append(append([]Event{}, prefix...), forged)
		if view.verifyApplicationAdmissions() == nil {
			t.Fatal("changed historical plan facts accepted", fault)
		}
	}
}

func TestRestoredApplicationLostResponseReconcilesWithoutCurrentKeyOrResend(t *testing.T) {
	c, request, d, w := applicationFixture(t)
	w.controlFault = "after"
	out, e := startApplicationDouble(t, c, d, w)
	if e == nil || !out.Pending || w.controlStarts != 1 {
		t.Fatal("lost start must remain pending", out, e)
	}
	if _, e = startApplicationDouble(t, c, d, w); e == nil || w.controlStarts != 1 {
		t.Fatal("unknown start resent")
	}
	if e = os.Remove(request.PrivateKeyFile); e != nil {
		t.Fatal(e)
	}
	out, e = processRuntime(context.Background(), c, d, w, "", true)
	if e != nil || out.Pending || w.controlStarts != 1 {
		t.Fatal("readonly reconciliation required", out, e)
	}
	if _, e = startApplicationDouble(t, c, d, w); e == nil {
		t.Fatal("new invocation ignored missing current PKI")
	}
	if _, e = processRuntime(context.Background(), c, d, w, "stopped", false); e != nil {
		t.Fatal("owned shutdown requires no source key", e)
	}
}

func TestRestoredApplicationAdmissionRejectsDriftBeforeIntent(t *testing.T) {
	c, request, d, w := applicationFixture(t)
	before, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	for _, fault := range []string{"PG-restart", "configuration", "unknown-control", "PG-stopped"} {
		t.Run(fault, func(t *testing.T) {
			original := w.state.Services["control"]
			pg := w.state.Services["postgres"]
			switch fault {
			case "PG-restart":
				d.process = func(r *engine.RuntimeContainer) { r.State.PID++ }
			case "configuration":
				d.readFault = true
			case "unknown-control":
				s := original
				s.State = "running"
				w.state.Services["control"] = s
			case "PG-stopped":
				s := pg
				s.State = "exited"
				w.state.Services["postgres"] = s
			}
			if _, e := startApplicationDouble(t, c, d, w); e == nil || w.controlStarts != 0 {
				t.Fatal("changed live facts admitted")
			}
			d.process, d.readFault = nil, false
			w.state.Services["control"], w.state.Services["postgres"] = original, pg
		})
	}
	after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if !bytes.Equal(before, after) {
		t.Fatal("refused admission wrote intent")
	}
	d.refuse = true
	if _, e := startApplicationDouble(t, c, d, w); e == nil || w.controlStarts != 0 {
		t.Fatal("resource refusal submitted a start")
	}
	d.refuse = false
	if e := os.Remove(request.PrivateKeyFile); e != nil {
		t.Fatal(e)
	}
	if _, e := startApplicationDouble(t, c, d, w); e == nil || w.controlStarts != 0 {
		t.Fatal("missing current PKI admitted")
	}
}

func TestRestoredApplicationUnsubmittedAndNonzeroExitNeverGrantSuccess(t *testing.T) {
	for _, fault := range []string{"before", "exit"} {
		t.Run(fault, func(t *testing.T) {
			c, _, d, w := applicationFixture(t)
			w.controlFault = fault
			out, e := startApplicationDouble(t, c, d, w)
			if e == nil || w.controlStarts != 1 || (fault == "before") != out.Pending {
				t.Fatal("failed or unsubmitted start reported success", out, e)
			}
			if fault == "before" {
				if _, e = processRuntime(context.Background(), c, d, w, "", true); e == nil {
					t.Fatal("unsubmitted state became a completed start")
				}
			}
			if _, e = startApplicationDouble(t, c, d, w); e == nil || w.controlStarts != 1 {
				t.Fatal("failed/unknown action automatically retried")
			}
		})
	}
}

func TestRestoredApplicationCleanRestartRequiresNewProcessQualificationsPreservingHistory(t *testing.T) {
	c, _, d, w := applicationFixture(t)
	if _, e := startApplicationDouble(t, c, d, w); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	old := *processState(l.events).record.RestoredApplication
	l.close()
	if _, e = processRuntime(context.Background(), c, d, w, "stopped", false); e != nil {
		t.Fatal(e)
	}
	// Simulate a genuine new PG start timestamp on the existing ID.
	w.databaseEngine.times["postgres"] = "2026-10-18T00:01:00Z"
	l, e = open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	_, e = l.processSelectedAdmittedLocked(context.Background(), d, w, "running", false, []string{"postgres"}, true)
	l.close()
	if e != nil {
		t.Fatal("known clean-stopped control prevented dependency recovery", e)
	}
	// Pin the explicit process double's controlled clock after its start to
	// exercise stale qualification refusal independently of effect counters.
	w.databaseEngine.times["postgres"] = "2026-10-18T00:01:00Z"
	if _, e = startApplicationDouble(t, c, d, w); e == nil {
		t.Fatal("old SQL/store proof admitted restarted PostgreSQL")
	}
	if _, e = runRestoredLiveDatabase(context.Background(), c, "requalify-restored-live-database", d, w); e != nil {
		t.Fatal(e)
	}
	if _, e = startApplicationDouble(t, c, d, w); e == nil {
		t.Fatal("new SQL with old stores admitted")
	}
	if _, e = runRestoredCurrentStores(context.Background(), c, "requalify-restored-current-stores", d, w); e != nil {
		t.Fatal(e)
	}
	if _, e = startApplicationDouble(t, c, d, w); e != nil || w.controlStarts != 2 {
		t.Fatal("qualified restart", e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal("historical activation after new qualifications", e)
	}
	defer l.close()
	current := processState(l.events).record.RestoredApplication
	if current == nil || reflect.DeepEqual(*current, old) || current.PostgresStartedAt == old.PostgresStartedAt {
		t.Fatal("new process qualification lineage missing")
	}
}
