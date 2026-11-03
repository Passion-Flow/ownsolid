package scope

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

func restoreSQLJournalFixture(t *testing.T) ([]Event, RestoreSQLRecord, string) {
	t.Helper()
	_, l, r, source, target := restoreReservationFixture(t)
	defer l.close()
	if _, e := l.reserveRestore(context.Background(), r, source, target, "prepare-restore", func(p RestorePlan) error { return writeBackupFile(l.root, restorePlanFile, encode(p)) }); e != nil {
		t.Fatal(e)
	}
	reservation := restoreState(l.events).record
	runtime := runtimeState(l.events).record
	owner := func(role string) engine.Ownership {
		return engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: reservation.AttemptID, Role: role + "-" + reservation.AttemptID}
	}
	peers := map[string]string{}
	for role, s := range runtime.Services {
		peers[role] = s.ID
	}
	task := engine.PostgresBackupTask{Owner: engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: reservation.AttemptID, Role: "database-" + engine.PostgresRestoreKind}, InputOwner: owner(engine.PostgresRestoreInputRole), OutputOwner: owner(engine.PostgresRestoreOutputRole), Image: "sha256:" + strings.Repeat("b", 64), UID: 999, GID: 999, NetworkID: runtime.NetworkID, Peers: peers, Host: "postgres", Port: 5432, Database: "synthetic_new_target", User: "synthetic_operator", Restore: &engine.PostgresRestoreProfile{SourceOwner: owner(engine.PostgresRestoreSourceRole), SourceUID: 999, SourceGID: 999, ArchiveSHA256: strings.Repeat("c", 64), ArchiveBytes: 20000, ExpectedSource: source.Source, ExpectedFiles: []engine.BackupArchiveFile{}}}
	for _, name := range []string{"database.dump", "contents.list", "source.json", "tools.txt"} {
		task.Restore.ExpectedFiles = append(task.Restore.ExpectedFiles, engine.BackupArchiveFile{Name: name, Bytes: 10, SHA256: strings.Repeat("d", 64)})
	}
	return append([]Event{}, l.events...), RestoreSQLRecord{PlanSHA256: reservation.PlanSHA256, Initializer: "sha256:" + strings.Repeat("e", 64), Task: task, Phase: "volume-create", Role: "input"}, l.marker.ScopeID
}

func appendSyntheticRestoreSQLStep(t *testing.T, events []Event, base RestoreSQLRecord, index int, scopeID string) []Event {
	t.Helper()
	step := restoreSQLSteps()[index]
	r := base
	r.Step = index
	r.Phase = step.phase
	r.Role = step.role
	r.ObjectID = ""
	r.ResultSHA256 = ""
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	start := Event{ScopeID: scopeID, OperationID: id, Action: "restore_sql_effect", Outcome: "started", RestoreSQL: &r}
	if e := validateImageEvent(events, start); e != nil {
		t.Fatal("fixed SQL step intent", index, e)
	}
	events = append(events, start)
	done := r
	if r.Phase == "utility-create" || r.Phase == "task-create" {
		done.ObjectID = fmt.Sprintf("%064x", index+1)
	}
	if r.Phase == "result-verify" || r.Phase == "complete" {
		done.ResultSHA256 = strings.Repeat("f", 64)
	}
	terminal := start
	terminal.Outcome = "completed"
	terminal.RestoreSQL = &done
	if e := validateImageEvent(events, terminal); e != nil {
		t.Fatal("fixed SQL step observation", index, e)
	}
	return append(events, terminal)
}

func TestRestoreSQLJournalOrdersCompleteProofAndKeepsActivationExcluded(t *testing.T) {
	// This is a state-machine test with named lineage doubles and synthetic
	// artifact metadata. It is not SQL/client execution or an actual restore.
	events, base, scopeID := restoreSQLJournalFixture(t)
	for index := range restoreSQLSteps() {
		if _, ok := restoreSQLProof(events); ok {
			t.Fatal("SQL proof before every required effect")
		}
		events = appendSyntheticRestoreSQLStep(t, events, base, index, scopeID)
		if e := restoreMutationGuard(events, Event{Action: "process_start", Process: &ProcessRecord{}}); e == nil {
			t.Fatal("SQL stage opened whole-application gate")
		}
	}
	if _, ok := restoreSQLProof(events); !ok {
		t.Fatal("complete fixed stage lacks its separate proof")
	}
	if bootstrapQualified(events) || schemaQualified(events) || runtimeAccessQualified(events) {
		t.Fatal("restore was forged into fresh qualification")
	}
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	reservation := restoreState(events).record
	if e := validateImageEvent(events, Event{OperationID: id, Action: "restore_abort_reservation", Outcome: "started", Restore: reservation}); e == nil {
		t.Fatal("pre-execution abort accepted after SQL work")
	}
}

