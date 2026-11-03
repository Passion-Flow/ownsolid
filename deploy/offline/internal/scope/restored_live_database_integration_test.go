//go:build offlineintegration

package scope

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Lose only the response after the actual Engine successfully starts the first
// schema checking container. SQL, private files and metadata remain real.
type actualLiveDatabaseStartLoss struct {
	*engine.Client
	calls int
	lost  bool
}

func (e *actualLiveDatabaseStartLoss) StartDatabaseTask(ctx context.Context, task engine.DatabaseTask, id string, files []engine.ConfigurationFile, reserve int) (engine.ResourceAdmission, error) {
	e.calls++
	out, err := e.Client.StartDatabaseTask(ctx, task, id, files, reserve)
	if err == nil && !e.lost {
		e.lost = true
		return out, errors.New("SYNTHETIC_REAL_SCHEMA_TASK_START_RESPONSE_LOST")
	}
	return out, err
}

func TestActualRetainedRestoredLiveDatabaseChecks(t *testing.T) {
	retention := os.Getenv("PROJECT_ONE_TEST_RESTORED_LIVE_DATABASE_RETENTION")
	if retention == "" {
		t.Skip("explicit isolated C09 retained target required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	var retained struct {
		ScopeID     string `json:"scope_id"`
		Config      string `json:"operator_config"`
		TLSRequest  string `json:"TLS_request"`
		Qualified   bool   `json:"qualified_dependency_recovery"`
		Observation Status `json:"status_observation"`
	}
	raw, e := bundle.ReadProtectedConfiguration(retention)
	if e != nil || wire.UniqueJSON(raw, &retained) != nil || !retained.Qualified || retained.Observation.ControlReplacement == nil || retained.Observation.ControlReplacement.Pending || retained.Observation.ControlReplacement.ProofSHA256 == "" {
		t.Fatal("accepted private C09 target required", e)
	}
	raw, e = bundle.ReadProtectedConfiguration(retained.Config)
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture || c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		t.Fatal("isolated protected configuration required", e)
	}
	out, binary := os.Getenv("PROJECT_ONE_TEST_RESTORED_LIVE_DATABASE_EVIDENCE"), os.Getenv("PROJECT_ONE_TEST_RESTORED_LIVE_DATABASE_OPERATOR")
	if !privateDirectory(out) || !canonical(binary) {
		t.Fatal("private evidence/current operator required")
	}
	put := func(name string, value any) {
		root, e := os.OpenRoot(out)
		if e != nil {
			t.Fatal(e)
		}
		defer root.Close()
		if e = writeBackupFile(root, name, encode(value)); e != nil {
			t.Fatal(e)
		}
	}
	operator := func(ctx context.Context, action string) ([]byte, error) {
		return exec.CommandContext(ctx, binary, action, "--config", retained.Config).CombinedOutput()
	}
	status := func(ctx context.Context) Status {
		raw, e := operator(ctx, "status")
		var s Status
		if e != nil || wire.StrictJSON(raw, &s) != nil {
			t.Fatal("current native status", e)
		}
		return s
	}
	beforeStatus := status(ctx)
	sameOriginalProofs := func(s Status) bool {
		return reflect.DeepEqual(s.ControlReplacement, beforeStatus.ControlReplacement) && reflect.DeepEqual(s.TLSProjection, beforeStatus.TLSProjection) && reflect.DeepEqual(s.RestoreSQL, beforeStatus.RestoreSQL) && reflect.DeepEqual(s.RestoreDatabase, beforeStatus.RestoreDatabase) && reflect.DeepEqual(s.RestoreStores, beforeStatus.RestoreStores)
	}
	if beforeStatus.ScopeID != retained.ScopeID || beforeStatus.Purpose != purpose || beforeStatus.RestoredLiveDatabase != nil || !reflect.DeepEqual(beforeStatus.ControlReplacement, retained.Observation.ControlReplacement) || !reflect.DeepEqual(beforeStatus.RestoreDatabase, retained.Observation.RestoreDatabase) {
		t.Fatal("original target changed or live qualification already recorded; continue checkpoint without replay")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		t.Fatal(e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	before, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	if processState(l.events).pending != "" || liveDatabaseState(l.events).record != nil || before.observed.Services["control"].State != "created" || before.observed.Services["postgres"].State != "exited" || before.observed.Services["postgres"].ExitCode != 0 {
		l.close()
		t.Fatal("clean stopped C09 target required")
	}
	originalRuntime, effectiveRuntime := encode(*runtimeState(l.events).record), encode(*effectiveRuntimeState(l.events).record)
	descriptor, payload := wire.Digest(encode(before.inputs.descriptor)), restoreConfigurationDigest(before.inputs.files["control-config"])
	firstSequence := len(l.events)
	l.close()
	qualified := false
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 90*time.Second)
		defer done()
		s := status(cleanup)
		if s.RestoredLiveDatabase != nil && s.RestoredLiveDatabase.Pending {
			if _, e := operator(cleanup, "abort-restored-live-database"); e != nil {
				t.Errorf("generated actor cleanup unresolved: %v", e)
			}
		}
		if _, e := operator(cleanup, "reconcile-process"); e != nil {
			t.Errorf("independent process observation: %v", e)
		}
		if _, e := operator(cleanup, "stop-runtime"); e != nil {
			t.Errorf("scoped dependency stop: %v", e)
		}
		final := status(cleanup)
		put("retention.json", map[string]any{"status": "retained_live_database_checkpoint_core_stopped_activation_pending", "scope_id": retained.ScopeID, "operator_config": retained.Config, "TLS_request": retained.TLSRequest, "native_operator": binary, "dependency_retention": retention, "qualified_live_database_checks": qualified, "status_observation": final, "qualification": "actual_local_schema_and_restricted_runtime_SQL_checks_only_not_store_activation_browser_or_native_target"})
	}()
	if _, e = operator(ctx, "inspect-restored-live-database"); e == nil {
		t.Fatal("stopped PostgreSQL admitted as live")
	}
	if _, e = operator(ctx, "start-restored-database"); e != nil {
		t.Fatal("explicit dependency start", e)
	}
	raw, e = operator(ctx, "inspect-restored-live-database")
	var inspection RestoredLiveDatabaseInspection
	if e != nil || wire.StrictJSON(raw, &inspection) != nil || inspection.Status != "validated_live_database_check_plan_no_effects" || inspection.Plan.PostgresPID <= 0 || len(inspection.Plan.Tasks) != 2 || inspection.Plan.Tasks[0].Kind != engine.DatabaseSchemaCheck || inspection.Plan.Tasks[1].Kind != engine.DatabaseRuntimeCheck || inspection.Plan.Tasks[0].Peers["control"] != before.observed.Services["control"].ID {
		t.Fatal("current native admission", e)
	}
	loss := &actualLiveDatabaseStartLoss{Client: client}
	failed, e := runRestoredLiveDatabase(ctx, c, "qualify-restored-live-database", loss, worker)
	if e == nil || !failed.Pending || failed.ProofSHA256 != "" || !loss.lost || loss.calls != 1 {
		t.Fatal("actual checking task start response loss required", failed, e)
	}
	for _, action := range []string{"qualify-restored-live-database", "start-database", "start-runtime"} {
		if _, e = operator(ctx, action); e == nil {
			t.Fatal("unknown effect resubmitted or activation opened", action)
		}
	}
	raw, e = operator(ctx, "reconcile-restored-live-database")
	var recovered RestoredLiveDatabaseResult
	if e != nil || wire.StrictJSON(raw, &recovered) != nil || recovered.Pending || recovered.ProofSHA256 == "" || recovered.CompletedSteps != 13 || loss.calls != 1 {
		t.Fatal("native independent SQL outcome/continuation", recovered, e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	state := liveDatabaseState(l.events)
	plan, e := l.readLiveDatabasePlan(*state.record)
	if e != nil || plan.GenerationID != recovered.GenerationID || plan.PostgresPID != inspection.Plan.PostgresPID || plan.PostgresStartedAt != inspection.Plan.PostgresStartedAt {
		l.close()
		t.Fatal("actual process/plan binding", e)
	}
	intents, completed := map[string]int{}, map[string]int{}
	for _, event := range l.events[firstSequence:] {
		if r := event.RestoredLiveDatabase; r != nil {
			step := liveDatabaseSteps()[r.Step]
			if step.kind != "" {
				key := step.kind + ":" + step.phase
				if event.Outcome == "started" {
					intents[key]++
				}
				if event.Outcome == "completed" {
					completed[key]++
				}
			}
		} else if event.Process == nil {
			l.close()
			t.Fatal("unrelated SQL/import/authority/store operation replayed")
		}
	}
	for _, kind := range []string{engine.DatabaseSchemaCheck, engine.DatabaseRuntimeCheck} {
		for _, phase := range []string{"task-create", "task-execute", "task-remove"} {
			key := kind + ":" + phase
			if intents[key] != 1 || completed[key] != 1 {
				l.close()
				t.Fatal("actual checking command counts", key, intents, completed)
			}
		}
	}
	if _, found, e := client.InspectVolume(ctx, plan.Tasks[0].VolumeOwner); e != nil || found {
		l.close()
		t.Fatal("generated configuration not removed", e)
	}
	journal, je := regular(l.root, journalName, c.MaximumJournalBytes)
	head, he := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	l.close()
	if je != nil || he != nil {
		t.Fatal("terminal private journal", je, he)
	}
	raw, e = operator(ctx, "qualify-restored-live-database")
	var again RestoredLiveDatabaseResult
	if e != nil || wire.StrictJSON(raw, &again) != nil || again != recovered {
		t.Fatal("historical repeat", e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	actualJournal, je := regular(l.root, journalName, c.MaximumJournalBytes)
	actualHead, he := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	current, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil || je != nil || he != nil || !bytes.Equal(journal, actualJournal) || !bytes.Equal(head, actualHead) || descriptor != wire.Digest(encode(current.inputs.descriptor)) || payload != restoreConfigurationDigest(current.inputs.files["control-config"]) || !bytes.Equal(originalRuntime, encode(*runtimeState(l.events).record)) || !bytes.Equal(effectiveRuntime, encode(*effectiveRuntimeState(l.events).record)) || current.observed.Services["control"] != before.observed.Services["control"] || current.observed.Services["postgres"].ID != before.observed.Services["postgres"].ID || current.observed.Services["postgres"].State != "running" || current.observed.NetworkID != before.observed.NetworkID || current.observed.IngressNetworkID != before.observed.IngressNetworkID {
		l.close()
		t.Fatal("original runtime/configuration/network/history changed", e)
	}
	l.close()
	if !sameOriginalProofs(status(ctx)) {
		t.Fatal("original restore proofs changed")
	}
	if _, e = operator(ctx, "stop-runtime"); e != nil {
		t.Fatal("scoped final stop", e)
	}
	final := status(ctx)
	if !sameOriginalProofs(final) || final.RestoredLiveDatabase == nil || final.RestoredLiveDatabase.ProofSHA256 != recovered.ProofSHA256 {
		t.Fatal("proofs changed after scoped stop")
	}
	postgres, found, e := client.InspectRuntime(ctx, retained.ScopeID, "postgres")
	if e != nil || !found || postgres.State.Status != "exited" || postgres.State.ExitCode != 0 || postgres.State.Running {
		t.Fatal("database stop", e)
	}
	control, found, e := client.InspectRuntime(ctx, retained.ScopeID, "control")
	if e != nil || !found || control.ID != before.observed.Services["control"].ID || control.State.Status != "created" || control.State.Running || control.State.PID != 0 {
		t.Fatal("control activated", e)
	}
	qualified = true
	put("actual.json", map[string]any{"status": "passed_actual_restored_live_database_checks", "scope_id": retained.ScopeID, "generation_id": recovered.GenerationID, "proof_sha256": recovered.ProofSHA256, "control_id": control.ID, "postgres_id": postgres.ID, "schema_task_start_calls_before_native_reconcile": loss.calls, "task_intents": intents, "task_completions": completed, "original_proofs_unchanged": true, "original_configuration_runtime_network_unchanged": true, "generated_configuration_removed": true, "completed_repeat_journal_head_unchanged": true, "application_never_started": true, "postgres_stopped": true, "qualification": "actual_local_readonly_SQL_checks_not_store_application_browser_or_native_target"})
}
