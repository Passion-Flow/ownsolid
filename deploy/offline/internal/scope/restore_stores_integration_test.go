//go:build offlineintegration

package scope

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Real SDK effects with explicitly test-only loss after genuine completion.
// No fault switch exists in the product configuration or native CLI.
type retainedStoreResponseLoss struct {
	*engine.Client
	copied, started bool
	copies, starts  int
}

func (c *retainedStoreResponseLoss) PutStoreRestoreArchive(ctx context.Context, task engine.DatabaseTask, image string, f *os.File) (engine.StoreRestoreProfile, error) {
	result, e := c.Client.PutStoreRestoreArchive(ctx, task, image, f)
	if task.Kind == engine.ModelFilesImport {
		c.copies++
		if e == nil && !c.copied {
			c.copied = true
			return result, errors.New("SYNTHETIC_REAL_STORE_COPY_RESPONSE_LOST")
		}
	}
	return result, e
}
func (c *retainedStoreResponseLoss) StartDatabaseTask(ctx context.Context, task engine.DatabaseTask, id string, files []engine.ConfigurationFile, reserve int) (engine.ResourceAdmission, error) {
	result, e := c.Client.StartDatabaseTask(ctx, task, id, files, reserve)
	if task.Kind == engine.ModelFilesImport {
		c.starts++
		if e == nil && !c.started {
			c.started = true
			return result, errors.New("SYNTHETIC_REAL_STORE_START_RESPONSE_LOST")
		}
	}
	return result, e
}
func TestActualReservedStoresOnRetainedDatabaseTarget(t *testing.T) {
	name := os.Getenv("PROJECT_ONE_TEST_RESTORED_STORES_RETENTION")
	if name == "" {
		t.Skip("explicit qualified isolated SQL/database target required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	read := func(name string, out any) []byte {
		raw, e := bundle.ReadProtectedConfiguration(name)
		if e != nil || wire.UniqueJSON(raw, out) != nil {
			t.Fatal("protected synthetic fixture required", e)
		}
		return raw
	}
	var retained struct {
		ScopeID     string `json:"scope_id"`
		Config      string `json:"operator_config"`
		Request     string `json:"restore_request"`
		Qualified   bool   `json:"qualified_database"`
		Observation Status `json:"status_observation"`
	}
	read(name, &retained)
	if !retained.Qualified || retained.Observation.RestoreDatabase == nil || retained.Observation.RestoreSQL == nil {
		t.Fatal("accepted database/SQL retention required")
	}
	raw, e := bundle.ReadProtectedConfiguration(retained.Config)
	if e != nil {
		t.Fatal(e)
	}
	config, e := configuration.Load(raw, nil)
	if e != nil || !config.Verification.AllowTestFixture || config.Engine == nil || config.Compose == nil {
		t.Fatal("explicit isolated configuration", e)
	}
	raw, e = bundle.ReadProtectedConfiguration(retained.Request)
	if e != nil {
		t.Fatal(e)
	}
	request, e := configuration.LoadRestoreRequest(raw, nil)
	if e != nil {
		t.Fatal(e)
	}
	modelRaw, e := bundle.ReadProtectedConfiguration(config.Compose.ModelFile)
	if e != nil {
		t.Fatal(e)
	}
	model, e := compose.DecodeModel(modelRaw, config.Verification.MaximumMetadataBytes)
	if e != nil {
		t.Fatal(e)
	}
	out := os.Getenv("PROJECT_ONE_TEST_RESTORED_STORES_EVIDENCE")
	binary := os.Getenv("PROJECT_ONE_TEST_RESTORED_STORES_OPERATOR")
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
	operator := func(c context.Context, action string, operation bool) ([]byte, error) {
		args := []string{action, "--config", retained.Config}
		if operation {
			args = append(args, "--operation-file", retained.Request)
		}
		return exec.CommandContext(c, binary, args...).CombinedOutput()
	}
	value, e := operator(ctx, "status", false)
	var before Status
	if e != nil || wire.StrictJSON(value, &before) != nil || before.ScopeID != retained.ScopeID || before.Purpose != purpose || before.RestoreStores != nil || before.RestoreDatabase == nil || before.RestoreSQL == nil || before.RestoreDatabase.ProofSHA256 != retained.Observation.RestoreDatabase.ProofSHA256 || before.RestoreSQL.ProofSHA256 != retained.Observation.RestoreSQL.ProofSHA256 {
		t.Fatal("unchanged qualified target without store attempt required", e)
	}
	l, e := open(config, false)
	if e != nil {
		t.Fatal(e)
	}
	creation := runtimeState(l.events).record
	pgID := creation.Services["postgres"].ID
	l.close()
	private := filepath.Join(out, "docker-client")
	if os.Mkdir(private, 0700) != nil || os.WriteFile(filepath.Join(private, "config.json"), []byte("{}\n"), 0600) != nil {
		t.Fatal("private Docker client")
	}
	pg := model.Services["postgres"]
	query := func(statement string) (string, error) {
		command := exec.CommandContext(ctx, config.Compose.DockerBinary, "--config", private, "--host", "unix://"+config.Engine.SocketFile, "exec", "--user", pg.User, pgID, "psql", "-X", "--set", "ON_ERROR_STOP=1", "-U", pg.Environment["POSTGRES_USER"], "-d", pg.Environment["POSTGRES_DB"], "-At", "-c", statement)
		value, e := command.Output()
		if e != nil {
			return "", errors.New("SYNTHETIC_RETAINED_STORE_QUERY_FAILED")
		}
		return strings.TrimSpace(string(value)), nil
	}
	stageStarted, qualified := false, false
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 60*time.Second)
		defer done()
		if stageStarted && !qualified {
			if _, e := operator(cleanup, "abort-restored-stores", true); e != nil {
				t.Errorf("generated store cleanup unresolved: %v", e)
			}
		}
		if _, e := operator(cleanup, "stop-runtime", false); e != nil {
			t.Errorf("retained scope stop unresolved: %v", e)
		}
		value, e := operator(cleanup, "status", false)
		var final Status
		if e != nil || wire.StrictJSON(value, &final) != nil {
			t.Errorf("final retained status unresolved: %v", e)
			return
		}
		put("retention.json", map[string]any{"status": "retained_selected_store_stage_recorded_target_stopped", "scope_id": retained.ScopeID, "operator_config": retained.Config, "restore_request": retained.Request, "native_operator": binary, "qualified_stores": qualified, "status_observation": final, "qualification": "SQL_database_selected_stores_only_not_activation_browser_full_profile_or_native_target"})
	}()
	if value, e = operator(ctx, "start-database", false); e != nil {
		t.Fatal("recorded PG-only start", e, string(value))
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		if v, e := query("SELECT 1"); e == nil && v == "1" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("PG readiness canceled")
		case <-deadline.C:
			t.Fatal("PG readiness expired")
		case <-time.After(200 * time.Millisecond):
		}
	}
	inventorySQL := "SELECT (SELECT count(*) FROM public.model_versions)||'|'||(SELECT count(*) FROM public.users)||'|'||(SELECT count(*) FROM public.datasets)||'|'||(SELECT count(*) FROM public.dataset_versions)||'|'||(SELECT max(version) FROM public.schema_migrations)"
	inventory, e := query(inventorySQL)
	if e != nil || inventory != "0|1|1|2|16" {
		t.Fatal("selected original inventory required", e)
	}
	passwordSQL := "SELECT string_agg(user_id::text||':'||password_phc,'|' ORDER BY user_id) FROM public.password_credentials"
	passwords, e := query(passwordSQL)
	if e != nil || passwords == "" {
		t.Fatal("retained passwords unavailable", e)
	}
	epochSQL := "SELECT sum(auth_epoch) FROM public.users"
	epoch, e := query(epochSQL)
	if e != nil {
		t.Fatal(e)
	}
	authSQL := "SELECT count(*) FROM public.audit_events WHERE audience='operator' AND action='operator.auth_invalidated'"
	authAudits, e := query(authSQL)
	if e != nil {
		t.Fatal(e)
	}
	audits := map[string]int{}
	for _, action := range []string{"model.files_imported", "dataset.files_imported"} {
		v, e := query("SELECT count(*) FROM public.audit_events WHERE audience='operator' AND action='" + action + "'")
		if e != nil {
			t.Fatal(e)
		}
		n, e := strconv.Atoi(v)
		if e != nil {
			t.Fatal("invalid import audit")
		}
		audits[action] = n
	}
	client, e := engine.New(*config.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	worker, e := compose.New(config)
	if e != nil {
		t.Fatal(e)
	}
	loss := &retainedStoreResponseLoss{Client: client}
	stageStarted = true
	if out, e := runRestoreStores(ctx, config, request, "restore-stores", loss, worker); e == nil || e.Error() != "SYNTHETIC_REAL_STORE_COPY_RESPONSE_LOST" || !out.Pending || !loss.copied || loss.copies != 1 {
		t.Fatal("genuine copied source loss not preserved", out, e)
	}
	if value, e = operator(ctx, "restore-stores", true); e == nil || strings.TrimSpace(string(value)) != "OFFLINE_SCOPE_RESTORE_STORES_RECONCILIATION_REQUIRED" {
		t.Fatal("uncertain copied input was resubmitted", e, string(value))
	}
	if out, e := runRestoreStores(ctx, config, request, "reconcile-restored-stores", loss, worker); e == nil || e.Error() != "SYNTHETIC_REAL_STORE_START_RESPONSE_LOST" || !out.Pending || !loss.started || loss.starts != 1 || loss.copies != 1 {
		t.Fatal("genuine successful start loss not preserved", out, e)
	}
	var result RestoreStoresResult
	commands := []string{"restore-stores_with_test_only_real_copy_loss", "reconcile_with_test_only_real_start_loss"}
	for {
		commands = append(commands, "reconcile-restored-stores")
		value, e = operator(ctx, "reconcile-restored-stores", true)
		if e == nil {
			if wire.StrictJSON(value, &result) != nil || result.Pending || result.ProofSHA256 == "" || result.CompletedSteps != 29 {
				t.Fatal("incomplete selected store result")
			}
			break
		}
		if strings.TrimSpace(string(value)) != "OFFLINE_SCOPE_RESTORE_STORES_TASK_DEADLINE" {
			t.Fatal("selected store reconcile unresolved", strings.TrimSpace(string(value)))
		}
	}
	qualified = true
	after, e := query(inventorySQL)
	if e != nil || after != inventory {
		t.Fatal("original SQL history changed", e)
	}
	after, e = query(passwordSQL)
	if e != nil || after != passwords {
		t.Fatal("original passwords changed", e)
	}
	after, e = query(epochSQL)
	if e != nil || after != epoch {
		t.Fatal("auth epoch replayed", e)
	}
	after, e = query(authSQL)
	if e != nil || after != authAudits {
		t.Fatal("auth invalidation replayed", e)
	}
	for action, n := range audits {
		v, e := query("SELECT count(*) FROM public.audit_events WHERE audience='operator' AND action='" + action + "'")
		if e != nil || v != strconv.Itoa(n+1) {
			t.Fatal("exact one import audit missing", action, e)
		}
	}
	journal, e := os.ReadFile(filepath.Join(config.ScopeRoot, journalName))
	if e != nil {
		t.Fatal(e)
	}
	value, e = operator(ctx, "restore-stores", true)
	var repeat RestoreStoresResult
	if e != nil || wire.StrictJSON(value, &repeat) != nil || repeat != result {
		t.Fatal("terminal native store repeat changed result", e)
	}
	again, e := os.ReadFile(filepath.Join(config.ScopeRoot, journalName))
	if e != nil || string(again) != string(journal) {
		t.Fatal("terminal store repeat changed journal", e)
	}
	value, e = operator(ctx, "start-runtime", false)
	if e == nil || strings.TrimSpace(string(value)) != "OFFLINE_SCOPE_RESTORE_EXECUTION_OR_ABORT_REQUIRED" {
		t.Fatal("store proof opened application activation", e, string(value))
	}
	l, e = open(config, false)
	if e != nil {
		t.Fatal(e)
	}
	state := restoreStoresState(l.events)
	if state.record == nil {
		l.close()
		t.Fatal("store journal absent")
	}
	plan, e := l.readRestoreStoresPlan()
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	intentCounts := map[string]int{}
	for _, event := range l.events {
		if event.RestoreStores != nil && event.Action == "restore_stores_effect" && event.Outcome == "started" && event.RestoreStores.Phase == "task-execute" {
			intentCounts[event.RestoreStores.Kind]++
		}
	}
	l.close()
	if len(intentCounts) != 4 {
		t.Fatal("fixed four starts absent")
	}
	for _, n := range intentCounts {
		if n != 1 {
			t.Fatal("start intent repeated")
		}
	}
	for _, job := range plan.Jobs {
		for _, task := range []engine.DatabaseTask{job.Import, job.Verify} {
			if _, found, e := client.InspectDatabaseTask(ctx, task); e == nil && found {
				t.Fatal("completed generated task retained")
			}
		}
		for _, owner := range []engine.Ownership{job.Import.VolumeOwner, *job.Import.SourceOwner} {
			if _, found, e := client.InspectVolume(ctx, owner); e != nil || found {
				t.Fatal("generated store input retained", e)
			}
		}
	}
	objects, e := client.RuntimeCandidates(ctx, retained.ScopeID, false)
	if e != nil {
		t.Fatal("exact generated actor absence", e)
	}
	expected := map[string]bool{}
	for _, service := range creation.Services {
		expected[service.ID] = true
	}
	if len(objects) != len(expected) {
		t.Fatal("unexpected retained generated actors")
	}
	for _, object := range objects {
		if !expected[object.ID] {
			t.Fatal("unknown actor retained")
		}
	}
	put("actual.json", map[string]any{"status": "passed_actual_reserved_selected_stores_on_retained_database_target", "scope_id": retained.ScopeID, "store_result": result, "SQL_proof_sha256": before.RestoreSQL.ProofSHA256, "database_proof_sha256": before.RestoreDatabase.ProofSHA256, "inventory": inventory, "passwords_history_epochs_unchanged": true, "auth_invalidation_not_replayed": true, "each_import_audit_increment": 1, "real_copy_and_start_response_loss_reconciled_without_resend": true, "model_copy_calls": loss.copies, "model_start_calls": loss.starts, "start_intents": intentCounts, "terminal_repeat_journal_identical": true, "activation_refused": true, "generated_inputs_absent": true, "commands": commands, "model_catalog_qualification": "empty_existing_catalog_only_no_nonempty_model_weight_claim", "qualification": "real_local_selected_file_backend_and_controller_only_not_activation_browser_full_profile_or_native_target"})
}
