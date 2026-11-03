package scope

import (
	"context"
	"maps"
	"strings"
	"testing"
)

func appendSyntheticRestoreDatabaseStep(t *testing.T, events []Event, base RestoreDatabaseRecord, index int, scopeID string) []Event {
	t.Helper()
	step := restoreDatabaseSteps()[index]
	r := base
	r.Step = index
	r.Phase = step.phase
	r.Kind = step.kind
	r.ObjectID = ""
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	start := Event{ScopeID: scopeID, OperationID: id, Action: "restore_database_effect", Outcome: "started", RestoreDatabase: &r}
	if e := validateImageEvent(events, start); e != nil {
		t.Fatal("fixed restored DB intent", index, e)
	}
	events = append(events, start)
	done := r
	if r.Phase == "utility-create" || r.Phase == "task-create" {
		done.ObjectID = strings.Repeat("a", 64)
	}
	terminal := start
	terminal.Outcome = "completed"
	terminal.RestoreDatabase = &done
	if e := validateImageEvent(events, terminal); e != nil {
		t.Fatal("fixed restored DB observation", index, e)
	}
	return append(events, terminal)
}

func TestRestoreDatabaseJournalPersistsExactOrderProofAndClosedActivation(t *testing.T) {
	// Named Engine/Compose doubles supply lineage; protected files/journal are
	// real. These synthetic process observations do not execute PostgreSQL.
	l, plan := completedRestoreSQLSnapshot(t)
	base, _, e := l.makeRestoreDatabaseRecord(context.Background(), plan)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	for i := range restoreDatabaseSteps() {
		if _, ok := restoreDatabaseProof(l.events); ok {
			l.close()
			t.Fatal("premature database proof")
		}
		events := appendSyntheticRestoreDatabaseStep(t, l.events, base, i, l.marker.ScopeID)
		for _, event := range events[len(events)-2:] {
			if e = l.appendEvent(event); e != nil {
				l.close()
				t.Fatal(e)
			}
		}
		if restoreMutationGuard(l.events, Event{Action: "process_start", Process: &ProcessRecord{}}) == nil {
			l.close()
			t.Fatal("DB-only qualification opened activation")
		}
	}
	proof, ok := restoreDatabaseProof(l.events)
	if !ok || proof == "" {
		l.close()
		t.Fatal("complete qualification lacks proof")
	}
	if bootstrapQualified(l.events) || schemaQualified(l.events) || runtimeAccessQualified(l.events) {
		l.close()
		t.Fatal("restore became a fresh-install proof")
	}
	c := l.config
	l.close()
	reopened, e := open(c, true)
	if e != nil {
		t.Fatal("durable reopen", e)
	}
	defer reopened.close()
	if again, ok := restoreDatabaseProof(reopened.events); !ok || again != proof {
		t.Fatal("reopened proof changed")
	}
	record := *restoreDatabaseState(reopened.events).record
	id, _ := nonce()
	if validateImageEvent(reopened.events, Event{ScopeID: reopened.marker.ScopeID, OperationID: id, Action: "restore_database_effect", Outcome: "started", RestoreDatabase: &record}) == nil {
		t.Fatal("completed qualification replay accepted")
	}
	sql := *restoreSQLState(reopened.events).record
	if validateImageEvent(reopened.events, Event{ScopeID: reopened.marker.ScopeID, OperationID: id, Action: "restore_sql_abort", Outcome: "started", RestoreSQL: &sql}) == nil {
		t.Fatal("prior SQL abort accepted after database work")
	}
}

