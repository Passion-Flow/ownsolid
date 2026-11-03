//go:build repository_integration

package licensing_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

type licenseWireFault interface {
	Arm(commitfault.Mode) error
	Commits() int32
	Outcomes() <-chan commitfault.Observation
	Close(context.Context) error
}

func licenseFaultStore(t *testing.T, f pairedLicenseScenario) (transaction.Store, licenseWireFault) {
	t.Helper()
	closeProxy := func(proxy licenseWireFault) {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if e := proxy.Close(ctx); e != nil {
				t.Error("bounded actual wire proxy cleanup", e)
			}
		})
	}
	if f.fixture.Store.Dialect() == transaction.PostgreSQL {
		configuration := f.fixture.PostgreSQL.Config()
		if configuration.ConnConfig.TLSConfig != nil || len(configuration.ConnConfig.Fallbacks) != 0 || configuration.ConnConfig.Host != "127.0.0.1" {
			t.Fatal("only the existing explicit plaintext loopback PG fixture is allowed;never downgrade TLS")
		}
		proxy, e := commitfault.NewPlaintextPostgreSQL(configuration.ConnConfig.Host, configuration.ConnConfig.Port)
		if e != nil {
			t.Fatal(e)
		}
		closeProxy(proxy)
		proxied := configuration.Copy()
		proxied.ConnConfig.Port, proxied.MaxConns = proxy.Port(), 2
		pool, e := pgxpool.NewWithConfig(f.ctx, proxied)
		if e != nil {
			t.Fatal("actual isolated PostgreSQL forwarding pool", e)
		}
		t.Cleanup(pool.Close)
		if e = pool.Ping(f.ctx); e != nil {
			t.Fatal("actual PostgreSQL forwarding startup", e)
		}
		store, e := transaction.FromPostgreSQL(pool)
		if e != nil {
			t.Fatal(e)
		}
		return store, proxy
	}
	if f.fixture.Store.Dialect() != transaction.MySQL {
		t.Fatal("explicit actual paired engine required")
	}
	configuration, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
	if e != nil {
		t.Fatal("actual private MySQL forwarding configuration", e)
	}
	proxy, e := commitfault.NewMySQL(configuration)
	if e != nil {
		t.Fatal(e)
	}
	closeProxy(proxy)
	configuration.Port = proxy.Port()
	configuration.Policy.MaximumOpenConnections, configuration.Policy.MaximumIdleConnections = 2, 0
	kernel, observation, e := mysqlstore.Open(f.ctx, configuration)
	if e != nil || !observation.TLSAuthenticated {
		t.Fatal("actual verified TLS MySQL forwarding connector required", e)
	}
	t.Cleanup(func() {
		if e := kernel.Close(); e != nil {
			t.Error("actual MySQL forwarding pool cleanup", e)
		}
	})
	store, e := transaction.FromMySQL(kernel)
	if e != nil {
		t.Fatal(e)
	}
	return store, proxy
}

