package scope

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestMVPNativeRestoreSourceDispatchRefusesPartialNativeWithoutLegacyFallback(t *testing.T) {
	c := fixture(t)
	dir := filepath.Join(filepath.Dir(c.ScopeRoot), "partial-native-source")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(dir, mvpNativeDirectory), 0700); e != nil {
		t.Fatal(e)
	}
	// This is deliberately not a complete native source. Its marker must
	// select the native reader and preserve failure, rather than an old plan.
	if e := os.WriteFile(filepath.Join(dir, backupPlanFile), []byte(`{"native_request":null}`), 0600); e != nil {
		t.Fatal(e)
	}
	r := configuration.RestoreSourceDefaults()
	r.BackupRoot, r.ManifestSHA256 = dir, wire.Digest([]byte("synthetic missing original manifest"))
	out, e := InspectRestoreSource(context.Background(), c, r)
	if e == nil || e.Error() != "OFFLINE_SCOPE_NATIVE_SOURCE_BINDING_INVALID" || out.Status != "" || out.NativePostgreSQL != nil {
		t.Fatal("partial native source fell through or acquired completion", e)
	}
}

func TestMVPNativeRestoreSelectedReservationNeverEntersLegacyEffects(t *testing.T) {
	// Real scope/reservation files and journal, with the original named Engine
	// and Compose doubles. Deliberately legacy-shaped schema29 metadata must
	// refuse before even consulting an effect client. No native SQL is run.
	c, l, request, source, target := restoreReservationFixture(t)
	source.Source.SchemaVersion = wire.MVPPostgreSQLCPUV1SchemaVersion
	if _, e := l.reserveRestore(context.Background(), request, source, target, "prepare-restore", func(p RestorePlan) error { return writeBackupFile(l.root, restorePlanFile, encode(p)) }); e != nil {
		l.close()
		t.Fatal(e)
	}
	plan, e := l.readBoundRestorePlan()
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	if _, e = l.makeRestoreQualificationInput(context.Background(), plan); e == nil || e.Error() != "OFFLINE_SCOPE_MVP_NATIVE_RESTORE_REQUIRED" {
		l.close()
		t.Fatal("native context fell through to legacy SQL context", e)
	}
	l.close()
	before, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if e != nil {
		t.Fatal(e)
	}
	// Nil effect clients would panic if any old import, migration, runtime
	// preparation, invalidation, store operation or cleanup were consulted.
	for _, mode := range []string{"restore-sql", "reconcile-restore-sql", "abort-restore-sql"} {
		out, e := runRestoreSQL(context.Background(), c, request, mode, nil, nil)
		if e == nil || e.Error() != "OFFLINE_SCOPE_MVP_NATIVE_RESTORE_REQUIRED" || out.CompletedSteps != 0 || out.NativePostgreSQL != nil {
			t.Fatal("legacy SQL dispatch consulted an effect or claimed completion", mode, e)
		}
	}
	for _, mode := range []string{"qualify-restored-database", "reconcile-restored-database", "abort-restored-database"} {
		out, e := runRestoreDatabase(context.Background(), c, request, mode, nil, nil)
		if e == nil || e.Error() != "OFFLINE_SCOPE_MVP_NATIVE_RESTORE_REQUIRED" || out.CompletedSteps != 0 || out.NativePostgreSQL != nil {
			t.Fatal("legacy database dispatch consulted an effect or claimed completion", mode, e)
		}
	}
	for _, mode := range []string{"restore-stores", "reconcile-restored-stores", "abort-restored-stores"} {
		out, e := runRestoreStores(context.Background(), c, request, mode, nil, nil)
		if e == nil || e.Error() != "OFFLINE_SCOPE_MVP_NATIVE_RESTORE_REQUIRED" || out.CompletedSteps != 0 || out.NativePostgreSQL != nil {
			t.Fatal("legacy store dispatch consulted an effect or claimed completion", mode, e)
		}
	}
	after, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("native refusal changed original reservation/journal", e)
	}
}

