package scope

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

// Explicit transport double for fixed effect/reconciliation decisions only.
// It creates no real Docker resource or product success response.
type tlsEffectDouble struct {
	tlsProjectionEngine
	owner                                                        engine.Ownership
	volume, utility, payload                                     bool
	creates, utilityCreates, puts, utilityRemoves, volumeRemoves int
	lost                                                         string
	consumers                                                    bool
}

func (d *tlsEffectDouble) exact(o engine.Ownership) {
	if o != d.owner {
		panic("synthetic foreign TLS ownership")
	}
}
func (d *tlsEffectDouble) CreateVolume(_ context.Context, o engine.Ownership) (engine.Volume, error) {
	d.exact(o)
	d.creates++
	d.volume = true
	if d.lost == "volume" {
		return engine.Volume{}, errors.New("SYNTHETIC_VOLUME_RESPONSE_LOST")
	}
	return engine.Volume{Name: o.VolumeName()}, nil
}
func (d *tlsEffectDouble) InspectVolume(_ context.Context, o engine.Ownership) (engine.Volume, bool, error) {
	d.exact(o)
	return engine.Volume{Name: o.VolumeName()}, d.volume, nil
}
func (d *tlsEffectDouble) CreateUtility(_ context.Context, o engine.Ownership, _ string, _, _ int) (engine.Utility, error) {
	d.exact(o)
	d.utilityCreates++
	d.utility = true
	if d.lost == "utility" {
		return engine.Utility{}, errors.New("SYNTHETIC_UTILITY_RESPONSE_LOST")
	}
	return engine.Utility{ID: strings.Repeat("a", 64)}, nil
}
func (d *tlsEffectDouble) InspectUtility(_ context.Context, o engine.Ownership, _ string, _, _ int) (engine.Utility, bool, error) {
	d.exact(o)
	return engine.Utility{ID: strings.Repeat("a", 64)}, d.utility, nil
}
func (d *tlsEffectDouble) PutConfiguration(_ context.Context, o engine.Ownership, _ string, _, _ int, _ []engine.ConfigurationFile) error {
	d.exact(o)
	d.puts++
	d.payload = true
	if d.lost == "payload" {
		return errors.New("SYNTHETIC_PAYLOAD_RESPONSE_LOST")
	}
	return nil
}
func (d *tlsEffectDouble) InspectConfiguration(_ context.Context, o engine.Ownership, _ string, _, _ int, _ []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	d.exact(o)
	status := "observed_empty_owned_volume"
	if d.payload {
		status = engine.ConfigurationPayloadVerified
	}
	return engine.ConfigurationObservation{Status: status}, nil
}
func (d *tlsEffectDouble) RemoveUtility(_ context.Context, o engine.Ownership, _ string, _, _ int) error {
	d.exact(o)
	d.utilityRemoves++
	d.utility = false
	if d.lost == "remove-utility" {
		return errors.New("SYNTHETIC_UTILITY_REMOVE_RESPONSE_LOST")
	}
	return nil
}
func (d *tlsEffectDouble) VerifyVolumeConsumers(_ context.Context, o engine.Ownership, _ []string) error {
	d.exact(o)
	if d.consumers {
		return errors.New("SYNTHETIC_FOREIGN_CONSUMER")
	}
	return nil
}
func (d *tlsEffectDouble) RemoveTLSGenerationVolume(_ context.Context, o engine.Ownership) error {
	d.exact(o)
	if d.consumers {
		return errors.New("SYNTHETIC_FOREIGN_CONSUMER")
	}
	d.volumeRemoves++
	d.volume = false
	if d.lost == "remove-volume" {
		return errors.New("SYNTHETIC_VOLUME_REMOVE_RESPONSE_LOST")
	}
	return nil
}
func tlsEffectFixture() (RestoredTLSPlan, *tlsEffectDouble) {
	scope, generation := strings.Repeat("b", 32), strings.Repeat("c", 32)
	p := RestoredTLSPlan{ScopeID: scope, GenerationID: generation, Initializer: "sha256:" + strings.Repeat("d", 64), UID: 10033, GID: 10034, ConfigurationOwner: engine.Ownership{ScopeID: scope, OperationID: generation, Role: engine.TLSConfigurationRole + "-" + generation}}
	return p, &tlsEffectDouble{owner: p.ConfigurationOwner}
}
func TestTLSProjectionEffectsReadBackLostResultsWithoutResend(t *testing.T) {
	for _, kind := range []struct{ phase, lost string }{{"volume-create", "volume"}, {"utility-create", "utility"}, {"payload-write", "payload"}, {"utility-remove", "remove-utility"}} {
		t.Run(kind.phase, func(t *testing.T) {
			p, d := tlsEffectFixture()
			d.volume, d.utility = true, true
			d.lost = kind.lost
			r := TLSProjectionRecord{GenerationID: p.GenerationID, Phase: kind.phase}
			if _, e := executeTLSProjectionStep(context.Background(), d, p, r, nil, false); e == nil {
				t.Fatal("lost result accepted")
			}
			if _, e := executeTLSProjectionStep(context.Background(), d, p, r, nil, true); e != nil {
				t.Fatal("readback unresolved", e)
			}
			if d.creates > 1 || d.utilityCreates > 1 || d.puts > 1 || d.utilityRemoves > 1 || d.volumeRemoves != 0 {
				t.Fatal("effect resent or data removed")
			}
		})
	}
}
func TestTLSProjectionEffectsMissingPartialAndForeignActorsNeverComplete(t *testing.T) {
	for _, phase := range []string{"volume-create", "utility-create", "payload-write", "complete"} {
		t.Run(phase, func(t *testing.T) {
			p, d := tlsEffectFixture()
			r := TLSProjectionRecord{GenerationID: p.GenerationID, Phase: phase}
			if _, e := executeTLSProjectionStep(context.Background(), d, p, r, nil, true); e == nil {
				t.Fatal("missing/partial result qualified")
			}
			if d.creates+d.utilityCreates+d.puts+d.utilityRemoves+d.volumeRemoves != 0 {
				t.Fatal("readback mutated state")
			}
		})
	}
	p, d := tlsEffectFixture()
	d.volume = true
	d.consumers = true
	r := TLSProjectionRecord{GenerationID: p.GenerationID, Phase: "complete"}
	if _, e := executeTLSProjectionStep(context.Background(), d, p, r, nil, false); e == nil {
		t.Fatal("foreign consumer qualified")
	}
	d.consumers = false
	d.utility = true
	if _, e := executeTLSProjectionStep(context.Background(), d, p, r, nil, false); e == nil {
		t.Fatal("remaining utility qualified")
	}
	d.utility = false
	if _, e := executeTLSProjectionStep(context.Background(), d, p, r, nil, false); e != nil {
		t.Fatal("exclusive prepared volume", e)
	}
}
func TestTLSProjectionEffectsCancelOrChangedOwnerSubmitNothing(t *testing.T) {
	p, d := tlsEffectFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := TLSProjectionRecord{GenerationID: p.GenerationID, Phase: "volume-create"}
	if _, e := executeTLSProjectionStep(ctx, d, p, r, nil, false); e == nil {
		t.Fatal("canceled effect submitted")
	}
	p.ConfigurationOwner.Role = "control-config"
	if _, e := executeTLSProjectionStep(context.Background(), d, p, r, nil, false); e == nil {
		t.Fatal("old configuration role submitted")
	}
	l := &locked{events: []Event{{Action: "tls_projection_effect", TLSProjection: &TLSProjectionRecord{GenerationID: p.GenerationID, Phase: "volume-create"}}}}
	if e := l.removeTLSProjectionActors(context.Background(), d, p); e == nil {
		t.Fatal("cleanup inspected old installation role")
	}
	if d.creates+d.utilityCreates+d.puts+d.utilityRemoves+d.volumeRemoves != 0 {
		t.Fatal("refusal mutated state")
	}
}
func TestTLSProjectionGeneratedCleanupObservesLostRemovalsAndPreservesOtherData(t *testing.T) {
	for _, fault := range []string{"none", "remove-utility", "remove-volume", "foreign-consumer"} {
		t.Run(fault, func(t *testing.T) {
			p, d := tlsEffectFixture()
			d.volume, d.utility = true, true
			d.lost = fault
			d.consumers = fault == "foreign-consumer"
			l := &locked{events: []Event{{Action: "tls_projection_effect", TLSProjection: &TLSProjectionRecord{GenerationID: p.GenerationID, Phase: "volume-create"}}}}
			e := l.removeTLSProjectionActors(context.Background(), d, p)
			if fault == "foreign-consumer" {
				if e == nil || !d.volume || d.volumeRemoves != 0 {
					t.Fatal("consumer volume removed")
				}
				return
			}
			if fault == "none" {
				if e != nil {
					t.Fatal(e)
				}
			} else {
				if e == nil {
					t.Fatal("lost cleanup accepted")
				}
				d.lost = ""
				if e = l.removeTLSProjectionActors(context.Background(), d, p); e != nil {
					t.Fatal("explicit cleanup observation", e)
				}
			}
			if d.volume || d.utility || d.volumeRemoves != 1 || d.utilityRemoves != 1 {
				t.Fatal("cleanup replayed delete or retained generated actors")
			}
		})
	}
}
