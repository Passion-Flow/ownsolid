package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Protected real files/journals with synthetic runtime and TLS projection facts.
// These tests never create, delete, start or replace an actual container.
func replacementPrivateFixture(t *testing.T) (*locked, ControlReplacementPlan) {
	t.Helper()
	l, tls := tlsProjectionPrivatePlanFixture(t)
	if e := l.publishTLSProjectionPlan(context.Background(), tls, false, func(p RestoredTLSPlan) error {
		return writeNew(l.root, tlsProjectionPlanFile(p.GenerationID), encode(p))
	}); e != nil {
		l.close()
		t.Fatal(e)
	}
	for i := 1; i < len(tlsProjectionSteps()); i++ {
		events := tlsProjectionComplete(t, l.events, tlsProjectionIntent(t, l.events, tlsProjectionPlanRecord(tls), i))
		for _, event := range events[len(events)-2:] {
			if e := l.appendEvent(event); e != nil {
				l.close()
				t.Fatal(e)
			}
		}
	}
	_, model, e := l.replacementModels(context.Background())
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	next, e := compose.ControlConfigurationModel(model, tls.GenerationID)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	original := runtimeState(l.events).record
	before := compose.RuntimeStatus{Status: "observed_complete_compose_resources", NetworkID: original.NetworkID, IngressNetworkID: original.IngressNetworkID, Services: map[string]compose.RuntimeServiceStatus{}}
	for name, service := range original.Services {
		before.Services[name] = service
	}
	var object engine.RuntimeContainer
	object.ID = original.Services["control"].ID
	object.Name = "/" + engine.RuntimeContainerName(l.marker.ScopeID, "control")
	object.Config.Labels = map[string]string{"io.projectone.isolated-scope": l.marker.ScopeID}
	object.State.Status = "created"
	object.State.StartedAt = "0001-01-01T00:00:00Z"
	hash, e := engine.RuntimeContainerDigest(object)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	proof, _ := tlsProjectionProof(l.events)
	metadata, _ := json.Marshal(object)
	p := ControlReplacementPlan{FormatVersion: 1, ScopeID: l.marker.ScopeID, GenerationID: tls.GenerationID, TLSProofSHA256: proof, TLSPlanSHA256: tlsProjectionPlanRecord(tls).PlanSHA256, RuntimeSHA256: wire.Digest(encode(*original)), ModelSHA256: wire.Digest(encode(next)), PreviousMetadataSHA256: hash, PreviousControlJSON: string(metadata), Before: before}
	if e = l.validateControlReplacementPlan(context.Background(), p); e != nil {
		l.close()
		t.Fatal(e)
	}
	return l, p
}
func TestControlReplacementPrivatePlanDurableReopenAndHistoricalReadback(t *testing.T) {
	l, p := replacementPrivateFixture(t)
	c := l.config
	if e := l.publishControlReplacementPlan(context.Background(), p, false, func() error { return writeNew(l.root, controlReplacementPlanFile, encode(p)) }); e != nil {
		l.close()
		t.Fatal(e)
	}
	journal, _ := regular(l.root, journalName, c.MaximumJournalBytes)
	l.close()
	var e error
	l, e = open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	tls, e := l.readTLSProjectionPlan(*tlsProjectionState(l.events).record)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{tls.Request.CertificateFile, tls.Request.PrivateKeyFile, tls.Request.TrustBundleFile} {
		if e = os.Remove(name); e != nil {
			t.Fatal(e)
		}
	}
	if e = l.publishControlReplacementPlan(context.Background(), p, false, nil); e != nil {
		t.Fatal("historical completed publication", e)
	}
	read, e := l.readControlReplacementPlan()
	if e != nil || !reflect.DeepEqual(read, p) {
		t.Fatal("private readback", e)
	}
	after, e := regular(l.root, journalName, c.MaximumJournalBytes)
	if e != nil || !bytes.Equal(journal, after) {
		t.Fatal("completed publication replayed", e)
	}
}
func TestControlReplacementPrivatePlanLostAndMissingPublicationNeedsExplicitRecovery(t *testing.T) {
	for _, write := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "written-response-lost"}[write], func(t *testing.T) {
			l, p := replacementPrivateFixture(t)
			defer l.close()
			calls := 0
			publish := func() error {
				calls++
				if write {
					if e := writeNew(l.root, controlReplacementPlanFile, encode(p)); e != nil {
						return e
					}
				}
				return errors.New("SYNTHETIC_PUBLICATION_RESPONSE_LOST")
			}
			if e := l.publishControlReplacementPlan(context.Background(), p, false, publish); e == nil {
				t.Fatal("unknown result accepted")
			}
			if e := l.publishControlReplacementPlan(context.Background(), p, false, publish); e == nil || calls != 1 {
				t.Fatal("unknown publication resent")
			}
			if e := l.verifyControlReplacementPlan(); e != nil {
				t.Fatal("first unknown plan must remain recoverable", e)
			}
			if e := l.publishControlReplacementPlan(context.Background(), p, true, func() error { calls++; return writeNew(l.root, controlReplacementPlanFile, encode(p)) }); e != nil {
				t.Fatal("explicit recovery", e)
			}
			expected := 2
			if write {
				expected = 1
			}
			if calls != expected {
				t.Fatal("written publication resent")
			}
		})
	}
}
func TestControlReplacementPrivatePlanRejectsDriftAndPreservesPartial(t *testing.T) {
	l, p := replacementPrivateFixture(t)
	defer l.close()
	for _, fault := range []string{"scope", "generation", "TLS-proof", "TLS-plan", "runtime", "model", "metadata", "ID", "name", "running", "peer", "network"} {
		t.Run(fault, func(t *testing.T) {
			raw := encode(p)
			var changed ControlReplacementPlan
			if wire.StrictJSON(raw, &changed) != nil {
				t.Fatal("fixture")
			}
			object, e := changed.previousControl()
			if e != nil {
				t.Fatal(e)
			}
			switch fault {
			case "scope":
				changed.ScopeID = strings.Repeat("0", 32)
			case "generation":
				changed.GenerationID = strings.Repeat("0", 32)
			case "TLS-proof":
				changed.TLSProofSHA256 = strings.Repeat("0", 64)
			case "TLS-plan":
				changed.TLSPlanSHA256 = strings.Repeat("0", 64)
			case "runtime":
				changed.RuntimeSHA256 = strings.Repeat("0", 64)
			case "model":
				changed.ModelSHA256 = strings.Repeat("0", 64)
			case "metadata":
				object.Config.Cmd = []string{"changed"}
			case "ID":
				object.ID = strings.Repeat("0", 64)
			case "name":
				object.Name = "/foreign"
			case "running":
				object.State.Running = true
			case "peer":
				service := changed.Before.Services["postgres"]
				service.ID = strings.Repeat("0", 64)
				changed.Before.Services["postgres"] = service
			case "network":
				changed.Before.NetworkID = strings.Repeat("0", 64)
			}
			metadata, _ := json.Marshal(object)
			changed.PreviousControlJSON = string(metadata)
			if e := l.validateControlReplacementPlan(context.Background(), changed); e == nil {
				t.Fatal("context drift accepted")
			}
		})
	}
	partial := []byte("{partial-preserve")
	if e := l.publishControlReplacementPlan(context.Background(), p, false, func() error { return writeNew(l.root, controlReplacementPlanFile, partial) }); e == nil {
		t.Fatal("partial accepted")
	}
	if e := l.publishControlReplacementPlan(context.Background(), p, true, func() error { t.Fatal("partial overwritten"); return nil }); e == nil {
		t.Fatal("partial reconciled")
	}
	after, e := regular(l.root, controlReplacementPlanFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil || !bytes.Equal(partial, after) {
		t.Fatal("partial lost", e)
	}
}
