package scope

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit two-service effect double: stopping PostgreSQL must not invent a
// start/exit for the never-started replacement control.
type dependencyWorkerDouble struct{ *replacementWorkerDouble }

func (w *dependencyWorkerDouble) StopRuntime(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model, compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	w.stops++
	for name, service := range w.state.Services {
		if service.State == "running" {
			service.State, service.ExitCode = "exited", 0
			w.state.Services[name] = service
		}
	}
	return w.state, nil
}

var _ processOperator = (*dependencyWorkerDouble)(nil)

func completedDependencyFixture(t *testing.T) (configuration.Config, configuration.TLSGenerationRequest, *replacementEngineDouble, *dependencyWorkerDouble) {
	t.Helper()
	c, request, d, w := replacementControllerFixture(t)
	if _, e := replaceRestoredControl(context.Background(), c, d, w, false); e != nil {
		t.Fatal(e)
	}
	// Prior restore fixture setup started/stopped its dependency; count only this slice.
	w.databaseStarts = 0
	return c, request, d, &dependencyWorkerDouble{w}
}

func startDependencyDouble(t *testing.T, c configuration.Config, d *replacementEngineDouble, w *dependencyWorkerDouble) (ProcessResult, error) {
	t.Helper()
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	return l.processSelectedAdmittedLocked(context.Background(), d, w, "running", false, []string{"postgres"}, true)
}

func TestRestoredDependencySelectsOnlyPostgresAndRepeatHasNoNewEffect(t *testing.T) {
	c, _, d, w := completedDependencyFixture(t)
	out, e := startDependencyDouble(t, c, d, w)
	if e != nil || out.Pending || w.databaseStarts != 1 || out.Resources.Services["postgres"].State != "running" || out.Resources.Services["control"].State != "created" {
		t.Fatal("selected dependency process only", e, out)
	}
	before, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	again, e := startDependencyDouble(t, c, d, w)
	after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if e != nil || again.Pending || again.Status != "observed_already_running" || w.databaseStarts != 1 || !bytes.Equal(before, after) {
		t.Fatal("already-running repeat wrote/submitted", e)
	}
	for _, selected := range [][]string{nil, {"postgres"}} {
		if _, e = processRuntimeSelected(context.Background(), c, d, w, "running", false, selected); e == nil {
			t.Fatal("generic start admitted after replacement")
		}
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal("journal reopen", e)
	}
	defer l.close()
	r := processState(l.events).record
	if r == nil || !validDependencyProcess(l.events, *r) || r.RestoredDependency == nil || r.RestoredDependency.EffectiveRuntimeSHA256 != r.CreationSHA256 {
		t.Fatal("immutable dependency admission missing")
	}
}

func TestRestoredDependencyLostCompletedStartReconcilesWithoutSourceKeyOrResend(t *testing.T) {
	c, request, d, w := completedDependencyFixture(t)
	w.databaseFault = "after"
	out, e := startDependencyDouble(t, c, d, w)
	if e == nil || !out.Pending || w.databaseStarts != 1 {
		t.Fatal("explicit lost completed dependency start required", e)
	}
	if _, e = startDependencyDouble(t, c, d, w); e == nil || w.databaseStarts != 1 {
		t.Fatal("pending start resubmitted")
	}
	if e = os.Remove(request.PrivateKeyFile); e != nil {
		t.Fatal(e)
	}
	out, e = processRuntime(context.Background(), c, d, w, "", true)
	if e != nil || out.Pending || w.databaseStarts != 1 || out.Resources.Services["control"].State != "created" {
		t.Fatal("independent reconcile failed or resent", e)
	}
	if _, e = startDependencyDouble(t, c, d, w); e == nil || w.databaseStarts != 1 {
		t.Fatal("new dependency invocation ignored absent PKI")
	}
	if _, e = processRuntime(context.Background(), c, d, w, "stopped", false); e != nil {
		t.Fatal("safe stop after source-key loss", e)
	}
}

