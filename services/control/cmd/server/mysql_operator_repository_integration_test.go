//go:build repository_integration

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/bootstrap"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This profile requires an actual new isolated MySQL namespace for each leaf.
// Only administrator data is synthetic. It never claims wire ACK-loss proof.
func TestActualMySQLBootstrapOperator(t *testing.T) {
	for _, leaf := range []string{"durable_bootstrap_and_readonly_reconcile", "retained_intent_resume_current_target_only", "failed_transaction_reconciles_then_explicit_resume", "foreign_connection_and_protected_output_refuse"} {
		t.Run(leaf, func(t *testing.T) {
			x := serverCompositionSetup(t, true)
			if x.d.mysql == nil || x.f.Store.Dialect() != transaction.MySQL {
				t.Fatal("MYSQL_OPERATOR_ACTUAL_MYSQL_REQUIRED")
			}
			op, e := x.d.mysqlOperator(x.ctx, x.c)
			if e != nil {
				t.Fatal("actual installation proof", e)
			}
			r := bootstrap.Request{FormatVersion: 1, AdministratorEmail: "operator@example.invalid", DisplayName: "Synthetic operator \u4e16\u754c", InstallationName: "Synthetic installation", WorkspaceName: "Synthetic Workspace"}
			raw, e := json.Marshal(r)
			if e != nil {
				t.Fatal(e)
			}
			input := x.private(t, "bootstrap-request.json", raw)
			output := filepath.Join(x.root, "credential-intent.json")
			run := func(command string) (string, error) {
				return bootstrap.RunMySQL(x.ctx, x.owner, op, command, input, output)
			}
			count := func(table string) int {
				t.Helper()
				var n int
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("", "SELECT COUNT(*) FROM `"+table+"`")).Scan(&n); e != nil {
					t.Fatal("actual isolated count", e)
				}
				return n
			}
			empty := func() {
				t.Helper()
				for _, table := range []string{"users", "tenants", "workspaces", "password_credentials", "workspace_memberships", "audit_events"} {
					if count(table) != 0 {
						t.Fatal("partial identity graph survived", table)
					}
				}
			}
			verified := func(command string) {
				t.Helper()
				if status, e := run(command); e != nil || status != bootstrap.Verified {
					t.Fatal(command, status, e)
				}
			}
			readIntent := func() []byte {
				t.Helper()
				var i bootstrap.MySQLIntent
				b, e := mysqloperator.ReadDocument(output, mysqloperator.MaximumIntentBytes, &i)
				if e != nil {
					t.Fatal(e)
				}
				return b
			}
			switch leaf {
			case "durable_bootstrap_and_readonly_reconcile":
				verified("bootstrap-admin-file")
				retained := readIntent()
				for _, table := range []string{"users", "tenants", "workspaces", "password_credentials", "workspace_memberships", "audit_events"} {
					if count(table) != 1 {
						t.Fatal("missing/duplicate actual bootstrap", table)
					}
				}
				for _, command := range []string{"reconcile-bootstrap-file", "resume-bootstrap-file"} {
					verified(command)
				}
				if count("audit_events") != 1 || !bytes.Equal(readIntent(), retained) {
					t.Fatal("reconcile changed authority/credential")
				}
				if status, e := run("bootstrap-admin-file"); e == nil || status != "" {
					t.Fatal("repeated create projected success")
				}
				var i bootstrap.MySQLIntent
				if _, e := mysqloperator.ReadDocument(output, mysqloperator.MaximumIntentBytes, &i); e != nil {
					t.Fatal(e)
				}
				x.exec(t, "", "UPDATE users SET display_name='Changed operator'")
				if status, e := run("reconcile-bootstrap-file"); e == nil || status != "" {
					t.Fatal("conflicting current graph accepted")
				}
			case "retained_intent_resume_current_target_only":
				binding, e := op.Binding(x.ctx)
				if e != nil {
					t.Fatal(e)
				}
				i := bootstrap.MySQLIntent{Format: "projectone.mysql.bootstrap-intent.v1", Request: r, RequestSHA256: mysqloperator.SHA256(raw), TargetSHA256: binding, Password: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32))}
				b, e := json.Marshal(i)
				if e != nil {
					t.Fatal(e)
				}
				var check bootstrap.MySQLIntent
				if e = mysqloperator.WriteDocument(output, b, &check); e != nil {
					t.Fatal(e)
				}
				if status, e := run("reconcile-bootstrap-file"); e != nil || status != bootstrap.NotCommitted {
					t.Fatal("actual uncommitted observation", status, e)
				}
				empty()
				if status, e := run("bootstrap-admin-file"); e == nil || status != "" {
					t.Fatal("existing intent replaced")
				}
				empty()
				wrong := *x.owner
				wrong.Config.Server.AdminOrigin = "https://changed.example.invalid"
				if status, e := bootstrap.RunMySQL(x.ctx, &wrong, op, "resume-bootstrap-file", input, output); e == nil || status != "" {
					t.Fatal("foreign configuration adopted")
				}
				empty()
				verified("resume-bootstrap-file")
				if !bytes.Equal(readIntent(), b) {
					t.Fatal("resume replaced credential")
				}
				if e = os.WriteFile(input, append(raw, '\n'), 0600); e != nil {
					t.Fatal(e)
				}
				if status, e := run("reconcile-bootstrap-file"); e == nil || status != "" {
					t.Fatal("changed exact request accepted")
				}
			case "failed_transaction_reconciles_then_explicit_resume":
				x.exec(t, "", "CREATE TRIGGER operator_bootstrap_fail BEFORE INSERT ON audit_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SYNTHETIC_OPERATOR_FAILURE'")
				if status, e := run("bootstrap-admin-file"); e == nil || status != "" {
					t.Fatal("failed transaction projected success")
				}
				empty()
				retained := readIntent()
				if status, e := run("reconcile-bootstrap-file"); e != nil || status != bootstrap.NotCommitted {
					t.Fatal("actual rollback reconcile", status, e)
				}
				empty()
				x.exec(t, "", "DROP TRIGGER operator_bootstrap_fail")
				verified("resume-bootstrap-file")
				if !bytes.Equal(readIntent(), retained) || count("audit_events") != 1 {
					t.Fatal("resume changed intent/duplicated graph")
				}
			case "foreign_connection_and_protected_output_refuse":
				foreign, e := x.d.mysql.Pool().Conn(x.ctx)
				if e != nil {
					t.Fatal(e)
				}
				_, e = mysqloperator.New(x.ctx, x.d.mysql, foreign, x.c, x.d.catalog, x.d.options)
				ce := foreign.Close()
				if e == nil || ce != nil {
					t.Fatal("unlocked connection adopted", e, ce)
				}
				if e = os.Symlink(input, output); e != nil {
					t.Fatal(e)
				}
				if status, e := run("bootstrap-admin-file"); e == nil || status != "" {
					t.Fatal("symlink credential path accepted")
				}
				empty()
			}
		})
	}
}