func TestMVPNativeRestoreSchema29CannotAcquireLegacySQLJournalProof(t *testing.T) {
	events, record, scopeID := restoreSQLJournalFixture(t)
	if !validRestoreSQLRecord(&record, restoreState(events).record, scopeID) {
		t.Fatal("original legacy record fixture refused")
	}
	record.Task.Restore.ExpectedSource.SchemaVersion = wire.MVPPostgreSQLCPUV1SchemaVersion
	if validRestoreSQLRecord(&record, restoreState(events).record, scopeID) {
		t.Fatal("native schema acquired a legacy twenty-step SQL record")
	}
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	if e = validateImageEvent(events, Event{ScopeID: scopeID, OperationID: id, Action: "restore_sql_effect", Outcome: "started", RestoreSQL: &record}); e == nil {
		t.Fatal("legacy SQL effect intent admitted for native schema")
	}
	if _, complete := restoreSQLProof(events); complete {
		t.Fatal("refused native journal intent became complete proof")
	}
}

// The tests below are synthetic protocol state-machine checks. Existing
// doubles supply reservation/task identities, and hashes stand for private
// receipts. No SQL, native process, store import, original output, or target
// qualification is performed or claimed by these tests.
func syntheticMVPNativeSQLJournalFixture(t *testing.T) ([]Event, RestoreSQLRecord, RestoreSQLRecord, string) {
	t.Helper()
	events, legacy, scopeID := restoreSQLJournalFixture(t)
	base := legacy
	base.Native = &MVPNativeRestoreStageRecord{FormatVersion: 1, Stage: mvpNativeRestoreSQLStage, ExecutionSHA256: strings.Repeat("a", 64), OriginalRestoreID: restoreState(events).record.AttemptID}
	base.Task = engine.PostgresBackupTask{}
	base.Step, base.Phase, base.Role = 0, "native-prepare", ""
	base.ObjectID, base.ResultSHA256 = "", ""
	return events, base, legacy, scopeID
}

func syntheticNativeJournalReceipt(stage string, step int) string {
	return wire.Digest([]byte(fmt.Sprintf("synthetic protocol receipt %s %d", stage, step)))
}

func appendSyntheticMVPNativeSQLJournalStep(t *testing.T, events []Event, base RestoreSQLRecord, index int, scopeID string) []Event {
	t.Helper()
	step := restoreSQLStepsForRecord(&base)[index]
	r := base
	r.Step, r.Phase, r.Role = index, step.phase, step.role
	r.ObjectID, r.ResultSHA256 = "", ""
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	start := Event{ScopeID: scopeID, OperationID: id, Action: "restore_sql_effect", Outcome: "started", RestoreSQL: &r}
	if e := validateImageEvent(events, start); e != nil {
		t.Fatal("synthetic native SQL intent", index, e)
	}
	events = append(events, start)
	done := r
	switch r.Phase {
	case "task-create":
		done.ObjectID = fmt.Sprintf("%064x", index+300)
	case "native-prepare", "task-execute":
		done.ResultSHA256 = syntheticNativeJournalReceipt("sql", index)
	case "complete":
		done.ResultSHA256 = syntheticNativeJournalReceipt("sql", 5)
	}
	terminal := start
	terminal.Outcome, terminal.RestoreSQL = "completed", &done
	if e := validateImageEvent(events, terminal); e != nil {
		t.Fatal("synthetic native SQL observation", index, e)
	}
	return append(events, terminal)
}

func TestMVPNativeRestoreJournalSQLRequiresEightOwnSteps(t *testing.T) {
	events, base, _, scopeID := syntheticMVPNativeSQLJournalFixture(t)
	if len(restoreSQLStepsForRecord(&base)) != 8 || len(restoreSQLSteps()) != 20 {
		t.Fatal("distinct fixed catalogs changed")
	}
	for i := range restoreSQLStepsForRecord(&base) {
		if _, ok := restoreSQLProof(events); ok {
			t.Fatal("premature native proof", i)
		}
		events = appendSyntheticMVPNativeSQLJournalStep(t, events, base, i, scopeID)
		if len(restoreSQLState(events).completed) != i+1 {
			t.Fatal("native completed-step count", i)
		}
	}
	if proof, ok := restoreSQLProof(events); !ok || !wire.ValidSHA256(proof) {
		t.Fatal("missing complete native eight-step protocol proof")
	}
	if bootstrapQualified(events) || schemaQualified(events) || runtimeAccessQualified(events) {
		t.Fatal("native protocol proof forged fresh-install qualification")
	}
	if restoreMutationGuard(events, Event{Action: "process_start", Process: &ProcessRecord{}}) == nil {
		t.Fatal("native SQL-only proof opened whole-application activation")
	}
}

