package scope

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// These are journal/task-contract controls with explicit synthetic metadata,
// never native SQL, installed role, or complete product acceptance evidence.
func activationBindingFixture() mvpNativeRuntimeActivation {
	return mvpNativeRuntimeActivation{SQLProofSHA256: strings.Repeat("a", 64), DatabaseProofSHA256: strings.Repeat("b", 64), StoresProofSHA256: strings.Repeat("c", 64), ContextSHA256: strings.Repeat("d", 64), RuntimeInputSHA256: strings.Repeat("e", 64)}
}
func activationTaskEvents() ([]Event, mvpNativeRuntimeActivation) {
	n := activationBindingFixture()
	generation := strings.Repeat("1", 32)
	record := RestoredLiveDatabaseRecord{GenerationID: generation, PlanSHA256: strings.Repeat("f", 64), NativeRuntime: &n, Step: 5, ObjectID: strings.Repeat("2", 64)}
	create := Event{OperationID: strings.Repeat("3", 32), Action: "live_database_effect", Outcome: "completed", RestoredLiveDatabase: &record}
	started := record
	started.Step = 6
	started.ObjectID = ""
	start := Event{OperationID: strings.Repeat("4", 32), Action: "live_database_effect", Outcome: "started", RestoredLiveDatabase: &started}
	completed := started
	result := Event{OperationID: start.OperationID, Action: "live_database_effect", Outcome: "completed", RestoredLiveDatabase: &completed}
	return []Event{create, start, result}, n
}
func TestMVPNativeActivationKeepsLegacyAndOncePreparationStepContracts(t *testing.T) {
	first := activationBindingFixture()
	later := first
	later.PreparationGenerationID = strings.Repeat("1", 32)
	later.PreparationProofSHA256 = strings.Repeat("f", 64)
	if len(liveDatabaseSteps()) != 13 || len(liveDatabaseSteps(nil)) != 13 || len(liveDatabaseSteps(&first)) != 16 || len(liveDatabaseSteps(&later)) != 13 {
		t.Fatal("variant step counts")
	}
	if liveDatabaseSteps(&first)[6] != (restoreDatabaseStep{"task-execute", engine.DatabaseRuntimeRestorePrepare}) || !reflect.DeepEqual(liveDatabaseTaskKinds(&later), liveDatabaseTaskKinds(nil)) {
		t.Fatal("wrong fixed recipe")
	}
	legacy := RestoredLiveDatabasePlan{FormatVersion: 1}
	if bytes.Contains(encode(legacy), []byte("native_runtime")) || bytes.Contains(encode(RestoredLiveDatabaseRecord{}), []byte("native_runtime")) {
		t.Fatal("changed omitted legacy wire")
	}
}
func TestMVPNativePreparationUsesOriginalDurableTaskAndResult(t *testing.T) {
	events, n := activationTaskEvents()
	generation, proof, e := mvpNativePreparation(events, n)
	if e != nil || generation != events[0].RestoredLiveDatabase.GenerationID || proof != wire.Digest(encode(events)) {
		t.Fatal("original result binding", e)
	}
	reconciled := append([]Event{}, events...)
	reconciled[2].Action = "live_database_reconcile"
	reconciled[2].ReferenceOperationID = events[1].OperationID
	reconciled[2].OperationID = strings.Repeat("5", 32)
	_, next, e := mvpNativePreparation(reconciled, n)
	if e != nil || next == proof || next == "" {
		t.Fatal("observed original result", e)
	}
	later := n
	later.PreparationGenerationID = generation
	later.PreparationProofSHA256 = proof
	r := RestoredLiveDatabaseRecord{GenerationID: strings.Repeat("6", 32), NativeRuntime: &later}
	events = append(events, Event{Action: "live_database_effect", Outcome: "started", RestoredLiveDatabase: &r})
	g, p, e := mvpNativePreparation(events, n)
	if e != nil || g != generation || p != proof {
		t.Fatal("checks-only generation changed original proof", e)
	}
}
func TestMVPNativePreparationUnknownFailedAbortOrMissingActorNeverAuthorizesReplay(t *testing.T) {
	for _, fault := range []string{"unknown", "failed", "abort", "missing-create", "wrong-id", "wrong-reference", "different-generation", "input-changed", "second-start"} {
		t.Run(fault, func(t *testing.T) {
			events, n := activationTaskEvents()
			switch fault {
			case "unknown":
				events = events[:2]
			case "failed":
				events[2].Outcome = "failed"
			case "abort":
				events[2].Action = "live_database_abort"
			case "missing-create":
				events = events[1:]
			case "wrong-id":
				events[0].RestoredLiveDatabase.ObjectID = ""
			case "wrong-reference":
				events[2].Action = "live_database_reconcile"
				events[2].ReferenceOperationID = strings.Repeat("7", 32)
			case "different-generation":
				events[0].RestoredLiveDatabase.GenerationID = strings.Repeat("7", 32)
			case "input-changed":
				n.RuntimeInputSHA256 = strings.Repeat("7", 64)
			case "second-start":
				events = append(events, events[1])
			}
			if _, _, e := mvpNativePreparation(events, n); e == nil {
				t.Fatal("uncertain or forged proof admitted")
			}
		})
	}
	// Abort before the preparation start creates no role-effect authority and does
	// not prevent a later fresh generation from submitting its first preparation.
	events, n := activationTaskEvents()
	g, p, e := mvpNativePreparation(events[:1], n)
	if e != nil || g != "" || p != "" {
		t.Fatal("unsubmitted preparation", e)
	}
}
func TestMVPNativeRuntimeIdentityBindsActualCredentialBytesAndCurrentIndex(t *testing.T) {
	files := []engine.ConfigurationFile{{Name: "runtime.yaml", Contents: []byte(`{"database":{"dsn_file":"/run/projectone/operator/private/runtime.uri"},"security":{"auth_hmac_key_file":"/run/projectone/operator/private/hmac","mail_keyring_file":"/run/projectone/operator/private/mail"}}`)}, {Name: "runtime.uri", Contents: []byte("SYNTHETIC_PROTECTED_RUNTIME_CREDENTIAL")}}
	digest, e := mvpNativeRuntimeInputDigest(files)
	if e != nil || !wire.ValidSHA256(digest) {
		t.Fatal(e)
	}
	index := []InputFile{}
	for _, f := range files {
		index = append(index, InputFile{Name: f.Name, Bytes: int64(len(f.Contents)), SHA256: wire.Digest(f.Contents)})
	}
	historical, e := mvpNativeRuntimeInputDigestAtIndex(index, files)
	if e != nil || historical != digest {
		t.Fatal("historical index binding", e)
	}
	files[1].Contents = append(files[1].Contents, 'X')
	changed, e := mvpNativeRuntimeInputDigest(files)
	if e != nil || changed == digest {
		t.Fatal("credential mutation ignored", e)
	}
	if _, e = mvpNativeRuntimeInputDigest(append(files, files[1])); e == nil {
		t.Fatal("duplicate credential")
	}
	if _, e = mvpNativeRuntimeInputDigest(files[:1]); e == nil {
		t.Fatal("missing credential")
	}
	index[1].SHA256 = strings.Repeat("9", 64)
	changed, e = mvpNativeRuntimeInputDigestAtIndex(index, files)
	if e != nil || changed == digest {
		t.Fatal("historical credential identity ignored", e)
	}
}
func TestMVPNativeActivationRequiresOriginalReadonlyStageProofs(t *testing.T) {
	n := activationBindingFixture()
	if validMVPNativeActivation(nil, &n) {
		t.Fatal("DTO hashes established original SQL/store proofs")
	}
	if !validMVPNativeActivation(nil, nil) {
		t.Fatal("legacy admission changed")
	}
}
func TestMVPNativeAdmissionViewRetainsPriorPreparationHistory(t *testing.T) {
	events, _ := activationTaskEvents()
	current := RestoredLiveDatabaseRecord{GenerationID: strings.Repeat("8", 32)}
	events = append(events, Event{RestoredLiveDatabase: &current})
	l := locked{events: events}
	view := l.liveDatabaseAdmissionView(current.GenerationID)
	if len(view.events) != 3 || len(l.events) != 4 {
		t.Fatal("prior once history dropped or original rewritten")
	}
}
func TestMVPNativeOriginalPrepareTaskStartLossObservesWithoutResend(t *testing.T) {
	c, _, d, _ := liveDatabaseFixture(t)
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	// Reuse the actual fixed Engine task lifecycle with synthetic SDK effects. No
	// native stage, SQL success, grants or target qualification is manufactured.
	base := restoreDatabaseState(l.events).record.task(engine.DatabaseRuntimeRestorePrepare)
	base.Owner.OperationID = strings.Repeat("9", 32)
	base.VolumeOwner = engine.Ownership{ScopeID: base.Owner.ScopeID, OperationID: base.Owner.OperationID, Role: engine.StoreRestoreConfigRole + "-" + base.Owner.OperationID}
	object, e := d.CreateDatabaseTask(context.Background(), base)
	if e != nil {
		t.Fatal(e)
	}
	d.effects.fault = "start-after"
	if _, e = l.executeRestoreTask(context.Background(), d, base, "task-execute", object.ID, nil, false); e == nil {
		t.Fatal("response loss required")
	}
	if _, e = l.executeRestoreTask(context.Background(), d, base, "task-execute", object.ID, nil, true); e != nil || d.effects.starts[engine.DatabaseRuntimeRestorePrepare] != 1 {
		t.Fatal("original task resent", e)
	}
}

