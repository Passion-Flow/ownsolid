package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func absentRuntimeRequest(t *testing.T, c configuration.Config) configuration.RuntimeAbsenceRecoveryRequest {
	t.Helper()
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	return configuration.RuntimeAbsenceRecoveryRequest{FormatVersion: 1, ScopeID: l.marker.ScopeID, RuntimeCreateOperationID: runtimeState(l.events).pending, Reason: "Synthetic private recovery reason not for public projection"}
}

func TestRuntimeAbsenceRecoveryNeverResubmitsAndNewCreationIsExplicit(t *testing.T) {
	c, client, worker := runtimeScopeFixture(t)
	worker.fault = "before"
	if _, e := createRuntime(context.Background(), c, client, worker, false); e == nil {
		t.Fatal("uncertain fixture")
	}
	request := absentRuntimeRequest(t, c)
	if _, e := createRuntime(context.Background(), c, client, worker, true); e == nil || worker.calls != 1 {
		t.Fatal("ordinary absence reconcile must remain unresolved", e)
	}
	out, e := recoverAbsentRuntime(context.Background(), c, request, client, worker)
	if e != nil || !wire.ValidSHA256(out.ProofSHA256) || worker.calls != 1 {
		t.Fatal("explicit metadata recovery", out, e)
	}
	before, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if e != nil {
		t.Fatal(e)
	}
	again, e := recoverAbsentRuntime(context.Background(), c, request, client, worker)
	after, re := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if e != nil || re != nil || again != out || !bytes.Equal(before, after) || worker.calls != 1 {
		t.Fatal("recovery repeated an effect or journal append", e, re)
	}
	changed := request
	changed.Reason = "Different recovery action"
	if _, e = recoverAbsentRuntime(context.Background(), c, changed, client, worker); e == nil {
		t.Fatal("changed request adopted")
	}
	worker.fault = ""
	if _, e = createRuntime(context.Background(), c, client, worker, false); e != nil || worker.calls != 2 {
		t.Fatal("separate admitted new creation", e)
	}
	if again, e = recoverAbsentRuntime(context.Background(), c, request, client, worker); e != nil || again != out || worker.calls != 2 {
		t.Fatal("historical recovery changed subsequent runtime", e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	if !runtimeState(l.events).completed {
		t.Fatal("old recovery cleared new completed creation")
	}
	uncertain, recovered := 0, 0
	for _, event := range l.events {
		if event.Action == "runtime_create" && event.Outcome == "uncertain" {
			uncertain++
		}
		if event.Action == runtimeAbsenceRecoveryAction {
			recovered++
		}
	}
	public, _ := json.Marshal(l.releaseView())
	if uncertain != 1 || recovered != 1 || bytes.Contains(public, []byte(request.Reason)) || bytes.Contains(public, []byte("recovery_sha256")) {
		t.Fatal("original failure lost or private recovery payload projected")
	}
}

type runtimeRecoveryRace struct {
	*runtimeWorkerDouble
	reads int
	fail  bool
}

func (w *runtimeRecoveryRace) ObserveRuntime(ctx context.Context, reader compose.RuntimeReader, c compose.RenderContext, m compose.Model) (compose.RuntimeStatus, error) {
	w.reads++
	if w.fail {
		return compose.RuntimeStatus{}, errors.New("SYNTHETIC_OBSERVATION_UNAVAILABLE")
	}
	if w.reads > 1 {
		return createdRuntimeFixture(), nil
	}
	return w.runtimeWorkerDouble.ObserveRuntime(ctx, reader, c, m)
}

type runtimeRecoveryConsumerRace struct {
	*runtimeEngineDouble
	checks, allowed int
}

func (d *runtimeRecoveryConsumerRace) VerifyVolumeConsumers(context.Context, engine.Ownership, []string) error {
	d.checks++
	if d.checks > d.allowed {
		return errors.New("SYNTHETIC_FOREIGN_VOLUME_CONSUMER")
	}
	return nil
}

func TestRuntimeAbsenceRecoveryRefusesWrongReferencePartialCompletedAndRacedResources(t *testing.T) {
	for _, fault := range []string{"scope", "reference", "configuration", "observed-network", "observed-container", "recorded-resources", "completed", "observation-error", "second-observation", "consumer", "late-consumer", "canceled"} {
		t.Run(fault, func(t *testing.T) {
			c, client, worker := runtimeScopeFixture(t)
			worker.fault = "before"
			if fault == "recorded-resources" {
				worker.fault = "after"
			} else if fault == "completed" {
				worker.fault = ""
			}
			createRuntime(context.Background(), c, client, worker, false)
			request := absentRuntimeRequest(t, c)
			if fault == "completed" {
				request.RuntimeCreateOperationID = strings.Repeat("f", 32)
			}
			var observer runtimeOperator = worker
			var engineClient runtimeEngine = client
			ctx := context.Background()
			switch fault {
			case "scope":
				request.ScopeID = strings.Repeat("f", 32)
			case "reference":
				request.RuntimeCreateOperationID = strings.Repeat("f", 32)
			case "configuration":
				c.TimeoutSeconds++
			case "observed-network":
				worker.state = compose.RuntimeStatus{Status: "observed_partial_compose_resources", NetworkID: strings.Repeat("a", 64)}
			case "observed-container":
				worker.state = createdRuntimeFixture()
			case "recorded-resources":
				worker.state = compose.RuntimeStatus{Status: "observed_absent_compose_resources"}
			case "observation-error", "second-observation":
				observer = &runtimeRecoveryRace{runtimeWorkerDouble: worker, fail: fault == "observation-error"}
			case "consumer", "late-consumer":
				allowed := 0
				if fault == "late-consumer" {
					l, e := open(c, false)
					if e != nil {
						t.Fatal(e)
					}
					allowed = len(volumeState(l.events).roles)
					l.close()
				}
				engineClient = &runtimeRecoveryConsumerRace{runtimeEngineDouble: client, allowed: allowed}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
			if _, e := recoverAbsentRuntime(ctx, c, request, engineClient, observer); e == nil || worker.calls != 1 {
				t.Fatal("unsafe recovery admitted or create resent", e)
			}
			after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
			if !bytes.Equal(before, after) {
				t.Fatal("refused recovery changed journal")
			}
		})
	}
}

func TestRuntimeAbsenceRecoveryJournalStrictnessAndLegacyEncoding(t *testing.T) {
	c, client, worker := runtimeScopeFixture(t)
	worker.fault = "before"
	createRuntime(context.Background(), c, client, worker, false)
	request := absentRuntimeRequest(t, c)
	if _, e := recoverAbsentRuntime(context.Background(), c, request, client, worker); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	prefix := l.events[:len(l.events)-1]
	original := l.events[len(l.events)-1]
	for _, fault := range []string{"reference", "operation-reuse", "outcome", "code", "hash", "hash-missing", "descriptor", "model", "network", "ingress", "service"} {
		t.Run(fault, func(t *testing.T) {
			event := original
			r := *original.Runtime
			event.Runtime = &r
			switch fault {
			case "reference":
				event.ReferenceOperationID = strings.Repeat("f", 32)
			case "operation-reuse":
				event.OperationID = prefix[0].OperationID
			case "outcome":
				event.Outcome = "unresolved"
			case "code":
				event.Code = "SYNTHETIC_FAILED"
			case "hash":
				r.RecoverySHA256 = "invalid"
			case "hash-missing":
				r.RecoverySHA256 = ""
			case "descriptor":
				r.DescriptorSHA256 = strings.Repeat("f", 64)
			case "model":
				r.ModelSHA256 = strings.Repeat("f", 64)
			case "network":
				r.NetworkID = strings.Repeat("a", 64)
			case "ingress":
				r.IngressNetworkID = strings.Repeat("a", 64)
			case "service":
				r.Services = createdRuntimeFixture().Services
			}
			if validateRuntimeEvent(prefix, event) == nil {
				t.Fatal("forged recovery journal accepted")
			}
		})
	}
	old := RuntimeRecord{DescriptorSHA256: strings.Repeat("a", 64), ModelSHA256: strings.Repeat("b", 64)}
	raw, _ := json.Marshal(old)
	want := `{"descriptor_sha256":"` + strings.Repeat("a", 64) + `","model_sha256":"` + strings.Repeat("b", 64) + `"}`
	if string(raw) != want {
		t.Fatal("historical runtime encoding changed", string(raw))
	}
}

func TestRuntimeAbsenceRecoveryDirtyPublicationPreservesPendingIntent(t *testing.T) {
	c, client, worker := runtimeScopeFixture(t)
	worker.fault = "before"
	createRuntime(context.Background(), c, client, worker, false)
	request := absentRuntimeRequest(t, c)
	name := filepath.Join(c.ScopeRoot, "data/release-view/updating")
	if e := os.WriteFile(name, []byte("synthetic dirty publication"), 0600); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if _, e := recoverAbsentRuntime(context.Background(), c, request, client, worker); e == nil || worker.calls != 1 {
		t.Fatal("dirty publication admitted recovery", e)
	}
	after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(before, after) {
		t.Fatal("dirty failure changed original pending intent")
	}
}