func TestMVPNativeRestoreJournalSQLRejectsLegacyTwentyStepCredit(t *testing.T) {
	events, native, legacy, scopeID := syntheticMVPNativeSQLJournalFixture(t)
	for i, step := range restoreSQLSteps() {
		wrong := native
		wrong.Step, wrong.Phase, wrong.Role = i, step.phase, step.role
		if validRestoreSQLRecord(&wrong, restoreState(events).record, scopeID) {
			t.Fatal("legacy step admitted into native catalog", i)
		}
		events = appendSyntheticRestoreSQLStep(t, events, legacy, i, scopeID)
	}
	if _, ok := restoreSQLProof(events); !ok {
		t.Fatal("legacy protocol was changed by native support")
	}
	// Deliberately invalid relabelling must not acquire native credit even
	// before a caller performs complete journal validation.
	relabelled := append([]Event(nil), events...)
	for i, event := range relabelled {
		if event.RestoreSQL == nil {
			continue
		}
		r := *event.RestoreSQL
		r.Native, r.Task = native.Native, engine.PostgresBackupTask{}
		relabelled[i].RestoreSQL = &r
	}
	if _, ok := restoreSQLProof(relabelled); ok {
		t.Fatal("old twenty-step history credited as native completion")
	}
}

func TestMVPNativeRestoreJournalSQLProofBindsPreparationReceipt(t *testing.T) {
	events, base, _, scopeID := syntheticMVPNativeSQLJournalFixture(t)
	for i := range restoreSQLStepsForRecord(&base) {
		events = appendSyntheticMVPNativeSQLJournalStep(t, events, base, i, scopeID)
	}
	before, ok := restoreSQLProof(events)
	if !ok {
		t.Fatal("missing original protocol proof")
	}
	changed := append([]Event(nil), events...)
	for i, event := range changed {
		if event.RestoreSQL == nil || event.RestoreSQL.Step != 0 || event.Outcome != "completed" {
			continue
		}
		r := *event.RestoreSQL
		r.ResultSHA256 = syntheticNativeJournalReceipt("different-private-preparation", 0)
		changed[i].RestoreSQL = &r
		if e := validateImageEvent(changed[:i], changed[i]); e != nil {
			t.Fatal("alternate synthetic receipt is not protocol-valid", e)
		}
	}
	after, ok := restoreSQLProof(changed)
	if !ok || before == after {
		t.Fatal("native proof omitted the step-zero final-execution receipt")
	}
}

func TestMVPNativeRestoreJournalSQLUnknownStartNeverGrantsProofOrReplay(t *testing.T) {
	events, base, _, scopeID := syntheticMVPNativeSQLJournalFixture(t)
	for i := 0; i < 2; i++ {
		events = appendSyntheticMVPNativeSQLJournalStep(t, events, base, i, scopeID)
	}
	r := base
	step := restoreSQLStepsForRecord(&base)[2]
	r.Step, r.Phase, r.Role = 2, step.phase, step.role
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	start := Event{ScopeID: scopeID, OperationID: id, Action: "restore_sql_effect", Outcome: "started", RestoreSQL: &r}
	if e := validateImageEvent(events, start); e != nil {
		t.Fatal(e)
	}
	events = append(events, start)
	if _, ok := restoreSQLProof(events); ok {
		t.Fatal("started native writer granted proof")
	}
	unknown := start
	unknown.Outcome, unknown.Code = "uncertain", "OFFLINE_SCOPE_MVP_NATIVE_RESTORE_UNRESOLVED"
	if e := validateImageEvent(events, unknown); e != nil {
		t.Fatal(e)
	}
	events = append(events, unknown)
	retry := start
	retry.OperationID, e = nonce()
	if e != nil {
		t.Fatal(e)
	}
	if validateImageEvent(events, retry) == nil {
		t.Fatal("uncertain native execution accepted another effect")
	}
	observe := retry
	observe.Action, observe.ReferenceOperationID = "restore_sql_reconcile", id
	observe.Outcome, observe.Code = "unresolved", "OFFLINE_SCOPE_MVP_NATIVE_RESTORE_UNRESOLVED"
	if e := validateImageEvent(events, observe); e != nil {
		t.Fatal("readonly unresolved reconciliation rejected", e)
	}
	events = append(events, observe)
	if state := restoreSQLState(events); state.pending != id || len(state.completed) != 2 {
		t.Fatal("unknown reconciliation lost exact pending operation")
	}
	if _, ok := restoreSQLProof(events); ok {
		t.Fatal("unresolved native execution granted proof")
	}
}