func TestRestoreDatabaseJournalUnknownFailureAbortAndContextChanges(t *testing.T) {
	l, plan := completedRestoreSQLSnapshot(t)
	defer l.close()
	base, _, e := l.makeRestoreDatabaseRecord(context.Background(), plan)
	if e != nil {
		t.Fatal(e)
	}
	events := append([]Event{}, l.events...)
	id, _ := nonce()
	r := base
	start := Event{ScopeID: l.marker.ScopeID, OperationID: id, Action: "restore_database_effect", Outcome: "started", RestoreDatabase: &r}
	if e := validateImageEvent(events, start); e != nil {
		t.Fatal(e)
	}
	events = append(events, start)
	unknown := start
	unknown.Outcome = "uncertain"
	unknown.Code = "OFFLINE_SCOPE_RESTORE_DATABASE_EFFECT_UNRESOLVED"
	if e := validateImageEvent(events, unknown); e != nil {
		t.Fatal(e)
	}
	events = append(events, unknown)
	retry := start
	retry.OperationID, _ = nonce()
	if validateImageEvent(events, retry) == nil {
		t.Fatal("unknown effect replay accepted")
	}
	observe := retry
	observe.Action = "restore_database_reconcile"
	observe.ReferenceOperationID = id
	observe.Outcome = "completed"
	if e := validateImageEvent(events, observe); e != nil {
		t.Fatal("independent observed resolution", e)
	}
	for _, fault := range []string{"SQL-proof", "payload", "context", "image", "peer", "reason", "config-operation", "skip-step", "object-before-effect", "mixed"} {
		t.Run(fault, func(t *testing.T) {
			changed := base
			changed.BaseTask.Peers = maps.Clone(base.BaseTask.Peers)
			v := observe
			v.RestoreDatabase = &changed
			switch fault {
			case "SQL-proof":
				changed.SQLProofSHA256 = strings.Repeat("e", 64)
			case "payload":
				changed.PayloadSHA256 = strings.Repeat("e", 64)
			case "context":
				changed.ContextSHA256 = strings.Repeat("e", 64)
			case "image":
				changed.BaseTask.Image = "sha256:" + strings.Repeat("e", 64)
			case "peer":
				changed.BaseTask.Peers["control"] = strings.Repeat("e", 64)
			case "reason":
				changed.BaseTask.Reason = "different reason"
			case "config-operation":
				changed.BaseTask.VolumeOwner.OperationID = strings.Repeat("e", 32)
			case "skip-step":
				changed.Step = 1
				changed.Phase = "utility-create"
			case "object-before-effect":
				v = start
				v.RestoreDatabase = &changed
				changed.ObjectID = strings.Repeat("e", 64)
			case "mixed":
				sql := *restoreSQLState(events).record
				v.RestoreSQL = &sql
			}
			if validateImageEvent(events, v) == nil {
				t.Fatal("changed restore operation adopted")
			}
		})
	}
	events = append([]Event{}, l.events...)
	for i := 0; i < 5; i++ {
		events = appendSyntheticRestoreDatabaseStep(t, events, base, i, l.marker.ScopeID)
	}
	r = base
	r.Step = 5
	r.Phase = "task-execute"
	r.Kind = restoreDatabaseSteps()[5].kind
	id, _ = nonce()
	start = Event{ScopeID: l.marker.ScopeID, OperationID: id, Action: "restore_database_effect", Outcome: "started", RestoreDatabase: &r}
	if e = validateImageEvent(events, start); e != nil {
		t.Fatal(e)
	}
	events = append(events, start)
	failed := start
	failed.Outcome = "failed"
	failed.Code = "OFFLINE_SCOPE_RESTORE_DATABASE_TASK_FAILED"
	if e = validateImageEvent(events, failed); e != nil {
		t.Fatal(e)
	}
	events = append(events, failed)
	next := r
	next.Step = 6
	next.Phase = "task-remove"
	retry = start
	retry.OperationID, _ = nonce()
	retry.RestoreDatabase = &next
	if validateImageEvent(events, retry) == nil {
		t.Fatal("failed task advanced without abort")
	}
	abort := start
	abort.OperationID, _ = nonce()
	abort.Action = "restore_database_abort"
	if e = validateImageEvent(events, abort); e != nil {
		t.Fatal("failed target cleanup intent", e)
	}
	events = append(events, abort)
	done := abort
	done.Outcome = "completed"
	if e = validateImageEvent(events, done); e != nil {
		t.Fatal(e)
	}
	events = append(events, done)
	if !restoreDatabaseState(events).aborted {
		t.Fatal("abort not retained")
	}
	if _, ok := restoreDatabaseProof(events); ok {
		t.Fatal("aborted target qualified")
	}
	if validateImageEvent(events, start) == nil {
		t.Fatal("aborted target resumed")
	}
}

func TestRecordedRestoreDatabaseBindsCurrentPrivateInputAndExactPeers(t *testing.T) {
	l, plan := completedRestoreSQLSnapshot(t)
	defer l.close()
	base, _, e := l.makeRestoreDatabaseRecord(context.Background(), plan)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := nonce()
	if e = l.appendEvent(Event{OperationID: id, Action: "restore_database_effect", Outcome: "started", RestoreDatabase: &base}); e != nil {
		t.Fatal(e)
	}
	if e = l.verifyRecordedRestoreDatabase(); e != nil {
		t.Fatal("positive immutable database qualification context", e)
	}
	for _, fault := range []string{"payload", "context", "SQL-proof", "initializer", "image", "user", "network", "peer", "operation", "reason"} {
		t.Run(fault, func(t *testing.T) {
			changed := base
			changed.BaseTask.Peers = maps.Clone(base.BaseTask.Peers)
			switch fault {
			case "payload":
				changed.PayloadSHA256 = strings.Repeat("e", 64)
			case "context":
				changed.ContextSHA256 = strings.Repeat("e", 64)
			case "SQL-proof":
				changed.SQLProofSHA256 = strings.Repeat("e", 64)
			case "initializer":
				changed.Initializer = "sha256:" + strings.Repeat("e", 64)
			case "image":
				changed.BaseTask.Image = "sha256:" + strings.Repeat("e", 64)
			case "user":
				changed.BaseTask.UID++
			case "network":
				changed.BaseTask.NetworkID = strings.Repeat("e", 64)
			case "peer":
				changed.BaseTask.Peers["control"] = strings.Repeat("e", 64)
			case "operation":
				changed.BaseTask.Owner.OperationID = strings.Repeat("e", 32)
			case "reason":
				changed.BaseTask.Reason = "changed audit reason"
			}
			old := l.events[len(l.events)-1].RestoreDatabase
			l.events[len(l.events)-1].RestoreDatabase = &changed
			if e = l.verifyRecordedRestoreDatabase(); e == nil {
				t.Fatal("changed qualification context accepted")
			}
			l.events[len(l.events)-1].RestoreDatabase = old
		})
	}
}
