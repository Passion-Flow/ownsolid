package databaseaccess

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func TestCatalogMatchesImmutableMigrations(t *testing.T) {
	entries, e := migrations.Files.ReadDir("postgresql")
	if e != nil {
		t.Fatal(e)
	}
	pattern := regexp.MustCompile(`(?m)^CREATE TABLE(?: IF NOT EXISTS)? ([a-z][a-z0-9_]*)`)
	partition := regexp.MustCompile(`_p[0-9]{6}$`)
	dropped := regexp.MustCompile(`(?m)^DROP TABLE(?: IF EXISTS)? ([a-z][a-z0-9_]*)`)
	tables := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		b, e := migrations.Files.ReadFile("postgresql/" + entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		for _, m := range pattern.FindAllStringSubmatch(string(b), -1) {
			// A partition takes its access through its parent (ADR 0029).
			if !partition.MatchString(m[1]) {
				tables[m[1]] = true
			}
		}
		for _, m := range dropped.FindAllStringSubmatch(string(b), -1) {
			delete(tables, m[1])
		}
		if regexp.MustCompile(`(?i)\b(SERIAL|CREATE SEQUENCE|GENERATED .*IDENTITY)\b`).Match(b) {
			t.Fatal("sequence contract must be declared before runtime qualification")
		}
	}
	found := []string{}
	for name := range tables {
		found = append(found, name)
	}
	sort.Strings(found)
	// The audit head is written through the owner trigger and has no runtime grants.
	wanted := append(postgreSQLTables(), "audit_chain_head")
	sort.Strings(wanted)
	if strings.Join(found, "\n") != strings.Join(wanted, "\n") {
		t.Fatal("runtime table grant catalog differs from migrations")
	}
}
func TestSCRAMCredentialFormatAndUnsupportedParameters(t *testing.T) {
	password, e := NewCredential()
	if e != nil || !credentialValid(password) {
		t.Fatal("credential generation", e)
	}
	verifier, e := newVerifier(password, 4096)
	if e != nil || !verifyCredential(verifier, password) || strings.Contains(verifier, password) {
		t.Fatal("verifier contract", e)
	}
	wrong, _ := NewCredential()
	if verifyCredential(verifier, wrong) {
		t.Fatal("wrong credential accepted")
	}
	for _, value := range []string{"", password + "=", "a", strings.Repeat("a", 44), "\u542b\u6709\u7528\u6237\u5185\u5bb9"} {
		if credentialValid(value) {
			t.Fatal("invalid secret format")
		}
	}
	for _, iterations := range []int{0, 4095, 1000001} {
		if _, e = newVerifier(password, iterations); e == nil {
			t.Fatal("unsupported iteration count")
		}
	}
	for _, value := range []string{"", "md5secret", strings.Replace(verifier, "4096:", "04096:", 1), strings.Replace(verifier, "SCRAM-SHA-256", "foreign", 1), verifier + "extra"} {
		if verifyCredential(value, password) {
			t.Fatal("invalid verifier accepted")
		}
	}
}
func TestProtectedInputFilesRejectLinksModesAndForeignParents(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(root, "private")
	if e := os.WriteFile(p, []byte("synthetic"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := readPrivate(p, 16); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(root, "link")
	os.Symlink(p, link)
	if _, e := readPrivate(link, 16); e == nil {
		t.Fatal("symlink accepted")
	}
	hard := filepath.Join(root, "hard")
	os.Link(p, hard)
	if _, e := readPrivate(p, 16); e == nil {
		t.Fatal("hard link accepted")
	}
	os.Remove(hard)
	os.Chmod(p, 0644)
	if _, e := readPrivate(p, 16); e == nil {
		t.Fatal("public file accepted")
	}
	os.Chmod(p, 0600)
	if _, e := readPrivate(p, 2); e == nil {
		t.Fatal("oversize input accepted")
	}
	os.Chmod(root, 0755)
	if _, e := readPrivate(p, 16); e == nil {
		t.Fatal("public parent accepted")
	}
	os.Chmod(root, 0700)
}

type actualFixture struct {
	service *identity.Service
	input   RuntimeInput
	lease   *pgxpool.Conn
	role    string
}

func newActualFixture(t *testing.T) actualFixture {
	t.Helper()
	return actualFixtureFor(t, identityfixture.New(t))
}

func actualFixtureFor(t *testing.T, s *identity.Service) actualFixture {
	t.Helper()
	ctx := context.Background()
	role := "m17_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password, e := NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	pc := s.DB.Config().Copy()
	pc.ConnConfig.User = role
	pc.ConnConfig.Password = password
	pc.MaxConns = 4
	pc.MinConns = 1
	c := s.Config
	c.Database.MaxOpenConnections = 4
	c.Database.MaxIdleConnections = 1
	c.Database.StatementTimeoutSeconds = 5
	lease, e := s.DB.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var locked bool
	if e = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || !locked {
		lease.Release()
		t.Fatal("actual installation lock", e)
	}
	t.Cleanup(func() {
		lease.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", database.InstallationLockID)
		lease.Release()
		s.DB.Exec(context.Background(), "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
		s.DB.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
	})
	return actualFixture{s, RuntimeInput{configuration: c, connection: pc}, lease, role}
}
func TestActualRuntimeAccessPreparationAccountWorkspaceDMLAndNoDDL(t *testing.T) {
	f := newActualFixture(t)
	ctx := context.Background()
	if status, e := Check(ctx, f.service.DB, f.lease, f.input); e != nil || status != NotCommitted {
		t.Fatal(status, e)
	}
	status, e := Prepare(ctx, f.service.DB, f.lease, f.input)
	if e != nil || status != Verified {
		t.Fatal(status, e)
	}
	var oid uint32
	var stored string
	if e = f.lease.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", f.role).Scan(&oid, &stored); e != nil || !verifyCredential(stored, f.input.connection.ConnConfig.Password) {
		t.Fatal("actual stored credential", e)
	}
	if status, e = Prepare(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	var repeatedOID uint32
	var repeated string
	f.lease.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", f.role).Scan(&repeatedOID, &repeated)
	if repeatedOID != oid || stored != repeated {
		t.Fatal("repeat reset role or password")
	}
	runtime, e := pgxpool.NewWithConfig(ctx, f.input.connection.Copy())
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.Close()
	s := *f.service
	s.DB = runtime
	mail := *s.Mail
	mail.DB = runtime
	s.Mail = &mail
	password := identityfixture.Bootstrap(t, &s)
	admin := identityfixture.Login(t, &s, "admin@example.invalid", password, "admin")
	w := workspaces.Service{DB: runtime, Identity: &s}
	created, e := w.Create(ctx, admin, "Runtime-granted workspace", admin.Me.User.ID, uuid.NewString())
	if e != nil || created.ID == "" {
		t.Fatal("actual workspace DML", e)
	}
	forbiddenRole := f.role + "_forbidden"
	var preexisting bool
	if e = f.lease.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", forbiddenRole).Scan(&preexisting); e != nil || preexisting {
		t.Fatal("unique forbidden-role fixture required", e)
	}
	defer f.lease.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{forbiddenRole}.Sanitize())
	for _, statement := range []string{"CREATE TABLE forbidden_runtime_ddl(id integer)", "UPDATE schema_migrations SET checksum='forbidden' WHERE version=1", "SELECT * FROM unrelated_fixture_table", "CREATE ROLE " + pgx.Identifier{forbiddenRole}.Sanitize()} {
		if _, e = runtime.Exec(ctx, statement); e == nil {
			t.Fatal("forbidden runtime operation accepted", statement)
		}
	}
	// PostgreSQL may return a warning/no-op for GRANT without grant option.
	// Assert actual unchanged ACLs rather than equating a nil SQL error to a grant.
	var beforeACL, afterACL string
	if e = runtime.QueryRow(ctx, "SELECT relacl::text FROM pg_class WHERE oid='users'::regclass").Scan(&beforeACL); e != nil {
		t.Fatal(e)
	}
	runtime.Exec(ctx, "GRANT SELECT ON users TO PUBLIC")
	if e = runtime.QueryRow(ctx, "SELECT relacl::text FROM pg_class WHERE oid='users'::regclass").Scan(&afterACL); e != nil || beforeACL != afterACL {
		t.Fatal("runtime changed grant authority", e)
	}
	if status, e = Check(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal("post-DML readonly verification", status, e)
	}
}

func TestActualRuntimeAccessRefusesExistingIdentityAndWrongContext(t *testing.T) {
	f := newActualFixture(t)
	ctx := context.Background()
	role := pgx.Identifier{f.role}.Sanitize()
	if _, e := f.lease.Exec(ctx, "CREATE ROLE "+role+" NOLOGIN"); e != nil {
		t.Fatal(e)
	}
	var before uint32
	f.lease.QueryRow(ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", f.role).Scan(&before)
	if _, e := Prepare(ctx, f.service.DB, f.lease, f.input); e == nil {
		t.Fatal("preexisting identity was adopted")
	}
	var after uint32
	var login bool
	f.lease.QueryRow(ctx, "SELECT oid,rolcanlogin FROM pg_roles WHERE rolname=$1", f.role).Scan(&after, &login)
	if before != after || login {
		t.Fatal("preexisting role changed")
	}
	if _, e := f.lease.Exec(ctx, "DROP ROLE "+role); e != nil {
		t.Fatal(e)
	}
	wrong := f.input
	wrong.connection = f.input.connection.Copy()
	wrong.connection.ConnConfig.Database = "other_test"
	if _, e := Prepare(ctx, f.service.DB, f.lease, wrong); e == nil {
		t.Fatal("foreign database accepted")
	}
	if _, e := f.lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
		t.Fatal(e)
	}
	if _, e := Prepare(ctx, f.service.DB, f.lease, f.input); e == nil {
		t.Fatal("unlocked operator mutated roles")
	}
	var exists bool
	f.lease.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", f.role).Scan(&exists)
	if exists {
		t.Fatal("invalid context created role")
	}
}
func TestActualRuntimeAccessTransactionRollsBackForbiddenPublicPrivileges(t *testing.T) {
	f := newActualFixture(t)
	ctx := context.Background()
	schema := f.service.DB.Config().ConnConfig.RuntimeParams["search_path"]
	if _, e := f.lease.Exec(ctx, "GRANT CREATE ON SCHEMA "+pgx.Identifier{schema}.Sanitize()+" TO PUBLIC"); e != nil {
		t.Fatal(e)
	}
	if _, e := Prepare(ctx, f.service.DB, f.lease, f.input); e == nil {
		t.Fatal("public schema create privilege accepted")
	}
	var exists, publicCreate bool
	f.lease.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", f.role).Scan(&exists)
	if exists {
		t.Fatal("failed preparation partially committed role")
	}
	// The original PUBLIC grant is preserved; only this disposable fixture owns it.
	f.lease.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace n CROSS JOIN LATERAL aclexplode(n.nspacl) a WHERE n.nspname=$1 AND a.grantee=0 AND a.privilege_type='CREATE')", schema).Scan(&publicCreate)
	if !publicCreate {
		t.Fatal("original public grant was removed")
	}
}
func TestActualRuntimeAccessDetectsCredentialAndPrivilegeDriftWithoutRepair(t *testing.T) {
	f := newActualFixture(t)
	ctx := context.Background()
	if _, e := Prepare(ctx, f.service.DB, f.lease, f.input); e != nil {
		t.Fatal(e)
	}
	wrong := f.input
	wrong.connection = f.input.connection.Copy()
	wrong.connection.ConnConfig.Password, _ = NewCredential()
	if _, e := Check(ctx, f.service.DB, f.lease, wrong); e == nil {
		t.Fatal("wrong credential accepted")
	}
	schema := f.service.DB.Config().ConnConfig.RuntimeParams["search_path"]
	table := pgx.Identifier{schema, "users"}.Sanitize()
	role := pgx.Identifier{f.role}.Sanitize()
	if _, e := f.lease.Exec(ctx, "GRANT SELECT ON "+table+" TO "+role+" WITH GRANT OPTION"); e != nil {
		t.Fatal(e)
	}
	if _, e := Prepare(ctx, f.service.DB, f.lease, f.input); e == nil {
		t.Fatal("excess grant authority repaired/adopted")
	}
	var stillGranted bool
	f.lease.QueryRow(ctx, "SELECT has_table_privilege($1,$2,'SELECT WITH GRANT OPTION')", f.role, table).Scan(&stillGranted)
	if !stillGranted {
		t.Fatal("conflict checker modified grant")
	}
}

