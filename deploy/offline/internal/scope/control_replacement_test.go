package scope

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

// Explicit transport/process doubles, with real PKI/protected inputs/private
// plan files/fsync/hash-chain journals. Actual replacement has a separate gate.
type replacementEngineDouble struct {
	*tlsControllerDouble
	scope, newID, fault string
	removed, verified   int
	fired               bool
}

func (d *replacementEngineDouble) fail(kind string) bool {
	if d.fault == kind && !d.fired {
		d.fired = true
		return true
	}
	return false
}

func (d *replacementEngineDouble) CreateUtility(ctx context.Context, o engine.Ownership, image string, uid, gid int) (engine.Utility, error) {
	if d.fail("inspector-create-before") {
		return engine.Utility{}, errors.New("SYNTHETIC_INSPECTOR_CREATE_NOT_SENT")
	}
	return d.tlsControllerDouble.CreateUtility(ctx, o, image, uid, gid)
}
func (d *replacementEngineDouble) RemoveUtility(ctx context.Context, o engine.Ownership, image string, uid, gid int) error {
	if d.fail("inspector-remove-before") {
		return errors.New("SYNTHETIC_INSPECTOR_REMOVE_NOT_SENT")
	}
	return d.tlsControllerDouble.RemoveUtility(ctx, o, image, uid, gid)
}
func (d *replacementEngineDouble) InspectRuntime(ctx context.Context, scope, name string) (engine.RuntimeContainer, bool, error) {
	object, found, e := d.databaseEngineDouble.InspectRuntime(ctx, scope, name)
	if !found || e != nil {
		return object, found, e
	}
	object.Name = "/" + engine.RuntimeContainerName(scope, name)
	object.Platform = "linux"
	object.Image = "sha256:" + strings.Repeat("a", 64)
	object.Config.Image = object.Image
	object.Config.User = "10033:10034"
	object.Config.Labels = map[string]string{"io.projectone.isolated-scope": scope, "io.projectone.purpose": "isolated-validation", "com.docker.compose.project": engine.RuntimeProject(scope), "com.docker.compose.service": name, "com.docker.compose.container-number": "1", "com.docker.compose.oneoff": "False"}
	object.HostConfig.ReadonlyRootfs = true
	object.HostConfig.NetworkMode = engine.RuntimeNetworkName(scope)
	return object, found, e
}
func (d *replacementEngineDouble) RemoveStoppedControl(ctx context.Context, scope string, expected engine.RuntimeContainer) error {
	actual, found, e := d.InspectRuntime(ctx, scope, "control")
	if e != nil || !found || actual.ID != expected.ID {
		return errors.New("SYNTHETIC_CONTROL_ID_CHANGED")
	}
	if d.fail("remove-before") {
		return errors.New("SYNTHETIC_REMOVE_NOT_SENT")
	}
	d.removed++
	delete(d.worker.state.Services, "control")
	d.worker.state.Status = "observed_partial_compose_resources"
	if d.fail("remove-after") {
		return errors.New("SYNTHETIC_REMOVE_RESPONSE_LOST")
	}
	return nil
}
func (d *replacementEngineDouble) InspectStoppedControlConfiguration(ctx context.Context, o engine.Ownership, id, target string, uid, gid int, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	object, found, e := d.InspectRuntime(ctx, o.ScopeID, "control")
	if e != nil || !found || object.ID != id || id != d.newID || target == "" || uid < 1 || gid < 1 || len(files) == 0 || !d.payloads[o.Role] {
		return engine.ConfigurationObservation{}, errors.New("SYNTHETIC_MOUNTED_PAYLOAD_INVALID")
	}
	d.verified++
	if d.fail("verify-after") {
		return engine.ConfigurationObservation{}, errors.New("SYNTHETIC_READ_RESPONSE_LOST")
	}
	return engine.ConfigurationObservation{Status: engine.ConfigurationPayloadVerified}, nil
}

type replacementWorkerDouble struct {
	*databaseWorkerDouble
	engine  *replacementEngineDouble
	creates int
}

