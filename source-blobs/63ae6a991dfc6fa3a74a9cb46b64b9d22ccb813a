//go:build repository_integration && privatecluster_schema_integration

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
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/privateclusters"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

var privateClusterTables = []string{"private_cluster_connections", "private_cluster_intents", "private_cluster_runs"}

// Every selected leaf owns a fresh actual schema. These synthetic ledger rows
// exercise SQL/restore contracts only; no Kubernetes or OCI proof is invented.
func TestActualPostgreSQLPrivateClusterActivation(t *testing.T) {
	for _, leaf := range []struct {
		name string
		run  func(*testing.T, context.Context, repositoryfixture.Fixture)
	}{
		{"active_catalog_replay_upgrade_and_checksum", privateClusterCatalog},
		{"restricted_runtime_grants", privateClusterGrants},
		{"populated_rollback_refused", privateClusterRollback},
		{"borrowed_restore_fence_atomicity", privateClusterRestore},
		{"bounded_public_page_and_nondefault_recovery", privateClusterReadBudget},
		{"bounded_readiness_traversal_and_graph_refusal", privateClusterReadiness},
	} {
		t.Run(leaf.name, func(t *testing.T) {
			var f repositoryfixture.Fixture
			if leaf.name == "restricted_runtime_grants" {
				f = repositoryfixture.NewUnbootstrapped(t)
			} else {
				f = repositoryfixture.New(t)
			}
			if f.PostgreSQL == nil {
				t.Fatal("PRIVATE_CLUSTER_ACTUAL_POSTGRES_REQUIRED")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			leaf.run(t, ctx, f)
		})
	}
}

func privateClusterCatalog(t *testing.T, ctx context.Context, f repositoryfixture.Fixture) {
	t.Helper()
	journal := func() string {
		t.Helper()
		var value string
		if e := f.PostgreSQL.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(j) ORDER BY version)::text FROM schema_migrations j WHERE version<=26`).Scan(&value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	original := journal()
	up, e := migrations.Files.ReadFile("postgresql/000027_private_cluster.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(up)
	var count, latest, triggers int
	var checksum string
	if e = f.PostgreSQL.QueryRow(ctx, `SELECT count(*),max(version) FROM schema_migrations`).Scan(&count, &latest); e != nil || count != 27 || latest != 27 {
		t.Fatal("actual27 migration journal", count, latest, e)
	}
	if e = f.PostgreSQL.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version=27`).Scan(&checksum); e != nil || checksum != hex.EncodeToString(sum[:]) {
		t.Fatal("actual27 checksum", e)
	}
	rows, e := f.PostgreSQL.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename LIKE 'private_cluster_%' ORDER BY tablename`)
	if e != nil {
		t.Fatal(e)
	}
	var names []string
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			t.Fatal(e)
		}
		names = append(names, name)
	}
	e = rows.Err()
	rows.Close()
	if e != nil || !reflect.DeepEqual(names, privateClusterTables) {
		t.Fatal("actual27 table inventory", names, e)
	}
	if e = f.PostgreSQL.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgrelid IN(SELECT oid FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relname LIKE 'private_cluster_%')`).Scan(&triggers); e != nil || triggers != 5 {
		t.Fatal("actual27 immutable triggers", triggers, e)
	}
	if e = database.Migrate(ctx, f.PostgreSQL); e != nil || journal() != original {
		t.Fatal("repeat changed predecessor journal", e)
	}
	// The down script is an explicit stopped, empty-state operation; there is
	// no automatic rollback of populated history or CASCADE fallback.
	down, e := migrations.Files.ReadFile("postgresql/000027_private_cluster.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := f.PostgreSQL.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, string(down)); e != nil {
		t.Fatal("actual empty down", e)
	}
	if _, e = tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=27`); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(ctx, f.PostgreSQL); e == nil || e.Error() != "SCHEMA_INCOMPATIBLE" {
		t.Fatal("predecessor cannot serve current artifact", e)
	}
	if e = database.Migrate(ctx, f.PostgreSQL); e != nil || journal() != original {
		t.Fatal("actual26 to27 upgrade", e)
	}
	if _, e = f.PostgreSQL.Exec(ctx, `UPDATE schema_migrations SET checksum=repeat('0',64) WHERE version=27`); e != nil {
		t.Fatal(e)
	}
	if e = database.Migrate(ctx, f.PostgreSQL); e == nil || e.Error() != "MIGRATION_CHECKSUM_MISMATCH" {
		t.Fatal("mutated journal accepted", e)
	}
	if _, e = f.PostgreSQL.Exec(ctx, `UPDATE schema_migrations SET checksum=$1 WHERE version=27`, checksum); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(ctx, f.PostgreSQL); e != nil || journal() != original {
		t.Fatal("exact journal recovery", e)
	}
}

func privateClusterGrants(t *testing.T, ctx context.Context, f repositoryfixture.Fixture) {
	t.Helper()
	role := "m28_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password, e := NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	pc := f.PostgreSQL.Config().Copy()
	pc.ConnConfig.User, pc.ConnConfig.Password = role, password
	pc.MaxConns, pc.MinConns = 2, 0
	c := config.Config{}
	c.Database.MaxOpenConnections, c.Database.StatementTimeoutSeconds = 2, 5
	input := RuntimeInput{configuration: c, connection: pc}
	lease, e := f.PostgreSQL.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var runtime *pgxpool.Pool
	t.Cleanup(func() {
		if runtime != nil {
			runtime.Close()
		}
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if _, e := lease.Exec(cleanup, `SELECT pg_advisory_unlock($1)`, database.InstallationLockID); e != nil {
			t.Error(e)
		}
		lease.Release()
		var exists bool
		if e := f.PostgreSQL.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&exists); e != nil {
			t.Error("role cleanup readback", e)
			return
		}
		if !exists {
			return
		}
		for _, command := range []string{"DROP OWNED BY " + pgx.Identifier{role}.Sanitize(), "DROP ROLE " + pgx.Identifier{role}.Sanitize()} {
			if _, e := f.PostgreSQL.Exec(cleanup, command); e != nil {
				t.Error("exact disposable role cleanup", e)
			}
		}
		var remains bool
		if e := f.PostgreSQL.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&remains); e != nil || remains {
			t.Error("runtime role remains", e)
		}
	})
	var locked bool
	if e = lease.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, database.InstallationLockID).Scan(&locked); e != nil || !locked {
		t.Fatal("exact installation lock", e)
	}
	for range 2 {
		if status, e := Prepare(ctx, f.PostgreSQL, lease, input); e != nil || status != Verified {
			t.Fatal("actual role prepare/replay", status, e)
		}
	}
	runtime, e = pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range privateClusterTables {
		var allowed, forbidden bool
		if e = runtime.QueryRow(ctx, `SELECT has_table_privilege(current_user,$1,'SELECT') AND has_table_privilege(current_user,$1,'INSERT') AND has_table_privilege(current_user,$1,'UPDATE') AND has_table_privilege(current_user,$1,'DELETE'),has_table_privilege(current_user,$1,'TRUNCATE,REFERENCES,TRIGGER,SELECT WITH GRANT OPTION,INSERT WITH GRANT OPTION,UPDATE WITH GRANT OPTION,DELETE WITH GRANT OPTION')`, name).Scan(&allowed, &forbidden); e != nil || !allowed || forbidden {
			t.Fatal("actual scoped DML only", name, e)
		}
		if _, e = runtime.Exec(ctx, "UPDATE "+pgx.Identifier{name}.Sanitize()+" SET tenant_id=tenant_id WHERE false"); e != nil {
			t.Fatal("actual permitted DML", name, e)
		}
	}
	for _, command := range []string{"CREATE TABLE forbidden_private_cluster(id integer)", "TRUNCATE private_cluster_runs", "ALTER TABLE private_cluster_connections DISABLE TRIGGER ALL", "UPDATE schema_migrations SET checksum=checksum WHERE false"} {
		_, e := runtime.Exec(ctx, command)
		var denied *pgconn.PgError
		if !errors.As(e, &denied) || denied.Code != "42501" {
			t.Fatal("actual privilege refusal required", e)
		}
	}
	if status, e := Check(ctx, f.PostgreSQL, lease, input); e != nil || status != Verified {
		t.Fatal("denial changed runtime role", status, e)
	}
}

func privateClusterSeed(t *testing.T, ctx context.Context, f repositoryfixture.Fixture) string {
	t.Helper()
	id, hash := uuid.NewString(), strings.Repeat("a", 64)
	_, e := f.PostgreSQL.Exec(ctx, `INSERT INTO private_cluster_connections(id,tenant_id,workspace_id,profile_id,cluster_uid,namespace_uid,namespace_name,installation_id,profile,config_sha256,credential_sha256,manifest_sha256,image_digest,config_digest,state,revision,created_by) VALUES($1,$2,$3,'synthetic-sql-profile',$4,$5,'projectone-test-sql-restore',$6,'local-kind1.36.4-linux-arm64',$7,$7,$7,$8,$8,'active',1,$9)`, id, f.Tenant, f.Workspace, uuid.NewString(), uuid.NewString(), uuid.NewString(), hash, "sha256:"+hash, f.User)
	if e != nil {
		t.Fatal("explicit synthetic constraint row", e)
	}
	return id
}

func privateClusterRollback(t *testing.T, ctx context.Context, f repositoryfixture.Fixture) {
	t.Helper()
	id := privateClusterSeed(t, ctx, f)
	down, e := migrations.Files.ReadFile("postgresql/000027_private_cluster.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := f.PostgreSQL.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.Exec(ctx, string(down))
	var guard *pgconn.PgError
	if !errors.As(e, &guard) || guard.Code != "P0001" || guard.Message != "PRIVATE_CLUSTER_ROLLBACK_REQUIRES_EMPTY_STATE" {
		t.Fatal("populated down must refuse", e)
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	var state string
	if e = f.PostgreSQL.QueryRow(ctx, `SELECT state FROM private_cluster_connections WHERE id=$1`, id).Scan(&state); e != nil || state != "active" {
		t.Fatal("rollback lost original row", e)
	}
	if e = database.Check(ctx, f.PostgreSQL); e != nil {
		t.Fatal("refusal altered migration journal", e)
	}
}

func privateClusterRestore(t *testing.T, ctx context.Context, f repositoryfixture.Fixture) {
	t.Helper()
	id := privateClusterSeed(t, ctx, f)
	run, lease := uuid.NewString(), uuid.NewString()
	hash := strings.Repeat("b", 64)
	if _, e := f.PostgreSQL.Exec(ctx, `INSERT INTO private_cluster_runs(id,tenant_id,workspace_id,connection_id,connection_revision,created_by,actor_epoch,created_session_id,created_token_hash,config_sha256,request,spec_sha256,phase,lease_uid,revision) VALUES($1,$2,$3,$4,1,$5,1,$6,$7,$8,'{}',$8,'lease_acquired',$9,1)`, run, f.Tenant, f.Workspace, id, f.User, uuid.NewString(), make([]byte, 32), hash, lease); e != nil {
		t.Fatal("synthetic unresolved remote hold", e)
	}
	if _, e := f.PostgreSQL.Exec(ctx, `UPDATE private_cluster_connections SET active_run_id=$1,revision=revision+1 WHERE id=$2`, run, id); e != nil {
		t.Fatal(e)
	}
	accounts := &identity.Service{Storage: f.Store}
	policy := config.DefaultRuntimePolicy()
	accounts.Config.Runtime = &policy
	if e := privateclusters.InvalidateAuthoritiesWithin(ctx, f.Store); e == nil || e.Error() != "PRIVATE_CLUSTER_TRANSACTION_REQUIRED" {
		t.Fatal("pool accepted as borrowed restore transaction", e)
	}
	read := func() (string, int64, int64, string) {
		t.Helper()
		var state, active, actualLease, phase string
		var revision, epoch int64
		if e := f.PostgreSQL.QueryRow(ctx, `SELECT c.state,c.revision,c.active_run_id::text,u.auth_epoch,r.lease_uid::text,r.phase FROM private_cluster_connections c JOIN private_cluster_runs r ON r.id=c.active_run_id JOIN users u ON u.id=c.created_by WHERE c.id=$1`, id).Scan(&state, &revision, &active, &epoch, &actualLease, &phase); e != nil || active != run || actualLease != lease || phase != "lease_acquired" {
			t.Fatal("restore changed unresolved physical identity", e)
		}
		return state, revision, epoch, active
	}
	if _, e := f.PostgreSQL.Exec(ctx, `CREATE FUNCTION reject_private_cluster_restore() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic restore failure'; END$$; CREATE TRIGGER reject_private_cluster_restore BEFORE UPDATE ON private_cluster_connections FOR EACH ROW EXECUTE FUNCTION reject_private_cluster_restore()`); e != nil {
		t.Fatal(e)
	}
	if e := recovery.InvalidateRestored(ctx, accounts, "Synthetic isolated private cluster restore failure"); e == nil {
		t.Fatal("failed module fence reported success")
	}
	if state, revision, epoch, _ := read(); state != "active" || revision != 2 || epoch != 1 {
		t.Fatal("failed restore did not roll back original authorities", state, revision, epoch)
	}
	var audits int
	if e := f.PostgreSQL.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='operator.auth_invalidated'`).Scan(&audits); e != nil || audits != 0 {
		t.Fatal("rolled-back restore audit persisted", audits, e)
	}
	if _, e := f.PostgreSQL.Exec(ctx, `DROP TRIGGER reject_private_cluster_restore ON private_cluster_connections; DROP FUNCTION reject_private_cluster_restore()`); e != nil {
		t.Fatal(e)
	}
	if e := recovery.InvalidateRestored(ctx, accounts, "Synthetic isolated private cluster restore"); e != nil {
		t.Fatal("actual original restore composition", e)
	}
	if state, revision, epoch, _ := read(); state != "restore_fenced" || revision != 3 || epoch != 2 {
		t.Fatal("restore fence/authority mismatch", state, revision, epoch)
	}
	if e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		return privateclusters.InvalidateAuthoritiesWithin(ctx, q)
	}); e != nil {
		t.Fatal("repeat fence", e)
	}
	if state, revision, _, _ := read(); state != "restore_fenced" || revision != 3 {
		t.Fatal("repeat fence changed history", state, revision)
	}
}
