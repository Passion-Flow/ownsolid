package migrate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fixtureBackend struct {
	state                                snapshot
	creates, starts, executes, completes int
	executeFailure                       bool
	effectBeforeFailure                  bool
	failComplete                         bool
	onStart                              func()
}

func (f *fixtureBackend) inspect(context.Context) (snapshot, error) {
	out := snapshot{Journal: f.state.Journal, Entries: append([]journalEntry(nil), f.state.Entries...), Present: map[string]bool{}}
	for k, v := range f.state.Present {
		out.Present[k] = v
	}
	return out, nil
}
func (f *fixtureBackend) createJournal(context.Context) error {
	f.creates++
	f.state.Journal = true
	return nil
}
func (f *fixtureBackend) start(_ context.Context, s step) error {
	f.starts++
	f.state.Entries = append(f.state.Entries, journalEntry{s.Number, Scope, s.SHA256, s.Object, "applying", true, false})
	if f.onStart != nil {
		f.onStart()
	}
	return nil
}
func (f *fixtureBackend) execute(_ context.Context, s step) error {
	f.executes++
	if !f.executeFailure || f.effectBeforeFailure {
		f.state.Present[s.Object] = true
	}
	if f.executeFailure {
		return errors.New("synthetic execution acknowledgement lost")
	}
	return nil
}
func (f *fixtureBackend) complete(_ context.Context, s step) error {
	f.completes++
	if f.failComplete {
		return errors.New("synthetic journal acknowledgement lost")
	}
	f.state.Entries[s.Number-1].State = "applied"
	f.state.Entries[s.Number-1].Completed = true
	return nil
}
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}
func smallCatalog(t *testing.T) catalog {
	t.Helper()
	c, e := defaultCatalog()
	if e != nil {
		t.Fatal(e)
	}
	c.steps = c.steps[:2]
	return c
}

