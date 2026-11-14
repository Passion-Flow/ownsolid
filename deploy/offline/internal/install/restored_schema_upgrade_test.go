package install

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// schemaFixtureRunner translates the installer's Compose exec to the one
// disposable PostgreSQL container. The SQL, roles, archive and stream are unchanged.
type schemaFixtureRunner struct {
	ExecRunner
	container string
	prefix    []string
}

func (r schemaFixtureRunner) Stream(ctx context.Context, in io.Reader, out io.Writer, name string, args ...string) (string, error) {
	if name != "docker" || len(args) <= len(r.prefix) || !reflect.DeepEqual(args[:len(r.prefix)], r.prefix) {
		return "", fmt.Errorf("unexpected installer command: %s", name)
	}
	return r.ExecRunner.Stream(ctx, in, out, name, append([]string{"exec", "-i", r.container}, args[len(r.prefix):]...)...)
}

const schemaFixtureSeed = `
INSERT INTO tenants(id,name,status)
VALUES ('10000000-0000-4000-8000-000000000001','Restore test tenant','active');
INSERT INTO workspaces(id,tenant_id,name,status)
VALUES ('10000000-0000-4000-8000-000000000002','10000000-0000-4000-8000-000000000001','Restore test workspace','active');
INSERT INTO users(id,email_canonical,email_display,display_name,locale,status)
VALUES ('10000000-0000-4000-8000-000000000003','restore@example.invalid','restore@example.invalid','Restore test user','en','active');
INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at,execution_profile)
VALUES ('10000000-0000-4000-8000-000000000004','10000000-0000-4000-8000-000000000001','10000000-0000-4000-8000-000000000002',
        'Restore test node',decode(repeat('00',32),'hex'),'10000000-0000-4000-8000-000000000003','2035-01-01T00:00:00Z','read_only');`

const schemaFixtureData = `
SELECT t.name,w.name,u.email_canonical,n.node_name,n.execution_profile,n.expires_at AT TIME ZONE 'UTC'
FROM node_join_tokens n JOIN tenants t ON n.tenant_id=t.id
JOIN workspaces w ON n.workspace_id=w.id AND w.tenant_id=t.id JOIN users u ON n.created_by=u.id
WHERE n.id='10000000-0000-4000-8000-000000000004';`

const schemaFixtureCatalog = `
SELECT c.relname||'.'||k.conname,pg_get_constraintdef(k.oid,true),pg_get_expr(k.conbin,k.conrelid,false)
FROM pg_catalog.pg_constraint k JOIN pg_catalog.pg_class c ON c.oid=k.conrelid
JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='public' AND k.contype='c' ORDER BY (c.relname||'.'||k.conname) COLLATE "C";`

const schemaFixtureLedger = `SELECT version,checksum,applied_at AT TIME ZONE 'UTC' FROM schema_migrations ORDER BY version;`

type schemaFixtureSnapshot struct {
	ledger, catalog, milestones string
	tables                      []tableFingerprint
}