func TestActualPairedLicenseCommitAcknowledgementLoss(t *testing.T) {
	for _, operation := range []string{"status", "check", "import_success", "import_denial", "diagnostics"} {
		t.Run(operation, func(t *testing.T) {
			for _, fault := range []struct {
				name      string
				mode      commitfault.Mode
				committed bool
			}{
				{"normal_acknowledged_commit", commitfault.Pass, true},
				{"commit_not_forwarded", commitfault.DropBeforeCommit, false},
				{"actual_server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true},
			} {
				t.Run(fault.name, func(t *testing.T) {
					f := pairedLicenseFixture(t)
					c := claims(f.scenario)
					if operation == "import_denial" {
						c.DeploymentID = uuid.NewString()
					}
					attempt := input(t, f.scenario, c, "")
					f.clock.set(f.base.Add(time.Second))
					store, proxy := licenseFaultStore(t, f)
					f.owner.Storage = store
					defer func() { f.owner.Storage = f.fixture.Store }()
					if fault.mode != commitfault.Pass {
						if e := proxy.Arm(fault.mode); e != nil {
							t.Fatal(e)
						}
					}
					var state licensing.State
					var receipt licensing.ImportResult
					var download httpserver.MemoryDownload
					var e error
					switch operation {
					case "status":
						state, e = f.service.StatusAdmin(f.ctx, f.admin)
					case "check":
						e = f.service.Check(f.ctx)
					case "import_success", "import_denial":
						receipt, e = f.service.Import(f.ctx, f.admin, attempt, uuid.NewString())
					case "diagnostics":
						download, e = f.service.Diagnostics(f.ctx, f.admin, uuid.NewString())
					}
					f.owner.Storage = f.fixture.Store
					if fault.mode == commitfault.Pass {
						switch operation {
						case "check":
							expect(t, e, "LICENSE_NEW_BUSINESS_UNAVAILABLE")
						case "import_denial":
							expect(t, e, "LICENSE_DEPLOYMENT_MISMATCH")
						default:
							if e != nil {
								t.Fatal("actual acknowledged License operation", e)
							}
						}
						if operation == "status" && (state.State != licensing.Missing || !state.ObservedAt.Equal(f.base.Add(time.Second))) {
							t.Fatal("acknowledged actual observation missing")
						}
						if operation == "import_success" && (receipt.ImportedLicenseID != c.LicenseID || receipt.Repeated || receipt.State.State != licensing.Active) {
							t.Fatal("acknowledged actual import missing")
						}
						if operation == "diagnostics" && (len(download.Bytes) == 0 || len(download.Bytes) > licensing.MaximumDiagnosticsBytes || download.SHA256 != wire.Digest(download.Bytes)) {
							t.Fatal("acknowledged actual diagnostics invalid")
						}
					} else {
						expect(t, e, string(transaction.CommitUnknown))
						if !reflect.DeepEqual(state, licensing.State{}) || !reflect.DeepEqual(receipt, licensing.ImportResult{}) || !reflect.DeepEqual(download, httpserver.MemoryDownload{}) {
							t.Fatal("unknown COMMIT produced phantom state,receipt or download")
						}
						select {
						case observed := <-proxy.Outcomes():
							if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
								t.Fatal("actual License COMMIT fault was not established", observed)
							}
						case <-f.ctx.Done():
							t.Fatal("actual License COMMIT observation missing")
						}
					}
					if proxy.Commits() != 1 {
						t.Fatal("initial License COMMIT automatically resent", proxy.Commits())
					}
					observed := f.base
					if fault.committed {
						observed = observed.Add(time.Second)
					}
					if !pairedLicenseObserved(t, f).Equal(observed) {
						t.Fatal("independent durable observation disagrees with wire outcome")
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
					var rows int
					if e := f.fixture.Store.QueryRow(f.ctx, f.fixture.Statement(`SELECT COUNT(*) FROM platform_license_artifacts`, `SELECT COUNT(*) FROM platform_license_artifacts`)).Scan(&rows); e != nil || rows != wantRows {
						t.Fatal("actual durable history does not match COMMIT outcome", e)
					}
					if pairedLicenseCount(t, f, "license.imported") != wantSuccess || pairedLicenseCount(t, f, "license.import_rejected") != wantDenial || pairedLicenseCount(t, f, "license.diagnostics_exported") != wantDiagnostics {
						t.Fatal("actual durable audit does not share COMMIT outcome")
					}
					// Recovery is a deliberate new request through the independent
					// original store. A missing ACK never triggers automatic resend.
					switch operation {
					case "import_success":
						recovered, e := f.service.Import(f.ctx, f.admin, attempt, uuid.NewString())
						if e != nil || recovered.ImportedLicenseID != c.LicenseID || recovered.Repeated != fault.committed || recovered.State.AcceptedLicenses != 1 || pairedLicenseCount(t, f, "license.imported") != 1 {
							t.Fatal("explicit signed-identity recovery lost or duplicated import", e)
						}
					case "diagnostics":
						recovered, e := f.service.Diagnostics(f.ctx, f.admin, uuid.NewString())
						if e != nil || len(recovered.Bytes) == 0 || recovered.SHA256 != wire.Digest(recovered.Bytes) || pairedLicenseCount(t, f, "license.diagnostics_exported") != wantDiagnostics+1 {
							t.Fatal("explicit diagnostics recovery lost independent export audit", e)
						}
					default:
						recovered, e := f.service.StatusAdmin(f.ctx, f.admin)
						if e != nil || recovered.State != licensing.Missing {
							t.Fatal("explicit read recovery failed", e)
						}
						if pairedLicenseCount(t, f, "license.import_rejected") != wantDenial {
							t.Fatal("read recovery resent rejected import")
						}
					}
					if !pairedLicenseObserved(t, f).Equal(f.base.Add(time.Second)) || proxy.Commits() != 1 {
						t.Fatal("explicit recovery lost clock fence or reused uncertain channel")
					}
					t.Logf("actual engine=%s operation=%s fault=%d initial_COMMIT=1 committed=%t independent_readback=true explicit_recovery=true", f.fixture.Store.Dialect(), operation, fault.mode, fault.committed)
				})
			}
		})
	}
}