func (w *replacementWorkerDouble) ObserveControlReplacement(ctx context.Context, r compose.RuntimeReader, scope compose.RenderContext, model compose.Model, before compose.RuntimeStatus, required bool) (compose.RuntimeStatus, error) {
	actual := w.state
	_, present := actual.Services["control"]
	if present != required || actual.NetworkID != before.NetworkID || actual.Services["postgres"] != before.Services["postgres"] {
		return actual, errors.New("SYNTHETIC_REPLACEMENT_OBSERVATION_UNRESOLVED")
	}
	return actual, nil
}
func (w *replacementWorkerDouble) CreateReplacementControl(ctx context.Context, r compose.RuntimeReader, scope compose.RenderContext, model compose.Model, before compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	if _, e := w.ObserveControlReplacement(ctx, r, scope, model, before, false); e != nil {
		return w.state, e
	}
	role := ""
	for _, mount := range model.Services["control"].Mounts {
		if strings.HasPrefix(mount.Source, engine.TLSConfigurationRole+"-") {
			role = mount.Source
		}
	}
	if role == "" {
		return w.state, errors.New("SYNTHETIC_UNPROJECTED_MODEL")
	}
	if w.engine.fail("create-before") {
		return w.state, errors.New("SYNTHETIC_CREATE_NOT_SENT")
	}
	w.creates++
	w.state.Services["control"] = compose.RuntimeServiceStatus{ID: w.engine.newID, State: "created"}
	w.databaseEngine.times["control"] = "0001-01-01T00:00:00Z"
	w.state.Status = "observed_complete_compose_resources"
	if w.engine.fail("create-after") {
		return w.state, errors.New("SYNTHETIC_CREATE_RESPONSE_LOST")
	}
	return w.state, nil
}
func replacementControllerFixture(t *testing.T) (configuration.Config, configuration.TLSGenerationRequest, *replacementEngineDouble, *replacementWorkerDouble) {
	t.Helper()
	c, request, tls, w := tlsControllerFixture(t)
	if _, e := runTLSProjection(context.Background(), c, request, "prepare-restored-tls", tls, w); e != nil {
		t.Fatal(e)
	}
	status, e := Read(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	d := &replacementEngineDouble{tlsControllerDouble: tls, scope: status.ScopeID, newID: strings.Repeat("f", 64)}
	return c, request, d, &replacementWorkerDouble{databaseWorkerDouble: w, engine: d}
}
func TestControlReplacementControllerCompletesWithoutActivatingAndKeepsHistoricalRuntime(t *testing.T) {
	c, request, d, w := replacementControllerFixture(t)
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	original := encode(*runtimeState(l.events).record)
	sql, _ := restoreSQLProof(l.events)
	l.close()
	out, e := replaceRestoredControl(context.Background(), c, d, w, false)
	if e != nil || out.Pending || out.ProofSHA256 == "" || out.CompletedSteps != len(controlReplacementSteps()) || d.removed != 1 || w.creates != 1 || d.verified != 2 {
		t.Fatal("replacement", out, e, d.removed, w.creates, d.verified)
	}
	// Historical repeat and safe stopped observation do not need source keys.
	if e = os.Remove(request.PrivateKeyFile); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	again, e := replaceRestoredControl(context.Background(), c, d, w, false)
	after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if e != nil || again != out || !bytes.Equal(before, after) || d.removed != 1 || w.creates != 1 {
		t.Fatal("terminal replay", e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	if string(original) != string(encode(*runtimeState(l.events).record)) {
		t.Fatal("original rewritten")
	}
	if proof, _ := restoreSQLProof(l.events); proof != sql {
		t.Fatal("SQL proof changed")
	}
	verified, e := l.verifyRuntimeContext(context.Background(), d, w, false)
	if e != nil || verified.creation.record.Services["control"].ID != d.newID {
		t.Fatal("effective observation", e)
	}
	l.close()
	if _, e = processRuntime(context.Background(), c, d, w, "running", false); e == nil {
		t.Fatal("replacement activated")
	}
	if _, e = runTLSProjection(context.Background(), c, request, "abort-restored-tls", d, w); e == nil || len(d.volumes) != 1 {
		t.Fatal("in-use lineage aborted", e)
	}
	if _, e = processRuntime(context.Background(), c, d, w, "stopped", false); e != nil {
		t.Fatal("effective stop", e)
	}
}

func TestControlReplacementControllerMissingPrivateInputsAndCancellationSubmitNoEffects(t *testing.T) {
	c, request, d, w := replacementControllerFixture(t)
	before, e := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if e != nil {
		t.Fatal(e)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = replaceRestoredControl(cancelled, c, d, w, false); e == nil {
		t.Fatal("cancelled replacement admitted")
	}
	if e = os.Remove(request.PrivateKeyFile); e != nil {
		t.Fatal(e)
	}
	if _, e = replaceRestoredControl(context.Background(), c, d, w, false); e == nil {
		t.Fatal("missing key admitted")
	}
	after, e := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if e != nil || !bytes.Equal(before, after) || d.removed != 0 || w.creates != 0 || d.utilityCreates != 1 {
		t.Fatal("failed admission changed scope", e)
	}
}
func TestControlReplacementControllerLostRemoveCreateAndReadResultsNeverResend(t *testing.T) {
	for _, fault := range []string{"remove-after", "create-after", "verify-after"} {
		t.Run(fault, func(t *testing.T) {
			c, _, d, w := replacementControllerFixture(t)
			d.fault = fault
			if _, e := replaceRestoredControl(context.Background(), c, d, w, false); e == nil {
				t.Fatal("lost result accepted")
			}
			removed, created := d.removed, w.creates
			if _, e := replaceRestoredControl(context.Background(), c, d, w, false); e == nil || d.removed != removed || w.creates != created {
				t.Fatal("unknown effect resent")
			}
			if _, e := processRuntime(context.Background(), c, d, w, "stopped", false); e != nil {
				t.Fatal("truthful partial stop", e)
			}
			out, e := replaceRestoredControl(context.Background(), c, d, w, true)
			if e != nil || out.Pending || out.ProofSHA256 == "" || d.removed != 1 || w.creates != 1 {
				t.Fatal("separate observation", out, e)
			}
		})
	}
}
func TestControlReplacementControllerAbsentUnknownAndPeerDriftPreserveRecoveryState(t *testing.T) {
	for _, fault := range []string{"remove-before", "create-before"} {
		t.Run(fault, func(t *testing.T) {
			c, _, d, w := replacementControllerFixture(t)
			d.fault = fault
			if _, e := replaceRestoredControl(context.Background(), c, d, w, false); e == nil {
				t.Fatal("unknown missing result accepted")
			}
			removed, created := d.removed, w.creates
			if out, e := replaceRestoredControl(context.Background(), c, d, w, true); e == nil || !out.Pending || d.removed != removed || w.creates != created {
				t.Fatal("missing effect resent", out, e)
			}
			peer := w.state.Services["postgres"]
			peer.State = "running"
			w.state.Services["postgres"] = peer
			if _, e := processRuntime(context.Background(), c, d, w, "stopped", false); e == nil {
				t.Fatal("running peer hidden")
			}
		})
	}
}
