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
// model checking container. Business file verification, private inputs and metadata remain real.
type actualCurrentStoresStartLoss struct {
	*engine.Client
	calls int
	lost  bool
}

func (e *actualCurrentStoresStartLoss) StartDatabaseTask(ctx context.Context, task engine.DatabaseTask, id string, files []engine.ConfigurationFile, reserve int) (engine.ResourceAdmission, error) {
	e.calls++
	out, err := e.Client.StartDatabaseTask(ctx, task, id, files, reserve)
	if err == nil && !e.lost {
		e.lost = true
		return out, errors.New("SYNTHETIC_REAL_STORE_TASK_START_RESPONSE_LOST")
	}
	return out, err
}

func TestActualRetainedRestoredCurrentStoresChecks(t *testing.T) {
	retention := os.Getenv("PROJECT_ONE_TEST_RESTORED_CURRENT_STORES_RETENTION")
	if retention == "" {
		t.Skip("explicit isolated C10 retained target required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	var retained struct {
		ScopeID     string `json:"scope_id"`
		Config      string `json:"operator_config"`
		TLSRequest  string `json:"TLS_request"`
		Qualified   bool   `json:"qualified_live_database_checks"`
		Observation Status `json:"status_observation"`
	}
	raw, e := bundle.ReadProtectedConfiguration(retention)
	if e != nil || wire.UniqueJSON(raw, &retained) != nil || !retained.Qualified || retained.Observation.ControlReplacement == nil || retained.Observation.ControlReplacement.Pending || retained.Observation.ControlReplacement.ProofSHA256 == "" {
		t.Fatal("accepted private C10 target required", e)
	}
	raw, e = bundle.ReadProtectedConfiguration(retained.Config)
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture || c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		t.Fatal("isolated protected configuration required", e)
	}
	out, binary := os.Getenv("PROJECT_ONE_TEST_RESTORED_CURRENT_STORES_EVIDENCE"), os.Getenv("PROJECT_ONE_TEST_RESTORED_CURRENT_STORES_OPERATOR")
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
	if beforeStatus.ScopeID != retained.ScopeID || beforeStatus.Purpose != purpose || beforeStatus.RestoredCurrentStores != nil || !reflect.DeepEqual(beforeStatus.ControlReplacement, retained.Observation.ControlReplacement) || !reflect.DeepEqual(beforeStatus.RestoreDatabase, retained.Observation.RestoreDatabase) {
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
	if processState(l.events).pending != "" || currentStoresState(l.events).record != nil || before.observed.Services["control"].State != "created" || before.observed.Services["postgres"].State != "exited" || before.observed.Services["postgres"].ExitCode != 0 {
		l.close()
		t.Fatal("clean stopped C10 target required")
	}
	originalRuntime, effectiveRuntime := encode(*runtimeState(l.events).record), encode(*effectiveRuntimeState(l.events).record)
	descriptor, payload := wire.Digest(encode(before.inputs.descriptor)), restoreConfigurationDigest(before.inputs.files["control-config"])
	firstSequence := len(l.events)
	originalJournal, e := regular(l.root, journalName, c.MaximumJournalBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	l.close()
	qualified := false
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 90*time.Second)
		defer done()
		s := status(cleanup)
		if s.RestoredCurrentStores != nil && s.RestoredCurrentStores.Pending {
			if _, e := operator(cleanup, "abort-restored-current-stores"); e != nil {
				t.Errorf("generated actor cleanup unresolved: %v", e)
			}
		}
		if s.RestoredLiveDatabase != nil && s.RestoredLiveDatabase.Pending {
			if _, e := operator(cleanup, "abort-restored-live-database"); e != nil {
				t.Errorf("fresh SQL cleanup unresolved: %v", e)
			}
		}
		if _, e := operator(cleanup, "reconcile-process"); e != nil {
			t.Errorf("independent process observation: %v", e)
		}
		if _, e := operator(cleanup, "stop-runtime"); e != nil {
			t.Errorf("scoped dependency stop: %v", e)
		}
		final := status(cleanup)
		put("retention.json", map[string]any{"status": "retained_current_stores_checkpoint_core_stopped_activation_pending", "scope_id": retained.ScopeID, "operator_config": retained.Config, "TLS_request": retained.TLSRequest, "native_operator": binary, "live_database_retention": retention, "qualified_current_stores_checks": qualified, "status_observation": final, "qualification": "actual_local_current_selected_store_checks_only_not_activation_browser_nonempty_model_weights_or_native_target"})
	}()
	if _, e = operator(ctx, "inspect-restored-current-stores"); e == nil {
		t.Fatal("stopped PostgreSQL admitted as live")
	}
	if _, e = operator(ctx, "start-restored-database"); e != nil {
		t.Fatal("explicit dependency start", e)
	}
	var currentSQL RestoredLiveDatabaseResult
	raw, e = operator(ctx, "requalify-restored-live-database")
	if e != nil || wire.StrictJSON(raw, &currentSQL) != nil || currentSQL.Pending || currentSQL.ProofSHA256 == "" || beforeStatus.RestoredLiveDatabase == nil || currentSQL.ProofSHA256 == beforeStatus.RestoredLiveDatabase.ProofSHA256 {
		t.Fatal("fresh SQL proof for restarted dependency", e)
	}
	var enginePolicy engine.Policy
	if wire.StrictJSON(wire.EngineProfile, &enginePolicy) != nil {
		t.Fatal("frozen Engine policy")
	}
	anonymous := out + "/metadata-docker-config"
	if e = os.Mkdir(anonymous, 0700); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := os.Remove(anonymous); e != nil {
			t.Errorf("owned empty Docker configuration cleanup: %v", e)
		}
	}()
	type businessCounts struct{ Users, Datasets, DatasetVersions, ReadyModels, AuditEvents, AuthorityEpochSum int64 }
	counts := func() businessCounts {
		pg, found, err := client.InspectRuntime(ctx, retained.ScopeID, "postgres")
		if err != nil || !found || pg.ID != before.observed.Services["postgres"].ID || !pg.State.Running || pg.State.PID <= 0 {
			t.Fatal("owned live metadata target", err)
		}
		service := before.model.Services["postgres"]
		const query = `SELECT pg_catalog.json_build_object('Users',(SELECT count(*) FROM public.users),'Datasets',(SELECT count(*) FROM public.datasets),'DatasetVersions',(SELECT count(*) FROM public.dataset_versions),'ReadyModels',(SELECT count(*) FROM public.model_versions WHERE state='ready'),'AuditEvents',(SELECT count(*) FROM public.audit_events),'AuthorityEpochSum',(SELECT coalesce(sum(auth_epoch),0) FROM public.users))`
		args := []string{"--config", anonymous, "--host", "unix://" + c.Engine.SocketFile, "container", "exec", "--user", service.User, pg.ID, "psql", "-X", "-A", "-t", "--no-password", "--host", "/var/run/postgresql", "--username", service.Environment["POSTGRES_USER"], "--dbname", service.Environment["POSTGRES_DB"], "--command", query}
		command := exec.CommandContext(ctx, c.Compose.DockerBinary, args...)
		command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "DOCKER_API_VERSION=" + enginePolicy.APIVersion}
		result, err := command.CombinedOutput()
		var observed businessCounts
		if err != nil || wire.StrictJSON(bytes.TrimSpace(result), &observed) != nil {
			t.Fatal("fixed readonly synthetic business-count query", err)
		}
		after, found, err := client.InspectRuntime(ctx, retained.ScopeID, "postgres")
		if err != nil || !found || after.ID != pg.ID || after.State.PID != pg.State.PID || after.State.StartedAt != pg.State.StartedAt {
			t.Fatal("metadata process changed", err)
		}
		return observed
	}
	beforeCounts := counts()
	if beforeCounts.Users < 1 || beforeCounts.Datasets < 1 || beforeCounts.DatasetVersions < 1 {
		t.Fatal("nonempty restored dataset/account fixture required")
	}
	raw, e = operator(ctx, "inspect-restored-current-stores")
	var inspection RestoredCurrentStoresInspection
	if e != nil || wire.StrictJSON(raw, &inspection) != nil || inspection.Status != "validated_current_store_check_plan_no_effects" || inspection.Plan.PostgresPID <= 0 || inspection.Plan.DatabaseProofSHA256 != currentSQL.ProofSHA256 || len(inspection.Plan.Tasks) != 2 || inspection.Plan.Tasks[0].Kind != engine.ModelFilesVerify || inspection.Plan.Tasks[1].Kind != engine.DatasetFilesVerify || inspection.Plan.Tasks[0].Peers["control"] != before.observed.Services["control"].ID {
		t.Fatal("current native admission", e)
	}
	loss := &actualCurrentStoresStartLoss{Client: client}
	failed, e := runRestoredCurrentStores(ctx, c, "qualify-restored-current-stores", loss, worker)
	if e == nil || !failed.Pending || failed.ProofSHA256 != "" || !loss.lost || loss.calls != 1 {
		t.Fatal("actual checking task start response loss required", failed, e)
	}
	for _, action := range []string{"qualify-restored-current-stores", "start-database", "start-runtime", "requalify-restored-live-database"} {
		if _, e = operator(ctx, action); e == nil {
			t.Fatal("unknown effect resubmitted or activation opened", action)
		}
	}
	raw, e = operator(ctx, "reconcile-restored-current-stores")
	var recovered RestoredCurrentStoresResult
	if e != nil || wire.StrictJSON(raw, &recovered) != nil || recovered.Pending || recovered.ProofSHA256 == "" || recovered.CompletedSteps != 13 || loss.calls != 1 {
		t.Fatal("native independent store outcome/continuation", recovered, e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	state := currentStoresState(l.events)
	plan, e := l.readCurrentStoresPlan(*state.record)
	if e != nil || plan.GenerationID != recovered.GenerationID || plan.DatabaseProofSHA256 != currentSQL.ProofSHA256 || plan.PostgresPID != inspection.Plan.PostgresPID || plan.PostgresStartedAt != inspection.Plan.PostgresStartedAt {
		l.close()
		t.Fatal("actual process/plan binding", e)
	}
	intents, completed := map[string]int{}, map[string]int{}
	for _, event := range l.events[firstSequence:] {
		if r := event.RestoredCurrentStores; r != nil {
			step := currentStoresSteps()[r.Step]
			if step.kind != "" {
				key := step.kind + ":" + step.phase
				if event.Outcome == "started" {
					intents[key]++
				}
				if event.Outcome == "completed" {
					completed[key]++
				}
			}
		} else if sql := event.RestoredLiveDatabase; sql != nil {
			if sql.GenerationID != currentSQL.GenerationID {
				l.close()
				t.Fatal("unexpected SQL generation replayed")
			}
		} else if event.Process == nil {
			l.close()
			t.Fatal("unrelated import/authority/store operation replayed")
		}
	}
	for _, kind := range []string{engine.ModelFilesVerify, engine.DatasetFilesVerify} {
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
	if je != nil || he != nil || !bytes.HasPrefix(journal, originalJournal) {
		t.Fatal("terminal private journal/history", je, he)
	}
	raw, e = operator(ctx, "qualify-restored-current-stores")
	var again RestoredCurrentStoresResult
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
	afterCounts := counts()
	if beforeCounts != afterCounts {
		t.Fatal("store checks changed business/authority/audit counts")
	}
	put("business-counts.json", map[string]any{"before": beforeCounts, "after": afterCounts, "unchanged": true, "nonempty_dataset_versions": beforeCounts.DatasetVersions, "nonempty_model_weight_coverage": beforeCounts.ReadyModels > 0})

	if _, e = operator(ctx, "stop-runtime"); e != nil {
		t.Fatal("scoped final stop", e)
	}
	final := status(ctx)
	if !sameOriginalProofs(final) || final.RestoredCurrentStores == nil || final.RestoredCurrentStores.ProofSHA256 != recovered.ProofSHA256 {
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
	put("actual.json", map[string]any{"status": "passed_actual_restored_current_stores_checks", "scope_id": retained.ScopeID, "generation_id": recovered.GenerationID, "proof_sha256": recovered.ProofSHA256, "control_id": control.ID, "postgres_id": postgres.ID, "model_task_start_calls_before_native_reconcile": loss.calls, "task_intents": intents, "task_completions": completed, "original_proofs_unchanged": true, "original_configuration_runtime_network_unchanged": true, "generated_configuration_removed": true, "completed_repeat_journal_head_unchanged": true, "application_never_started": true, "postgres_stopped": true, "SQL_requalification": currentSQL, "business_authority_audit_counts_unchanged": true, "dataset_versions_checked": beforeCounts.DatasetVersions, "ready_models_checked": beforeCounts.ReadyModels, "qualification": "actual_local_selected_store_verification_not_application_browser_or_native_target"})
}
