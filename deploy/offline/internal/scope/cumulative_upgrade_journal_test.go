package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

func upgradeJournalFixture(t *testing.T) []Event {
	t.Helper()
	c, d, w := bootstrapScopeFixture(t)
	for _, kind := range []string{engine.DatabaseBootstrap, engine.DatabaseBootstrapReconcile} {
		if _, e := createDatabaseTask(context.Background(), c, d, w, kind, false); e != nil {
			t.Fatal(e)
		}
		if _, e := databaseTaskAction(context.Background(), c, d, w, "execute", false, kind); e != nil {
			t.Fatal(e)
		}
		if _, e := databaseTaskAction(context.Background(), c, d, w, "removed", false, kind); e != nil {
			t.Fatal(e)
		}
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	// Explicit journal-state fixture only: no archive, SQL backup or real Engine
	// acceptance is asserted by this metadata transition control.
	return append(append([]Event{}, l.events...), Event{Action: "backup_effect", Outcome: "completed", Backup: &BackupRecord{AttemptID: strings.Repeat("a", 32), Phase: "complete", ArtifactSHA256: strings.Repeat("b", 64)}})
}
func upgradeEvent(step int) Event {
	return Event{OperationID: fmt.Sprintf("%032x", step+100), Action: "upgrade_effect", Outcome: "started", CumulativeUpgrade: &CumulativeUpgradeRecord{GenerationID: strings.Repeat("c", 32), PlanSHA256: strings.Repeat("d", 64), Step: step, Phase: cumulativeUpgradeSteps()[step]}}
}
func TestCumulativeUpgradeRefusesReplayMixedPayloadAndOutOfOrderEffects(t *testing.T) {
	events := upgradeJournalFixture(t)
	start := upgradeEvent(0)
	if validateImageEvent(nil, start) == nil {
		t.Fatal("unqualified source admitted")
	}
	if e := validateImageEvent(events, start); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Event){func(e *Event) { e.Process = &ProcessRecord{} }, func(e *Event) { e.Backup = &BackupRecord{} }, func(e *Event) { e.Image = &ImageRecord{} }, func(e *Event) { e.JournalCapacity = &JournalCapacityRecord{} }, func(e *Event) { e.CumulativeUpgrade.Step = 1; e.CumulativeUpgrade.Phase = cumulativeUpgradeSteps()[1] }} {
		v := upgradeEvent(0)
		change(&v)
		if validateImageEvent(events, v) == nil {
			t.Fatal("mixed or skipped intent admitted")
		}
	}
	events = append(events, start)
	if validateImageEvent(events, start) == nil {
		t.Fatal("pending submission replay allowed")
	}
	for _, e := range []Event{{Action: "process_start"}, {Action: "image_import"}, {Action: "backup_effect"}, {Action: journalCapacityAction}} {
		if validateImageEvent(events, e) == nil {
			t.Fatal("unrelated mutation during intent")
		}
	}
	lost := start
	lost.Outcome = "uncertain"
	lost.Code = "OFFLINE_SCOPE_CUMULATIVE_UPGRADE_EFFECT_UNRESOLVED"
	if e := validateImageEvent(events, lost); e != nil {
		t.Fatal(e)
	}
	events = append(events, lost)
	finish := start
	finish.Outcome = "completed"
	if validateImageEvent(events, finish) == nil {
		t.Fatal("uncertain original submission accepted another terminal")
	}
	finish.Action = "upgrade_reconcile"
	finish.OperationID = strings.Repeat("e", 32)
	finish.ReferenceOperationID = start.OperationID
	if e := validateImageEvent(events, finish); e != nil {
		t.Fatal(e)
	}
	events = append(events, finish)
	if validateImageEvent(events, finish) == nil {
		t.Fatal("completed reconciliation replay")
	}
	next := upgradeEvent(1)
	if e := validateImageEvent(events, next); e != nil {
		t.Fatal(e)
	}
	events = append(events, next)
	recover := next
	recover.Action = "upgrade_recover"
	recover.Outcome = "not_applied"
	recover.OperationID = strings.Repeat("f", 32)
	recover.ReferenceOperationID = next.OperationID
	if e := validateImageEvent(events, recover); e != nil {
		t.Fatal(e)
	}
	events = append(events, recover)
	if validateImageEvent(events, next) == nil {
		t.Fatal("recovery silently restarted original effect")
	}
	retry := next
	retry.Action = "upgrade_retry"
	retry.OperationID = strings.Repeat("9", 32)
	retry.ReferenceOperationID = recover.OperationID
	if e := validateImageEvent(events, retry); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(Event{Action: "legacy"})
	if bytes.Contains(raw, []byte("cumulative_upgrade")) {
		t.Fatal("historical encoding changed")
	}
}
func TestCumulativeUpgradeActivationRequiresCompletedReplacementAndRetainsOriginalView(t *testing.T) {
	events := upgradeJournalFixture(t)
	// State-only complete prefix. Production plan/actual effect validation is a
	// separate controller gate and must not be inferred from this test.
	for step, phase := range cumulativeUpgradeSteps() {
		e := upgradeEvent(step)
		e.Outcome = "completed"
		if phase == "replacement-complete" {
			r := *effectiveRuntimeState(events).record
			e.CumulativeUpgrade.Effective = &r
		}
		events = append(events, e)
	}
	current := effectiveRuntimeState(events)
	after := map[string]ProcessSnapshot{}
	for name, svc := range current.record.Services {
		after[name] = ProcessSnapshot{ID: svc.ID, State: "running", StartedAt: "2026-10-27T00:00:00Z"}
	}
	events = append(events, Event{Action: "process_start", Outcome: "completed", Process: &ProcessRecord{Target: "running", CreationSHA256: wire.Digest(encode(*current.record)), After: after}})
	e := upgradeEvent(len(cumulativeUpgradeSteps()) - 1)
	e.Action = "upgrade_activate"
	e.Outcome = "completed"
	e.OperationID = strings.Repeat("6", 32)
	e.CumulativeUpgrade.Readiness = &compose.ControlReadiness{ControlID: effectiveRuntimeState(events).record.Services["control"].ID, PID: 42, StartedAt: "2026-10-27T00:00:00Z", ObservedAt: "2026-10-27T00:00:01Z", Audiences: []string{"portal", "admin"}, InputSHA256: strings.Repeat("7", 64)}
	e.CumulativeUpgrade.ReadinessSHA256 = wire.Digest(encode(*e.CumulativeUpgrade.Readiness))
	e.CumulativeUpgrade.Installed = &view.InstalledRelease{ReleaseID: "m17-current", Profile: wire.CumulativeCPUProfile, ArchiveSHA256: strings.Repeat("8", 64), ManifestSHA256: strings.Repeat("9", 64), SourceCommit: strings.Repeat("a", 40), SchemaVersion: 22, ActivationSequence: len(events) + 1}
	if err := validateImageEvent(events, e); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Event){func(x *Event) { x.Outcome = "started" }, func(x *Event) { x.CumulativeUpgrade.ReadinessSHA256 = "" }, func(x *Event) { x.CumulativeUpgrade.Installed.SchemaVersion = 23 }, func(x *Event) { x.CumulativeUpgrade.Step = 0; x.CumulativeUpgrade.Phase = cumulativeUpgradeSteps()[0] }} {
		raw, _ := json.Marshal(e)
		var bad Event
		json.Unmarshal(raw, &bad)
		change(&bad)
		if validateImageEvent(events, bad) == nil {
			t.Fatal("unready activation accepted")
		}
	}
	s := cumulativeUpgradeState(events)
	if s.activated {
		t.Fatal("replacement falsely activated release")
	}
	e.Sequence = len(events) + 1
	events = append(events, e)
	s = cumulativeUpgradeState(events)
	if !s.activated || s.activationSequence != len(events) {
		t.Fatal("activation lost")
	}
	if validateImageEvent(events, e) == nil {
		t.Fatal("activation replayed")
	}
}