// TestRestoredSchemaUpgrade compares the published migration path before and
// after the installer's logical PostgreSQL round trip.
func TestRestoredSchemaUpgrade(t *testing.T) {
	container := os.Getenv("OWNSOLID_SCHEMA_FIXTURE_CONTAINER")
	if container == "" {
		t.Skip("requires the single PostgreSQL runner in deploy/offline/README.md")
	}
	work := os.Getenv("OWNSOLID_SCHEMA_FIXTURE_DIR")
	expect := os.Getenv("OWNSOLID_SCHEMA_FIXTURE_EXPECT")
	if work == "" || (expect != "blocked" && expect != "current") {
		t.Fatal("fixture requires its private directory and an explicit candidate outcome")
	}
	runner := schemaFixtureRunner{container: container, prefix: composeArguments(Config{}, "exec", "--no-TTY", "postgres")}
	mode, err := runner.Run(t.Context(), "docker", "inspect", "--format",
		`{{.HostConfig.NetworkMode}} {{index .Config.Labels "ownsolid.test"}}`, container)
	if err != nil || strings.TrimSpace(mode) != "none restored-schema-upgrade" {
		t.Fatal("refusing to modify a container outside the disconnected schema fixture")
	}
	o := newOperation(Config{}, runner, io.Discard)
	query := func(db, sql string) string {
		t.Helper()
		raw, err := o.query(t.Context(), db, sql)
		if err != nil {
			t.Fatalf("%s query: %v", db, err)
		}
		return raw
	}
	snapshot := func(db string) schemaFixtureSnapshot {
		t.Helper()
		tables, err := o.fingerprint(t.Context(), db)
		if err != nil {
			t.Fatal(err)
		}
		return schemaFixtureSnapshot{
			ledger: query(db, schemaFixtureLedger), catalog: query(db, schemaFixtureCatalog), tables: tables,
			milestones: query(db, `SELECT to_regclass('public.cell_clusters') IS NOT NULL,
				EXISTS(SELECT FROM pg_catalog.pg_attribute WHERE attrelid='gateway_instances'::regclass
				AND attname='delivery' AND NOT attisdropped)`),
		}
	}
	configure := func(db string) {
		t.Helper()
		dsn := "host=/var/run/postgresql dbname=" + db + " user=" + migratorRole + " sslmode=disable\n"
		const yaml = `
server:
  listen_address: 127.0.0.1:8443
  portal_origin: https://portal.example.invalid:8443
  admin_origin: https://admin.example.invalid:8443
  tls_certificate_file: /fixture/certificate.pem
  tls_private_key_file: /fixture/private-key.pem
database:
  dsn_file: /fixture/%s.dsn
smtp:
  host: 127.0.0.1
  port: 587
  from_address: restore@example.invalid
`
		for suffix, content := range map[string]string{".dsn": dsn, ".yaml": fmt.Sprintf(yaml, db)} {
			if err := os.WriteFile(filepath.Join(work, db+suffix), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	control := func(db, binary, command, want string, wantExit int) {
		t.Helper()
		raw, err := runner.Run(t.Context(), "docker", "exec", container, "/inputs/control-"+binary, command, "--config", "/fixture/"+db+".yaml")
		exit := 0
		if err != nil {
			exitErr, ok := err.(interface{ ExitCode() int })
			if !ok {
				t.Fatal(err)
			}
			exit = exitErr.ExitCode()
		}
		if exit != wantExit || strings.TrimSpace(raw) != want {
			t.Fatalf("%s %s %s: got exit %d and %q, want exit %d and %q", db, binary, command, exit, raw, wantExit, want)
		}
		t.Logf("%s %s %s exited %d (%s)", db, binary, command, exit, want)
	}
	create := func(db string, vector bool) {
		t.Helper()
		if db != databaseName {
			if err := o.createDatabase(t.Context(), db); err != nil {
				t.Fatal(err)
			}
		}
		configure(db)
		if vector {
			query(db, "CREATE EXTENSION vector VERSION '0.8.7'")
		}
	}

	const native, restored56, restoredCurrent, freshCurrent = databaseName, "restored_56", "restored_current", "fresh_current"
	create(native, true)
	control(native, "schema55", "migrate", "MIGRATION_COMPLETE", 0)
	query(native, schemaFixtureSeed)
	wantData := query(native, schemaFixtureData)
	if wantData != "Restore test tenant|Restore test workspace|restore@example.invalid|Restore test node|read_only|2035-01-01 00:00:00" {
		t.Fatalf("fixture rows were not seeded: %q", wantData)
	}
	before := snapshot(native)
	if strings.Count(before.ledger, "\n")+1 != 55 || before.milestones != "f|f" {
		t.Fatal("historical binary did not create schema 55")
	}

	path := filepath.Join(t.TempDir(), backupDatabaseName)
	archive, err := o.dump(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("installer custom archive: %d bytes, SHA-256 %s", archive.Bytes, archive.SHA256)
	for _, db := range []string{restored56, restoredCurrent} {
		create(db, false)
		if err := o.loadDump(t.Context(), db, path); err != nil {
			t.Fatal(err)
		}
		control(db, "schema55", "check-schema", "SCHEMA_CURRENT", 0)
		restored := snapshot(db)
		if restored.ledger != before.ledger || !reflect.DeepEqual(restored.tables, before.tables) || query(db, schemaFixtureData) != wantData {
			t.Fatalf("%s did not restore the exact predecessor ledger and row fingerprints", db)
		}
	}
	restoredBefore := snapshot(restored56)
	reportSchemaFixtureCatalog(t, "schema 55", before.catalog, restoredBefore.catalog)

	control(native, "schema56", "migrate", "MIGRATION_COMPLETE", 0)
	after56 := snapshot(native)
	control(native, "schema56", "check-schema", "SCHEMA_CURRENT", 0)
	control(native, "schema56", "migrate", "MIGRATION_COMPLETE", 0)
	if after56.milestones != "t|f" || !strings.HasPrefix(after56.ledger, before.ledger+"\n56|") ||
		!reflect.DeepEqual(snapshot(native), after56) || query(native, schemaFixtureData) != wantData {
		t.Fatal("native migration 56 did not preserve the data and settle once")
	}

	for range 2 {
		control(restored56, "schema56", "migrate", "MIGRATION_FAILED", 1)
		control(restored56, "schema55", "check-schema", "SCHEMA_CURRENT", 0)
		if !reflect.DeepEqual(snapshot(restored56), restoredBefore) || query(restored56, schemaFixtureData) != wantData {
			t.Fatal("failed historical upgrade changed the schema 55 ledger, catalog, milestones or data")
		}
	}

	// After proving rollback, advance only this disposable copy with the exact
	// historical DDL and checksum. The read-only CLI check exposes the admission
	// error that the migration command masks.
	migration, err := os.ReadFile(os.Getenv("OWNSOLID_SCHEMA_FIXTURE_SQL56"))
	if err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256(migration))
	if !strings.Contains(after56.ledger, "\n56|"+checksum+"|") {
		t.Fatal("diagnostic migration does not match the historical ledger")
	}
	query(restored56, "BEGIN;\n"+string(migration)+
		"\nINSERT INTO schema_migrations(version,checksum) VALUES (56,'"+checksum+"'); COMMIT;")
	control(restored56, "schema56", "check-schema", "SCHEMA_CATALOG_INCOMPATIBLE", 1)
	reportSchemaFixtureCatalog(t, "after executing 000056", after56.catalog, query(restored56, schemaFixtureCatalog))

	create(freshCurrent, true)
	control(freshCurrent, "candidate", "migrate", "MIGRATION_COMPLETE", 0)
	target := snapshot(freshCurrent)
	targetChecksums := query(freshCurrent, "SELECT version,checksum FROM schema_migrations ORDER BY version")
	control(native, "candidate", "migrate", "MIGRATION_COMPLETE", 0)
	control(native, "candidate", "check-schema", "SCHEMA_CURRENT", 0)
	if query(native, "SELECT version,checksum FROM schema_migrations ORDER BY version") != targetChecksums ||
		query(native, schemaFixtureData) != wantData {
		t.Fatal("native upgrade did not reach the fresh candidate lineage with its data")
	}

	currentBefore := snapshot(restoredCurrent)
	var settled schemaFixtureSnapshot
	for attempt := range 2 {
		if expect == "blocked" {
			control(restoredCurrent, "candidate", "migrate", "MIGRATION_FAILED", 1)
			control(restoredCurrent, "schema55", "check-schema", "SCHEMA_CURRENT", 0)
			settled = currentBefore
		} else {
			control(restoredCurrent, "candidate", "migrate", "MIGRATION_COMPLETE", 0)
			control(restoredCurrent, "candidate", "check-schema", "SCHEMA_CURRENT", 0)
			if query(restoredCurrent, "SELECT version,checksum FROM schema_migrations ORDER BY version") != targetChecksums {
				t.Fatal("restored upgrade did not reach the fresh candidate lineage")
			}
			if attempt == 0 {
				settled = snapshot(restoredCurrent)
			}
		}
		if !reflect.DeepEqual(snapshot(restoredCurrent), settled) || query(restoredCurrent, schemaFixtureData) != wantData {
			t.Fatal("candidate upgrade did not preserve the data and expected schema on repeat")
		}
	}
	t.Logf("candidate native schema %d; restored schema %d; %d predecessor table fingerprints checked",
		strings.Count(target.ledger, "\n")+1, strings.Count(settled.ledger, "\n")+1, len(before.tables))
}

func reportSchemaFixtureCatalog(t *testing.T, stage, native, restored string) {
	t.Helper()
	nativeRows, restoredRows := strings.Split(native, "\n"), strings.Split(restored, "\n")
	if len(nativeRows) != len(restoredRows) {
		t.Fatalf("%s: check constraint counts differ", stage)
	}
	var changed int
	for i, left := range nativeRows {
		right := restoredRows[i]
		name, _, _ := strings.Cut(left, "|")
		rightName, _, _ := strings.Cut(right, "|")
		if name != rightName {
			t.Fatalf("%s: check constraint identity changed from %s to %s", stage, name, rightName)
		}
		if left != right {
			changed++
			if name == "tenants.tenants_status_check" {
				t.Logf("%s: native %s", stage, left)
				t.Logf("%s: restored %s", stage, right)
			}
		}
	}
	if changed == 0 {
		t.Fatalf("%s: expected deparsing differences in historical check constraints", stage)
	}
	t.Logf("%s: %d of %d check constraints have different catalog text", stage, changed, len(nativeRows))
}
