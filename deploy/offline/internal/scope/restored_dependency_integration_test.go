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

// Only the response after a successful real selected Compose start is lost.
// Real Docker metadata, PKI, mounted files and process effects remain in use.
type actualRestoredDatabaseStartLoss struct {
	*compose.Client
	calls int
	lost  bool
}

func (w *actualRestoredDatabaseStartLoss) StartDatabaseRuntime(ctx context.Context, client compose.RuntimeReader, owner compose.RenderContext, model compose.Model, before compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	w.calls++
	out, e := w.Client.StartDatabaseRuntime(ctx, client, owner, model, before)
	if e == nil && !w.lost {
		w.lost = true
		return out, errors.New("SYNTHETIC_REAL_RESTORED_POSTGRES_START_RESPONSE_LOST")
	}
	return out, e
}

func TestActualRetainedRestoredDatabaseDependencyStartLoss(t *testing.T) {
	retention := os.Getenv("PROJECT_ONE_TEST_RESTORED_DEPENDENCY_RETENTION")
	if retention == "" {
		t.Skip("explicit isolated replaced stopped target required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	var retained struct {
		ScopeID     string `json:"scope_id"`
		Config      string `json:"operator_config"`
		TLSRequest  string `json:"TLS_request"`
		Qualified   bool   `json:"qualified_control_replacement"`
		Observation Status `json:"status_observation"`
	}
	raw, e := bundle.ReadProtectedConfiguration(retention)
	if e != nil || wire.UniqueJSON(raw, &retained) != nil || !retained.Qualified || retained.Observation.ControlReplacement == nil || retained.Observation.ControlReplacement.Pending || retained.Observation.ControlReplacement.ProofSHA256 == "" {
		t.Fatal("qualified private replaced target required", e)
	}
	raw, e = bundle.ReadProtectedConfiguration(retained.Config)
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture || c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		t.Fatal("isolated protected configuration required", e)
	}
	out, binary := os.Getenv("PROJECT_ONE_TEST_RESTORED_DEPENDENCY_EVIDENCE"), os.Getenv("PROJECT_ONE_TEST_RESTORED_DEPENDENCY_OPERATOR")
	if !privateDirectory(out) || !canonical(binary) {
		t.Fatal("private evidence/current native required")
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
	if beforeStatus.ScopeID != retained.ScopeID || beforeStatus.Purpose != purpose || !reflect.DeepEqual(beforeStatus.ControlReplacement, retained.Observation.ControlReplacement) || !reflect.DeepEqual(beforeStatus.RestoreSQL, retained.Observation.RestoreSQL) || !reflect.DeepEqual(beforeStatus.RestoreDatabase, retained.Observation.RestoreDatabase) || !reflect.DeepEqual(beforeStatus.RestoreStores, retained.Observation.RestoreStores) || !reflect.DeepEqual(beforeStatus.TLSProjection, retained.Observation.TLSProjection) {
		t.Fatal("original restoration/replacement lineage changed")
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
	for _, service := range before.observed.Services {
		if service.State != "created" && service.State != "exited" || service.ExitCode != 0 {
			l.close()
			t.Fatal("clean stopped original target required")
		}
	}
	if before.observed.Services["control"].State != "created" {
		l.close()
		t.Fatal("control must remain never started")
	}
	originalRuntime := encode(*runtimeState(l.events).record)
	effectiveRuntime := encode(*effectiveRuntimeState(l.events).record)
	descriptor := wire.Digest(encode(before.inputs.descriptor))
	payload := restoreConfigurationDigest(before.inputs.files["control-config"])
	if processState(l.events).pending != "" {
		l.close()
		t.Fatal("existing process effect requires independent recovery, not test replay")
	}
	for _, event := range l.events {
		if event.Process != nil && event.Process.RestoredDependency != nil {
			l.close()
			t.Fatal("dependency recovery already recorded; continue its retained checkpoint")
		}
	}
	firstSequence := len(l.events)
	l.close()
	qualified := false
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 70*time.Second)
		defer done()
		// Observation can resolve a lost completed start; it never sends start.
		if _, e := operator(cleanup, "reconcile-process"); e != nil {
			t.Errorf("independent process observation unresolved: %v", e)
		}
		if _, e := operator(cleanup, "stop-runtime"); e != nil {
			t.Errorf("scoped dependency stop unresolved: %v", e)
		}
		final := status(cleanup)
		put("retention.json", map[string]any{"status": "retained_restored_dependency_checkpoint_core_stopped_activation_pending", "scope_id": retained.ScopeID, "operator_config": retained.Config, "TLS_request": retained.TLSRequest, "native_operator": binary, "control_replacement_retention": retention, "qualified_dependency_recovery": qualified, "status_observation": final, "qualification": "actual_local_selected_postgres_process_only_not_SQL_readiness_runtime_authority_application_browser_or_native_target"})
	}()
	loss := &actualRestoredDatabaseStartLoss{Client: worker}
	l, e = open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	failed, e := l.processSelectedAdmittedLocked(ctx, client, loss, "running", false, []string{"postgres"}, true)
	l.close()
	if e == nil || !failed.Pending || !loss.lost || loss.calls != 1 {
		t.Fatal("actual successful selected database start response loss required", e)
	}
	for _, action := range []string{"start-restored-database", "start-database", "start-runtime"} {
		if _, e = operator(ctx, action); e == nil {
			t.Fatal("unknown start resubmitted or activation opened", action)
		}
	}
	raw, e = operator(ctx, "reconcile-process")
	var recovered ProcessResult
	if e != nil || wire.StrictJSON(raw, &recovered) != nil || recovered.Pending || recovered.Status != "recorded_process_completed" || recovered.Resources.Services["postgres"].State != "running" || recovered.Resources.Services["control"] != before.observed.Services["control"] {
		t.Fatal("actual native independent completed process observation", e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	current := processState(l.events)
	if current.record == nil || current.record.RestoredDependency == nil || !validDependencyProcess(l.events, *current.record) || !bytes.Equal(effectiveRuntime, encode(*effectiveRuntimeState(l.events).record)) {
		l.close()
		t.Fatal("current dependency proof/effective runtime missing")
	}
	admission := *current.record.RestoredDependency
	journal, e := regular(l.root, journalName, c.MaximumJournalBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	head, e := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	l.close()
	raw, e = operator(ctx, "start-restored-database")
	var repeated ProcessResult
	if e != nil || wire.StrictJSON(raw, &repeated) != nil || repeated.Pending || repeated.Status != "observed_already_running" {
		t.Fatal("already-running native admission repeat", e)
	}
	for _, action := range []string{"start-database", "start-runtime"} {
		if _, e = operator(ctx, action); e == nil {
			t.Fatal("generic activation opened", action)
		}
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	repeatedJournal, je := regular(l.root, journalName, c.MaximumJournalBytes)
	repeatedHead, he := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	l.close()
	if je != nil || he != nil || !bytes.Equal(journal, repeatedJournal) || !bytes.Equal(head, repeatedHead) || loss.calls != 1 {
		t.Fatal("repeat/refusal wrote or resubmitted")
	}
	raw, e = operator(ctx, "stop-runtime")
	var stopped ProcessResult
	if e != nil || wire.StrictJSON(raw, &stopped) != nil || stopped.Pending || stopped.Resources.Services["control"] != before.observed.Services["control"] || stopped.Resources.Services["postgres"].ID != before.observed.Services["postgres"].ID || stopped.Resources.Services["postgres"].State != "exited" || stopped.Resources.Services["postgres"].ExitCode != 0 {
		t.Fatal("real selected dependency stopped without starting control", e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	finalRuntime, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil || descriptor != wire.Digest(encode(finalRuntime.inputs.descriptor)) || payload != restoreConfigurationDigest(finalRuntime.inputs.files["control-config"]) || !bytes.Equal(originalRuntime, encode(*runtimeState(l.events).record)) || !bytes.Equal(effectiveRuntime, encode(*effectiveRuntimeState(l.events).record)) || finalRuntime.observed.NetworkID != before.observed.NetworkID || finalRuntime.observed.IngressNetworkID != before.observed.IngressNetworkID {
		l.close()
		t.Fatal("original configuration/runtime/network record changed", e)
	}
	counts := map[string]int{}
	for _, event := range l.events[firstSequence:] {
		if event.Process == nil || event.Action != "process_start" && event.Action != "process_reconcile" && event.Action != "process_stop" {
			l.close()
			t.Fatal("unrelated restore/import/authority work replayed")
		}
		if event.Outcome == "started" {
			counts[event.Action]++
		}
	}
	if counts["process_start"] != 1 || counts["process_stop"] != 1 {
		l.close()
		t.Fatal("unexpected process intent count", counts)
	}
	endJournal, e := regular(l.root, journalName, c.MaximumJournalBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	l.close()
	raw, e = operator(ctx, "stop-runtime")
	if e != nil || wire.StrictJSON(raw, &stopped) != nil || stopped.Status != "observed_already_stopped" {
		t.Fatal("already-stopped repeat", e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	again, e := regular(l.root, journalName, c.MaximumJournalBytes)
	l.close()
	if e != nil || !bytes.Equal(endJournal, again) {
		t.Fatal("stopped repeat changed journal", e)
	}
	final := status(ctx)
	if !reflect.DeepEqual(final.ControlReplacement, beforeStatus.ControlReplacement) || !reflect.DeepEqual(final.RestoreSQL, beforeStatus.RestoreSQL) || !reflect.DeepEqual(final.RestoreDatabase, beforeStatus.RestoreDatabase) || !reflect.DeepEqual(final.RestoreStores, beforeStatus.RestoreStores) || !reflect.DeepEqual(final.TLSProjection, beforeStatus.TLSProjection) {
		t.Fatal("original restoration proofs changed")
	}
	qualified = true
	put("actual.json", map[string]any{"status": "passed_actual_restored_database_dependency_recovery", "scope_id": retained.ScopeID, "dependency_admission": admission, "start_calls": loss.calls, "real_successful_start_response_loss_observed_without_resend": true, "postgres_id": before.observed.Services["postgres"].ID, "control_id": before.observed.Services["control"].ID, "control_never_started": true, "original_runtime_inputs_networks_and_restore_proofs_unchanged": true, "effect_start_intents": counts, "already_running_repeat_journal_and_head_identical": true, "final_scoped_stop_and_stopped_repeat": true, "qualification": "actual_local_Docker_amd64_emulated_selected_postgres_process_only_not_SQL_readiness_runtime_authority_application_browser_or_native_target"})
}
