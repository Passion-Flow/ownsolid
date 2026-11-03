package scope

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit SDK/process doubles. Protected configuration/PKI, private plans and
// fsynced scope journals are real local files; no backend or Docker work is claimed.
type tlsControllerDouble struct {
	*databaseEngineDouble
	owner                                                        engine.Ownership
	volumes, utilities, payloads                                 map[string]bool
	creates, utilityCreates, puts, utilityRemoves, volumeRemoves int
	fault                                                        string
	cancel                                                       context.CancelFunc
}

func (d *tlsControllerDouble) generated(o engine.Ownership) bool {
	return o.Role == engine.TLSConfigurationRole+"-"+o.OperationID
}
func (d *tlsControllerDouble) fail(kind string) bool {
	if d.fault == kind {
		d.fault = ""
		return true
	}
	return false
}
func (d *tlsControllerDouble) ValidateConfigurationFiles(files []engine.ConfigurationFile, uid, gid int) error {
	if len(files) == 0 || uid < 1 || gid < 1 {
		return errors.New("SYNTHETIC_INVALID_CONFIGURATION")
	}
	return nil
}
func (d *tlsControllerDouble) CreateVolume(_ context.Context, o engine.Ownership) (engine.Volume, error) {
	if !d.generated(o) {
		panic("foreign TLS volume")
	}
	d.owner = o
	d.creates++
	if d.fail("volume-before") {
		return engine.Volume{}, errors.New("SYNTHETIC_CREATE_UNKNOWN")
	}
	d.volumes[o.Role] = true
	if d.fail("volume-after") {
		return engine.Volume{}, errors.New("SYNTHETIC_CREATE_RESPONSE_LOST")
	}
	return engine.Volume{Name: o.VolumeName()}, nil
}
func (d *tlsControllerDouble) InspectVolume(ctx context.Context, o engine.Ownership) (engine.Volume, bool, error) {
	if !d.generated(o) {
		return d.databaseEngineDouble.InspectVolume(ctx, o)
	}
	return engine.Volume{Name: o.VolumeName()}, d.volumes[o.Role], nil
}
func (d *tlsControllerDouble) CreateUtility(_ context.Context, o engine.Ownership, _ string, _, _ int) (engine.Utility, error) {
	if !d.generated(o) || !d.volumes[o.Role] {
		panic("foreign TLS utility")
	}
	d.utilityCreates++
	d.utilities[o.Role] = true
	if d.fail("utility-after") {
		return engine.Utility{}, errors.New("SYNTHETIC_UTILITY_RESPONSE_LOST")
	}
	return engine.Utility{ID: wire.Digest([]byte(o.Role))}, nil
}
func (d *tlsControllerDouble) InspectUtility(_ context.Context, o engine.Ownership, _ string, _, _ int) (engine.Utility, bool, error) {
	if !d.generated(o) {
		panic("foreign utility inspection")
	}
	return engine.Utility{ID: wire.Digest([]byte(o.Role))}, d.utilities[o.Role], nil
}
func (d *tlsControllerDouble) PutConfiguration(_ context.Context, o engine.Ownership, _ string, _, _ int, _ []engine.ConfigurationFile) error {
	if !d.generated(o) || !d.utilities[o.Role] {
		panic("foreign payload")
	}
	d.puts++
	d.payloads[o.Role] = true
	if d.cancel != nil {
		d.cancel()
	}
	if d.fail("payload-after") {
		return errors.New("SYNTHETIC_COPY_RESPONSE_LOST")
	}
	return nil
}
func (d *tlsControllerDouble) InspectConfiguration(_ context.Context, o engine.Ownership, _ string, _, _ int, _ []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	if !d.generated(o) {
		panic("foreign payload inspection")
	}
	status := "observed_empty_owned_volume"
	if d.payloads[o.Role] {
		status = engine.ConfigurationPayloadVerified
	}
	return engine.ConfigurationObservation{Status: status}, nil
}
func (d *tlsControllerDouble) RemoveUtility(_ context.Context, o engine.Ownership, _ string, _, _ int) error {
	if !d.generated(o) {
		panic("foreign utility removal")
	}
	d.utilityRemoves++
	delete(d.utilities, o.Role)
	if d.fail("utility-remove-after") {
		return errors.New("SYNTHETIC_UTILITY_DELETE_RESPONSE_LOST")
	}
	return nil
}
func (d *tlsControllerDouble) RemoveTLSGenerationVolume(_ context.Context, o engine.Ownership) error {
	if !d.generated(o) || d.utilities[o.Role] {
		panic("foreign/live volume removal")
	}
	d.volumeRemoves++
	delete(d.volumes, o.Role)
	delete(d.payloads, o.Role)
	if d.fail("volume-remove-after") {
		return errors.New("SYNTHETIC_VOLUME_DELETE_RESPONSE_LOST")
	}
	return nil
}
func (d *tlsControllerDouble) VerifyVolumeConsumers(ctx context.Context, o engine.Ownership, ids []string) error {
	if !d.generated(o) {
		return d.databaseEngineDouble.VerifyVolumeConsumers(ctx, o, ids)
	}
	if d.utilities[o.Role] && len(ids) == 0 {
		return errors.New("SYNTHETIC_UTILITY_CONSUMER")
	}
	return nil
}
func (d *tlsControllerDouble) RuntimeCandidates(ctx context.Context, scope string, networks bool) ([]engine.RuntimeObject, error) {
	objects, e := d.databaseEngineDouble.RuntimeCandidates(ctx, scope, networks)
	if e != nil || networks {
		return objects, e
	}
	for role, active := range d.utilities {
		if active {
			objects = append(objects, engine.RuntimeObject{ID: wire.Digest([]byte(role))})
		}
	}
	return objects, nil
}
func tlsControllerFixture(t *testing.T) (configuration.Config, configuration.TLSGenerationRequest, *tlsControllerDouble, *databaseWorkerDouble) {
	t.Helper()
	l, p, d, w := tlsProjectionPrivatePlanRuntimeFixture(t)
	c := l.config
	l.close()
	// Explicit stopped process metadata; no process execution is simulated as health.
	s := w.state.Services["postgres"]
	s.State = "exited"
	s.ExitCode = 0
	w.state.Services["postgres"] = s
	return c, p.Request, &tlsControllerDouble{databaseEngineDouble: d, volumes: map[string]bool{}, utilities: map[string]bool{}, payloads: map[string]bool{}}, w
}
func TestTLSProjectionControllerCompletesAndTerminalRepeatDoesNotReplay(t *testing.T) {
	c, r, d, w := tlsControllerFixture(t)
	out, e := runTLSProjection(context.Background(), c, r, "prepare-restored-tls", d, w)
	if e != nil || out.Pending || out.ProofSHA256 == "" || out.CompletedSteps != 6 || len(d.volumes) != 1 || len(d.utilities) != 0 || d.creates != 1 || d.puts != 1 {
		t.Fatal("TLS preparation", out, e)
	}
	before, e := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if e != nil {
		t.Fatal(e)
	}
	again, e := runTLSProjection(context.Background(), c, r, "prepare-restored-tls", d, w)
	after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if e != nil || again != out || !bytes.Equal(before, after) || d.puts != 1 {
		t.Fatal("terminal replay", e)
	}
	status, e := Read(context.Background(), c)
	if e != nil || status.TLSProjection == nil || status.TLSProjection.ProofSHA256 != out.ProofSHA256 {
		t.Fatal("preparation status", e)
	}
	if _, e = processRuntime(context.Background(), c, d, w, "running", false); e == nil {
		t.Fatal("TLS preparation opened activation")
	}
}
func TestTLSProjectionControllerLostResultsRequireObservationWithoutResend(t *testing.T) {
	for _, fault := range []string{"volume-after", "utility-after", "payload-after", "utility-remove-after"} {
		t.Run(fault, func(t *testing.T) {
			c, r, d, w := tlsControllerFixture(t)
			d.fault = fault
			if _, e := runTLSProjection(context.Background(), c, r, "prepare-restored-tls", d, w); e == nil {
				t.Fatal("lost result accepted")
			}
			creates, puts := d.creates, d.puts
			if _, e := runTLSProjection(context.Background(), c, r, "prepare-restored-tls", d, w); e == nil || d.creates != creates || d.puts != puts {
				t.Fatal("ordinary resubmit")
			}
			out, e := runTLSProjection(context.Background(), c, r, "reconcile-restored-tls", d, w)
			if e != nil || out.Pending || out.ProofSHA256 == "" || d.creates != 1 || d.utilityCreates != 1 || d.puts != 1 || d.utilityRemoves != 1 {
				t.Fatal("explicit observation and later effects", out, e)
			}
		})
	}
}
func TestTLSProjectionControllerAbsentUnknownEffectAbortsAndRetriesNewGeneration(t *testing.T) {
	c, r, d, w := tlsControllerFixture(t)
	d.fault = "volume-before"
	failed, e := runTLSProjection(context.Background(), c, r, "prepare-restored-tls", d, w)
	if e == nil {
		t.Fatal("absent unknown create accepted")
	}
	if _, e = runTLSProjection(context.Background(), c, r, "reconcile-restored-tls", d, w); e == nil || d.creates != 1 {
		t.Fatal("absent unknown create resent")
	}
	if _, e = runTLSProjection(context.Background(), c, r, "abort-restored-tls", d, w); e != nil {
		t.Fatal("safe abort", e)
	}
	out, e := runTLSProjection(context.Background(), c, r, "prepare-restored-tls", d, w)
	if e != nil || out.GenerationID == failed.GenerationID || out.ProofSHA256 == "" || d.creates != 2 || d.puts != 1 {
		t.Fatal("cleaned same-target new generation", out, e)
	}
}
func TestTLSProjectionControllerSourceIndependentAbortCleanupLossAndCancel(t *testing.T) {
	for _, fault := range []string{"missing-source", "cleanup-loss", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			c, r, d, w := tlsControllerFixture(t)
			ctx := context.Background()
			if fault == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				d.cancel = cancel
			} else {
				d.fault = "payload-after"
			}
			if _, e := runTLSProjection(ctx, c, r, "prepare-restored-tls", d, w); e == nil {
				t.Fatal("interruption accepted")
			}
			if fault == "missing-source" {
				if e := os.Remove(r.PrivateKeyFile); e != nil {
					t.Fatal(e)
				}
			}
			if fault == "cleanup-loss" {
				d.fault = "volume-remove-after"
				if _, e := runTLSProjection(context.Background(), c, r, "abort-restored-tls", d, w); e == nil {
					t.Fatal("lost cleanup accepted")
				}
			}
			out, e := runTLSProjection(context.Background(), c, r, "abort-restored-tls", d, w)
			if e != nil || out.Pending || out.Status != "TLS_projection_aborted_original_configuration_preserved" || len(d.volumes) != 0 || len(d.utilities) != 0 || d.volumeRemoves != 1 {
				t.Fatal("generated-only independent cleanup", out, e)
			}
			l, e := open(c, false)
			if e != nil {
				t.Fatal(e)
			}
			defer l.close()
			if _, ok := restoreStoresProof(l.events); !ok {
				t.Fatal("abort erased restored data lineage")
			}
		})
	}
}

