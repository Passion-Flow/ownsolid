package scope

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

func rotationRequest(t *testing.T) configuration.TLSGenerationRequest {
	t.Helper()
	directory := t.TempDir()
	if e := os.Chmod(directory, 0700); e != nil {
		t.Fatal(e)
	}
	cert, key, ca := restoredTLSCertificate(t)
	r := configuration.TLSGenerationRequest{FormatVersion: 1, CertificateFile: filepath.Join(directory, "certificate.pem"), PrivateKeyFile: filepath.Join(directory, "private-key.pem"), TrustBundleFile: filepath.Join(directory, "ca.pem"), Reason: "Synthetic isolated certificate rotation"}
	for name, raw := range map[string][]byte{r.CertificateFile: cert, r.PrivateKeyFile: key, r.TrustBundleFile: ca} {
		if e := os.WriteFile(name, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	return r
}

func TestTLSRotationSuccessiveGenerationsRetainHistoryAndRecoverLostEffects(t *testing.T) {
	ctx := context.Background()
	c, _, d, w := replacementControllerFixture(t)
	if _, e := replaceRestoredControl(ctx, c, d, w, false); e != nil {
		t.Fatal(e)
	}
	original, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	firstPlan, _ := os.ReadFile(filepath.Join(c.ScopeRoot, controlReplacementPlanFile))
	for i, id := range []string{strings.Repeat("e", 64), strings.Repeat("d", 64)} {
		oldID := w.state.Services["control"].ID
		r := rotationRequest(t)
		d.tlsControllerDouble.fault = "payload-after"
		failed, e := runTLSProjection(ctx, c, r, "rotate-restored-tls", d, w)
		if e == nil || !failed.Pending {
			t.Fatal("lost preparation response accepted", failed, e)
		}
		puts := d.puts
		if _, e = runTLSProjection(ctx, c, r, "rotate-restored-tls", d, w); e == nil || d.puts != puts {
			t.Fatal("unknown preparation resent", e)
		}
		out, e := runTLSProjection(ctx, c, r, "reconcile-restored-tls", d, w)
		if e != nil || out.Pending || d.puts != puts {
			t.Fatal("preparation observation", out, e)
		}
		l, e := open(c, false)
		if e != nil {
			t.Fatal(e)
		}
		verified, e := l.verifyRuntimeContext(ctx, d, w, false)
		if e != nil || verified.creation.record.Services["control"].ID != oldID || dependencyAdmission(l.events) != nil {
			t.Fatal("prepared generation changed effective runtime or authorized mixed proofs", e)
		}
		l.close()
		before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
		if _, e = runTLSProjection(ctx, c, r, "rotate-restored-tls", d, w); e != nil {
			t.Fatal("same request repeat", e)
		}
		after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
		if !bytes.Equal(before, after) {
			t.Fatal("completed preparation replayed")
		}
		d.newID, d.fault, d.fired = id, "remove-after", false
		removed := d.removed
		if _, e = replaceRestoredControl(ctx, c, d, w, false); e == nil || d.removed != removed+1 {
			t.Fatal("lost stopped removal not retained", e)
		}
		if _, e = runTLSProjection(ctx, c, r, "abort-restored-tls", d, w); e == nil {
			t.Fatal("replacement generation aborted")
		}
		if _, e = replaceRestoredControl(ctx, c, d, w, false); e == nil || d.removed != removed+1 {
			t.Fatal("unknown removal resent", e)
		}
		if out, e := replaceRestoredControl(ctx, c, d, w, true); e != nil || out.Pending || d.removed != removed+1 || w.state.Services["control"].ID != id {
			t.Fatal("replacement recovery", out, e)
		}
		l, e = open(c, false)
		if e != nil {
			t.Fatal("historical plans reopen", e)
		}
		verified, e = l.verifyRuntimeContext(ctx, d, w, false)
		if e != nil || effectiveRuntimeState(l.events).record.Services["control"].ID != id || verified.observed.Services["control"].ID != id || dependencyAdmission(l.events) == nil {
			t.Fatal("new effective context", e)
		}
		l.close()
		current, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
		retained, _ := os.ReadFile(filepath.Join(c.ScopeRoot, controlReplacementPlanFile))
		if !bytes.HasPrefix(current, original) || !bytes.Equal(retained, firstPlan) || len(d.volumes) != i+2 {
			t.Fatal("original history/rollback configuration erased")
		}
	}
}

func TestTLSRotationRefusesRunningFailedAndUnexplainedControlBeforeIntent(t *testing.T) {
	ctx := context.Background()
	c, _, d, w := replacementControllerFixture(t)
	if _, e := replaceRestoredControl(ctx, c, d, w, false); e != nil {
		t.Fatal(e)
	}
	r := rotationRequest(t)
	before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	for _, state := range []string{"running", "exited"} {
		s := w.state.Services["control"]
		s.State = state
		w.state.Services["control"] = s
		if _, e := runTLSProjection(ctx, c, r, "rotate-restored-tls", d, w); e == nil {
			t.Fatal("unexplained process admitted", state)
		}
		after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
		if !bytes.Equal(before, after) {
			t.Fatal("refused rotation wrote intent")
		}
	}
}

func TestTLSRotationAdmitsRecordedCleanExitedControlAndRetainsHistoricalAdmissions(t *testing.T) {
	c, _, d, w := applicationFixture(t)
	if _, e := startApplicationDouble(t, c, d, w); e != nil {
		t.Fatal(e)
	}
	if _, e := processRuntime(context.Background(), c, d, w, "stopped", false); e != nil {
		t.Fatal(e)
	}
	r := rotationRequest(t)
	if out, e := runTLSProjection(context.Background(), c, r, "rotate-restored-tls", d.replacementEngineDouble, w); e != nil || out.Pending {
		t.Fatal("known clean exited preparation", out, e)
	}
	d.newID = strings.Repeat("e", 64)
	if out, e := replaceRestoredControl(context.Background(), c, d.replacementEngineDouble, w, false); e != nil || out.Pending {
		t.Fatal("known clean exited replacement", out, e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal("historical live SQL/stores/application facts", e)
	}
	if e = l.verifyApplicationAdmissions(); e != nil {
		l.close()
		t.Fatal(e)
	}
	if _, e = l.admitRestoredApplication(context.Background(), d, w); e == nil {
		l.close()
		t.Fatal("old SQL/store proof authorized changed generation")
	}
	l.close()
	if _, e = startDependencyDouble(t, c, d.replacementEngineDouble, w.dependencyWorkerDouble); e != nil {
		t.Fatal("new generation dependency", e)
	}
	if _, e = runRestoredLiveDatabase(context.Background(), c, "requalify-restored-live-database", d, w); e != nil {
		t.Fatal("new TLS predecessor-bound SQL input", e)
	}
	if _, e = runRestoredCurrentStores(context.Background(), c, "requalify-restored-current-stores", d, w); e != nil {
		t.Fatal("new generation stores", e)
	}
	if _, e = startApplicationDouble(t, c, d, w); e != nil {
		t.Fatal("new generation activation", e)
	}
	if _, e = processRuntime(context.Background(), c, d, w, "stopped", false); e != nil {
		t.Fatal("new generation shutdown", e)
	}
}

func TestTLSRotationPreviousDeleteIntentCannotAdmitUnexplainedCurrentAbsence(t *testing.T) {
	old, current := strings.Repeat("a", 32), strings.Repeat("b", 32)
	events := []Event{
		{Action: "control_replacement_effect", Outcome: "started", ControlReplacement: &ControlReplacementRecord{GenerationID: old, Phase: "control-remove"}},
		{Action: "control_replacement_effect", Outcome: "started", ControlReplacement: &ControlReplacementRecord{GenerationID: current, Phase: "plan-publish"}},
	}
	l := locked{events: events}
	before := compose.RuntimeStatus{Status: "observed_complete_compose_resources", NetworkID: strings.Repeat("a", 64), Services: map[string]compose.RuntimeServiceStatus{"control": {ID: strings.Repeat("b", 64), State: "created"}, "postgres": {ID: strings.Repeat("c", 64), State: "exited"}}}
	absent := before
	absent.Status = "observed_partial_compose_resources"
	absent.Services = map[string]compose.RuntimeServiceStatus{"postgres": before.Services["postgres"]}
	if controlReplacementIntended(events, "control-remove") || l.validateReplacementObservation(ControlReplacementPlan{Before: before}, absent) == nil {
		t.Fatal("previous generation delete intent adopted unexpected current absence")
	}
}

func TestTLSRotationAbortUnusedGenerationAllowsNewGenerationAndKeepsEffectiveIdentity(t *testing.T) {
	ctx := context.Background()
	c, _, d, w := replacementControllerFixture(t)
	if _, e := replaceRestoredControl(ctx, c, d, w, false); e != nil {
		t.Fatal(e)
	}
	r := rotationRequest(t)
	oldID := w.state.Services["control"].ID
	d.tlsControllerDouble.fault = "payload-after"
	if _, e := runTLSProjection(ctx, c, r, "rotate-restored-tls", d, w); e == nil {
		t.Fatal("lost preparation accepted")
	}
	if _, e := runTLSProjection(ctx, c, r, "abort-restored-tls", d, w); e != nil {
		t.Fatal("unused generation cleanup", e)
	}
	if out, e := runTLSProjection(ctx, c, r, "rotate-restored-tls", d, w); e != nil || out.Pending || len(d.volumes) != 2 || w.state.Services["control"].ID != oldID {
		t.Fatal("new generation after verified abort", out, e)
	}
}
