//go:build sso_target_qualification

package ssoqualification_test

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestM11TargetIsolationPreflight(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("SSO_TARGET_NATIVE_LINUX_AMD64_REQUIRED")
	}
	scope := os.Getenv("PROJECT_ONE_SSO_QUALIFICATION_SCOPE")
	id, e := uuid.Parse(scope)
	if e != nil || id == uuid.Nil || id.String() != scope {
		t.Fatal("SSO_TARGET_SCOPE_REQUIRED")
	}
	pc, e := pgxpool.ParseConfig(os.Getenv("PROJECT_ONE_TEST_DSN"))
	if e != nil {
		t.Fatal("SSO_TARGET_PRIVATE_DATABASE_CONFIG_INVALID")
	}
	suffix := strings.ReplaceAll(scope, "-", "")
	if pc.ConnConfig.Host != "127.0.0.1" || pc.ConnConfig.Database != "projectone_sso_"+suffix+"_test" || pc.ConnConfig.User != "projectone_sso_"+suffix {
		t.Fatal("SSO_TARGET_DEDICATED_LOOPBACK_DATABASE_AND_ROLE_REQUIRED")
	}
	pc.MaxConns = 2
	pc.MinConns = 0
	pc.ConnConfig.ConnectTimeout = 5 * time.Second
	pc.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal("SSO_TARGET_DATABASE_UNAVAILABLE")
	}
	defer db.Close()
	tx, e := db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if e != nil {
		t.Fatal("SSO_TARGET_READ_ONLY_PREFLIGHT_FAILED")
	}
	defer tx.Rollback(ctx)
	var cluster string
	var version, objects int
	var superuser bool
	if e = tx.QueryRow(ctx, `SELECT current_setting('cluster_name'),current_setting('server_version_num')::integer,(SELECT rolsuper FROM pg_roles WHERE rolname=current_user)`).Scan(&cluster, &version, &superuser); e != nil {
		t.Fatal("SSO_TARGET_CLUSTER_IDENTITY_UNAVAILABLE")
	}
	if cluster != "sso-qualification-"+scope || version != 180006 || !superuser {
		t.Fatal("SSO_TARGET_DISPOSABLE_MARKED_POSTGRES_18_6_CLUSTER_REQUIRED")
	}
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND c.relkind IN ('r','p','v','m','S','f')`).Scan(&objects); e != nil || objects != 0 {
		t.Fatal("SSO_TARGET_EMPTY_DISPOSABLE_DATABASE_REQUIRED")
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal("SSO_TARGET_READ_ONLY_PREFLIGHT_FAILED")
	}
	t.Log("Read-only preflight verified exact dedicated scope/database/operator/cluster marker/PostgreSQL18.6/native architecture and empty user storage before any test schema, role, provider or product migration.")
}
