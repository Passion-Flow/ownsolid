package scope

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func replacementJournalFixture(t *testing.T) ([]Event, ControlReplacementRecord) {
	t.Helper()
	events, base := tlsProjectionJournalFixture(t)
	for i := range tlsProjectionSteps() {
		events = tlsProjectionComplete(t, events, tlsProjectionIntent(t, events, base, i))
	}
	proof, _ := tlsProjectionProof(events)
	return events, ControlReplacementRecord{GenerationID: base.GenerationID, PlanSHA256: strings.Repeat("b", 64), TLSProofSHA256: proof, RuntimeSHA256: base.RuntimeSHA256, ModelSHA256: strings.Repeat("c", 64), Phase: controlReplacementSteps()[0]}
}
func replacementIntent(t *testing.T, r ControlReplacementRecord, index int) Event {
	t.Helper()
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	r.Step = index
	r.Phase = controlReplacementSteps()[index]
	r.ObjectID = ""
	r.Effective = nil
	return Event{OperationID: id, Action: "control_replacement_effect", Outcome: "started", ControlReplacement: &r}
}
func replacementCompletion(events []Event, intent Event) Event {
	done := intent
	r := *intent.ControlReplacement
	done.ControlReplacement = &r
	done.Outcome = "completed"
	if r.Phase == "inspector-create" {
		r.ObjectID = strings.Repeat("e", 64)
	}
	if r.Phase == "control-create" || r.Phase == "control-verify" {
		r.ObjectID = strings.Repeat("f", 64)
	}
	if r.Phase == "complete" {
		original := runtimeState(events).record
		next := *original
		next.ModelSHA256 = r.ModelSHA256
		next.Services = map[string]compose.RuntimeServiceStatus{}
		for name, service := range original.Services {
			next.Services[name] = service
		}
		control := next.Services["control"]
		control.ID = strings.Repeat("f", 64)
		next.Services["control"] = control
		r.Effective = &next
	}
	return done
}
func appendReplacementStep(t *testing.T, events []Event, r ControlReplacementRecord, index int) []Event {
	t.Helper()
	intent := replacementIntent(t, r, index)
	if e := validateImageEvent(events, intent); e != nil {
		t.Fatal("intent", index, e)
	}
	events = append(events, intent)
	done := replacementCompletion(events, intent)
	if e := validateImageEvent(events, done); e != nil {
		t.Fatal("completion", index, e)
	}
	return append(events, done)
}
func TestControlReplacementJournalPreservesHistoricalProofsAndEffectivePrefix(t *testing.T) {
	events, base := replacementJournalFixture(t)
	original := encode(*runtimeState(events).record)
	tlsProof, _ := tlsProjectionProof(events)
	for i := range controlReplacementSteps() {
		if _, ok := controlReplacementProof(events); ok {
			t.Fatal("premature proof")
		}
		if !reflect.DeepEqual(runtimeState(events), effectiveRuntimeState(events)) {
			t.Fatal("premature effective identity")
		}
		events = appendReplacementStep(t, events, base, i)
		if validateImageEvent(events, Event{Action: "tls_projection_abort", TLSProjection: tlsProjectionState(events).record}) == nil {
			t.Fatal("TLS lineage invalidated")
		}
		if validateImageEvent(events, Event{Action: "process_start", Process: &ProcessRecord{SelectedServices: []string{"postgres"}}}) == nil {
			t.Fatal("replacement opened startup")
		}
	}
	if proof, ok := controlReplacementProof(events); !ok || !wire.ValidSHA256(proof) {
		t.Fatal("missing separate proof")
	}
	if string(original) != string(encode(*runtimeState(events).record)) {
		t.Fatal("original runtime changed")
	}
	if proof, _ := tlsProjectionProof(events); proof != tlsProof {
		t.Fatal("TLS proof changed")
	}
	effective := effectiveRuntimeState(events)
	if effective.record.Services["control"].ID != strings.Repeat("f", 64) || effective.record.ModelSHA256 != base.ModelSHA256 {
		t.Fatal("missing effective identity")
	}
	if e := validateImageEvent(events, replacementIntent(t, base, 0)); e == nil {
		t.Fatal("replacement replayed")
	}
}
func TestControlReplacementJournalUnknownOutcomesRequireReadOnlyReconciliation(t *testing.T) {
	events, base := replacementJournalFixture(t)
	for index := range controlReplacementSteps() {
		intent := replacementIntent(t, base, index)
		if e := validateImageEvent(events, intent); e != nil {
			t.Fatal(e)
		}
		events = append(events, intent)
		unknown := intent
		unknown.Outcome = "uncertain"
		unknown.Code = "OFFLINE_SCOPE_CONTROL_REPLACEMENT_EFFECT_UNRESOLVED"
		if e := validateImageEvent(events, unknown); e != nil {
			t.Fatal(e)
		}
		events = append(events, unknown)
		if e := validateImageEvent(events, replacementIntent(t, base, index)); e == nil {
			t.Fatal("unknown effect resent")
		}
		observed := replacementCompletion(events, intent)
		observed.OperationID, _ = nonce()
		observed.Action = "control_replacement_reconcile"
		observed.ReferenceOperationID = intent.OperationID
		if e := validateImageEvent(events, observed); e != nil {
			t.Fatal("observation", index, e)
		}
		events = append(events, observed)
	}
	if _, ok := controlReplacementProof(events); !ok {
		t.Fatal("missing reconciled proof")
	}
}
func TestControlReplacementJournalRejectsForgedAndMixedPayloads(t *testing.T) {
	initial, base := replacementJournalFixture(t)
	for _, fault := range []string{"TLS", "plan", "model", "runtime", "generation", "step", "phase", "mixed", "reference", "object", "effective", "operation"} {
		t.Run(fault, func(t *testing.T) {
			events := append([]Event(nil), initial...)
			intent := replacementIntent(t, base, 0)
			if e := validateImageEvent(events, intent); e != nil {
				t.Fatal(e)
			}
			events = append(events, intent)
			done := replacementCompletion(events, intent)
			r := done.ControlReplacement
			switch fault {
			case "TLS":
				r.TLSProofSHA256 = strings.Repeat("0", 64)
			case "plan":
				r.PlanSHA256 = strings.Repeat("0", 64)
			case "model":
				r.ModelSHA256 = strings.Repeat("0", 64)
			case "runtime":
				r.RuntimeSHA256 = strings.Repeat("0", 64)
			case "generation":
				r.GenerationID = strings.Repeat("0", 32)
			case "step":
				r.Step++
			case "phase":
				r.Phase = "foreign"
			case "mixed":
				done.TLSProjection = tlsProjectionState(events).record
			case "reference":
				done.ReferenceOperationID = intent.OperationID
			case "object":
				r.ObjectID = strings.Repeat("f", 64)
			case "effective":
				r.Effective = runtimeState(events).record
			case "operation":
				done.OperationID = strings.Repeat("0", 32)
			}
			if e := validateImageEvent(events, done); e == nil {
				t.Fatal("forged completion accepted")
			}
		})
	}
	events := initial
	for i := 0; i < len(controlReplacementSteps())-1; i++ {
		events = appendReplacementStep(t, events, base, i)
	}
	intent := replacementIntent(t, base, len(controlReplacementSteps())-1)
	events = append(events, intent)
	for _, fault := range []string{"old-control", "peer", "network", "descriptor", "model", "state"} {
		t.Run(fault, func(t *testing.T) {
			done := replacementCompletion(events, intent)
			r := done.ControlReplacement.Effective
			switch fault {
			case "old-control":
				r.Services["control"] = runtimeState(events).record.Services["control"]
			case "peer":
				service := r.Services["postgres"]
				service.ID = strings.Repeat("0", 64)
				r.Services["postgres"] = service
			case "network":
				r.NetworkID = strings.Repeat("0", 64)
			case "descriptor":
				r.DescriptorSHA256 = strings.Repeat("0", 64)
			case "model":
				r.ModelSHA256 = strings.Repeat("0", 64)
			case "state":
				service := r.Services["control"]
				service.State = "running"
				r.Services["control"] = service
			}
			if e := validateImageEvent(events, done); e == nil {
				t.Fatal("forged effective runtime")
			}
		})
	}
}
