//go:build repository_integration && fullserve_browser_integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/bootstrap"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// This private snapshot includes a PHC for equality only. Never write it to
// evidence or diagnostics; retain only its digest and public fixture counts.
type liveBootstrapState struct {
	Complete                         bool
	Administrator, Tenant, Workspace string
	PasswordPHC                      string
	Users, Tenants, Workspaces       int
	Memberships, Audits, Sessions    int
	Mail                             int
}

func liveBootstrapSnapshot(t *testing.T, x *serverCompositionStory) liveBootstrapState {
	t.Helper()
	var s liveBootstrapState
	e := x.f.PostgreSQL.QueryRow(x.ctx, `SELECT bootstrap_complete,
	 COALESCE(primary_admin_user_id::text,''),COALESCE(tenant_id::text,''),COALESCE(default_workspace_id::text,''),
	 COALESCE((SELECT password_phc FROM password_credentials WHERE user_id=primary_admin_user_id),''),
	 (SELECT count(*) FROM users),(SELECT count(*) FROM tenants),(SELECT count(*) FROM workspaces),
	 (SELECT count(*) FROM workspace_memberships),(SELECT count(*) FROM audit_events WHERE action='installation.bootstrapped'),
	 (SELECT count(*) FROM sessions),(SELECT count(*) FROM mail_deliveries)
	 FROM installation_state WHERE id=1`).Scan(&s.Complete, &s.Administrator, &s.Tenant, &s.Workspace,
		&s.PasswordPHC, &s.Users, &s.Tenants, &s.Workspaces, &s.Memberships, &s.Audits, &s.Sessions, &s.Mail)
	if e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_STATE_UNAVAILABLE")
	}
	return s
}