func TestNativeSchemaCatalogIsExplicitAndClosed(t *testing.T) {
	c, e := defaultCatalog()
	if e != nil {
		t.Fatal(e)
	}
	legacy, le := foundationCatalog()
	if le != nil || len(legacy.tables) != 14 || len(legacy.steps) != 31 {
		t.Fatal("legacy foundation boundary changed")
	}
	if len(c.tables) != 108 || len(c.steps) != 476 {
		t.Fatalf("unexpected scoped catalog: %d tables, %d steps", len(c.tables), len(c.steps))
	}
	seen := map[string]bool{}
	for _, s := range c.steps {
		if s.Number < 1 || s.SHA256 == "" || seen[s.Object] || strings.Contains(s.SQL, "$1") || strings.Contains(s.SQL, "ENGINE=") || strings.Contains(s.SQL, "::") {
			t.Fatal("catalog has a duplicate or foreign dialect")
		}
		seen[s.Object] = true
	}
	for _, table := range c.tables {
		for _, c := range table.Constraints {
			if len(c.Name) > 63 {
				t.Fatal("native constraint name too long")
			}
		}
	}
	for _, required := range []string{"TENANTS", "WORKSPACES", "USERS", "PASSWORD_CREDENTIALS", "WORKSPACE_MEMBERSHIPS", "INSTALLATION_STATE", "SESSIONS", "INVITATIONS", "PASSWORD_RESET_TOKENS", "MAIL_DELIVERIES", "AUDIT_EVENTS", "RATE_LIMIT_BUCKETS", "IDEMPOTENCY_RECORDS", "WORKSPACE_NOTES", "INVITATIONS_PENDING_EMAIL_IDX", "INSTALLATION_SEED"} {
		if !seen[required] {
			t.Fatal("missing required native foundation object", required)
		}
	}
	if !strings.Contains(invitationTable().ddl(), "GENERATED ALWAYS AS") || !strings.Contains(invitationTable().ddl(), "VARCHAR(35 CHAR)") {
		t.Fatal("native pending uniqueness or locale evolution missing")
	}
	plan, e := Plan()
	if e != nil || len(plan) != len(c.steps) {
		t.Fatal("native plan unavailable")
	}
	plan[0].SQL = "mutated"
	again, _ := Plan()
	if again[0].SQL == "mutated" {
		t.Fatal("caller changed runtime catalog")
	}
}
func TestNativeSchemaApplyAndReadinessKeepPlatformGateClosed(t *testing.T) {
	c := smallCatalog(t)
	f := &fixtureBackend{state: snapshot{Present: map[string]bool{}}}
	r, e := run(testContext(t), c, f, Apply)
	if e != nil || !r.FoundationReady || r.RuntimeReady || r.PlatformReady || f.creates != 1 || f.executes != 2 {
		t.Fatal("foundation apply failed", r, e)
	}
	_, e = run(testContext(t), c, f, Readiness)
	if e != nil || f.executes != 2 {
		t.Fatal("readiness mutated schema")
	}
}
func TestNativeSchemaLostDDLReplyRequiresExplicitReconciliation(t *testing.T) {
	c := smallCatalog(t)
	f := &fixtureBackend{state: snapshot{Present: map[string]bool{}}, executeFailure: true, effectBeforeFailure: true}
	r, e := run(testContext(t), c, f, Apply)
	if e == nil || r.FoundationReady || r.DirtyStep != 1 {
		t.Fatal("uncertain DDL accepted")
	}
	_, e = run(testContext(t), c, f, Apply)
	if FailureCode(e) != "DAMENG_SCHEMA_RECONCILIATION_REQUIRED" || f.executes != 1 {
		t.Fatal("dirty DDL replayed")
	}
	f.executeFailure = false
	r, e = run(testContext(t), c, f, Reconcile)
	if e != nil || r.AppliedSteps != 1 || r.FoundationReady || f.executes != 1 {
		t.Fatal("reconciliation repeated or continued mutation")
	}
	r, e = run(testContext(t), c, f, Apply)
	if e != nil || !r.FoundationReady || f.executes != 2 {
		t.Fatal("explicit continuation failed")
	}
}
func TestNativeSchemaAbsentDirtyDDLNeedsExplicitRetry(t *testing.T) {
	c := smallCatalog(t)
	f := &fixtureBackend{state: snapshot{Present: map[string]bool{}}, executeFailure: true}
	_, _ = run(testContext(t), c, f, Apply)
	_, e := run(testContext(t), c, f, Reconcile)
	if FailureCode(e) != "DAMENG_SCHEMA_EXPLICIT_RETRY_REQUIRED" || f.executes != 1 {
		t.Fatal("absent dirty DDL replayed by reconcile")
	}
	f.executeFailure = false
	r, e := run(testContext(t), c, f, Retry)
	if e != nil || r.AppliedSteps != 1 || r.FoundationReady || f.executes != 2 {
		t.Fatal("single explicit retry failed")
	}
}
func TestNativeSchemaUnknownJournalCompletionNeverReplaysDDL(t *testing.T) {
	c := smallCatalog(t)
	f := &fixtureBackend{state: snapshot{Present: map[string]bool{}}, failComplete: true}
	_, e := run(testContext(t), c, f, Apply)
	if e == nil {
		t.Fatal("completion failure accepted")
	}
	f.failComplete = false
	r, e := run(testContext(t), c, f, Reconcile)
	if e != nil || r.AppliedSteps != 1 || f.executes != 1 {
		t.Fatal("DDL replay after journal failure")
	}
}
func TestNativeSchemaRejectsDriftGapsAndUnjournaledObjects(t *testing.T) {
	c := smallCatalog(t)
	base := journalEntry{1, Scope, c.steps[0].SHA256, c.steps[0].Object, "applied", true, true}
	for _, name := range []string{"checksum", "scope", "gap", "state", "timestamp", "missing", "extra", "dirty-followed"} {
		t.Run(name, func(t *testing.T) {
			s := snapshot{Journal: true, Entries: []journalEntry{base}, Present: map[string]bool{base.Object: true}}
			switch name {
			case "checksum":
				s.Entries[0].Source = strings.Repeat("0", 64)
			case "scope":
				s.Entries[0].Scope = "another-scope"
			case "gap":
				s.Entries[0].Step = 2
			case "state":
				s.Entries[0].State = "verified"
			case "timestamp":
				s.Entries[0].Completed = false
			case "missing":
				delete(s.Present, base.Object)
			case "extra":
				s.Present["FOREIGN_TABLE"] = true
			case "dirty-followed":
				s.Entries[0].State = "applying"
				s.Entries[0].Completed = false
				s.Entries = append(s.Entries, journalEntry{2, Scope, c.steps[1].SHA256, c.steps[1].Object, "applying", true, false})
			}
			if r, e := assess(c, s); e == nil || r.FoundationReady {
				t.Fatal("invalid journal/schema accepted")
			}
		})
	}
}
func TestNativeSchemaDeadlineAndEmptyReadinessDoNotWrite(t *testing.T) {
	c := smallCatalog(t)
	f := &fixtureBackend{state: snapshot{Present: map[string]bool{}}}
	if _, e := run(context.Background(), c, f, Apply); e == nil {
		t.Fatal("deadline absent accepted")
	}
	if _, e := run(testContext(t), c, f, Readiness); FailureCode(e) != "DAMENG_SCHEMA_NOT_READY" {
		t.Fatal("empty schema reported ready")
	}
	if f.creates+f.starts+f.executes+f.completes != 0 {
		t.Fatal("refused check wrote")
	}
}
func TestNativeSchemaCancelledDispatchRetainsDirtyJournal(t *testing.T) {
	c := smallCatalog(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	f := &fixtureBackend{state: snapshot{Present: map[string]bool{}}, onStart: cancel}
	r, e := run(ctx, c, f, Apply)
	if !errors.Is(e, context.Canceled) || r.DirtyStep != 1 || f.executes != 0 || f.completes != 0 {
		t.Fatal("cancel after journal start dispatched DDL")
	}
	fresh := &fixtureBackend{state: snapshot{Present: map[string]bool{}}}
	_, e = run(ctx, c, fresh, Apply)
	if !errors.Is(e, context.Canceled) || fresh.creates+fresh.starts+fresh.executes+fresh.completes != 0 {
		t.Fatal("cancelled caller mutated schema")
	}
}

func TestNativeSchemaMetadataExpressionPreservesMeaning(t *testing.T) {
	if expression(` "STATUS" IN ('pending','accepted') `) != expression(`STATUS in ('pending', 'accepted')`) {
		t.Fatal("safe metadata format differs")
	}
	if expression(`STATUS='pending'`) == expression(`STATUS='PENDING'`) || expression(`A=1 OR B=2`) == expression(`A=1 AND B=2`) {
		t.Fatal("metadata literals/predicates collapsed")
	}
	if lockID("EXAMPLE_OWNER") != lockID("EXAMPLE_OWNER") || lockID("EXAMPLE_OWNER") < 0 || lockID("EXAMPLE_OWNER") > 1073741823 {
		t.Fatal("lock identity invalid")
	}
}
