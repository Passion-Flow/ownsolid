//go:build offlineintegration

package engine

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func runActualRestoredQualification(t *testing.T, f actualStoreRestoreFixture, payload []ConfigurationFile, prepare func(Ownership, []ConfigurationFile, bool), setActive func(*DatabaseTask, string)) []map[string]any {
	t.Helper()
	ctx, c := f.Context, f.Client
	operatorInput := Ownership{f.ScopeID, f.Nonce(), "operator-config"}
	prepare(operatorInput, payload, false)
	results := []map[string]any{}
	run := func(kind string, success bool) {
		op := f.Nonce()
		task := DatabaseTask{Owner: Ownership{f.ScopeID, op, "database-" + kind}, VolumeOwner: operatorInput, Kind: kind, Image: f.Image, UID: f.UID, GID: f.GID, NetworkID: f.NetworkID, Peers: f.Peers}
		if task.restoredAuthority() {
			task.VolumeOwner = Ownership{f.ScopeID, op, StoreRestoreConfigRole + "-" + op}
			task.Reason = "M17 isolated restored authority acceptance"
			prepare(task.VolumeOwner, payload, false)
		}
		label := kind + "-" + op[:8]
		f.Put(label+"-intent.json", task)
		setActive(&task, "")
		v, e := c.CreateDatabaseTask(ctx, task)
		setActive(&task, v.ID)
		if e != nil {
			t.Fatal("actual restored qualification create", e)
		}
		f.Put(label+"-created.json", map[string]string{"id": v.ID})
		id := v.ID
		observer, e := c.ObserveDatabaseTaskOutput(ctx, task, id)
		if e != nil {
			t.Fatal("exact pre-start diagnostic attach", e)
		}
		defer observer.Close()
		transport := c.http.Transport
		loss := &restoreResultLoss{base: transport, method: http.MethodPost, suffix: "/start"}
		if task.restoredAuthority() {
			c.http.Transport = loss
		}
		_, e = c.StartDatabaseTask(ctx, task, id, payload, 30)
		c.http.Transport = transport
		if task.restoredAuthority() {
			if e == nil || loss.hits != 1 {
				t.Fatal("actual authority successful start result loss missing", e)
			}
		} else if e != nil {
			t.Fatal("actual qualification start", e)
		}
		for deadline := time.Now().Add(30 * time.Second); ; {
			var found bool
			v, found, e = c.InspectDatabaseTask(ctx, task)
			if e != nil || !found {
				t.Fatal(e)
			}
			if v.State.Status == "exited" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("actual qualification deadline")
			}
			time.Sleep(100 * time.Millisecond)
		}
		f.Put(label+"-outcome.json", map[string]any{"id": id, "exit_code": v.State.ExitCode, "expected_success": success, "oom_killed": v.State.OOMKilled})
		output, observeError := observer.Wait(ctx)
		if observeError != nil {
			t.Fatal("bounded safe task output", observeError)
		}
		f.Put(label+"-output.json", output)
		if kind == DatabaseRuntimePrepare && !success && output.StderrCode != "RUNTIME_ACCESS_INITIAL_INSTALLATION_REQUIRED" {
			t.Fatal("fresh guard failure category", output.StderrCode)
		}
		if (v.State.ExitCode == 0) != success || v.State.OOMKilled {
			t.Fatal("actual qualification outcome", kind, v.State.ExitCode)
		}
		if _, e = c.StartDatabaseTask(ctx, task, id, payload, 30); e == nil {
			t.Fatal("terminal qualification replay")
		}
		if e = c.RemoveDatabaseTask(ctx, task, id); e != nil {
			t.Fatal(e)
		}
		setActive(nil, "")
		results = append(results, map[string]any{"kind": kind, "exit_code": v.State.ExitCode, "expected_success": success, "successful_start_result_loss_reconciled_without_resend": task.restoredAuthority()})
	}
	run(DatabaseSchemaCheck, true)
	run(DatabaseRuntimePrepare, false)
	run(DatabaseRuntimeRestorePrepare, true)
	run(DatabaseRuntimeCheck, true)
	runtimeRole := "restore_runtime_" + f.ScopeID[:8]
	if _, e := f.Query("ALTER ROLE " + runtimeRole + " CREATEDB"); e != nil {
		t.Fatal("synthetic new-role privilege mutation", e)
	}
	run(DatabaseRuntimeCheck, false)
	if _, e := f.Query("ALTER ROLE " + runtimeRole + " NOCREATEDB"); e != nil {
		t.Fatal("restore exact synthetic role flag", e)
	}
	run(DatabaseRuntimeCheck, true)
	// Password hashes stay only in this private test process and are never
	// written to evidence, argv or logs. Compare before/after without printing.
	passwords, e := f.Query("SELECT string_agg(user_id::text||':'||password_phc,'|' ORDER BY user_id) FROM public.password_credentials")
	if e != nil {
		t.Fatal(e)
	}
	epoch, e := f.Query("SELECT sum(auth_epoch) FROM public.users")
	if e != nil {
		t.Fatal(e)
	}
	beforeEpoch, e := strconv.Atoi(epoch)
	if e != nil {
		t.Fatal(e)
	}
	audits, e := f.Query("SELECT count(*) FROM public.audit_events WHERE audience='operator' AND action='operator.auth_invalidated'")
	if e != nil {
		t.Fatal(e)
	}
	beforeAudits, e := strconv.Atoi(audits)
	if e != nil {
		t.Fatal(e)
	}
	// Explicit synthetic authority rows, never actual mail or invitations.
	seed := `WITH u AS (SELECT id,auth_epoch FROM public.users ORDER BY id LIMIT 1),
s AS (INSERT INTO public.sessions(id,user_id,audience,token_hash,auth_epoch,created_at,last_seen_at,absolute_expires_at) SELECT gen_random_uuid(),id,'portal',decode(repeat('01',32),'hex'),auth_epoch,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '1 hour' FROM u RETURNING id),
r AS (INSERT INTO public.password_reset_tokens(id,user_id,audience,token_hash,created_at,expires_at) SELECT gen_random_uuid(),id,'portal',decode(repeat('02',32),'hex'),clock_timestamp(),clock_timestamp()+interval '1 hour' FROM u RETURNING id,user_id),
i AS (INSERT INTO public.invitations(id,tenant_id,workspace_id,email_canonical,locale,role,status,token_hash,created_by,created_at,updated_at,expires_at) SELECT gen_random_uuid(),w.tenant_id,w.id,'synthetic-restored@example.invalid','en','member','pending',decode(repeat('03',32),'hex'),u.id,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '1 hour' FROM u CROSS JOIN public.workspaces w ORDER BY w.id LIMIT 1 RETURNING id),
m AS (INSERT INTO public.mail_deliveries(id,purpose,recipient_email,locale,reset_token_id,user_id,state,payload_key_id,payload_nonce,payload_ciphertext,next_attempt_at,message_id,created_at,updated_at) SELECT gen_random_uuid(),'password_reset','synthetic-restored@example.invalid','en',r.id,r.user_id,'queued','explicit-synthetic-payload',decode(repeat('00',12),'hex'),decode('00','hex'),clock_timestamp(),gen_random_uuid()::text||'@example.invalid',clock_timestamp(),clock_timestamp() FROM r RETURNING id)
SELECT (SELECT count(*) FROM s)||'|'||(SELECT count(*) FROM r)||'|'||(SELECT count(*) FROM i)||'|'||(SELECT count(*) FROM m)`
	if count, e := f.Query(seed); e != nil || count != "1|1|1|1" {
		t.Fatal("explicit restored authority seed", e)
	}
	run(DatabaseRestoredAuthInvalidate, true)
	remaining, e := f.Query("SELECT (SELECT count(*) FROM public.sessions WHERE revoked_at IS NULL)||'|'||(SELECT count(*) FROM public.password_reset_tokens WHERE consumed_at IS NULL AND revoked_at IS NULL)||'|'||(SELECT count(*) FROM public.invitations WHERE status='pending')||'|'||(SELECT count(*) FROM public.mail_deliveries WHERE state IN ('queued','sending'))")
	if e != nil || remaining != "0|0|0|0" {
		t.Fatal("restored executable authority survived", e)
	}
	after, e := f.Query("SELECT string_agg(user_id::text||':'||password_phc,'|' ORDER BY user_id) FROM public.password_credentials")
	if e != nil || after != passwords {
		t.Fatal("restored password/user identity changed", e)
	}
	afterEpoch, e := f.Query("SELECT sum(auth_epoch) FROM public.users")
	if e != nil || afterEpoch != strconv.Itoa(beforeEpoch+1) {
		t.Fatal("restored auth epoch mismatch", e)
	}
	afterAudits, e := f.Query("SELECT count(*) FROM public.audit_events WHERE audience='operator' AND action='operator.auth_invalidated'")
	if e != nil || afterAudits != strconv.Itoa(beforeAudits+1) {
		t.Fatal("exact authority audit missing", e)
	}
	f.Put("restored-qualification.json", map[string]any{"schema_checked": true, "distinct_runtime_role_prepared_and_authenticated": true, "excess_createdb_privilege_refused": true, "explicit_test_flag_restored_and_reverified": true, "seeded_session_reset_invitation_mail_invalidated": true, "users_and_password_hashes_unchanged": true, "auth_epoch_increment": 1, "exact_operator_audit_increment": 1, "no_messages_sent": true, "work_and_shared_cleanup_seconds": []int{70, 70}, "outer_go_timeout_seconds": 150, "qualification": "real_selected_schema_SQL_access_and_authority_only_not_browser_full_scope_restore_or_native_target"})
	return results
}
