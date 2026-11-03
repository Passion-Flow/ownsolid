package scope

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func restoreStoresPlanFixture(t *testing.T) (*locked, RestoreStorePlan) {
	l, p, _, _ := restoreStoresPlanRuntimeFixture(t)
	return l, p
}
func restoreStoresPlanRuntimeFixture(t *testing.T) (*locked, RestoreStorePlan, *databaseEngineDouble, *databaseWorkerDouble) {
	return restoreStoresPlanRuntimeWithInputFixture(t, nil)
}
func restoreStoresPlanRuntimeWithInputFixture(t *testing.T, mutate func(*preparedInputs), modelConfiguration ...func(*compose.Model)) (*locked, RestoreStorePlan, *databaseEngineDouble, *databaseWorkerDouble) {
	t.Helper()
	var configureModel func(*compose.Model)
	if len(modelConfiguration) > 0 {
		configureModel = modelConfiguration[0]
	}
	l, restore, sql, d, w := restoreSQLSnapshotRuntimeWithConfiguredInputsFixture(t, []string{"models", "datasets"}, mutate, configureModel)
	// Actual files/journal with explicitly synthetic source/process metadata.
	// The separate tagged test verifies the real original archive constructor.
	for i := range restoreSQLSteps() {
		events := appendSyntheticRestoreSQLStep(t, l.events, sql, i, l.marker.ScopeID)
		for _, e := range events[len(events)-2:] {
			if err := l.appendEvent(e); err != nil {
				l.close()
				t.Fatal(err)
			}
		}
	}
	database, _, e := l.makeRestoreDatabaseRecord(context.Background(), restore)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	for i := range restoreDatabaseSteps() {
		events := appendSyntheticRestoreDatabaseStep(t, l.events, database, i, l.marker.ScopeID)
		for _, e := range events[len(events)-2:] {
			if err := l.appendEvent(e); err != nil {
				l.close()
				t.Fatal(err)
			}
		}
	}
	proof, _ := restoreDatabaseProof(l.events)
	generation, e := nonce()
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	p := RestoreStorePlan{FormatVersion: 1, GenerationID: generation, RestoreAttemptID: restore.AttemptID, ScopeID: l.marker.ScopeID, RestorePlanSHA256: database.PlanSHA256, SQLProofSHA256: database.SQLProofSHA256, DatabaseProofSHA256: proof, DescriptorSHA256: restore.Target.DescriptorSHA256, RuntimeSHA256: restore.Target.RuntimeSHA256, PayloadSHA256: database.PayloadSHA256, ContextSHA256: database.ContextSHA256, Initializer: database.Initializer, Jobs: []RestoreStoreJob{}}
	for _, kind := range restoredStoreCatalog()[:2] {
		target := volumeState(l.events).roles[kind.role]
		profile := engine.StoreRestoreProfile{Artifact: engine.StoreRestoreArtifact{Role: kind.role, UID: target.UID, GID: target.GID, Bytes: 3072, SHA256: strings.Repeat("a", 64), MaximumEntries: restore.Request.MaximumEntries}, FilesSHA256: strings.Repeat("b", 64), DirectoriesSHA256: strings.Repeat("c", 64)}
		job, e := assembleRestoreStoreJob(database.BaseTask, l.volumeOwner(target), profile, generation, kind)
		if e != nil {
			l.close()
			t.Fatal(e)
		}
		p.Jobs = append(p.Jobs, job)
	}
	if e = l.validateRestoreStorePlanContext(context.Background(), p); e != nil {
		l.close()
		t.Fatal(e)
	}
	return l, p, d, w
}
func appendSyntheticRestoreStoresStep(t *testing.T, events []Event, base RestoreStoresRecord, index int, scope string) []Event {
	t.Helper()
	step := restoreStoresSteps(base.Stores)[index]
	r := base
	r.Step = index
	r.Phase = step.phase
	r.Role = step.role
	r.Kind = step.kind
	r.ObjectID = ""
	id, _ := nonce()
	start := Event{ScopeID: scope, OperationID: id, Action: "restore_stores_effect", Outcome: "started", RestoreStores: &r}
	if e := validateImageEvent(events, start); e != nil {
		t.Fatal("store intent", index, e)
	}
	events = append(events, start)
	done := r
	if r.Phase == "utility-create" || r.Phase == "task-create" {
		done.ObjectID = strings.Repeat("a", 64)
	}
	terminal := start
	terminal.Outcome = "completed"
	terminal.RestoreStores = &done
	if e := validateImageEvent(events, terminal); e != nil {
		t.Fatal("store observation", index, e)
	}
	return append(events, terminal)
}
func TestRestoreStoresJournalOrdersAllSelectedRolesAndKeepsActivationExcluded(t *testing.T) {
	l, p := restoreStoresPlanFixture(t)
	defer l.close()
	base := restoreStoresPlanRecord(p)
	events := append([]Event{}, l.events...)
	if len(restoreStoresSteps(base.Stores)) != 29 {
		t.Fatal("selected fixed effect count")
	}
	for i := range restoreStoresSteps(base.Stores) {
		if _, ok := restoreStoresProof(events); ok {
			t.Fatal("premature store proof")
		}
		events = appendSyntheticRestoreStoresStep(t, events, base, i, l.marker.ScopeID)
		if restoreMutationGuard(events, Event{Action: "process_start", Process: &ProcessRecord{}}) == nil {
			t.Fatal("store stage opened activation")
		}
	}
	if _, ok := restoreStoresProof(events); !ok {
		t.Fatal("complete separate store proof missing")
	}
	r := *restoreDatabaseState(events).record
	id, _ := nonce()
	if validateImageEvent(events, Event{ScopeID: l.marker.ScopeID, OperationID: id, Action: "restore_database_abort", Outcome: "started", RestoreDatabase: &r}) == nil {
		t.Fatal("earlier database abort accepted after stores")
	}
}
func TestRestoreStoresPlanPublicationReopenAndLostResultNeverOverwrites(t *testing.T) {
	l, p := restoreStoresPlanFixture(t)
	writes := 0
	publisher := func(plan RestoreStorePlan) error {
		writes++
		if e := writeBackupFile(l.root, restoreStoresPlanFile, encode(plan)); e != nil {
			return e
		}
		return errors.New("SYNTHETIC_COMPLETE_FILE_RESULT_LOST")
	}
	if e := l.publishRestoreStoresPlan(context.Background(), p, false, publisher); e == nil {
		l.close()
		t.Fatal("lost publication accepted")
	}
	if e := l.publishRestoreStoresPlan(context.Background(), p, false, publisher); e == nil || writes != 1 {
		l.close()
		t.Fatal("unknown publication resent")
	}
	c := l.config
	l.close()
	var e error
	l, e = open(c, true)
	if e != nil {
		t.Fatal("pending complete file reopen", e)
	}
	defer l.close()
	if e = l.publishRestoreStoresPlan(context.Background(), p, true, func(RestoreStorePlan) error { t.Fatal("reconcile overwrote complete plan"); return nil }); e != nil {
		t.Fatal(e)
	}
	before := wire.Digest(encode(l.events))
	if e = l.publishRestoreStoresPlan(context.Background(), p, false, publisher); e != nil || writes != 1 || before != wire.Digest(encode(l.events)) {
		t.Fatal("completed plan repeat changed state", e)
	}
}
func TestRestoreStoresPlanMissingReconstructionAndPartialPreservation(t *testing.T) {
	for _, fault := range []string{"missing", "partial"} {
		t.Run(fault, func(t *testing.T) {
			l, p := restoreStoresPlanFixture(t)
			writer := func(RestoreStorePlan) error {
				if fault == "partial" {
					if e := writeBackupFile(l.root, restoreStoresPlanFile, []byte("{")); e != nil {
						t.Fatal(e)
					}
				}
				return errors.New("SYNTHETIC_PLAN_PUBLICATION_LOST")
			}
			if e := l.publishRestoreStoresPlan(context.Background(), p, false, writer); e == nil {
				l.close()
				t.Fatal("unknown publication accepted")
			}
			c := l.config
			l.close()
			var e error
			l, e = open(c, true)
			if e != nil {
				t.Fatal("first unpublished plan should remain observable", e)
			}
			defer l.close()
			writes := 0
			writer = func(plan RestoreStorePlan) error {
				writes++
				return writeBackupFile(l.root, restoreStoresPlanFile, encode(plan))
			}
			e = l.publishRestoreStoresPlan(context.Background(), p, true, writer)
			if fault == "missing" {
				if e != nil || writes != 1 {
					t.Fatal("explicit missing reconstruction", e)
				}
			} else {
				if e == nil || writes != 0 {
					t.Fatal("partial file overwritten or accepted")
				}
				raw, e := regular(l.root, restoreStoresPlanFile, l.config.Verification.MaximumMetadataBytes)
				if e != nil || string(raw) != "{" {
					t.Fatal("partial not preserved")
				}
			}
		})
	}
}
func TestRestoreStoresPlanRejectsRecordedMutationAndUnrecordedFile(t *testing.T) {
	l, p := restoreStoresPlanFixture(t)
	if e := l.publishRestoreStoresPlan(context.Background(), p, false, func(plan RestoreStorePlan) error { return writeBackupFile(l.root, restoreStoresPlanFile, encode(plan)) }); e != nil {
		l.close()
		t.Fatal(e)
	}
	if e := l.root.Remove(restoreStoresPlanFile); e != nil {
		l.close()
		t.Fatal(e)
	}
	if e := writeBackupFile(l.root, restoreStoresPlanFile, []byte("{}")); e != nil {
		l.close()
		t.Fatal(e)
	}
	c := l.config
	l.close()
	if reopened, e := open(c, true); e == nil {
		reopened.close()
		t.Fatal("completed changed plan admitted")
	}
	l, p = restoreStoresPlanFixture(t)
	if e := writeBackupFile(l.root, restoreStoresPlanFile, encode(p)); e != nil {
		l.close()
		t.Fatal(e)
	}
	c = l.config
	l.close()
	if reopened, e := open(c, true); e == nil {
		reopened.close()
		t.Fatal("unrecorded store plan admitted")
	}
}