func TestMVPNativeRestoreJournalSQLDiscriminatorAndIdentityFailClosed(t *testing.T) {
	events, base, legacy, scopeID := syntheticMVPNativeSQLJournalFixture(t)
	for _, fault := range []string{"version", "stage", "restore-id", "malformed-restore-id", "missing-execution-sha", "bad-execution-sha", "legacy-task", "plan-sha", "initializer", "legacy-phase", "role", "negative-step", "step-past-end"} {
		t.Run(fault, func(t *testing.T) {
			r := base
			native := *base.Native
			r.Native = &native
			switch fault {
			case "version":
				r.Native.FormatVersion = 2
			case "stage":
				r.Native.Stage = mvpNativeRestoreDatabaseStage
			case "restore-id":
				r.Native.OriginalRestoreID = strings.Repeat("f", 32)
			case "malformed-restore-id":
				r.Native.OriginalRestoreID = "not-an-attempt"
			case "missing-execution-sha":
				r.Native.ExecutionSHA256 = ""
			case "bad-execution-sha":
				r.Native.ExecutionSHA256 = strings.Repeat("z", 64)
			case "legacy-task":
				r.Task = legacy.Task
			case "plan-sha":
				r.PlanSHA256 = strings.Repeat("f", 64)
			case "initializer":
				r.Initializer = "mutable:latest"
			case "legacy-phase":
				r.Phase, r.Role = "volume-create", "input"
			case "role":
				r.Role = "native-writer"
			case "negative-step":
				r.Step = -1
			case "step-past-end":
				r.Step = 8
			}
			if validRestoreSQLRecord(&r, restoreState(events).record, scopeID) {
				t.Fatal("invalid native identity admitted")
			}
		})
	}
}

func TestMVPNativeRestoreJournalSQLCannotSwitchProtocolsMidstream(t *testing.T) {
	events, native, legacy, scopeID := syntheticMVPNativeSQLJournalFixture(t)
	for _, direction := range []string{"native-to-legacy", "legacy-to-native"} {
		t.Run(direction, func(t *testing.T) {
			prior := append([]Event(nil), events...)
			var next RestoreSQLRecord
			if direction == "native-to-legacy" {
				prior = appendSyntheticMVPNativeSQLJournalStep(t, prior, native, 0, scopeID)
				next = legacy
				step := restoreSQLSteps()[1]
				next.Step, next.Phase, next.Role = 1, step.phase, step.role
			} else {
				prior = appendSyntheticRestoreSQLStep(t, prior, legacy, 0, scopeID)
				next = native
				step := restoreSQLStepsForRecord(&native)[1]
				next.Step, next.Phase, next.Role = 1, step.phase, step.role
			}
			id, e := nonce()
			if e != nil {
				t.Fatal(e)
			}
			if validateImageEvent(prior, Event{ScopeID: scopeID, OperationID: id, Action: "restore_sql_effect", Outcome: "started", RestoreSQL: &next}) == nil {
				t.Fatal("retained protocol identity replaced")
			}
		})
	}
}

func TestMVPNativeRestoreJournalSQLCompletionRequiresObservedReceipt(t *testing.T) {
	events, base, _, scopeID := syntheticMVPNativeSQLJournalFixture(t)
	for i := 0; i < 7; i++ {
		events = appendSyntheticMVPNativeSQLJournalStep(t, events, base, i, scopeID)
	}
	r := base
	r.Step, r.Phase, r.Role = 7, "complete", ""
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	start := Event{ScopeID: scopeID, OperationID: id, Action: "restore_sql_effect", Outcome: "started", RestoreSQL: &r}
	if e := validateImageEvent(events, start); e != nil {
		t.Fatal(e)
	}
	events = append(events, start)
	for _, fault := range []string{"missing-receipt", "writer-receipt", "different-receipt", "physical-id-on-complete"} {
		t.Run(fault, func(t *testing.T) {
			done := r
			done.ResultSHA256 = syntheticNativeJournalReceipt("sql", 5)
			switch fault {
			case "missing-receipt":
				done.ResultSHA256 = ""
			case "writer-receipt":
				done.ResultSHA256 = syntheticNativeJournalReceipt("sql", 2)
			case "different-receipt":
				done.ResultSHA256 = syntheticNativeJournalReceipt("other-observer", 5)
			case "physical-id-on-complete":
				done.ObjectID = strings.Repeat("f", 64)
			}
			event := start
			event.Outcome, event.RestoreSQL = "completed", &done
			if validateImageEvent(events, event) == nil {
				t.Fatal("completion lacked its original observation receipt")
			}
		})
	}
	if _, ok := restoreSQLProof(events); ok {
		t.Fatal("incomplete final native step granted proof")
	}
}