func TestRestoreSQLJournalUnknownOutcomeRequiresObservationAndImmutableTask(t *testing.T) {
	events, base, scopeID := restoreSQLJournalFixture(t)
	id, _ := nonce()
	start := Event{ScopeID: scopeID, OperationID: id, Action: "restore_sql_effect", Outcome: "started", RestoreSQL: &base}
	if e := validateImageEvent(events, start); e != nil {
		t.Fatal(e)
	}
	events = append(events, start)
	if e := validateImageEvent(events, start); e == nil {
		t.Fatal("unknown effect replay accepted")
	}
	uncertain := start
	uncertain.Outcome = "uncertain"
	uncertain.Code = "OFFLINE_SCOPE_RESTORE_SQL_EFFECT_UNRESOLVED"
	if e := validateImageEvent(events, uncertain); e != nil {
		t.Fatal(e)
	}
	events = append(events, uncertain)
	observe := start
	observe.Action = "restore_sql_reconcile"
	observe.OperationID, _ = nonce()
	observe.ReferenceOperationID = id
	observe.Outcome = "completed"
	bad := base
	bad.Task.Database = "different_target"
	badEvent := observe
	badEvent.RestoreSQL = &bad
	if e := validateImageEvent(events, badEvent); e == nil {
		t.Fatal("different SQL target adopted")
	}
	if e := validateImageEvent(events, observe); e != nil {
		t.Fatal(e)
	}
	events = append(events, observe)
	skip := base
	skip.Step = 2
	skip.Phase = "payload-write"
	badEvent = start
	badEvent.OperationID, _ = nonce()
	badEvent.RestoreSQL = &skip
	if e := validateImageEvent(events, badEvent); e == nil {
		t.Fatal("SQL steps skipped")
	}
}

func TestRestoreSQLJournalFailedExecutionAndRecordedAbortNeverQualify(t *testing.T) {
	events, base, scopeID := restoreSQLJournalFixture(t)
	index := 0
	for restoreSQLSteps()[index].phase != "task-execute" {
		events = appendSyntheticRestoreSQLStep(t, events, base, index, scopeID)
		index++
	}
	r := base
	r.Step = index
	r.Phase = "task-execute"
	r.Role = "postgres"
	id, _ := nonce()
	start := Event{ScopeID: scopeID, OperationID: id, Action: "restore_sql_effect", Outcome: "started", RestoreSQL: &r}
	if e := validateImageEvent(events, start); e != nil {
		t.Fatal(e)
	}
	events = append(events, start)
	failed := start
	failed.Outcome = "failed"
	failed.Code = "OFFLINE_SCOPE_RESTORE_SQL_TASK_FAILED"
	if e := validateImageEvent(events, failed); e != nil {
		t.Fatal(e)
	}
	events = append(events, failed)
	if _, ok := restoreSQLProof(events); ok {
		t.Fatal("failed SQL task qualified")
	}
	abort := start
	abort.Action = "restore_sql_abort"
	abort.OperationID, _ = nonce()
	abort.Code = ""
	if e := validateImageEvent(events, abort); e != nil {
		t.Fatal(e)
	}
	events = append(events, abort)
	lost := abort
	lost.Outcome = "uncertain"
	lost.Code = "OFFLINE_SCOPE_RESTORE_SQL_CLEANUP_UNRESOLVED"
	if e := validateImageEvent(events, lost); e != nil {
		t.Fatal(e)
	}
	events = append(events, lost)
	done := abort
	done.Outcome = "completed"
	if e := validateImageEvent(events, done); e != nil {
		t.Fatal(e)
	}
	events = append(events, done)
	if !restoreSQLState(events).aborted {
		t.Fatal("cleanup abandonment not recorded")
	}
	if e := restoreMutationGuard(events, Event{Action: "process_start", Process: &ProcessRecord{}}); e == nil {
		t.Fatal("aborted SQL permitted activation")
	}
}
