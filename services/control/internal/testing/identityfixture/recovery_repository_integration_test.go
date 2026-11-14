//go:build linux && repository_integration && identity_fixture_recovery_integration

package identityfixture

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

const identityCustodyChild = "PROJECT_ONE_IDENTITY_FIXTURE_CUSTODY_CHILD"
const identityCustodyFailure = "IDENTITY_CUSTODY_INTENTIONAL_CHILD_FAILURE"
const identityCustodyRequest = "11111111-1111-4111-8111-111111111111"
const identityCustodyIntent = `{"operation":"synthetic_object_upload","outcome":"unknown","part":1}`

var identityCustodySchema = regexp.MustCompile(`^m01_[a-f0-9]{32}$`)

func identityCustodyRuntime(t *testing.T) {
	t.Helper()
	if _, inherited := os.LookupEnv("GOTMPDIR"); inherited {
		t.Fatal("identity custody runtime must not inherit compiler GOTMPDIR")
	}
	root := os.Getenv("TMPDIR")
	info, err := os.Lstat(root)
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("private canonical runtime TMPDIR required")
	}
	temp := t.TempDir()
	var fs syscall.Statfs_t
	if syscall.Statfs(temp, &fs) != nil || fs.Type != 0xef53 {
		t.Fatal("actual identity custody TempDir must be durable ext4")
	}
	t.Logf("IDENTITY_CUSTODY_RUNTIME path=%s filesystem=ext4 gotmpdir=unset", temp)
}

func identityCustodyObserver(t *testing.T, ctx context.Context, schema string) *pgxpool.Pool {
	t.Helper()
	dsn, err := databaseInput(os.Getenv("PROJECT_ONE_TEST_DSN_FILE"), os.Getenv("PROJECT_ONE_TEST_DSN"))
	if err != nil || dsn == "" || !identityCustodySchema.MatchString(schema) {
		t.Fatal("explicit isolated identity custody input required")
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil || c.ConnConfig.Host != "127.0.0.1" || c.ConnConfig.Port < 1024 || !strings.HasSuffix(c.ConnConfig.Database, "_test") {
		t.Fatal("exact isolated loopback test database required")
	}
	c.MaxConns = 1
	c.MinConns = 0
	c.ConnConfig.RuntimeParams["search_path"] = schema
	c.ConnConfig.RuntimeParams["statement_timeout"] = fixtureStatementTimeoutMilliseconds
	c.ConnConfig.RuntimeParams["lock_timeout"] = fixtureLockTimeoutMilliseconds
	p, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		t.Fatal("independent identity custody connection failed")
	}
	return p
}

func identityCustodyExists(t *testing.T, ctx context.Context, p *pgxpool.Pool, schema string) bool {
	t.Helper()
	var exists bool
	if err := p.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)", schema).Scan(&exists); err != nil {
		t.Fatal("exact identity schema observation failed", err)
	}
	return exists
}