func syntheticMVPNativeDatabaseJournalFixture(t *testing.T) ([]Event, RestoreDatabaseRecord, string) {
	t.Helper()
	l, plan := completedRestoreSQLSnapshot(t)
	defer l.close()
	base, _, e := l.makeRestoreDatabaseRecord(context.Background(), plan)
	if e != nil {
		t.Fatal(e)
	}
	sql := *restoreSQLState(l.events).record
	sql.Native = &MVPNativeRestoreStageRecord{FormatVersion: 1, Stage: mvpNativeRestoreSQLStage, ExecutionSHA256: strings.Repeat("a", 64), OriginalRestoreID: plan.AttemptID}
	sql.Task = engine.PostgresBackupTask{}
	sql.Step, sql.Phase, sql.Role = 0, "native-prepare", ""
	sql.ObjectID, sql.ResultSHA256 = "", ""
	events := []Event{}
	for _, event := range l.events {
		if event.RestoreSQL == nil {
			events = append(events, event)
		}
	}
	for i := range restoreSQLStepsForRecord(&sql) {
		events = appendSyntheticMVPNativeSQLJournalStep(t, events, sql, i, l.marker.ScopeID)
	}
	base.SQLProofSHA256, _ = restoreSQLProof(events)
	base.Native = &MVPNativeRestoreStageRecord{FormatVersion: 1, Stage: mvpNativeRestoreDatabaseStage, ExecutionSHA256: sql.Native.ExecutionSHA256, OriginalRestoreID: plan.AttemptID}
	base.Step, base.Phase, base.Kind = 0, "task-create", engine.NativePostgreSQLRestoreVerify
	base.ObjectID, base.ResultSHA256 = "", ""
	return events, base, l.marker.ScopeID
}

func appendSyntheticMVPNativeDatabaseJournalStep(t *testing.T, events []Event, base RestoreDatabaseRecord, index int, scopeID string) []Event {
	t.Helper()
	step := restoreDatabaseStepsForRecord(&base)[index]
	r := base
	r.Step, r.Phase, r.Kind = index, step.phase, step.kind
	r.ObjectID, r.ResultSHA256 = "", ""
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	start := Event{ScopeID: scopeID, OperationID: id, Action: "restore_database_effect", Outcome: "started", RestoreDatabase: &r}
	if e := validateImageEvent(events, start); e != nil {
		t.Fatal("synthetic native database intent", index, e)
	}
	events = append(events, start)
	done := r
	if r.Phase == "task-create" {
		done.ObjectID = strings.Repeat("b", 64)
	}
	if r.Phase == "task-execute" || r.Phase == "complete" {
		done.ResultSHA256 = syntheticNativeJournalReceipt("database", 1)
	}
	terminal := start
	terminal.Outcome, terminal.RestoreDatabase = "completed", &done
	if e := validateImageEvent(events, terminal); e != nil {
		t.Fatal("synthetic native database observation", index, e)
	}
	return append(events, terminal)
}

