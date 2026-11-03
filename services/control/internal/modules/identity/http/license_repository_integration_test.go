//go:build repository_integration

package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

// The forwarding store belongs only to the License service in this test.
// Ordinary HTTP authentication/CSRF use their real independent original pool.
func licenseHTTPForwarding(t *testing.T, owner *identity.Service, clock *licenseClock) (*licensing.Service, *commitfault.PlaintextPostgreSQL) {
	t.Helper()
	c := owner.DB.Config()
	if c.ConnConfig.Host != "127.0.0.1" || c.ConnConfig.TLSConfig != nil || len(c.ConnConfig.Fallbacks) != 0 {
		t.Fatal("explicit isolated plaintext PG profile required;never downgrade TLS")
	}
	proxy, e := commitfault.NewPlaintextPostgreSQL(c.ConnConfig.Host, c.ConnConfig.Port)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := proxy.Close(ctx); e != nil {
			t.Error("bounded License HTTP wire cleanup", e)
		}
	})
	c.ConnConfig.Port, c.MaxConns = proxy.Port(), 2
	pool, e := pgxpool.NewWithConfig(t.Context(), c)
	if e != nil {
		t.Fatal("real License HTTP forwarding pool", e)
	}
	t.Cleanup(pool.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if pool.Ping(ctx) != nil {
		t.Fatal("real License HTTP forwarding startup")
	}
	store, e := transaction.FromPostgreSQL(pool)
	if e != nil {
		t.Fatal(e)
	}
	isolated := *owner
	isolated.Storage, isolated.DB = store, nil
	trust, e := owner.Config.LicenseTrust()
	if e != nil {
		t.Fatal(e)
	}
	service, e := licensing.New(&isolated, trust, owner.Config.LicensePolicy(), clock.now)
	if e != nil {
		t.Fatal(e)
	}
	return service, proxy
}