func liveBootstrapDigest(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func liveNativeBootstrap(t *testing.T, x *serverCompositionStory, input liveAccountBrowserInput, operatorConfiguration config.Config) string {
	t.Helper()
	before := liveBootstrapSnapshot(t, x)
	if before != (liveBootstrapState{}) || x.d.pgLease == nil {
		t.Fatal("LIVE_BROWSER_NATIVE_FRESH_BOOTSTRAP_REQUIRED")
	}
	raw, e := yaml.Marshal(operatorConfiguration)
	if e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_CONFIGURATION_FAILED")
	}
	configuration := x.private(t, "native-bootstrap.yaml", raw)
	request := bootstrap.Request{FormatVersion: 1, AdministratorEmail: "admin@example.invalid", DisplayName: "Browser administrator", InstallationName: "Synthetic live browser installation", WorkspaceName: "Default"}
	raw, e = json.Marshal(request)
	if e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_REQUEST_FAILED")
	}
	requestFile := x.private(t, "native-bootstrap-request.json", raw)
	credential := filepath.Join(x.root, "native-bootstrap-credential.json")
	if _, e = os.Lstat(credential); !os.IsNotExist(e) {
		t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_FRESH_OUTPUT_REQUIRED")
	}
	configurationSHA, requestSHA := liveBrowserFileSHA(t, configuration), liveBrowserFileSHA(t, requestFile)
	receiptFile := filepath.Join(input.ResultDirectory, "native-bootstrap.json")
	commands := []map[string]any{}
	receipt := map[string]any{"format": "projectone.fullserve.native-bootstrap.v1", "status": "prepared_not_accepted", "binary_sha256": input.ControlSHA256, "configuration_sha256": configurationSHA, "request_sha256": requestSHA, "commands": commands}
	save := func() {
		t.Helper()
		receipt["commands"] = commands
		raw, e := json.Marshal(receipt)
		if e != nil {
			t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_RECEIPT_FAILED")
		}
		liveWriteBrowserEvidence(t, receiptFile, raw)
	}
	save()
	if e = x.d.Close(); e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_LEASE_RELEASE_FAILED")
	}
	call := func(stage, command string, lost bool, expectedCode int, expectedOutput string) {
		t.Helper()
		if liveBrowserFileSHA(t, input.ControlFile) != input.ControlSHA256 || liveBrowserFileSHA(t, configuration) != configurationSHA || liveBrowserFileSHA(t, requestFile) != requestSHA {
			t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_INPUT_CHANGED")
		}
		argv := []string{command, "--config", configuration}
		if command != "check-schema" {
			argv = append(argv, "--bootstrap-input", requestFile, "--credential-output", credential)
		}
		ctx, cancel := context.WithTimeout(x.ctx, 10*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, input.ControlFile, argv...)
		child.Env = liveCLIEnvironment()
		child.WaitDelay = 2 * time.Second
		stdout, stderr := &liveCLIProbeOutput{}, &liveCLIProbeOutput{}
		child.Stdout, child.Stderr = stdout, stderr
		var pipe *os.File
		if lost {
			reader, writer, e := os.Pipe()
			if e != nil {
				t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_RESULT_PIPE_FAILED")
			}
			if reader.Close() != nil {
				writer.Close()
				t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_RESULT_PIPE_FAILED")
			}
			pipe, child.Stdout = writer, writer
		}
		startError := child.Start()
		if pipe != nil {
			if pipe.Close() != nil {
				t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_RESULT_PIPE_CLOSE_FAILED")
			}
		}
		if startError != nil {
			t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_START_FAILED")
		}
		waitError := child.Wait()
		if child.ProcessState == nil {
			t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_JOIN_UNCONFIRMED")
		}
		signal := 0
		if status, ok := child.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			signal = int(status.Signal())
		}
		code := child.ProcessState.ExitCode()
		accepted := ctx.Err() == nil && code == expectedCode
		if lost {
			accepted = accepted && waitError != nil && signal == int(syscall.SIGPIPE) && stdout.Len() == 0 && stderr.Len() == 0
		} else if expectedCode == 0 {
			accepted = accepted && waitError == nil && stdout.String() == expectedOutput+"\n" && stderr.Len() == 0
		} else {
			accepted = accepted && waitError != nil && stdout.Len() == 0 && stderr.String() == expectedOutput+"\n" && signal == 0
		}
		step := map[string]any{"stage": stage, "command": command, "pid": child.Process.Pid, "exit_code": code, "signal": signal, "joined": true, "deadline_expired": ctx.Err() != nil, "lost_stdout": lost, "output_accepted": accepted, "stdout_sha256": liveBootstrapDigest(stdout.Bytes()), "stderr_sha256": liveBootstrapDigest(stderr.Bytes())}
		if accepted {
			step["accepted_static_output"] = expectedOutput
		}
		commands = append(commands, step)
		save()
		if !accepted {
			t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_COMMAND_NOT_ACCEPTED", stage)
		}
	}
	call("schema_check", "check-schema", false, 0, "SCHEMA_CURRENT")
	call("initial_bootstrap_lost_stdout", "bootstrap-admin-file", true, -1, "")
	raw, e = config.Secret(credential)
	var intent bootstrap.Intent
	if e != nil || json.Unmarshal(raw, &intent) != nil || intent.FormatVersion != 1 || intent.Request != request || intent.RequestSHA256 != requestSHA || intent.Password == "" {
		t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_PROTECTED_INTENT_FAILED")
	}
	credentialBytes := append([]byte(nil), raw...)
	committed := liveBootstrapSnapshot(t, x)
	if !committed.Complete || committed.Users != 1 || committed.Tenants != 1 || committed.Workspaces != 1 || committed.Memberships != 1 || committed.Audits != 1 || committed.Sessions != 0 || committed.Mail != 0 || committed.Administrator == "" || committed.Tenant == "" || committed.Workspace == "" || committed.PasswordPHC == "" {
		t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_LOST_RESULT_COMMIT_REQUIRED")
	}
	stateRaw, e := json.Marshal(committed)
	if e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_STATE_DIGEST_FAILED")
	}
	receipt["status"], receipt["credential_sha256"], receipt["committed_state_sha256"] = "lost_result_reconciliation_pending", liveBrowserFileSHA(t, credential), liveBootstrapDigest(stateRaw)
	save()
	for _, entry := range []struct {
		stage, command, output string
		code                   int
	}{
		{"reconcile_after_loss", "reconcile-bootstrap-file", bootstrap.Verified, 0},
		{"repeat_reconcile", "reconcile-bootstrap-file", bootstrap.Verified, 0},
		{"verified_resume_readonly", "resume-bootstrap-file", bootstrap.Verified, 0},
		{"replay_refusal", "bootstrap-admin-file", "BOOTSTRAP_ALREADY_COMPLETE", 1},
	} {
		call(entry.stage, entry.command, false, entry.code, entry.output)
		current, e := config.Secret(credential)
		if e != nil || !bytes.Equal(current, credentialBytes) || liveBootstrapSnapshot(t, x) != committed {
			t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_RECONCILIATION_CHANGED_STATE")
		}
	}
	x.d, e = openSelectedDatabase(x.ctx, x.c, "serve")
	if e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_BOOTSTRAP_OPERATOR_REOPEN_FAILED")
	}
	x.owner.DB, x.mail.DB = x.d.postgres, x.d.postgres
	receipt["status"], receipt["unchanged_intent_and_state"] = "passed_actual_native_bootstrap_result_loss_and_readonly_reconciliation_only", true
	receipt["counts"] = []int{committed.Users, committed.Tenants, committed.Workspaces, committed.Memberships, committed.Audits, committed.Sessions, committed.Mail}
	save()
	t.Log("LIVE_BROWSER_NATIVE_BOOTSTRAP_ASSERTIONS measured_CLI=true actual_SIGPIPE=true committed=true readonly_reconcile_repeat_resume=true replay_refused=true unchanged_intent_state=true")
	return intent.Password
}