func TestMVPNativeRestoreJournalDatabaseUsesFourReadonlySteps(t *testing.T) {
	events, base, scopeID := syntheticMVPNativeDatabaseJournalFixture(t)
	if len(restoreDatabaseStepsForRecord(&base)) != 4 {
		t.Fatal("native database readonly catalog changed")
	}
	legacy := base
	legacy.Native, legacy.Phase, legacy.Kind = nil, "volume-create", ""
	if validRestoreDatabaseRecord(&legacy, events, scopeID) {
		t.Fatal("native SQL admitted legacy mutation sequence")
	}
	wrong := base
	wrong.Kind = engine.DatabaseRestoredAuthInvalidate
	if validRestoreDatabaseRecord(&wrong, events, scopeID) {
		t.Fatal("native database admitted duplicate authority invalidation")
	}
	changed := base
	changedNative := *base.Native
	changed.Native = &changedNative
	changed.Native.ExecutionSHA256 = strings.Repeat("f", 64)
	if validRestoreDatabaseRecord(&changed, events, scopeID) {
		t.Fatal("native database replaced original preparation")
	}
	for i := range restoreDatabaseStepsForRecord(&base) {
		if _, ok := restoreDatabaseProof(events); ok {
			t.Fatal("premature native database proof", i)
		}
		events = appendSyntheticMVPNativeDatabaseJournalStep(t, events, base, i, scopeID)
	}
	if proof, ok := restoreDatabaseProof(events); !ok || !wire.ValidSHA256(proof) {
		t.Fatal("missing complete native readonly database proof")
	}
}

func TestMVPNativeRestoreJournalStoresUsesOriginalAttemptAndFourReadonlySteps(t *testing.T) {
	events, database, scopeID := syntheticMVPNativeDatabaseJournalFixture(t)
	for i := range restoreDatabaseStepsForRecord(&database) {
		events = appendSyntheticMVPNativeDatabaseJournalStep(t, events, database, i, scopeID)
	}
	databaseProof, _ := restoreDatabaseProof(events)
	base := RestoreStoresRecord{Native: &MVPNativeRestoreStageRecord{FormatVersion: 1, Stage: mvpNativeRestoreStoresStage, ExecutionSHA256: database.Native.ExecutionSHA256, OriginalRestoreID: database.Native.OriginalRestoreID}, GenerationID: database.Native.OriginalRestoreID, PlanSHA256: strings.Repeat("c", 64), RestorePlanSHA256: database.PlanSHA256, SQLProofSHA256: database.SQLProofSHA256, DatabaseProofSHA256: databaseProof, Stores: []string{"models", "datasets", "training", "preparations"}, Phase: "task-create", Role: "native-observer", Kind: engine.NativePostgreSQLRestoreVerify}
	if len(restoreStoresStepsForRecord(&base)) != 4 || !validRestoreStoresRecord(&base, events) {
		t.Fatal("original attempt's native stores catalog rejected")
	}
	for _, fault := range []string{"new-generation", "partial-stores", "store-order", "database-proof", "changed-preparation", "legacy-protocol"} {
		t.Run(fault, func(t *testing.T) {
			r := base
			r.Stores = append([]string(nil), base.Stores...)
			switch fault {
			case "new-generation":
				r.GenerationID = strings.Repeat("f", 32)
			case "partial-stores":
				r.Stores = r.Stores[:2]
			case "store-order":
				r.Stores[0], r.Stores[1] = r.Stores[1], r.Stores[0]
			case "database-proof":
				r.DatabaseProofSHA256 = strings.Repeat("f", 64)
			case "changed-preparation":
				native := *base.Native
				r.Native = &native
				r.Native.ExecutionSHA256 = strings.Repeat("f", 64)
			case "legacy-protocol":
				r.Native, r.GenerationID = nil, strings.Repeat("f", 32)
				r.Phase, r.Role, r.Kind = "plan-publish", "", ""
			}
			if validRestoreStoresRecord(&r, events) {
				t.Fatal("changed native stores identity admitted")
			}
		})
	}
	for i, step := range restoreStoresStepsForRecord(&base) {
		if _, ok := restoreStoresProof(events); ok {
			t.Fatal("premature native stores proof", i)
		}
		r := base
		r.Step, r.Phase, r.Role, r.Kind = i, step.phase, step.role, step.kind
		r.ObjectID, r.ResultSHA256 = "", ""
		id, e := nonce()
		if e != nil {
			t.Fatal(e)
		}
		start := Event{ScopeID: scopeID, OperationID: id, Action: "restore_stores_effect", Outcome: "started", RestoreStores: &r}
		if e := validateImageEvent(events, start); e != nil {
			t.Fatal("synthetic native stores intent", i, e)
		}
		events = append(events, start)
		done := r
		if r.Phase == "task-create" {
			done.ObjectID = strings.Repeat("d", 64)
		}
		if r.Phase == "task-execute" || r.Phase == "complete" {
			done.ResultSHA256 = syntheticNativeJournalReceipt("stores", 1)
		}
		terminal := start
		terminal.Outcome, terminal.RestoreStores = "completed", &done
		if e := validateImageEvent(events, terminal); e != nil {
			t.Fatal("synthetic native stores observation", i, e)
		}
		events = append(events, terminal)
	}
	if proof, ok := restoreStoresProof(events); !ok || !wire.ValidSHA256(proof) {
		t.Fatal("missing complete native readonly stores proof")
	}
}