func TestTLSProjectionControllerPartialCopiedPayloadNeverQualifiesOrResends(t *testing.T) {
	c, r, d, w := tlsControllerFixture(t)
	d.fault = "payload-after"
	if _, e := runTLSProjection(context.Background(), c, r, "prepare-restored-tls", d, w); e == nil {
		t.Fatal("lost copy accepted")
	}
	d.payloads[d.owner.Role] = false
	if _, e := runTLSProjection(context.Background(), c, r, "reconcile-restored-tls", d, w); e == nil || d.puts != 1 {
		t.Fatal("partial copied data qualified or was resent")
	}
	if _, e := runTLSProjection(context.Background(), c, r, "abort-restored-tls", d, w); e != nil || len(d.volumes) != 0 {
		t.Fatal("partial generated cleanup", e)
	}
}
func TestTLSProjectionControllerKnownUtilityDoesNotBlockScopedStop(t *testing.T) {
	c, r, d, w := tlsControllerFixture(t)
	d.fault = "payload-after"
	if _, e := runTLSProjection(context.Background(), c, r, "prepare-restored-tls", d, w); e == nil {
		t.Fatal("lost copy accepted")
	}
	if e := os.Remove(r.PrivateKeyFile); e != nil {
		t.Fatal(e)
	}
	if _, e := processRuntime(context.Background(), c, d, w, "stopped", false); e != nil {
		t.Fatal("source-independent exact utility view blocked stop", e)
	}
	if _, e := runTLSProjection(context.Background(), c, r, "abort-restored-tls", d, w); e != nil {
		t.Fatal(e)
	}
}
func TestTLSProjectionPublicMetadataAbortAvoidsSDKOnlyBeforeActorIntent(t *testing.T) {
	for _, actor := range []bool{false, true} {
		t.Run(map[bool]string{false: "metadata-only", true: "actor-intended"}[actor], func(t *testing.T) {
			c, r, d, w := tlsControllerFixture(t)
			l, e := open(c, true)
			if e != nil {
				t.Fatal(e)
			}
			generation, _ := nonce()
			plan, _, e := l.currentTLSProjectionInput(context.Background(), d, w, r, generation)
			if e != nil {
				l.close()
				t.Fatal(e)
			}
			if e = l.publishTLSProjectionPlan(context.Background(), plan, false, func(p RestoredTLSPlan) error {
				return writeNew(l.root, tlsProjectionPlanFile(p.GenerationID), encode(p))
			}); e != nil {
				l.close()
				t.Fatal(e)
			}
			if actor {
				r := tlsProjectionPlanRecord(plan)
				r.Step = 1
				r.Phase = "volume-create"
				id, _ := nonce()
				if e = l.appendEvent(Event{OperationID: id, Action: "tls_projection_effect", Outcome: "started", TLSProjection: &r}); e != nil {
					l.close()
					t.Fatal(e)
				}
			}
			l.close()
			if e = os.Remove(r.PrivateKeyFile); e != nil {
				t.Fatal(e)
			}
			c.Engine.Utility = nil
			out, e := RunTLSProjection(context.Background(), c, r, "abort-restored-tls")
			if actor {
				if e == nil || e.Error() != "OFFLINE_SCOPE_TLS_PROJECTION_CONFIGURATION_REQUIRED" {
					t.Fatal("actor uncertainty bypassed SDK cleanup", out, e)
				}
			} else if e != nil || out.Status != "TLS_projection_aborted_original_configuration_preserved" {
				t.Fatal("host-only metadata abort required SDK/source", out, e)
			}
		})
	}
}