// The following controls exercise the real journal validator using the existing
// synthetic SDK fixture's prerequisites. They do not manufacture native SQL,
// grants, files, or a NativePostgreSQLRestoreReference.
func nativeLiveJournalFixture(t *testing.T) ([]Event, RestoredLiveDatabaseRecord) {
	t.Helper()
	c, _, _, _ := liveDatabaseFixture(t)
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	n := activationBindingFixture()
	n.SQLProofSHA256, _ = restoreSQLProof(l.events)
	n.DatabaseProofSHA256, _ = restoreDatabaseProof(l.events)
	n.StoresProofSHA256, _ = restoreStoresProof(l.events)
	if !validMVPNativeActivation(l.events, &n) {
		t.Fatal("synthetic prerequisite journal")
	}
	return append([]Event{}, l.events...), RestoredLiveDatabaseRecord{GenerationID: strings.Repeat("8", 32), PlanSHA256: strings.Repeat("a", 64), Admission: *dependencyAdmission(l.events), NativeRuntime: &n}
}
func appendNativeLiveJournalStep(t *testing.T, events []Event, r RestoredLiveDatabaseRecord, index int) []Event {
	t.Helper()
	r.Step = index
	r.ObjectID = ""
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	intent := Event{OperationID: id, Action: "live_database_effect", Outcome: "started", RestoredLiveDatabase: &r}
	if e = validateLiveDatabaseEvent(events, intent); e != nil {
		t.Fatal("intent", index, e)
	}
	events = append(events, intent)
	resultRecord := r
	step := liveDatabaseSteps(r.NativeRuntime)[index]
	if step.phase == "utility-create" || step.phase == "task-create" {
		resultRecord.ObjectID = strings.Repeat("6", 64)
	}
	result := Event{OperationID: id, Action: "live_database_effect", Outcome: "completed", RestoredLiveDatabase: &resultRecord}
	if e = validateLiveDatabaseEvent(events, result); e != nil {
		t.Fatal("result", index, e)
	}
	return append(events, result)
}
func TestMVPNativeJournalPreparationProofRequiresOriginalOrderAndSurvivesChecksOnlyGeneration(t *testing.T) {
	events, r := nativeLiveJournalFixture(t)
	for i := range liveDatabaseSteps(r.NativeRuntime) {
		events = appendNativeLiveJournalStep(t, events, r, i)
	}
	if _, ok := liveDatabaseProof(events); !ok || !liveDatabaseState(events).complete {
		t.Fatal("synthetic native16step journal incomplete")
	}
	generation, proof, e := mvpNativePreparation(events, *r.NativeRuntime)
	if e != nil || generation != r.GenerationID || proof == "" {
		t.Fatal("exact once task result", e)
	}
	next := r
	next.GenerationID = strings.Repeat("9", 32)
	binding := *r.NativeRuntime
	binding.PreparationGenerationID = generation
	binding.PreparationProofSHA256 = proof
	next.NativeRuntime = &binding
	id, _ := nonce()
	replay := next
	original := *r.NativeRuntime
	replay.NativeRuntime = &original
	if validateLiveDatabaseEvent(events, Event{OperationID: id, Action: "live_database_effect", Outcome: "started", RestoredLiveDatabase: &replay}) == nil {
		t.Fatal("second preparation authorized")
	}
	forged := binding
	forged.PreparationProofSHA256 = strings.Repeat("7", 64)
	replay.NativeRuntime = &forged
	if validateLiveDatabaseEvent(events, Event{OperationID: id, Action: "live_database_effect", Outcome: "started", RestoredLiveDatabase: &replay}) == nil {
		t.Fatal("forged once proof authorized")
	}
	for i := range liveDatabaseSteps(next.NativeRuntime) {
		events = appendNativeLiveJournalStep(t, events, next, i)
	}
	state := liveDatabaseState(events)
	if !state.complete || len(state.completed) != 13 || state.record.NativeRuntime.PreparationProofSHA256 != proof {
		t.Fatal("checks-only retained history")
	}
	g, p, e := mvpNativePreparation(events, binding)
	if e != nil || g != generation || p != proof {
		t.Fatal("reopened original history changed", e)
	}
}
func TestMVPNativeJournalUnknownPreparationAbortCannotCreateFreshRoleAuthority(t *testing.T) {
	events, r := nativeLiveJournalFixture(t)
	for i := 0; i < 6; i++ {
		events = appendNativeLiveJournalStep(t, events, r, i)
	}
	r.Step = 6
	id, _ := nonce()
	intent := Event{OperationID: id, Action: "live_database_effect", Outcome: "started", RestoredLiveDatabase: &r}
	if e := validateLiveDatabaseEvent(events, intent); e != nil {
		t.Fatal(e)
	}
	events = append(events, intent)
	abortID, _ := nonce()
	abort := Event{OperationID: abortID, ReferenceOperationID: id, Action: "live_database_abort", Outcome: "started", RestoredLiveDatabase: &r}
	if e := validateLiveDatabaseEvent(events, abort); e != nil {
		t.Fatal(e)
	}
	events = append(events, abort)
	abort.Outcome = "completed"
	if e := validateLiveDatabaseEvent(events, abort); e != nil {
		t.Fatal(e)
	}
	events = append(events, abort)
	if _, ok := liveDatabaseProof(events); ok {
		t.Fatal("unknown role effect produced live proof")
	}
	if _, _, e := mvpNativePreparation(events, *r.NativeRuntime); e == nil {
		t.Fatal("abort manufactured successful task result")
	}
	r.GenerationID = strings.Repeat("9", 32)
	r.Step = 0
	newID, _ := nonce()
	if validateLiveDatabaseEvent(events, Event{OperationID: newID, Action: "live_database_effect", Outcome: "started", RestoredLiveDatabase: &r}) == nil {
		t.Fatal("aborted uncertain preparation replayed")
	}
}