// These spies exercise the production abort boundary, not SQL or actual Engine
// deletion. Original-custody callbacks model explicit readback success/failure.
type mvpNativeAbortSpy struct {
	actors                 map[string]engine.RuntimeContainer
	records                bool
	removes, volumeRemoves int
	removeError            error
}

func (s *mvpNativeAbortSpy) InspectDatabaseTask(_ context.Context, t engine.DatabaseTask) (engine.RuntimeContainer, bool, error) {
	a, ok := s.actors[t.Kind]
	return a, ok, nil
}
func (s *mvpNativeAbortSpy) RemoveDatabaseTask(_ context.Context, t engine.DatabaseTask, id string) error {
	s.removes++
	if a, ok := s.actors[t.Kind]; !ok || a.ID != id {
		return errors.New("synthetic actor replacement")
	}
	delete(s.actors, t.Kind)
	return s.removeError
}
func (s *mvpNativeAbortSpy) InspectVolume(context.Context, engine.Ownership) (engine.Volume, bool, error) {
	return engine.Volume{}, s.records, nil
}
func (s *mvpNativeAbortSpy) RemoveNativePostgreSQLVolume(context.Context, engine.Ownership) error {
	s.volumeRemoves++
	s.records = false
	return nil
}
func nativeAbortBoundaryFixture() (mvpNativeRestoreAbortActor, []engine.Ownership, *mvpNativeAbortSpy) {
	id := strings.Repeat("c", 64)
	task := engine.DatabaseTask{Kind: engine.NativePostgreSQLRestore}
	actor := engine.RuntimeContainer{ID: id}
	actor.State.Status = "exited"
	return mvpNativeRestoreAbortActor{Role: "native-writer", Task: task, PhysicalID: id}, []engine.Ownership{{ScopeID: strings.Repeat("a", 32), OperationID: strings.Repeat("b", 32), Role: engine.NativePostgreSQLRestoreRecordsRole + "-" + strings.Repeat("b", 32)}}, &mvpNativeAbortSpy{actors: map[string]engine.RuntimeContainer{task.Kind: actor}, records: true}
}
func TestMVPNativeRestoreAbortPreservesFailedUnknownAndNeverStartedActors(t *testing.T) {
	for _, fault := range []string{"nonzero", "oom", "missing", "created", "running", "replacement"} {
		t.Run(fault, func(t *testing.T) {
			task, owners, spy := nativeAbortBoundaryFixture()
			actor := spy.actors[task.Task.Kind]
			switch fault {
			case "nonzero":
				actor.State.ExitCode = 1
			case "oom":
				actor.State.OOMKilled = true
			case "created":
				actor.State.Status = "created"
			case "running":
				actor.State.Status = "running"
				actor.State.Running = true
			case "replacement":
				actor.ID = strings.Repeat("d", 64)
			}
			spy.actors[task.Task.Kind] = actor
			if fault == "missing" {
				delete(spy.actors, task.Task.Kind)
			}
			e := cleanupMVPNativeRestoreAbort(context.Background(), spy, []mvpNativeRestoreAbortActor{task}, owners, false, func() error { return nil }, func(string) error { return nil })
			if e == nil || spy.removes != 0 || spy.volumeRemoves != 0 || !spy.records {
				t.Fatal("uncertain original actor/records were removed", e, spy.removes, spy.volumeRemoves)
			}
			if fault != "missing" {
				if _, ok := spy.actors[task.Task.Kind]; !ok {
					t.Fatal("original actor lost")
				}
			}
		})
	}
}
func TestMVPNativeRestoreAbortMissingOrChangedOriginalNeverRemovesCustody(t *testing.T) {
	for _, fault := range []string{"missing-complete", "changed-stdout", "missing-observation"} {
		t.Run(fault, func(t *testing.T) {
			task, owners, spy := nativeAbortBoundaryFixture()
			original := func() error { return errors.New("synthetic original readback refused") }
			output := func(string) error { return nil }
			if fault == "missing-observation" {
				original = func() error { return nil }
				output = func(string) error { return errors.New("synthetic original observer missing") }
			}
			e := cleanupMVPNativeRestoreAbort(context.Background(), spy, []mvpNativeRestoreAbortActor{task}, owners, false, original, output)
			if e == nil || spy.removes != 0 || spy.volumeRemoves != 0 || !spy.records || len(spy.actors) != 1 {
				t.Fatal("missing original allowed destructive cleanup", e)
			}
		})
	}
}
func TestMVPNativeRestoreAbortChecksAllActorsBeforeFirstDeletion(t *testing.T) {
	first, owners, spy := nativeAbortBoundaryFixture()
	second := first
	second.Role = "native-observer"
	second.Task.Kind = engine.NativePostgreSQLRestoreVerify
	second.PhysicalID = strings.Repeat("d", 64)
	actor := engine.RuntimeContainer{ID: second.PhysicalID}
	actor.State.Status = "exited"
	actor.State.ExitCode = 1
	spy.actors[second.Task.Kind] = actor
	e := cleanupMVPNativeRestoreAbort(context.Background(), spy, []mvpNativeRestoreAbortActor{first, second}, owners, false, func() error { return nil }, func(string) error { return nil })
	if e == nil || spy.removes != 0 || spy.volumeRemoves != 0 || len(spy.actors) != 2 || !spy.records {
		t.Fatal("failed observer allowed earlier actor/records deletion", e)
	}
}
func TestMVPNativeRestoreAbortInterruptedCleanupNeverResendsRemove(t *testing.T) {
	for _, state := range []string{"actor-present", "records-present", "all-absent"} {
		t.Run(state, func(t *testing.T) {
			actor, owners, spy := nativeAbortBoundaryFixture()
			if state != "actor-present" {
				delete(spy.actors, actor.Task.Kind)
			}
			if state == "all-absent" {
				spy.records = false
			}
			e := cleanupMVPNativeRestoreAbort(context.Background(), spy, []mvpNativeRestoreAbortActor{actor}, owners, true, func() error { return nil }, func(string) error { return nil })
			if (e == nil) != (state == "all-absent") || spy.removes != 0 || spy.volumeRemoves != 0 {
				t.Fatal("interrupted abort replayed cleanup", state, e)
			}
		})
	}
}
func TestMVPNativeRestoreAbortVerifiedOriginalAllowsOneCleanup(t *testing.T) {
	actor, owners, spy := nativeAbortBoundaryFixture()
	originalReads, outputReads := 0, 0
	e := cleanupMVPNativeRestoreAbort(context.Background(), spy, []mvpNativeRestoreAbortActor{actor}, owners, false, func() error { originalReads++; return nil }, func(string) error { outputReads++; return nil })
	if e != nil || originalReads != 1 || outputReads != 1 || spy.removes != 1 || spy.volumeRemoves != 1 || spy.records || len(spy.actors) != 0 {
		t.Fatal("verified original cleanup not once-bound", e)
	}
}
func TestMVPNativeRestoreAbortLostDeleteResponsePreservesRecordsAndNeverRepeats(t *testing.T) {
	actor, owners, spy := nativeAbortBoundaryFixture()
	spy.removeError = errors.New("synthetic lost remove response")
	e := cleanupMVPNativeRestoreAbort(context.Background(), spy, []mvpNativeRestoreAbortActor{actor}, owners, false, func() error { return nil }, func(string) error { return nil })
	if e == nil || spy.removes != 1 || spy.volumeRemoves != 0 || !spy.records {
		t.Fatal("lost actor deletion response erased original records", e)
	}
	e = cleanupMVPNativeRestoreAbort(context.Background(), spy, []mvpNativeRestoreAbortActor{actor}, owners, true, func() error { return nil }, func(string) error { return nil })
	if e == nil || spy.removes != 1 || spy.volumeRemoves != 0 || !spy.records {
		t.Fatal("uncertain abort repeated deletion", e)
	}
}
