package databaseaccess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func mvpRuntimeInput(t *testing.T, f actualFixture) RuntimeInput {
	t.Helper()
	v := f.input
	profile := config.MVPPostgreSQLCPUV1Profile
	v.configuration.Server.ApplicationProfile = &profile
	v.configuration.ReleaseViewConfigFile = "/run/projectone/admin-view.json"
	policy := v.configuration.Policy()
	policy.HTTP.ShutdownSeconds = 20
	v.configuration.Runtime = &policy
	if err := v.configuration.ValidateApplicationProfile(); err != nil {
		t.Fatal("MVP fixture policy", err)
	}
	return v
}

func mvpCommonDataFingerprint(t *testing.T, f actualFixture) string {
	t.Helper()
	h := sha256.New()
	for _, table := range runtimeTables(f.input) {
		var raw string
		if err := f.lease.QueryRow(t.Context(), "SELECT COALESCE(string_agg(to_jsonb(r)::text,E'\\n' ORDER BY to_jsonb(r)::text),'') FROM "+pgx.Identifier{table}.Sanitize()+" r").Scan(&raw); err != nil {
			t.Fatal("data fingerprint", table, err)
		}
		h.Write([]byte(table + "\x00" + raw + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestMVPPostgreSQLCPUV1UpgradePreservesAllOldRowsRoleAndFourStores(t *testing.T) {
	for _, boundary := range []string{"normal", "schema-committed", "schema-unknown", "grant-unknown"} {
		t.Run(boundary, func(t *testing.T) {
			f := newActualFixture(t, "localization")
			ctx := t.Context()
			// Supported operator-only 30s statement budget on the original held
			// lease; runtime remains the independent configured 5s principal.
			if _, e := f.lease.Exec(ctx, "SET statement_timeout=30000"); e != nil {
				t.Fatal(e)
			}
			if status, err := Prepare(ctx, f.service.DB, f.lease, f.input); err != nil || status != Verified {
				t.Fatal(status, err)
			}
			password := identityfixture.Bootstrap(t, f.service)
			actor := identityfixture.Login(t, f.service, "admin@example.invalid", password, "portal")
			w := workspaces.Service{DB: f.service.DB, Identity: f.service}
			if _, err := w.CreateNote(ctx, actor, actor.Me.Workspace.ID, "Retained old note", "synthetic account22 content", uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			before := mvpCommonDataFingerprint(t, f)
			identity, err := readMVPPrincipal(ctx, f.lease, f.input)
			if err != nil {
				t.Fatal(err)
			}
			stores := []string{f.input.configuration.Models.ArtifactRoot, f.input.configuration.Datasets.ArtifactRoot, f.input.configuration.Training.ArtifactRoot, f.input.configuration.ModelPreparation.ArtifactRoot}
			for _, root := range stores {
				if err = os.MkdirAll(root, 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(root, "retained.synthetic"), []byte(root), 0600); err != nil {
					t.Fatal(err)
				}
			}
			input := mvpRuntimeInput(t, f)
			if state, err := InspectMVPPostgreSQLCPUV1(ctx, f.service.DB, f.lease, input); err != nil || state != MVPPostgreSQLCPUV1UpgradeRequired {
				t.Fatal(state, err)
			}
			switch boundary {
			case "schema-committed":
				if err = database.Migrate(ctx, f.lease, config.MVPPostgreSQLCPUV1Profile); err != nil {
					t.Fatal(err)
				}
			case "schema-unknown":
				steps := mvpUpgradeSteps{migrate: func(ctx context.Context, c database.MigrationConnection, profiles ...string) error {
					if err := database.Migrate(ctx, c, profiles...); err != nil {
						return err
					}
					return &transaction.Error{Kind: transaction.CommitUnknown}
				}, commit: func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) }}
				status, e := upgradeMVPPostgreSQLCPUV1(ctx, f.service.DB, f.lease, input, steps)
				if status != "" || e == nil || e.Error() != string(transaction.CommitUnknown) {
					t.Fatal("unknown schema commit reported success", status, e)
				}
			case "grant-unknown":
				steps := mvpUpgradeSteps{migrate: func(ctx context.Context, c database.MigrationConnection, profiles ...string) error {
					e := database.Migrate(ctx, c, profiles...)
					if e != nil {
						t.Logf("underlying migration failure %T: %v", e, e)
					}
					return e
				}, commit: func(ctx context.Context, tx pgx.Tx) error {
					if e := tx.Commit(ctx); e != nil {
						return e
					}
					return errors.New("synthetic lost grant acknowledgment")
				}}
				status, e := upgradeMVPPostgreSQLCPUV1(ctx, f.service.DB, f.lease, input, steps)
				if status != "" || e == nil || e.Error() != string(transaction.CommitUnknown) {
					t.Fatal("unknown grant commit reported success", status, e)
				}
			}
			if boundary == "schema-committed" || boundary == "schema-unknown" {
				if state, e := InspectMVPPostgreSQLCPUV1(ctx, f.service.DB, f.lease, input); e != nil || state != MVPPostgreSQLCPUV1GrantsPending {
					t.Fatal("pending readback", state, e)
				}
				pending, e := pgxpool.NewWithConfig(ctx, input.connection.Copy())
				if e != nil {
					t.Fatal(e)
				}
				if e = database.Check(ctx, pending, config.MVPPostgreSQLCPUV1Profile); e == nil {
					t.Fatal("actual restricted pending principal admitted schema readiness")
				}
				pending.Close()
				if _, e := Check(ctx, f.service.DB, f.lease, input); e == nil {
					t.Fatal("pending grants admitted serving")
				}
			}
			for i := 0; i < 2; i++ {
				if status, e := UpgradeMVPPostgreSQLCPUV1(ctx, f.service.DB, f.lease, input); e != nil || status != MVPPostgreSQLCPUV1UpgradeComplete {
					t.Fatal("explicit recovery/noop", status, e)
				}
			}
			after, e := readMVPPrincipal(ctx, f.lease, input)
			if e != nil || after != identity {
				t.Fatal("OID/SCRAM changed", e)
			}
			if got := mvpCommonDataFingerprint(t, f); got != before {
				t.Fatal("old data/identity changed")
			}
			for _, root := range stores {
				raw, e := os.ReadFile(filepath.Join(root, "retained.synthetic"))
				if e != nil || string(raw) != root {
					t.Fatal("four-store content changed", e)
				}
			}
			var count, version int
			if e = f.lease.QueryRow(ctx, "SELECT (SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()),(SELECT max(version) FROM schema_migrations)").Scan(&count, &version); e != nil || count != 108 || version != 29 {
				t.Fatal("native target inventory", count, version, e)
			}
			runtime, e := pgxpool.NewWithConfig(ctx, input.connection.Copy())
			if e != nil {
				t.Fatal(e)
			}
			defer runtime.Close()
			if e = VerifyMVPPostgreSQLCPUV1Principal(ctx, f.service.DB, f.lease, input); e != nil {
				t.Fatal("native same-session principal guard", e)
			}
			runtimeStore, e := transaction.FromPostgreSQL(runtime)
			if e != nil {
				t.Fatal(e)
			}
			if e = database.CheckMVPPostgreSQLCPUV1RuntimeWithin(ctx, runtimeStore); e != nil {
				t.Fatal("actual runtime admission", e)
			}
			operatorStore, e := transaction.FromPostgreSQL(f.service.DB)
			if e != nil {
				t.Fatal(e)
			}
			if e = database.CheckMVPPostgreSQLCPUV1RuntimeWithin(ctx, operatorStore); e == nil {
				t.Fatal("operator superuser admitted as runtime")
			}
			clone := *f.service
			clone.DB = runtime
			clone.Config = input.configuration
			identityfixture.Login(t, &clone, "admin@example.invalid", password, "portal")
			for _, statement := range []string{"CREATE TABLE forbidden_mvp_ddl(id integer)", "DELETE FROM schema_migrations", "TRUNCATE artifact_objects"} {
				if _, e = runtime.Exec(ctx, statement); e == nil {
					t.Fatal("runtime elevated action accepted", statement)
				}
			}
			if state, e := InspectMVPPostgreSQLCPUV1(ctx, f.service.DB, f.lease, input); e != nil || state != MVPPostgreSQLCPUV1UpgradeComplete {
				t.Fatal(state, e)
			}
		})
	}
}

func TestMVPPostgreSQLCPUV1RejectsPartialGrantsAndPlatformLineage(t *testing.T) {
	f := newActualFixture(t, "localization")
	ctx := t.Context()
	if _, err := Prepare(ctx, f.service.DB, f.lease, f.input); err != nil {
		t.Fatal(err)
	}
	identityfixture.Bootstrap(t, f.service)
	input := mvpRuntimeInput(t, f)
	if err := database.Migrate(ctx, f.lease, config.MVPPostgreSQLCPUV1Profile); err != nil {
		t.Fatal(err)
	}
	if _, err := f.lease.Exec(ctx, "GRANT SELECT ON artifact_objects TO "+pgx.Identifier{f.role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if status, err := UpgradeMVPPostgreSQLCPUV1(ctx, f.service.DB, f.lease, input); err == nil || status != "" {
		t.Fatal("partial grants repaired silently", status, err)
	}
	if _, err := f.lease.Exec(ctx, "REVOKE SELECT ON artifact_objects FROM "+pgx.Identifier{f.role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := UpgradeMVPPostgreSQLCPUV1(ctx, f.service.DB, f.lease, input); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{
		"ALTER TABLE artifact_objects ALTER COLUMN created_at DROP DEFAULT",
		"ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check",
		"ALTER TABLE model_preparation_reviews DROP CONSTRAINT model_review_approval_identity_required",
		"ALTER TABLE middleware_operations DISABLE TRIGGER ALL",
		"CREATE INDEX mvp_unexpected_idx ON users(id)",
		"UPDATE schema_migrations SET checksum=repeat('0',64) WHERE version=1",
		"DROP INDEX gateway_request_held_deployment_idx; CREATE INDEX gateway_request_held_deployment_idx ON gateway_requests USING hash(deployment_id) WHERE capacity_held",
		"ALTER TABLE users ALTER COLUMN display_name TYPE varchar(100) COLLATE \"C\"",
	} {
		tx, err := f.lease.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, change); err != nil {
			tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err = database.CheckMVPPostgreSQLCPUV1Within(ctx, transaction.PostgreSQLTransaction(tx)); err == nil {
			t.Fatal("catalog/ledger corruption admitted", change)
		}
		tx.Rollback(ctx)
	}
}

func TestMVPPostgreSQLCPUV1ColdAndUpgradeHaveExactSameCatalog(t *testing.T) {
	f := newActualFixture(t, "localization")
	ctx := t.Context()
	if err := database.Migrate(ctx, f.lease, config.MVPPostgreSQLCPUV1Profile); err != nil {
		t.Fatal(err)
	}
	var upgraded string
	if err := f.lease.QueryRow(ctx, database.MVPPostgreSQLCPUV1CatalogSQL).Scan(&upgraded); err != nil {
		t.Fatal(err)
	}
	schema := "mvp_cold_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := f.lease.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	pc := f.service.DB.Config().Copy()
	pc.ConnConfig.RuntimeParams["search_path"] = schema
	cold, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cold.Close()
		if _, e := f.lease.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); e != nil {
			t.Error("exact owned cold schema cleanup", e)
		}
	}()
	if err = database.Migrate(ctx, cold, config.MVPPostgreSQLCPUV1Profile); err != nil {
		t.Fatal("cold install", err)
	}
	var fresh string
	if err = cold.QueryRow(ctx, database.MVPPostgreSQLCPUV1CatalogSQL).Scan(&fresh); err != nil || fresh != upgraded {
		t.Fatal("cold/upgrade structural parity", err)
	}
	if err = database.Check(ctx, cold, config.MVPPostgreSQLCPUV1Profile); err != nil {
		t.Fatal(err)
	}
	if err = database.Check(ctx, cold, "platform"); err == nil {
		t.Fatal("new lineage accepted as platform")
	}
	if err = database.Migrate(ctx, cold, "releases"); err == nil {
		t.Fatal("new lineage accepted as account22")
	}
}