func TestIdentityFixtureCustodyChild(t *testing.T) {
	mode := os.Getenv(identityCustodyChild)
	switch mode {
	case "recoverable_failure", "recoverable_success", "legacy_failure", "legacy_success", "full_recoverable_failure", "full_recoverable_success":
	default:
		t.Fatal("exact explicit identity custody child mode required")
	}
	identityCustodyRuntime(t)
	setup := SetupOptions{TimeoutSeconds: 180, StatementTimeoutSeconds: 30}
	var service *identity.Service
	if strings.HasPrefix(mode, "full_recoverable_") {
		if os.Getenv("PROJECT_ONE_TEST_APPLICATION_PROFILE") != "" {
			t.Fatal("full custody control requires the original default catalog")
		}
		service = NewRecoverableWithSetup(t, setup)
	} else if strings.HasPrefix(mode, "recoverable_") {
		service = NewM16RecoverableWithSetup(t, setup)
	} else {
		service = NewM16WithSetup(t, setup)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var schema, statement, lock string
	var backend, setupConsumers int
	if err := service.DB.QueryRow(ctx, "SELECT current_schema(),current_setting('statement_timeout'),current_setting('lock_timeout'),pg_backend_pid()").Scan(&schema, &statement, &lock, &backend); err != nil || !identityCustodySchema.MatchString(schema) || statement != "5s" || lock != "3s" {
		t.Fatal("canonical runtime budgets changed", err)
	}
	if err := service.DB.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name=$1", "projectone-identity-setup-"+schema).Scan(&setupConsumers); err != nil || setupConsumers != 0 {
		t.Fatal("setup pool did not join before runtime", err)
	}
	if strings.HasPrefix(mode, "full_") {
		var tables, versions, latest int
		if err := service.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()),count(*),max(version) FROM schema_migrations").Scan(&tables, &versions, &latest); err != nil || tables != 108 || versions != 27 || latest != 27 {
			t.Fatal("original complete platform catalog changed", tables, versions, latest, err)
		}
		if err := database.Check(ctx, service.DB); err != nil {
			t.Fatal("original complete platform migration checksums changed", err)
		}
		t.Logf("IDENTITY_CUSTODY_CATALOG tables=%d migrations=%d latest=%d full_default=true", tables, versions, latest)
	}
	t.Logf("IDENTITY_CUSTODY_BACKEND=%d", backend)
	tenant, workspace, actor, event := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	metadata, err := json.Marshal(map[string]any{"synthetic": true, "state": "unknown", "intent": identityCustodyIntent})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := service.DB.Begin(ctx)
	if err != nil {
		t.Fatal("synthetic metadata transaction failed", err)
	}
	defer tx.Rollback(ctx)
	commands := []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO tenants(id,name,status) VALUES($1,'Identity custody tenant','active')", []any{tenant}},
		{"INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Identity custody Workspace','active')", []any{workspace, tenant}},
		{"INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,'identity-custody@example.invalid','identity-custody@example.invalid','Synthetic identity custody actor','en','active')", []any{actor}},
		{"INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id,metadata) VALUES($1,clock_timestamp(),$2,$3,$4,'operator','fixture.identity_custody_unknown','failure','identity_fixture',$5,$5,$6::jsonb)", []any{event, actor, tenant, workspace, identityCustodyRequest, string(metadata)}},
	}
	for _, command := range commands {
		if _, err := tx.Exec(ctx, command.sql, command.args...); err != nil {
			t.Fatal("synthetic metadata insert failed", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal("synthetic metadata commit failed", err)
	}
	sum := sha256.Sum256([]byte(identityCustodyIntent))
	t.Logf("IDENTITY_CUSTODY_METADATA_COMMITTED schema=%s intent_sha256=%x", schema, sum)
	if strings.HasSuffix(mode, "_failure") {
		t.Fatal(identityCustodyFailure)
	}
}

func TestActualIdentityFixtureFailureCustody(t *testing.T) {
	if os.Getenv(identityCustodyChild) != "" || os.Getenv("PROJECT_ONE_TEST_NATIVE_POSTGRES_INPUT") != "" {
		t.Fatal("explicit independent parent fixture required")
	}
	identityCustodyRuntime(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"recoverable_failure", "recoverable_success", "legacy_failure", "legacy_success", "full_recoverable_failure", "full_recoverable_success"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 240*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, binary, "-test.v", "-test.timeout=210s", "-test.run=^TestIdentityFixtureCustodyChild$")
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, identityCustodyChild+"=") {
					child.Env = append(child.Env, entry)
				}
			}
			child.Env = append(child.Env, identityCustodyChild+"="+mode)
			output, runError := child.CombinedOutput()
			sum := sha256.Sum256(output)
			t.Logf("IDENTITY_CUSTODY_CHILD_LOG mode=%s sha256=%x base64=%s", mode, sum, base64.StdEncoding.EncodeToString(output))
			exit := 0
			if runError != nil {
				var failed *exec.ExitError
				if !errors.As(runError, &failed) {
					t.Fatal("custody child did not complete", runError)
				}
				exit = failed.ExitCode()
			}
			text := string(output)
			failure := strings.HasSuffix(mode, "_failure")
			if failure && (exit != 1 || !strings.Contains(text, identityCustodyFailure)) || !failure && exit != 0 || strings.Contains(text, "--- SKIP:") || strings.Contains(text, "WARNING: DATA RACE") || strings.Contains(text, "panic:") {
				t.Fatal("child outcome did not match explicit custody mode", exit)
			}
			matches := regexp.MustCompile(`PROJECT_ONE_OWNED_SCHEMA=(m01_[a-f0-9]{32})`).FindAllStringSubmatch(text, -1)
			if len(matches) != 1 {
				t.Fatal("exact owned child schema was not announced")
			}
			schema := matches[0][1]
			observer := identityCustodyObserver(t, ctx, schema)
			defer observer.Close()
			t.Cleanup(func() {
				if t.Failed() {
					t.Log("IDENTITY_CUSTODY_REGRESSION_RETAINED_SCHEMA", schema)
					return
				}
				cleanup, stop := context.WithTimeout(context.Background(), fixtureMaintenanceCleanupSeconds*time.Second)
				defer stop()
				pool := identityCustodyObserver(t, cleanup, schema)
				defer pool.Close()
				if identityCustodyExists(t, cleanup, pool, schema) {
					if _, err := disposeOwnedPostgresTables(cleanup, pool, schema); err != nil {
						t.Error("disposable custody schema table cleanup failed", err)
						return
					}
					if _, err := pool.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
						t.Error("disposable custody schema drop failed", err)
						return
					}
				}
				if identityCustodyExists(t, cleanup, pool, schema) {
					t.Error("disposable custody schema remains")
					return
				}
				t.Log("IDENTITY_CUSTODY_CLEANUP", schema, "absent=true")
			})
			retained := mode == "recoverable_failure" || mode == "full_recoverable_failure"
			if identityCustodyExists(t, ctx, observer, schema) != retained {
				t.Fatal("original failed/successful schema custody mismatch")
			}
			if retained {
				if !strings.Contains(text, "IDENTITY_FIXTURE_FAILURE_PRESERVED_SCHEMA="+schema+" setup_and_runtime_pools_closed=true") {
					t.Fatal("original pool join/recovery identity missing")
				}
				var state, intent, request string
				var count int
				if err := observer.QueryRow(ctx, "SELECT metadata->>'state',metadata->>'intent',request_id::text FROM "+pgx.Identifier{schema, "audit_events"}.Sanitize()+" WHERE action='fixture.identity_custody_unknown'").Scan(&state, &intent, &request); err != nil || state != "unknown" || intent != identityCustodyIntent || request != identityCustodyRequest {
					t.Fatal("independent original metadata/intent readback failed", err)
				}
				if err := observer.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{schema, "audit_events"}.Sanitize()+" WHERE action='fixture.identity_custody_unknown'").Scan(&count); err != nil || count != 1 {
					t.Fatal("original retained audit count changed", err)
				}
				intentSum := sha256.Sum256([]byte(intent))
				t.Logf("IDENTITY_CUSTODY_READBACK schema=%s state=unknown audits=%d intent_sha256=%s", schema, count, hex.EncodeToString(intentSum[:]))
			}
			t.Logf("IDENTITY_CUSTODY_CASE mode=%s child_exit=%d retained=%t", mode, exit, retained)
		})
	}
}