func TestRestoredDependencyBeforeSubmitOrChangedPeerNeverBecomesSuccessfulRecovery(t *testing.T) {
	for _, fault := range []string{"before", "other-changed"} {
		t.Run(fault, func(t *testing.T) {
			c, _, d, w := completedDependencyFixture(t)
			w.databaseFault = fault
			out, e := startDependencyDouble(t, c, d, w)
			if e == nil || !out.Pending || w.databaseStarts != 1 {
				t.Fatal("unknown/changed process effect required", e)
			}
			if _, e = processRuntime(context.Background(), c, d, w, "", true); e == nil || w.databaseStarts != 1 {
				t.Fatal("unproven state accepted/resubmitted")
			}
		})
	}
}

func TestRestoredDependencyAdmissionRefusesBeforeAnyEffect(t *testing.T) {
	for _, fault := range []string{"incomplete-replacement", "missing-key", "configuration-readback", "running-control"} {
		t.Run(fault, func(t *testing.T) {
			c, request, d, base := replacementControllerFixture(t)
			w := &dependencyWorkerDouble{base}
			if fault != "incomplete-replacement" {
				if _, e := replaceRestoredControl(context.Background(), c, d, w, false); e != nil {
					t.Fatal(e)
				}
			}
			w.databaseStarts = 0
			if fault == "missing-key" {
				if e := os.Remove(request.PrivateKeyFile); e != nil {
					t.Fatal(e)
				}
			}
			if fault == "configuration-readback" {
				d.fault = "verify-after"
			}
			if fault == "running-control" {
				s := w.state.Services["control"]
				s.State = "running"
				w.state.Services["control"] = s
			}
			before, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
			if _, e := startDependencyDouble(t, c, d, w); e == nil || w.databaseStarts != 0 {
				t.Fatal("unsafe dependency admitted")
			}
			after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
			if !bytes.Equal(before, after) {
				t.Fatal("failed admission wrote intent")
			}
		})
	}
}

func TestRestoredDependencyJournalRejectsForgedOrBroadenedAdmission(t *testing.T) {
	c, _, d, w := completedDependencyFixture(t)
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	prefix := append([]Event{}, l.events...)
	l.close()
	if _, e = startDependencyDouble(t, c, d, w); e != nil {
		t.Fatal(e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	event := l.events[len(prefix)]
	if e = validateProcessEvent(prefix, event); e != nil {
		t.Fatal("valid intent", e)
	}
	if e = validateImageEvent(prefix, event); e != nil {
		t.Fatal("global transition admission", e)
	}
	for _, fault := range []string{"missing", "replacement", "TLS", "runtime", "configuration-plan", "all-services", "running-control", "stopped"} {
		t.Run(fault, func(t *testing.T) {
			var forged Event
			if wire.StrictJSON(encode(event), &forged) != nil {
				t.Fatal("fixture decode")
			}
			r := forged.Process
			switch fault {
			case "missing":
				r.RestoredDependency = nil
			case "replacement":
				r.RestoredDependency.ReplacementProofSHA256 = strings.Repeat("a", 64)
			case "TLS":
				r.RestoredDependency.TLSProofSHA256 = strings.Repeat("b", 64)
			case "runtime":
				r.RestoredDependency.EffectiveRuntimeSHA256 = strings.Repeat("c", 64)
			case "configuration-plan":
				r.RestoredDependency.ConfigurationPlanSHA256 = strings.Repeat("d", 64)
			case "all-services":
				r.SelectedServices = nil
			case "running-control":
				s := r.Before["control"]
				s.State = "running"
				s.StartedAt = "2026-10-17T00:00:01Z"
				r.Before["control"] = s
			case "stopped":
				r.Target = "stopped"
				forged.Action = "process_stop"
				r.Admission = nil
			}
			if validateProcessEvent(prefix, forged) == nil {
				t.Fatal("forged admission accepted")
			}
			if validateImageEvent(prefix, forged) == nil {
				t.Fatal("global transition accepted forged admission")
			}
		})
	}
}
