//go:build repository_integration && middleware_mysql_endpoint_integration

package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// The coordinator supplies an already-running, exclusively owned TLS fixture.
// Never seed, migrate or write the monitored MySQL database in these tests.
func actualMySQLConfiguration(t *testing.T) Configuration {
	t.Helper()
	c, e := LoadFiles(os.Getenv("PROJECT_ONE_MIDDLEWARE_MYSQL_TEST_CONFIGURATION"), "")
	if e != nil || len(c.Profiles) != 1 || c.Profiles[0].Engine != "mysql" || c.Policy.ProbeSeconds > 10 {
		t.Fatal("MIDDLEWARE_ACTUAL_MYSQL_PRIVATE_FIXTURE_REQUIRED")
	}
	p := c.Profiles[0]
	captured, e := Capture(p)
	if e != nil {
		t.Fatal("MIDDLEWARE_ACTUAL_MYSQL_CAPTURE_REQUIRED")
	}
	settings := *captured.mysql
	ip := net.ParseIP(settings.Host)
	if ip == nil || !ip.IsLoopback() || !strings.HasSuffix(settings.Database, "_test") || settings.Policy.MaximumOpenConnections > 2 {
		t.Fatal("MIDDLEWARE_ACTUAL_MYSQL_BOUNDED_LOOPBACK_TEST_DATABASE_REQUIRED")
	}
	root := t.TempDir()
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	// Private mutations below affect only disposable copies, never input secrets.
	settings.PasswordFile = filepath.Join(root, "password")
	settings.CAFile = filepath.Join(root, "ca.pem")
	p.MySQLConfigurationFile = filepath.Join(root, "mysql.json")
	p.MySQLEnvironmentFile = ""
	raw, e := json.Marshal(settings)
	if e != nil {
		t.Fatal(e)
	}
	for path, data := range map[string][]byte{settings.PasswordFile: captured.password, settings.CAFile: captured.ca, p.MySQLConfigurationFile: raw} {
		if e = os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	c.Profiles = []Profile{p}
	return c
}

func TestActualMiddlewareMySQLProbe(t *testing.T) {
	for _, name := range []string{"health_version_identity_tls", "wrong_password_zero_output", "wrong_ca_zero_output", "wrong_tls_name_zero_output", "version_mismatch_zero_output", "server_uuid_mismatch_zero_output", "captured_input_drift_zero_output"} {
		t.Run(name, func(t *testing.T) {
			c := actualMySQLConfiguration(t)
			p := c.Profiles[0]
			captured, e := Capture(p)
			if e != nil {
				t.Fatal(e)
			}
			settings := *captured.mysql
			switch name {
			case "wrong_password_zero_output":
				if e = os.WriteFile(settings.PasswordFile, []byte(uuid.NewString()), 0600); e != nil {
					t.Fatal(e)
				}
			case "wrong_ca_zero_output":
				_, synthetic := mysqlCaptureFixture(t)
				ca, e := os.ReadFile(synthetic.CAFile)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(settings.CAFile, ca, 0600); e != nil {
					t.Fatal(e)
				}
			case "wrong_tls_name_zero_output":
				settings.ServerName = "wrong-" + uuid.NewString() + ".invalid"
			case "version_mismatch_zero_output":
				settings.ExpectedVersion = "8.4.0"
				if captured.mysql.ExpectedVersion == settings.ExpectedVersion {
					settings.ExpectedVersion = "8.4.1"
				}
			case "server_uuid_mismatch_zero_output":
				p.ExpectedMySQLServerUUID = uuid.NewString()
			case "captured_input_drift_zero_output":
				if e = os.WriteFile(settings.PasswordFile, []byte(uuid.NewString()), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if name != "captured_input_drift_zero_output" {
				raw, e := json.Marshal(settings)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(p.MySQLConfigurationFile, raw, 0600); e != nil {
					t.Fatal(e)
				}
				captured, e = Capture(p)
				if e != nil {
					t.Fatal("negative fixture must remain structurally valid", e)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			out, e := ProbeMySQL(ctx, captured, c.Policy)
			if name == "health_version_identity_tls" {
				if e != nil || !out.valid() || out.ServerUUID != p.ExpectedMySQLServerUUID || out.Version != settings.ExpectedVersion || out.Database != settings.Database || out.User != settings.User {
					t.Fatal("actual MySQL observation failed", e)
				}
			} else if e == nil || out != (MySQLObservation{}) {
				t.Fatal("refusal exposed partial/positive observation", e)
			}
		})
	}
}

func TestActualMiddlewareMySQLWorkflow(t *testing.T) {
	for _, name := range []string{"durable_health_restart_and_external_refusal", "server_identity_failure_is_persisted", "queued_input_drift_has_no_result"} {
		t.Run(name, func(t *testing.T) {
			c := actualMySQLConfiguration(t)
			if name == "server_identity_failure_is_persisted" {
				c.Profiles[0].ExpectedMySQLServerUUID = uuid.NewString()
			}
			p := pairedSetup(t)
			var e error
			p.configuration = c
			p.s, e = New(p.accounts, &workspaces.Service{Identity: p.accounts}, c)
			if e != nil {
				t.Fatal(e)
			}
			v, _ := p.register(t)
			input := OperationInput{Kind: "health", Reason: "Actual synthetic MySQL TLS health", IdempotencyKey: uuid.NewString(), Version: v.Version}
			if out, e := p.s.Enqueue(p.ctx, p.viewer, p.f.Workspace, v.ID, input, uuid.NewString()); e == nil || out.ID != "" {
				t.Fatal("viewer acquired health mutation")
			}
			operation, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, input, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			replay, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, input, uuid.NewString())
			if e != nil || replay.ID != operation.ID {
				t.Fatal("operation identity changed on replay", e)
			}
			if name == "queued_input_drift_has_no_result" {
				captured, e := Capture(c.Profiles[0])
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(captured.mysql.PasswordFile, []byte(uuid.NewString()), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if e = p.s.Pass(p.ctx); e != nil {
				t.Fatal("actual worker pass", e)
			}
			// Reopen the owner and read the committed result through current auth.
			reopened, e := New(p.accounts, &workspaces.Service{Identity: p.accounts}, c)
			if e != nil {
				t.Fatal(e)
			}
			stored, e := reopened.Operation(p.ctx, p.owner, p.f.Workspace, operation.ID)
			if e != nil {
				t.Fatal(e)
			}
			if name != "durable_health_restart_and_external_refusal" {
				if stored.State != "failed" || stored.ErrorCode == "" || len(stored.Result) != 0 {
					t.Fatal("failed health produced success evidence")
				}
				return
			}
			var result executionEvidence
			if stored.State != "succeeded" || json.Unmarshal(stored.Result, &result) != nil || result.Kind != "health" || result.MySQLObservation == nil || result.Observation != nil || !result.MySQLObservation.valid() {
				t.Fatal("durable MySQL evidence absent")
			}
			if e = reopened.VerifyStored(p.ctx); e != nil {
				t.Fatal("restart integrity", e)
			}
			for _, kind := range []string{"backup", "isolated_restore", "reconcile"} {
				refused := OperationInput{Kind: kind, Reason: "External mutation must be refused", Version: v.Version, IdempotencyKey: uuid.NewString()}
				if kind == "isolated_restore" {
					refused.ConfirmIsolated = true
					refused.BackupID = uuid.NewString()
					refused.TargetKey = "isolated"
				}
				if kind == "reconcile" {
					refused.OriginalOperationID = uuid.NewString()
				}
				out, e := reopened.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, refused, uuid.NewString())
				wantCode(t, e, "MIDDLEWARE_EXTERNAL_MUTATION_REFUSED")
				if out.ID != "" {
					t.Fatal("external mutation returned operation")
				}
			}
			history, e := reopened.History(p.ctx, p.owner, p.f.Workspace, v.ID, "")
			if e != nil || len(history.Items) != 1 {
				t.Fatal("refusal created another operation", e)
			}
			// The real terminal trigger must refuse replacement even when the
			// attempted malformed evidence carries its matching digest. Missing
			// facts are separately covered by the pure observation decoder tests.
			var evidence map[string]json.RawMessage
			_ = json.Unmarshal(stored.Result, &evidence)
			var observation map[string]json.RawMessage
			_ = json.Unmarshal(evidence["mysql_observation"], &observation)
			delete(observation, "read_only")
			evidence["mysql_observation"], _ = json.Marshal(observation)
			corrupt, _ := json.Marshal(evidence)
			_, mutationError := p.f.Store.Exec(p.ctx, p.f.Statement("UPDATE middleware_operations SET result_json=$1,result_sha256=$2 WHERE id=$3", "UPDATE middleware_operations SET result_json=?,result_sha256=? WHERE id=?", string(corrupt), hash(corrupt), operation.ID))
			wantState := "P0001"
			if p.f.Store.Dialect() == transaction.MySQL {
				wantState = "45000"
			}
			var rejected *transaction.Error
			if !errors.As(mutationError, &rejected) || rejected.SQLState != wantState || p.f.Store.Dialect() == transaction.MySQL && rejected.Number != 1644 {
				t.Fatal("actual terminal trigger did not reject evidence replacement", mutationError)
			}
			unchanged, e := reopened.Operation(p.ctx, p.owner, p.f.Workspace, operation.ID)
			if e != nil || unchanged.State != stored.State || !bytes.Equal(unchanged.Result, stored.Result) {
				t.Fatal("terminal evidence changed after refused replacement", e)
			}
			if e = reopened.VerifyStored(p.ctx); e != nil {
				t.Fatal("retained terminal evidence integrity", e)
			}
		})
	}
}