func TestRestoreStoresJournalRefusesChangedProofRoleAndUnknownReplay(t *testing.T) {
	l, p := restoreStoresPlanFixture(t)
	defer l.close()
	base := restoreStoresPlanRecord(p)
	events := append([]Event{}, l.events...)
	id, _ := nonce()
	start := Event{ScopeID: l.marker.ScopeID, OperationID: id, Action: "restore_stores_effect", Outcome: "started", RestoreStores: &base}
	if e := validateImageEvent(events, start); e != nil {
		t.Fatal(e)
	}
	events = append(events, start)
	unknown := start
	unknown.Outcome = "uncertain"
	unknown.Code = "OFFLINE_SCOPE_RESTORE_STORES_PLAN_UNRESOLVED"
	if e := validateImageEvent(events, unknown); e != nil {
		t.Fatal(e)
	}
	events = append(events, unknown)
	retry := start
	retry.OperationID, _ = nonce()
	if validateImageEvent(events, retry) == nil {
		t.Fatal("unknown publication resent")
	}
	observe := retry
	observe.Action = "restore_stores_reconcile"
	observe.ReferenceOperationID = id
	observe.Outcome = "completed"
	if e := validateImageEvent(events, observe); e != nil {
		t.Fatal("independent exact observation", e)
	}
	for _, fault := range []string{"generation", "plan", "SQL", "database", "main-plan", "duplicate-role", "unknown-role", "reordered-role", "skip-step", "object-intent", "mixed"} {
		t.Run(fault, func(t *testing.T) {
			changed := base
			changed.Stores = append([]string{}, base.Stores...)
			event := observe
			event.RestoreStores = &changed
			switch fault {
			case "generation":
				changed.GenerationID = strings.Repeat("f", 32)
			case "plan":
				changed.PlanSHA256 = strings.Repeat("f", 64)
			case "SQL":
				changed.SQLProofSHA256 = strings.Repeat("f", 64)
			case "database":
				changed.DatabaseProofSHA256 = strings.Repeat("f", 64)
			case "main-plan":
				changed.RestorePlanSHA256 = strings.Repeat("f", 64)
			case "duplicate-role":
				changed.Stores = []string{"models", "models"}
			case "unknown-role":
				changed.Stores = []string{"models", "outside"}
			case "reordered-role":
				changed.Stores = []string{"datasets", "models"}
			case "skip-step":
				changed.Step = 1
				changed.Phase = "volume-create"
				changed.Role = "configuration"
			case "object-intent":
				event = start
				event.RestoreStores = &changed
				changed.ObjectID = strings.Repeat("a", 64)
			case "mixed":
				event.RestoreDatabase = restoreDatabaseState(events).record
			}
			if validateImageEvent(events, event) == nil {
				t.Fatal("changed store context adopted")
			}
		})
	}
}
func TestRestoreStoresJournalFailureRequiresAbortWithoutActivation(t *testing.T) {
	l, p := restoreStoresPlanFixture(t)
	defer l.close()
	base := restoreStoresPlanRecord(p)
	events := append([]Event{}, l.events...)
	for i := 0; i < 10; i++ {
		events = appendSyntheticRestoreStoresStep(t, events, base, i, l.marker.ScopeID)
	}
	step := restoreStoresSteps(base.Stores)[10]
	if step.phase != "task-execute" {
		t.Fatal("fixed import execution index")
	}
	r := base
	r.Step = 10
	r.Phase = step.phase
	r.Role = step.role
	r.Kind = step.kind
	id, _ := nonce()
	start := Event{ScopeID: l.marker.ScopeID, OperationID: id, Action: "restore_stores_effect", Outcome: "started", RestoreStores: &r}
	if e := validateImageEvent(events, start); e != nil {
		t.Fatal(e)
	}
	events = append(events, start)
	failed := start
	failed.Outcome = "failed"
	failed.Code = "OFFLINE_SCOPE_RESTORE_STORES_TASK_FAILED"
	if e := validateImageEvent(events, failed); e != nil {
		t.Fatal(e)
	}
	events = append(events, failed)
	next := r
	next.Step++
	next.Phase = "task-remove"
	retry := start
	retry.OperationID, _ = nonce()
	retry.RestoreStores = &next
	if validateImageEvent(events, retry) == nil {
		t.Fatal("failed import advanced")
	}
	abort := start
	abort.OperationID, _ = nonce()
	abort.Action = "restore_stores_abort"
	if e := validateImageEvent(events, abort); e != nil {
		t.Fatal("cleanup intent", e)
	}
	events = append(events, abort)
	done := abort
	done.Outcome = "completed"
	if e := validateImageEvent(events, done); e != nil {
		t.Fatal("cleanup result", e)
	}
	events = append(events, done)
	if !restoreStoresState(events).aborted {
		t.Fatal("abort not retained")
	}
	if _, ok := restoreStoresProof(events); ok {
		t.Fatal("aborted stores qualified")
	}
	if validateImageEvent(events, start) == nil {
		t.Fatal("aborted store generation resumed")
	}
}
