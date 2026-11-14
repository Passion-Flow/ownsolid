//go:build linux && repository_integration && repository_fixture_recovery_integration

package repositoryfixture

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
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const custodyChildEnvironment = "PROJECT_ONE_FIXTURE_CUSTODY_CHILD"
const custodyOperation = "11111111-1111-1111-1111-111111111111"
const custodyInstance = "22222222-2222-2222-2222-222222222222"
const custodyIntentKey = "33333333-3333-3333-3333-333333333333"
const custodyInput = `{"kind":"backup","reason":"Synthetic metadata custody only","idempotency_key":"33333333-3333-3333-3333-333333333333","version":1,"confirm_isolated":false}`
const custodyFailure = "FIXTURE_CUSTODY_INTENTIONAL_CHILD_FAILURE"
const linuxExt4Magic = 0xef53

func custodyFingerprint() string {
	sum := sha256.Sum256([]byte(custodyInstance + "\x00" + custodyInput + "\x00" + strings.Repeat("a", 64)))
	return hex.EncodeToString(sum[:])
}

func custodyRuntime(t *testing.T) {
	t.Helper()
	if _, present := os.LookupEnv("GOTMPDIR"); present {
		t.Fatal("fixture custody runtime must not inherit compiler GOTMPDIR")
	}
	root := os.Getenv("TMPDIR")
	info, err := os.Lstat(root)
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("explicit private durable runtime TMPDIR required")
	}
	temp := t.TempDir()
	var fs syscall.Statfs_t
	if syscall.Statfs(temp, &fs) != nil || fs.Type != linuxExt4Magic {
		t.Fatal("actual fixture custody runtime TempDir must be ext4")
	}
	t.Logf("FIXTURE_CUSTODY_RUNTIME path=%s filesystem=ext4 gotmpdir=unset", temp)
}

func custodyPool(t *testing.T, ctx context.Context, schema string) *pgxpool.Pool {
	t.Helper()
	raw, err := config.Secret(os.Getenv("PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE"))
	if err != nil {
		t.Fatal("actual protected PostgreSQL fixture input required")
	}
	c, err := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if err != nil || c.ConnConfig.Host != "127.0.0.1" || c.ConnConfig.Port < 1024 || !strings.HasSuffix(c.ConnConfig.Database, "_test") || !repositoryOwnedSchema.MatchString(schema) {
		t.Fatal("exact isolated PostgreSQL fixture required")
	}
	c.MaxConns = 1
	c.ConnConfig.RuntimeParams["search_path"] = schema
	c.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	c.ConnConfig.RuntimeParams["lock_timeout"] = "3000"
	p, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		t.Fatal("independent fixture connection failed")
	}
	return p
}

func custodyExists(t *testing.T, ctx context.Context, p *pgxpool.Pool, schema string) bool {
	t.Helper()
	var exists bool
	if err := p.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)", schema).Scan(&exists); err != nil {
		t.Fatal("exact fixture schema observation failed")
	}
	return exists
}

