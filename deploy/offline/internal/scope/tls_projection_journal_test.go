package scope

import (
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func tlsProjectionJournalFixture(t *testing.T) ([]Event, TLSProjectionRecord) {
	t.Helper()
	l, p := restoreStoresPlanFixture(t)
	defer l.close()
	events := append([]Event{}, l.events...)
	for i := range restoreStoresSteps(restoreStoresPlanRecord(p).Stores) {
		events = appendSyntheticRestoreStoresStep(t, events, restoreStoresPlanRecord(p), i, l.marker.ScopeID)
	}
	proof, _ := restoreStoresProof(events)
	input, _, _ := inputsState(events)
	runtime := runtimeState(events)
	return events, TLSProjectionRecord{GenerationID: strings.Repeat("d", 32), PlanSHA256: strings.Repeat("c", 64), StoreProofSHA256: proof, DescriptorSHA256: input.DescriptorSHA256, RuntimeSHA256: wire.Digest(encode(*runtime.record)), Phase: tlsProjectionSteps()[0]}
}
func tlsProjectionIntent(t *testing.T, events []Event, base TLSProjectionRecord, index int) Event {
	t.Helper()
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	base.Step = index
	base.Phase = tlsProjectionSteps()[index]
	base.ObjectID = ""
	return Event{OperationID: id, Action: "tls_projection_effect", Outcome: "started", TLSProjection: &base}
}
func tlsProjectionComplete(t *testing.T, events []Event, intent Event) []Event {
	t.Helper()
	if e := validateImageEvent(events, intent); e != nil {
		t.Fatal("TLS projection intent", e)
	}
	events = append(events, intent)
	done := intent
	r := *intent.TLSProjection
	done.TLSProjection = &r
	done.Outcome = "completed"
	if r.Phase == "utility-create" {
		r.ObjectID = strings.Repeat("a", 64)
	}
	if e := validateImageEvent(events, done); e != nil {
		t.Fatal("TLS projection result", e)
	}
	return append(events, done)
}
func TestTLSProjectionJournalOrdersPreparationAndKeepsApplicationExcluded(t *testing.T) {
	events, base := tlsProjectionJournalFixture(t)
	for i := range tlsProjectionSteps() {
		if _, ok := tlsProjectionProof(events); ok {
			t.Fatal("premature proof")
		}
		events = tlsProjectionComplete(t, events, tlsProjectionIntent(t, events, base, i))
		if restoreMutationGuard(events, Event{Action: "process_start", Process: &ProcessRecord{}}) == nil {
			t.Fatal("TLS projection opened serving")
		}
	}
	if proof, ok := tlsProjectionProof(events); !ok || !wire.ValidSHA256(proof) {
		t.Fatal("missing separate projection proof")
	}
	if validateImageEvent(events, tlsProjectionIntent(t, events, base, 0)) == nil {
		t.Fatal("completed preparation replay admitted")
	}
	old := Event{Action: "restore_stores_abort", RestoreStores: restoreStoresState(events).record}
	if validateImageEvent(events, old) == nil {
		t.Fatal("earlier stage abort admitted after TLS generation")
	}
}
func TestTLSProjectionJournalRequiresExplicitUnknownOutcomeReconciliation(t *testing.T) {
	events, base := tlsProjectionJournalFixture(t)
	intent := tlsProjectionIntent(t, events, base, 0)
	if e := validateImageEvent(events, intent); e != nil {
		t.Fatal(e)
	}
	events = append(events, intent)
	unknown := intent
	unknown.Outcome = "uncertain"
	unknown.Code = "OFFLINE_SCOPE_TLS_PROJECTION_PLAN_UNRESOLVED"
	if e := validateImageEvent(events, unknown); e != nil {
		t.Fatal(e)
	}
	events = append(events, unknown)
	if validateImageEvent(events, tlsProjectionIntent(t, events, base, 0)) == nil {
		t.Fatal("uncertain publication resubmitted")
	}
	id, _ := nonce()
	observe := intent
	observe.OperationID = id
	observe.ReferenceOperationID = intent.OperationID
	observe.Action = "tls_projection_reconcile"
	observe.Outcome = "unresolved"
	observe.Code = "OFFLINE_SCOPE_TLS_PROJECTION_PLAN_UNRESOLVED"
	if e := validateImageEvent(events, observe); e != nil {
		t.Fatal(e)
	}
	events = append(events, observe)
	id, _ = nonce()
	observe.OperationID = id
	observe.Outcome = "completed"
	observe.Code = ""
	if e := validateImageEvent(events, observe); e != nil {
		t.Fatal(e)
	}
	events = append(events, observe)
	if tlsProjectionState(events).pending != "" || len(tlsProjectionState(events).completed) != 1 {
		t.Fatal("readback did not resolve exactly one effect")
	}
}
func TestTLSProjectionJournalRefusesChangedContextAndMixedPayloads(t *testing.T) {
	events, base := tlsProjectionJournalFixture(t)
	for _, fault := range []string{"generation", "old-restore", "old-store", "plan", "store-proof", "descriptor", "runtime", "step", "phase", "object", "code", "reference", "mixed", "action", "reused-operation"} {
		t.Run(fault, func(t *testing.T) {
			intent := tlsProjectionIntent(t, events, base, 0)
			r := *intent.TLSProjection
			intent.TLSProjection = &r
			switch fault {
			case "generation":
				r.GenerationID = "bad"
			case "old-restore":
				r.GenerationID = restoreState(events).record.AttemptID
			case "old-store":
				r.GenerationID = restoreStoresState(events).record.GenerationID
			case "plan":
				r.PlanSHA256 = "bad"
			case "store-proof":
				r.StoreProofSHA256 = strings.Repeat("e", 64)
			case "descriptor":
				r.DescriptorSHA256 = strings.Repeat("e", 64)
			case "runtime":
				r.RuntimeSHA256 = strings.Repeat("e", 64)
			case "step":
				r.Step = 1
			case "phase":
				r.Phase = "arbitrary-copy"
			case "object":
				r.ObjectID = strings.Repeat("a", 64)
			case "code":
				intent.Code = "OFFLINE_SCOPE_TEST"
			case "reference":
				intent.ReferenceOperationID = strings.Repeat("a", 32)
			case "mixed":
				intent.Runtime = runtimeState(events).record
			case "action":
				intent.Action = "process_start"
			case "reused-operation":
				intent.OperationID = events[0].OperationID
			}
			if validateImageEvent(events, intent) == nil {
				t.Fatal("unsafe TLS transition admitted")
			}
		})
	}
}
func TestTLSProjectionAbortPreservesLineageAndRequiresCleanupCompletion(t *testing.T) {
	events, base := tlsProjectionJournalFixture(t)
	for i := range tlsProjectionSteps() {
		events = tlsProjectionComplete(t, events, tlsProjectionIntent(t, events, base, i))
	}
	r := *tlsProjectionState(events).record
	r.ObjectID = ""
	id, _ := nonce()
	abort := Event{OperationID: id, Action: "tls_projection_abort", Outcome: "started", TLSProjection: &r}
	if e := validateImageEvent(events, abort); e != nil {
		t.Fatal(e)
	}
	events = append(events, abort)
	if _, ok := tlsProjectionProof(events); ok {
		t.Fatal("cleanup intent retained active projection proof")
	}
	unknown := abort
	unknown.Outcome = "uncertain"
	unknown.Code = "OFFLINE_SCOPE_TLS_PROJECTION_CLEANUP_UNRESOLVED"
	if e := validateImageEvent(events, unknown); e != nil {
		t.Fatal(e)
	}
	events = append(events, unknown)
	done := abort
	done.Outcome = "completed"
	if e := validateImageEvent(events, done); e != nil {
		t.Fatal(e)
	}
	events = append(events, done)
	if s := tlsProjectionState(events); !s.aborted || s.pending != "" {
		t.Fatal("abort not terminal")
	}
	if _, ok := restoreStoresProof(events); !ok {
		t.Fatal("new configuration cleanup erased restored stores proof")
	}
	if validateImageEvent(events, tlsProjectionIntent(t, events, base, 0)) == nil {
		t.Fatal("aborted generation reused")
	}
}