func TestFreshRuntimeGrants(t *testing.T) {
	f := newActualFixture(t)
	if status, e := Prepare(t.Context(), f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	if status, e := Check(t.Context(), f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
}

// Agent gateways share the serving role (ADR 0048), so its connection limit follows
// role_connection_limit on an existing role; nothing else about the role changes.
func TestActualRuntimeAccessFollowsTheConfiguredConnectionLimit(t *testing.T) {
	f := newActualFixture(t)
	ctx := context.Background()
	if status, e := Prepare(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	limit := func() int {
		var n int
		if e := f.lease.QueryRow(ctx, "SELECT rolconnlimit FROM pg_roles WHERE rolname=$1", f.role).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	if got := limit(); got != int(f.input.configuration.Database.MaxOpenConnections) {
		t.Fatalf("a new role holds %d connections, not the pool", got)
	}
	raised := f.input
	raised.configuration.Database.RoleConnectionLimit = 12
	if status, e := Prepare(ctx, f.service.DB, f.lease, raised); e != nil || status != Verified || limit() != 12 {
		t.Fatalf("raised limit: %s %v, role holds %d", status, e, limit())
	}
	// The pool alone still verifies a role that holds more, as an older control would.
	if status, e := Check(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	if _, e := f.lease.Exec(ctx, "ALTER ROLE "+pgx.Identifier{f.role}.Sanitize()+" CONNECTION LIMIT 2"); e != nil {
		t.Fatal(e)
	}
	if _, e := Check(ctx, f.service.DB, f.lease, f.input); e == nil || e.Error() != "RUNTIME_ACCESS_ROLE_OR_CREDENTIAL_CONFLICT" {
		t.Fatalf("a limit below the pool: %v", e)
	}
}