// Each child creates only synthetic controller metadata. No M17 command,
// container effect, source/target scope or native archive is invoked here.
func TestRepositoryFixtureCustodyChild(t *testing.T) {
	mode := os.Getenv(custodyChildEnvironment)
	switch mode {
	case "recoverable_failure", "recoverable_success", "legacy_failure", "legacy_success":
	default:
		t.Fatal("explicit exact fixture custody child mode required")
	}
	custodyRuntime(t)
	var f Fixture
	if strings.HasPrefix(mode, "recoverable_") {
		f = NewRecoverableUnbootstrapped(t)
	} else {
		f = NewUnbootstrapped(t)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tx, err := f.PostgreSQL.Begin(ctx)
	if err != nil {
		t.Fatal("synthetic metadata transaction failed")
	}
	defer tx.Rollback(ctx)
	actor, err := json.Marshal(map[string]any{"user": f.User, "audience": "portal", "session": uuid.NewString(), "token_hash": make([]byte, 32), "epoch": 1})
	if err != nil {
		t.Fatal(err)
	}
	commands := []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO tenants(id,name,status) VALUES($1,'Fixture custody tenant','active')", []any{f.Tenant}},
		{"INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Fixture custody Workspace','active')", []any{f.Workspace, f.Tenant}},
		{"INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,'custody@example.invalid','custody@example.invalid','Fixture custody actor','en','active')", []any{f.User}},
		{"INSERT INTO middleware_instances(id,tenant_id,workspace_id,profile_key,kind,name,profile_fingerprint,created_by,creation_key,creation_fingerprint) VALUES($1,$2,$3,'custody','platform_owned','Synthetic metadata fixture',$4,$5,$6,$4)", []any{custodyInstance, f.Tenant, f.Workspace, strings.Repeat("a", 64), f.User, uuid.NewString()}},
		{"INSERT INTO middleware_operations(id,tenant_id,workspace_id,instance_id,kind,state,requested_by,intent_key,intent_fingerprint,input_json,actor_json,instance_version,profile_fingerprint,request_id,attempt_id,error_code,completed_at) VALUES($1,$2,$3,$4,'backup','unknown',$5,$6,$7,$8,$9,1,$10,$11,$12,'MIDDLEWARE_EXECUTION_UNCERTAIN',clock_timestamp())", []any{custodyOperation, f.Tenant, f.Workspace, custodyInstance, f.User, custodyIntentKey, custodyFingerprint(), custodyInput, string(actor), strings.Repeat("a", 64), uuid.NewString(), uuid.NewString()}},
		{"INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id,metadata) VALUES($1,clock_timestamp(),$2,$3,$4,'system','middleware.operation_unknown','failure','middleware_operation',$5,$6,'{\"code\":\"MIDDLEWARE_EXECUTION_UNCERTAIN\",\"synthetic\":true}'::jsonb)", []any{uuid.NewString(), f.User, f.Tenant, f.Workspace, custodyOperation, uuid.NewString()}},
	}
	for _, command := range commands {
		if _, err = tx.Exec(ctx, command.sql, command.args...); err != nil {
			t.Fatal("synthetic controller metadata insert failed", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal("synthetic controller metadata commit failed", err)
	}
	t.Log("FIXTURE_CUSTODY_METADATA_COMMITTED", os.Getenv("PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA"))
	if strings.HasSuffix(mode, "_failure") {
		t.Fatal(custodyFailure)
	}
}

func TestActualRepositoryFixtureFailureCustody(t *testing.T) {
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "postgresql" || os.Getenv(custodyChildEnvironment) != "" {
		t.Fatal("explicit parent selected PostgreSQL profile required")
	}
	custodyRuntime(t)
	setup, err := loadSetupPolicy()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"recoverable_failure", "recoverable_success", "legacy_failure", "legacy_success"} {
		t.Run(mode, func(t *testing.T) {
			schema := "m20_repository_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Duration(setup.SetupSeconds+repositoryFixtureCleanupSeconds)*time.Second)
			defer cancel()
			before := custodyPool(t, ctx, schema)
			if custodyExists(t, ctx, before, schema) {
				before.Close()
				t.Fatal("new exact fixture schema already exists")
			}
			before.Close()
			t.Cleanup(func() {
				cleanup, stop := context.WithTimeout(context.Background(), repositoryFixtureCleanupSeconds*time.Second)
				defer stop()
				pool := custodyPool(t, cleanup, schema)
				defer pool.Close()
				if t.Failed() {
					t.Log("FIXTURE_CUSTODY_REGRESSION_RETAINED_SCHEMA", schema)
					return
				}
				if custodyExists(t, cleanup, pool, schema) {
					// This is explicitly disposable metadata only. Required
					// child/raw and independent row readback precede cleanup.
					if err := disposeOwnedRepositoryTables(cleanup, pool, schema); err != nil {
						t.Error("exact synthetic retained schema cleanup failed", err)
						return
					}
					if _, err := pool.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
						t.Error("exact synthetic retained schema drop failed")
						return
					}
				}
				if custodyExists(t, cleanup, pool, schema) {
					t.Error("synthetic fixture schema remained after cleanup")
				}
				t.Log("FIXTURE_CUSTODY_CLEANUP", schema, "absent=true")
			})
			child := exec.CommandContext(ctx, binary, "-test.v", "-test.run=^TestRepositoryFixtureCustodyChild$")
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, custodyChildEnvironment+"=") && !strings.HasPrefix(entry, "PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA=") {
					child.Env = append(child.Env, entry)
				}
			}
			child.Env = append(child.Env, custodyChildEnvironment+"="+mode, "PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA="+schema)
			output, runError := child.CombinedOutput()
			sum := sha256.Sum256(output)
			t.Logf("FIXTURE_CUSTODY_CHILD_LOG mode=%s sha256=%x base64=%s", mode, sum, base64.StdEncoding.EncodeToString(output))
			exit := 0
			if runError != nil {
				var failed *exec.ExitError
				if !errors.As(runError, &failed) {
					t.Fatal("fixture child did not complete", runError)
				}
				exit = failed.ExitCode()
			}
			failure := strings.HasSuffix(mode, "_failure")
			if failure && (exit != 1 || !strings.Contains(string(output), custodyFailure)) || !failure && exit != 0 {
				t.Fatal("actual child outcome did not match explicit scenario", exit)
			}
			retained := mode == "recoverable_failure"
			// Reopen only after the child and all of its cleanups have exited.
			observer := custodyPool(t, ctx, schema)
			defer observer.Close()
			if custodyExists(t, ctx, observer, schema) != retained {
				t.Fatal("exact failed/successful fixture custody mismatch")
			}
			if retained {
				if !strings.Contains(string(output), "REPOSITORY_FAILURE_PRESERVED_SCHEMA "+schema) {
					t.Fatal("nonsecret exact recovery identity missing")
				}
				table := pgx.Identifier{schema, "middleware_operations"}.Sanitize()
				var state, input, key, fingerprint string
				var operations, audits int
				if err := observer.QueryRow(ctx, "SELECT state,input_json,intent_key::text,intent_fingerprint FROM "+table+" WHERE id=$1", custodyOperation).Scan(&state, &input, &key, &fingerprint); err != nil || state != "unknown" || input != custodyInput || key != custodyIntentKey || fingerprint != custodyFingerprint() {
					t.Fatal("reopened original operation/intent changed or disappeared", err)
				}
				if err := observer.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&operations); err != nil || operations != 1 {
					t.Fatal("unexpected retained operation count", err)
				}
				if err := observer.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{schema, "audit_events"}.Sanitize()+" WHERE target_id=$1 AND action='middleware.operation_unknown' AND outcome='failure' AND metadata->>'code'='MIDDLEWARE_EXECUTION_UNCERTAIN'", custodyOperation).Scan(&audits); err != nil || audits != 1 {
					t.Fatal("reopened original audit changed or disappeared", err)
				}
				t.Logf("FIXTURE_CUSTODY_READBACK schema=%s state=unknown operations=%d audits=%d intent_sha256=%s", schema, operations, audits, fingerprint)
			}
			t.Logf("FIXTURE_CUSTODY_CASE mode=%s child_exit=%d retained=%t", mode, exit, retained)
		})
	}
}
