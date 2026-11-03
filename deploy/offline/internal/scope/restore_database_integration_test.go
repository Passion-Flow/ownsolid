//go:build offlineintegration

package scope

import (
	"context"
	"encoding/json"
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
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestActualRetainedSQLTargetDatabaseQualification(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_RESTORED_DATABASE_RETENTION") == "" {
		t.Skip("explicit isolated retained SQL target required")
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
		ScopeID   string                `json:"scope_id"`
		Status    string                `json:"status"`
		Config    string                `json:"operator_config"`
		Request   string                `json:"restore_request"`
		Resources compose.RuntimeStatus `json:"resources"`
	}
	read(os.Getenv("PROJECT_ONE_TEST_RESTORED_DATABASE_RETENTION"), &retained)
	var accepted struct {
		ScopeID  string `json:"scope_id"`
		Status   string `json:"status"`
		SQLProof string `json:"SQL_proof_sha256"`
		Counts   string `json:"restored_counts"`
	}
	read(os.Getenv("PROJECT_ONE_TEST_RESTORED_DATABASE_ACCEPTANCE"), &accepted)
	if retained.Status != "retained_exact_SQL_restored_scope_stopped_qualification_pending" || accepted.Status != "passed_actual_new_scope_durable_SQL_restore_other_qualification_pending" || retained.ScopeID != accepted.ScopeID || !wire.ValidSHA256(accepted.SQLProof) {
		t.Fatal("independently accepted SQL target required")
	}
	raw, e := bundle.ReadProtectedConfiguration(retained.Config)
	if e != nil {
		t.Fatal(e)
	}
	config, e := configuration.Load(raw, nil)
	if e != nil || !config.Verification.AllowTestFixture || config.Engine == nil || config.Compose == nil {
		t.Fatal("explicit isolated fixture configuration required", e)
	}
	modelRaw, e := bundle.ReadProtectedConfiguration(config.Compose.ModelFile)
	if e != nil {
		t.Fatal(e)
	}
	model, e := compose.DecodeModel(modelRaw, config.Verification.MaximumMetadataBytes)
	if e != nil {
		t.Fatal(e)
	}
	out := os.Getenv("PROJECT_ONE_TEST_RESTORED_DATABASE_EVIDENCE")
	binary := os.Getenv("PROJECT_ONE_TEST_RESTORED_DATABASE_OPERATOR")
	if !privateDirectory(out) || !canonical(binary) {
		t.Fatal("private evidence/current native operator required")
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
	operator := func(commandContext context.Context, action string, operation bool) ([]byte, error) {
		args := []string{action, "--config", retained.Config}
		if operation {
			args = append(args, "--operation-file", retained.Request)
		}
		return exec.CommandContext(commandContext, binary, args...).CombinedOutput()
	}
	var before Status
	value, e := operator(ctx, "status", false)
	if e != nil || wire.StrictJSON(value, &before) != nil || before.ScopeID != retained.ScopeID || before.Purpose != purpose || before.RestoreSQL == nil || before.RestoreSQL.ProofSHA256 != accepted.SQLProof || before.RestoreDatabase != nil || before.BootstrapQualified {
		t.Fatal("unchanged stopped SQL-only target required", e)
	}
	private := filepath.Join(out, "docker-client")
	if os.Mkdir(private, 0700) != nil || os.WriteFile(filepath.Join(private, "config.json"), []byte("{}\n"), 0600) != nil {
		t.Fatal("private Docker client")
	}
	pg := model.Services["postgres"]
	pgID := retained.Resources.Services["postgres"].ID
	query := func(statement string) (string, error) {
		command := exec.CommandContext(ctx, config.Compose.DockerBinary, "--config", private, "--host", "unix://"+config.Engine.SocketFile, "exec", "--user", pg.User, pgID, "psql", "-X", "--set", "ON_ERROR_STOP=1", "-U", pg.Environment["POSTGRES_USER"], "-d", pg.Environment["POSTGRES_DB"], "-At", "-c", statement)
		data, e := command.Output()
		if e != nil {
			return "", errors.New("SYNTHETIC_RESTORED_DATABASE_QUERY_FAILED")
		}
		return strings.TrimSpace(string(data)), nil
	}
	stageStarted, qualified := false, false
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 60*time.Second)
		defer done()
		if stageStarted && !qualified {
			if _, e := operator(cleanup, "abort-restored-database", true); e != nil {
				t.Errorf("recorded database cleanup requires attention: %v", e)
			}
		}
		if _, e := operator(cleanup, "stop-runtime", false); e != nil {
			t.Errorf("retained runtime stop requires attention: %v", e)
		}
		value, e := operator(cleanup, "status", false)
		var final Status
		if e != nil || wire.StrictJSON(value, &final) != nil {
			t.Errorf("final retained status requires attention: %v", e)
			return
		}
		put("retention.json", map[string]any{"status": "retained_original_SQL_target_stopped_database_stage_recorded", "scope_id": retained.ScopeID, "operator_config": retained.Config, "operator_config_sha256": wire.Digest(raw), "restore_request": retained.Request, "native_operator": binary, "qualified_database": qualified, "status_observation": final, "qualification": "SQL_and_database_stage_only_files_activation_browser_native_target_pending"})
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
	inventorySQL := "SELECT (SELECT count(*) FROM public.users)||'|'||(SELECT count(*) FROM public.datasets)||'|'||(SELECT count(*) FROM public.dataset_versions)||'|'||(SELECT max(version) FROM public.schema_migrations)"
	inventory, e := query(inventorySQL)
	if e != nil || inventory != accepted.Counts {
		t.Fatal("retained SQL inventory changed", e)
	}
	passwordSQL := "SELECT string_agg(user_id::text||':'||password_phc,'|' ORDER BY user_id) FROM public.password_credentials"
	passwords, e := query(passwordSQL)
	if e != nil || passwords == "" {
		t.Fatal("restored credentials unavailable", e)
	}
	epochSQL := "SELECT sum(auth_epoch) FROM public.users"
	epoch, e := query(epochSQL)
	if e != nil {
		t.Fatal(e)
	}
	beforeEpoch, e := strconv.Atoi(epoch)
	if e != nil {
		t.Fatal("invalid synthetic epoch")
	}
	auditSQL := "SELECT count(*) FROM public.audit_events WHERE audience='operator' AND action='operator.auth_invalidated'"
	audit, e := query(auditSQL)
	if e != nil {
		t.Fatal(e)
	}
	beforeAudit, e := strconv.Atoi(audit)
	if e != nil {
		t.Fatal("invalid synthetic audit count")
	}
	stageStarted = true
	action := "qualify-restored-database"
	var result RestoreDatabaseResult
	commands := []string{}
	for {
		commands = append(commands, action)
		value, e = operator(ctx, action, true)
		if e == nil {
			if wire.StrictJSON(value, &result) != nil || result.ProofSHA256 == "" || result.CompletedSteps != len(restoreDatabaseSteps()) || result.Pending {
				t.Fatal("incomplete database result")
			}
			break
		}
		code := strings.TrimSpace(string(value))
		if code != "OFFLINE_SCOPE_RESTORE_DATABASE_TASK_DEADLINE" {
			t.Fatal("database qualification unresolved", code)
		}
		action = "reconcile-restored-database"
	}
	qualified = true
	afterInventory, e := query(inventorySQL)
	if e != nil || afterInventory != inventory {
		t.Fatal("restored business inventory changed", e)
	}
	afterPasswords, e := query(passwordSQL)
	if e != nil || afterPasswords != passwords {
		t.Fatal("restored passwords changed", e)
	}
	afterEpoch, e := query(epochSQL)
	if e != nil || afterEpoch != strconv.Itoa(beforeEpoch+1) {
		t.Fatal("restored auth epoch did not increment once", e)
	}
	afterAudit, e := query(auditSQL)
	if e != nil || afterAudit != strconv.Itoa(beforeAudit+1) {
		t.Fatal("restored authority audit did not increment once", e)
	}
	authority, e := query("SELECT (SELECT count(*) FROM public.sessions WHERE revoked_at IS NULL)||'|'||(SELECT count(*) FROM public.password_reset_tokens WHERE consumed_at IS NULL AND revoked_at IS NULL)||'|'||(SELECT count(*) FROM public.invitations WHERE status='pending')||'|'||(SELECT count(*) FROM public.mail_deliveries WHERE state IN ('queued','sending'))")
	if e != nil || authority != "0|0|0|0" {
		t.Fatal("old executable authority survived", e)
	}
	journal, e := os.ReadFile(filepath.Join(config.ScopeRoot, journalName))
	if e != nil {
		t.Fatal(e)
	}
	value, e = operator(ctx, "qualify-restored-database", true)
	var repeat RestoreDatabaseResult
	if e != nil || wire.StrictJSON(value, &repeat) != nil || repeat != result {
		t.Fatal("terminal database repeat changed result", e)
	}
	again, e := os.ReadFile(filepath.Join(config.ScopeRoot, journalName))
	if e != nil || string(journal) != string(again) {
		t.Fatal("terminal database repeat changed journal", e)
	}
	value, e = operator(ctx, "start-runtime", false)
	if e == nil || strings.TrimSpace(string(value)) != "OFFLINE_SCOPE_RESTORE_EXECUTION_OR_ABORT_REQUIRED" {
		t.Fatal("DB-only stage opened application activation", e, string(value))
	}
	intents := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(journal)), "\n") {
		var event Event
		if json.Unmarshal([]byte(line), &event) != nil {
			t.Fatal("journal observation")
		}
		if event.RestoreDatabase != nil && event.Action == "restore_database_effect" && event.Outcome == "started" && event.RestoreDatabase.Phase == "task-execute" {
			intents[event.RestoreDatabase.Kind]++
		}
	}
	if len(intents) != 4 {
		t.Fatal("fixed four command intents absent")
	}
	for _, n := range intents {
		if n != 1 {
			t.Fatal("task start intent repeated")
		}
	}
	put("actual.json", map[string]any{"status": "passed_actual_retained_SQL_target_database_stage", "scope_id": retained.ScopeID, "SQL_proof_sha256": accepted.SQLProof, "database_result": result, "inventory": inventory, "original_passwords_unchanged": true, "auth_epoch_increment": 1, "authority_audit_increment": 1, "old_executable_authority_remaining": authority, "start_intents": intents, "commands": commands, "terminal_repeat_journal_identical": true, "application_activation_refused": true, "qualification": "actual_local_amd64_emulated_schema_runtime_role_login_auth_only_files_activation_browser_native_target_pending"})
}
