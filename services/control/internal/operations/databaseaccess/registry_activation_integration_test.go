//go:build repository_integration

package databaseaccess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func TestActualPostgreSQLRegistryActivation(t *testing.T) {
	f := repositoryfixture.NewUnbootstrapped(t)
	if f.PostgreSQL == nil {
		t.Fatal("REGISTRY_ACTIVATION_REAL_POSTGRES_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	journal := func() string {
		t.Helper()
		var value string
		if e := f.PostgreSQL.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(j) ORDER BY version)::text FROM schema_migrations j WHERE version<=23`).Scan(&value); e != nil {
			t.Fatal("actual original journal", e)
		}
		return value
	}
	original := journal()
	registryTables := []string{"image_registry_blobs", "image_registry_deployment_refs", "image_registry_grants", "image_registry_imports", "image_registry_mutations", "image_registry_offline_refs", "image_registry_pins"}
	if !t.Run("active_catalog_and_replay", func(t *testing.T) {
		if e := database.Check(ctx, f.PostgreSQL); e != nil {
			t.Fatal(e)
		}
		raw, e := migrations.Files.ReadFile("postgresql/000024_image_registry.up.sql")
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		var count, latest, triggers int
		var checksum string
		if e = f.PostgreSQL.QueryRow(ctx, `SELECT count(*),max(version) FROM schema_migrations WHERE version<=24`).Scan(&count, &latest); e != nil || count != 24 || latest != 24 {
			t.Fatal("actual24 journal", count, latest, e)
		}
		if e = f.PostgreSQL.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version=24`).Scan(&checksum); e != nil || checksum != hex.EncodeToString(sum[:]) {
			t.Fatal("actual24 checksum", e)
		}
		rows, e := f.PostgreSQL.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename LIKE 'image_registry_%' ORDER BY tablename`)
		if e != nil {
			t.Fatal(e)
		}
		var names []string
		for rows.Next() {
			var n string
			if e = rows.Scan(&n); e != nil {
				t.Fatal(e)
			}
			names = append(names, n)
		}
		e = rows.Err()
		rows.Close()
		if e != nil || !reflect.DeepEqual(names, registryTables) {
			t.Fatal("actual registry tables", names, e)
		}
		if e = f.PostgreSQL.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgrelid IN(SELECT oid FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relname LIKE 'image_registry_%')`).Scan(&triggers); e != nil || triggers != 4 {
			t.Fatal("actual identity triggers", triggers, e)
		}
		if e = database.Migrate(ctx, f.PostgreSQL); e != nil || journal() != original {
			t.Fatal("repeat migration changed original journal", e)
		}
	}) {
		return
	}
	if !t.Run("restricted_runtime_grants", func(t *testing.T) {
		role := "m24_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		password, e := NewCredential()
		if e != nil {
			t.Fatal(e)
		}
		pc := f.PostgreSQL.Config().Copy()
		pc.ConnConfig.User = role
		pc.ConnConfig.Password = password
		pc.MaxConns = 2
		pc.MinConns = 0
		c := config.Config{}
		c.Database.MaxOpenConnections = 2
		c.Database.MaxIdleConnections = 0
		c.Database.StatementTimeoutSeconds = 5
		input := RuntimeInput{configuration: c, connection: pc}
		lease, e := f.PostgreSQL.Acquire(ctx)
		if e != nil {
			t.Fatal(e)
		}
		var locked bool
		if e = lease.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, database.InstallationLockID).Scan(&locked); e != nil || !locked {
			lease.Release()
			t.Fatal("actual installation lock", e)
		}
		defer func() {
			cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			if _, e := lease.Exec(cleanup, `SELECT pg_advisory_unlock($1)`, database.InstallationLockID); e != nil {
				t.Error("owned installation unlock", e)
			}
			lease.Release()
			for _, q := range []string{"DROP OWNED BY " + pgx.Identifier{role}.Sanitize(), "DROP ROLE IF EXISTS " + pgx.Identifier{role}.Sanitize()} {
				if _, e := f.PostgreSQL.Exec(cleanup, q); e != nil {
					t.Error("owned role cleanup", e)
				}
			}
			var remains bool
			if e := f.PostgreSQL.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&remains); e != nil || remains {
				t.Error("owned runtime role remains", e)
			}
		}()
		if status, e := Prepare(ctx, f.PostgreSQL, lease, input); e != nil || status != Verified {
			t.Fatal("actual restricted role prepare", status, e)
		}
		if status, e := Prepare(ctx, f.PostgreSQL, lease, input); e != nil || status != Verified {
			t.Fatal("actual restricted role repeat", status, e)
		}
		runtime, e := pgxpool.NewWithConfig(ctx, pc)
		if e != nil {
			t.Fatal(e)
		}
		defer runtime.Close()
		var actor string
		if e = runtime.QueryRow(ctx, `SELECT current_user`).Scan(&actor); e != nil || actor != role {
			t.Fatal("actual runtime login", e)
		}
		for _, name := range registryTables {
			var allowed, forbidden bool
			if e = runtime.QueryRow(ctx, `SELECT has_table_privilege(current_user,$1,'SELECT') AND has_table_privilege(current_user,$1,'INSERT') AND has_table_privilege(current_user,$1,'UPDATE') AND has_table_privilege(current_user,$1,'DELETE'),has_table_privilege(current_user,$1,'TRUNCATE,REFERENCES,TRIGGER,SELECT WITH GRANT OPTION,INSERT WITH GRANT OPTION,UPDATE WITH GRANT OPTION,DELETE WITH GRANT OPTION')`, name).Scan(&allowed, &forbidden); e != nil || !allowed || forbidden {
				t.Fatal("actual table privileges", name, e)
			}
			if _, e = runtime.Exec(ctx, "UPDATE "+pgx.Identifier{name}.Sanitize()+" SET tenant_id=tenant_id WHERE false"); e != nil {
				t.Fatal("actual runtime DML", name, e)
			}
		}
		for _, q := range []string{"CREATE TABLE forbidden_runtime_table(id integer)", "TRUNCATE image_registry_pins", "UPDATE schema_migrations SET checksum=checksum WHERE false"} {
			_, e = runtime.Exec(ctx, q)
			var denied *pgconn.PgError
			if !errors.As(e, &denied) || denied.Code != "42501" {
				t.Fatal("actual permission refusal required", e)
			}
		}
		if status, e := Check(ctx, f.PostgreSQL, lease, input); e != nil || status != Verified {
			t.Fatal("actual runtime check after refusals", status, e)
		}
	}) {
		return
	}
	if !t.Run("empty_rollback_and_reapply", func(t *testing.T) {
		raw, e := migrations.Files.ReadFile("postgresql/000024_image_registry.down.sql")
		if e != nil {
			t.Fatal(e)
		}
		tx, e := f.PostgreSQL.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(context.Background())
		if _, e = tx.Exec(ctx, string(raw)); e != nil {
			t.Fatal("actual empty24 rollback", e)
		}
		if tag, e := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=24`); e != nil || tag.RowsAffected() != 1 {
			t.Fatal("actual24 journal rollback", e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal("actual selected rollback commit", e)
		}
		var count int
		if e = f.PostgreSQL.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relname LIKE 'image_registry_%'`).Scan(&count); e != nil || count != 0 {
			t.Fatal("registry objects remain after down", count, e)
		}
		if journal() != original {
			t.Fatal("rollback changed original journal")
		}
		if e = database.Migrate(ctx, f.PostgreSQL); e != nil {
			t.Fatal("actual23 to24 upgrade", e)
		}
		if e = database.Check(ctx, f.PostgreSQL); e != nil || journal() != original {
			t.Fatal("actual reapply journal", e)
		}
	}) {
		return
	}
	t.Run("identity_and_workspace_guards", func(t *testing.T) {
		for _, command := range []struct {
			q    string
			args []any
		}{
			{`INSERT INTO tenants(id,name,status) VALUES($1,'Registry activation tenant','active')`, []any{f.Tenant}},
			{`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Registry activation Workspace','active')`, []any{f.Workspace, f.Tenant}},
			{`INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,'registry-activation@example.invalid','registry-activation@example.invalid','Registry activation actor','en','active',true)`, []any{f.User}},
		} {
			if _, e := f.PostgreSQL.Exec(ctx, command.q, command.args...); e != nil {
				t.Fatal("actual synthetic guard setup", e)
			}
		}
		pin := uuid.NewString()
		digest := strings.Repeat("a", 64)
		if _, e := f.PostgreSQL.Exec(ctx, `INSERT INTO image_registry_pins(id,tenant_id,workspace_id,asset_id,source_archive_sha256,source_manifest_sha256,backend_identity,state,created_by,actor_epoch) VALUES($1,$2,$3,'synthetic-guard-asset',$4,$4,$5,'importing',$6,1)`, pin, f.Tenant, f.Workspace, digest, "sha256:"+digest, f.User); e != nil {
			t.Fatal("actual guarded pin setup", e)
		}
		_, e := f.PostgreSQL.Exec(ctx, `UPDATE image_registry_pins SET asset_id='changed',revision=revision+1 WHERE id=$1`, pin)
		var guard *pgconn.PgError
		if !errors.As(e, &guard) || guard.Code != "P0001" || guard.Message != "REGISTRY_IMMUTABLE_IDENTITY" {
			t.Fatal("actual immutable identity refusal", e)
		}
		_, e = f.PostgreSQL.Exec(ctx, `INSERT INTO image_registry_blobs(tenant_id,workspace_id,pin_id,ordinal,digest,size,media_type,kind) VALUES($1,$2,$3,0,$4,1,'application/octet-stream','layer')`, f.Tenant, uuid.NewString(), pin, "sha256:"+digest)
		var foreign *pgconn.PgError
		if !errors.As(e, &foreign) || foreign.Code != "23503" {
			t.Fatal("actual foreign Workspace refusal", e)
		}
		var asset string
		var revision int64
		var count int
		if e = f.PostgreSQL.QueryRow(ctx, `SELECT asset_id,revision FROM image_registry_pins WHERE id=$1`, pin).Scan(&asset, &revision); e != nil || asset != "synthetic-guard-asset" || revision != 1 {
			t.Fatal("rejected identity mutation changed row", e)
		}
		if e = f.PostgreSQL.QueryRow(ctx, `SELECT count(*) FROM image_registry_blobs`).Scan(&count); e != nil || count != 0 {
			t.Fatal("foreign Workspace mutation persisted", e)
		}
	})
}