func TestM20LicenseUnknownHTTPAndExplicitRecovery(t *testing.T) {
	for _, operation := range []string{"status", "import_success", "import_denial", "diagnostics"} {
		t.Run(operation, func(t *testing.T) {
			for _, fault := range []struct {
				name      string
				mode      commitfault.Mode
				committed bool
			}{
				{"commit_not_forwarded", commitfault.DropBeforeCommit, false},
				{"actual_server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true},
			} {
				t.Run(fault.name, func(t *testing.T) {
					base := time.Now().UTC().Truncate(time.Second).Add(5 * time.Second)
					clock := &licenseClock{value: base}
					owner, handler, admin, portal := licenseClients(t, clock)
					original := handler.Licensing.Service
					admin.request("GET", "/api/v1/admin/license", nil, 200, "", nil)
					claims := licenseClaims(t, owner, base)
					if operation == "import_denial" {
						claims.DeploymentID = uuid.NewString()
					}
					attempt := licenseInput(t, owner, claims, "")
					admin.csrf = admin.request("GET", "/api/v1/auth/csrf", nil, 200, "", nil)["token"].(string)
					forwarding, proxy := licenseHTTPForwarding(t, owner, clock)
					handler.Licensing.Service = forwarding
					clock.set(base.Add(time.Second))
					if e := proxy.Arm(fault.mode); e != nil {
						t.Fatal(e)
					}
					method, path := "GET", "/api/v1/admin/license"
					var body []byte
					if operation == "diagnostics" {
						path += "/diagnostics"
					}
					if operation == "import_success" || operation == "import_denial" {
						method = "POST"
						body, _ = json.Marshal(attempt)
					}
					request, e := http.NewRequest(method, admin.s.URL+path, bytes.NewReader(body))
					if e != nil {
						t.Fatal(e)
					}
					request.Host = "admin.example.invalid"
					request.Header.Set("Origin", "https://admin.example.invalid")
					request.Header.Set("X-CSRF-Token", admin.csrf)
					if body != nil {
						request.Header.Set("Content-Type", "application/json")
					}
					for _, cookie := range admin.cookies {
						request.AddCookie(cookie)
					}
					response, e := admin.s.Client().Do(request)
					if e != nil {
						t.Fatal("real TLS unknown response", e)
					}
					raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
					response.Body.Close()
					if e != nil || len(raw) == 1<<20 || response.StatusCode != 409 || response.Header.Get("Retry-After") != "" || response.Header.Get("Content-Disposition") != "" || response.Header.Get("X-Artifact-SHA256") != "" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Request-ID") == "" {
						t.Fatal("unknown License response lost safe HTTP contract", response.StatusCode, e)
					}
					var problem map[string]any
					if json.Unmarshal(raw, &problem) != nil || problem["code"] != "STORAGE_COMMIT_OUTCOME_UNKNOWN" || problem["data"] != nil || problem["request_id"] != response.Header.Get("X-Request-ID") {
						t.Fatal("unknown License response exposed success or lost reconciliation code")
					}
					select {
					case observed := <-proxy.Outcomes():
						if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
							t.Fatal("real HTTP COMMIT fault not established", observed)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("HTTP COMMIT wire observation missing")
					}
					if proxy.Commits() != 1 {
						t.Fatal("HTTP silently resent uncertain operation")
					}
					handler.Licensing.Service = original
					count := func(query string) int {
						t.Helper()
						var n int
						ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
						defer cancel()
						if e := owner.DB.QueryRow(ctx, query).Scan(&n); e != nil {
							t.Fatal(e)
						}
						return n
					}
					wantRows, wantSuccess, wantDenial, wantDiagnostics := 0, 0, 0, 0
					if fault.committed {
						switch operation {
						case "import_success":
							wantRows, wantSuccess = 1, 1
						case "import_denial":
							wantDenial = 1
						case "diagnostics":
							wantDiagnostics = 1
						}
					}
					if count("SELECT count(*) FROM platform_license_artifacts") != wantRows || count("SELECT count(*) FROM audit_events WHERE action='license.imported'") != wantSuccess || count("SELECT count(*) FROM audit_events WHERE action='license.import_rejected'") != wantDenial || count("SELECT count(*) FROM audit_events WHERE action='license.diagnostics_exported'") != wantDiagnostics {
						t.Fatal("HTTP history/audit not atomic with actual wire outcome")
					}
					var observed time.Time
					if e := owner.DB.QueryRow(t.Context(), "SELECT observed_at FROM platform_license_state WHERE id=1").Scan(&observed); e != nil {
						t.Fatal(e)
					}
					wantTime := base
					if fault.committed {
						wantTime = base.Add(time.Second)
					}
					if !observed.Equal(wantTime) {
						t.Fatal("HTTP durable clock disagrees with actual COMMIT outcome")
					}
					switch operation {
					case "import_success":
						recovered := admin.mutate("POST", path, attempt, 200, "")
						if recovered["imported_license_id"] != claims.LicenseID || recovered["repeated"] != fault.committed || count("SELECT count(*) FROM platform_license_artifacts") != 1 || count("SELECT count(*) FROM audit_events WHERE action='license.imported'") != 1 {
							t.Fatal("explicit HTTP signed import recovery duplicated or lost state")
						}
					case "diagnostics":
						licenseDiagnostic(t, admin)
						if count("SELECT count(*) FROM audit_events WHERE action='license.diagnostics_exported'") != wantDiagnostics+1 {
							t.Fatal("explicit HTTP diagnostics audit missing")
						}
					default:
						value := admin.request("GET", "/api/v1/admin/license", nil, 200, "", nil)
						if value["state"] != "missing" || count("SELECT count(*) FROM audit_events WHERE action='license.import_rejected'") != wantDenial {
							t.Fatal("explicit HTTP refresh resent denied import")
						}
					}
					minimal := portal.request("GET", "/api/v1/license/status", nil, 200, "", nil)
					if len(minimal) != 3 || proxy.Commits() != 1 {
						t.Fatal("Portal metadata boundary or no-resend invariant lost")
					}
					t.Logf("actual_TLS=true operation=%s fault=%d initial_COMMIT=1 committed=%t HTTP409=true Retry_After_absent=true explicit_recovery=true", operation, fault.mode, fault.committed)
				})
			}
		})
	}
}
