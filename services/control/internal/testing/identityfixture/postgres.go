package identityfixture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var isolatedSchema = regexp.MustCompile(`^m01_[a-f0-9]{32}$`)
var isolatedRestoreDatabase = regexp.MustCompile(`^m12_restore_[a-f0-9]{32}_test$`)

// PostgresFixture verifies the exact owned loopback test database/container before
// database-aware export or creating a fresh disposable restore database. It never
// accepts a production database or substitutes the historical fixture.
type PostgresFixture struct {
	Container, Context, Database, User, Schema string
	native                                     *nativePostgresInput
}

// PreflightOwnedPostgres performs only reads before another test creates its
// disposable schema. The generated search path is not created or adopted.
func PreflightOwnedPostgres(t *testing.T) PostgresFixture {
	t.Helper()
	input, e := databaseInput(os.Getenv("PROJECT_ONE_TEST_DSN_FILE"), os.Getenv("PROJECT_ONE_TEST_DSN"))
	if e != nil || input == "" {
		t.Fatal("EXPLICIT_TEST_DATABASE_CONFIGURATION_REQUIRED")
	}
	c, e := pgxpool.ParseConfig(input)
	if e != nil {
		t.Fatal("EXPLICIT_TEST_DATABASE_CONFIGURATION_REQUIRED")
	}
	c.MaxConns = 1
	if os.Getenv("PROJECT_ONE_TEST_NATIVE_POSTGRES_INPUT") != "" {
		delete(c.ConnConfig.RuntimeParams, "search_path")
	} else {
		c.ConnConfig.RuntimeParams["search_path"] = "m01_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, e := pgxpool.NewWithConfig(ctx, c)
	if e != nil {
		t.Fatal("EXPLICIT_TEST_DATABASE_READ_PREFLIGHT_FAILED")
	}
	defer pool.Close()
	if file := os.Getenv("PROJECT_ONE_TEST_NATIVE_POSTGRES_INPUT"); file != "" {
		return nativePostgresScope(t, pool, file, false)
	}
	return OwnedPostgres(t, pool)
}

func OwnedPostgres(t *testing.T, pool *pgxpool.Pool) PostgresFixture {
	t.Helper()
	if file := os.Getenv("PROJECT_ONE_TEST_NATIVE_POSTGRES_INPUT"); file != "" {
		return OwnedNativePostgres(t, pool, file)
	}
	c := pool.Config().ConnConfig
	selected := PostgresFixture{Container: os.Getenv("PROJECT_ONE_FIXTURE_CONTAINER"), Context: os.Getenv("PROJECT_ONE_FIXTURE_DOCKER_CONTEXT"), Database: c.Database, User: c.User, Schema: c.RuntimeParams["search_path"]}
	if c.Host != "127.0.0.1" || !strings.HasSuffix(c.Database, "_test") || !isolatedSchema.MatchString(selected.Schema) || selected.Container == "" || selected.Context == "" {
		t.Fatal("EXPLICIT_ISOLATED_POSTGRES_FIXTURE_REQUIRED")
	}
	inspectContext, cancelInspect := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelInspect()
	raw, e := exec.CommandContext(inspectContext, "docker", "--context", selected.Context, "inspect", selected.Container, "--format", `{"owner":{{json (index .Config.Labels "projectone.owner")}},"milestone":{{json (index .Config.Labels "projectone.milestone")}},"fixture_id":{{json (index .Config.Labels "projectone.fixture_id")}},"running":{{json .State.Running}},"ports":{{json .HostConfig.PortBindings}}}`).Output()
	var actual struct {
		Owner     string `json:"owner"`
		Milestone string `json:"milestone"`
		FixtureID string `json:"fixture_id"`
		Running   bool   `json:"running"`
		Ports     map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"ports"`
	}
	if e != nil || json.Unmarshal(raw, &actual) != nil || actual.Owner != "mvp-development" || (actual.Milestone != "M12" && actual.Milestone != "M02" && actual.Milestone != "M03" && actual.Milestone != "M04" && actual.Milestone != "M05" && actual.Milestone != "M06" && actual.Milestone != "M07" && actual.Milestone != "M08") || !actual.Running || len(actual.Ports["5432/tcp"]) != 1 {
		t.Fatal("OWNED_M12_DATABASE_CONTAINER_REQUIRED")
	}
	port := actual.Ports["5432/tcp"][0]
	published, e := strconv.ParseUint(port.HostPort, 10, 16)
	if e != nil || port.HostIP != "127.0.0.1" || uint16(published) != c.Port {
		t.Fatal("EXACT_LOOPBACK_DATABASE_PORT_REQUIRED")
	}

	id, parseError := uuid.Parse(actual.FixtureID)
	var cluster, version string
	queryError := pool.QueryRow(inspectContext, "SELECT current_setting('cluster_name'),current_setting('server_version_num')").Scan(&cluster, &version)
	if parseError != nil || id == uuid.Nil || id.String() != actual.FixtureID || queryError != nil || cluster != map[string]string{"M12": "knowledge-fixture-", "M02": "workspaces-fixture-", "M03": "nodes-fixture-", "M04": "components-fixture-", "M05": "maintenance-fixture-", "M06": "models-fixture-", "M07": "gateway-fixture-", "M08": "datasets-fixture-"}[actual.Milestone]+actual.FixtureID || version != "180006" {
		t.Fatal("EXACT_OWNED_DATABASE_CLUSTER_REQUIRED")
	}
	return selected
}
func (f PostgresFixture) command(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "docker", append([]string{"--context", f.Context}, args...)...)
}
func (f PostgresFixture) DumpSchema(t *testing.T, ctx context.Context) []byte {
	t.Helper()
	if !f.ownedSchema() {
		t.Fatal("EXACT_TEST_SCHEMA_REQUIRED")
	}
	command := f.command(ctx, "exec", f.Container, "pg_dump", "-U", f.User, "-d", f.Database, "-Fc", "--no-owner", "--no-privileges", "--lock-wait-timeout=5s", "--schema", f.Schema)
	if f.native != nil {
		command = f.nativeCommand(t, ctx, "pg_dump", f.Database, "-Fc", "--no-owner", "--no-privileges", "--lock-wait-timeout=5s", "--schema", f.Schema)
	}
	var data fixtureArchive
	var diagnostic bytes.Buffer
	command.Stdout = &data
	command.Stderr = &diagnostic
	if f.native != nil {
		command.Stderr = &fixtureDiagnostic{}
	}
	if e := command.Run(); e != nil || data.Len() < 100 || data.Len() > 64*1024*1024 {
		t.Fatal("ACTUAL_BOUNDED_LOGICAL_EXPORT_FAILED")
	}
	return data.Bytes()
}

// RestoreFresh creates only a generated empty _test database, explicitly installs
// the approved extension there, restores the schema, and returns an isolated pool.
// Cleanup force-drops only that exact newly created database.
func (f PostgresFixture) RestoreFresh(t *testing.T, ctx context.Context, source *pgxpool.Pool, archive []byte, account ...bool) *pgxpool.Pool {
	if len(account) > 1 {
		t.Fatal("RESTORE_PROFILE_AMBIGUOUS")
	}
	accountOnly := len(account) == 1 && account[0]
	t.Helper()
	target := "m12_restore_" + strings.ReplaceAll(uuid.NewString(), "-", "") + "_test"
	if f.native != nil {
		target = f.native.RestoreDatabase
		if !f.ownedSchema() || source.Config().ConnConfig.Database != f.Database {
			t.Fatal("OWNED_NATIVE_RESTORE_SOURCE_REQUIRED")
		}
		var count int
		if e := source.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname=$1", target).Scan(&count); e != nil || count != 0 {
			t.Fatal("OWNED_NATIVE_EMPTY_RESTORE_TARGET_REQUIRED")
		}
	}
	if !isolatedRestoreDatabase.MatchString(target) {
		t.Fatal("GENERATED_RESTORE_DATABASE_REQUIRED")
	}
	adminConfig := source.Config().Copy()
	delete(adminConfig.ConnConfig.RuntimeParams, "search_path")
	// Database creation/drop may wait for a checkpoint; they are maintenance,
	// independent of the source service's shorter query deadline.
	adminConfig.ConnConfig.RuntimeParams["statement_timeout"] = fixtureMaintenanceStatementMilliseconds
	adminConfig.ConnConfig.RuntimeParams["lock_timeout"] = fixtureMaintenanceLockMilliseconds
	operator, e := pgxpool.NewWithConfig(ctx, adminConfig)
	if e != nil {
		t.Fatal("RESTORE_OPERATOR_UNAVAILABLE")
	}
	t.Cleanup(operator.Close)
	if _, e = operator.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{target}.Sanitize()+" TEMPLATE template0"); e != nil {
		t.Fatal("ISOLATED_EMPTY_DATABASE_CREATE_FAILED")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), fixtureMaintenanceCleanupSeconds*time.Second)
		defer cancel()
		if _, e := operator.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{target}.Sanitize()+" WITH (FORCE)"); e != nil {
			var server *pgconn.PgError
			if errors.As(e, &server) {
				t.Errorf("OWNED_RESTORE_DATABASE_CLEANUP_FAILED SQLSTATE=%s", server.Code)
			} else {
				t.Errorf("OWNED_RESTORE_DATABASE_CLEANUP_FAILED error_type=%T", e)
			}
		}
	})
	restoredConfig := source.Config().Copy()
	restoredConfig.ConnConfig.Database = target
	restored, e := pgxpool.NewWithConfig(ctx, restoredConfig)
	if e != nil {
		t.Fatal("ISOLATED_RESTORE_POOL_FAILED")
	}
	t.Cleanup(restored.Close)
	needsVector := !accountOnly
	if accountOnly {
		if e = source.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname=current_schema() AND tablename='knowledge_chunks')").Scan(&needsVector); e != nil {
			t.Fatal("RESTORE_VECTOR_SCOPE_UNRESOLVED")
		}
	}
	if needsVector {
		if _, e = restored.Exec(ctx, `CREATE EXTENSION vector WITH SCHEMA public VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC`); e != nil {
			t.Fatal("APPROVED_RESTORE_VECTOR_PREREQUISITE_FAILED")
		}
	}
	command := f.command(ctx, "exec", "-i", f.Container, "pg_restore", "-U", f.User, "-d", target, "--no-owner", "--no-privileges", "--exit-on-error")
	if f.native != nil {
		command = f.nativeCommand(t, ctx, "pg_restore", target, "--no-owner", "--no-privileges", "--exit-on-error")
	}
	command.Stdin = bytes.NewReader(archive)
	var diagnostic bytes.Buffer
	command.Stderr = &diagnostic
	if f.native != nil {
		command.Stderr = &fixtureDiagnostic{}
	}
	if e = command.Run(); e != nil {
		t.Fatal("ACTUAL_ISOLATED_LOGICAL_RESTORE_FAILED")
	}
	return restored
}

const (
	maximumFixtureArchiveBytes              = 64 * 1024 * 1024
	fixtureMaintenanceStatementMilliseconds = "20000"
	fixtureMaintenanceLockMilliseconds      = "5000"
	fixtureMaintenanceCleanupSeconds        = 25
)

// Native fixture diagnostics are bounded and never printed with credentials or
// source rows. A noisy client fails the command instead of growing memory.
type fixtureDiagnostic struct{ bytes.Buffer }

func (b *fixtureDiagnostic) Write(raw []byte) (int, error) {
	if len(raw) > (64<<10)-b.Len() {
		return 0, errors.New("NATIVE_FIXTURE_DIAGNOSTIC_LIMIT")
	}
	return b.Buffer.Write(raw)
}

// fixtureArchive stops capture immediately instead of buffering an unbounded dump.
type fixtureArchive struct{ bytes.Buffer }

func (b *fixtureArchive) Write(raw []byte) (int, error) {
	if len(raw) > maximumFixtureArchiveBytes-b.Len() {
		return 0, errors.New("FIXTURE_ARCHIVE_LIMIT")
	}
	return b.Buffer.Write(raw)
}
