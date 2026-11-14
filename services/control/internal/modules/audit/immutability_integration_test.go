package audit_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

func TestAuditServingRoleCannotMutateEvents(t *testing.T) {
	const scopedAuditSchema = 54
	db := newAuditFixture(t, scopedAuditSchema)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Exec(ctx, chainInsert, uuid.NewString(), now); err != nil {
		t.Fatal(err)
	}
	before := auditExport(t, db)
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	after := auditExport(t, db)
	if len(before) != 1 || len(after) != 1 || !bytes.Equal(before[0], after[0]) {
		t.Fatal("the guard migration changed an existing event")
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	execSQL := func(statement string) {
		t.Helper()
		if _, err := tx.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	var schema string
	if err := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	future := time.Date(now.Year(), now.Month()+4, 1, 0, 0, 0, 0, time.UTC)
	execSQL("SELECT manage_time_partitions(current_schema()::text, 'audit_events', 4, 0)")
	partition := pgx.Identifier{"audit_events_p" + future.Format("200601")}.Sanitize()
	role := pgx.Identifier{"audit_fixture_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	execSQL("CREATE ROLE " + role + " NOLOGIN")
	execSQL("GRANT USAGE ON SCHEMA " + pgx.Identifier{schema}.Sanitize() + " TO " + role)
	execSQL("GRANT SELECT ON cells TO " + role)
	execSQL("GRANT SELECT, INSERT, UPDATE, DELETE ON audit_events TO " + role)
	// Deliberately grant extra rights in the fixture to test the database guard
	// independently of the provisioning contract.
	execSQL("GRANT TRUNCATE ON audit_events TO " + role)
	execSQL("GRANT SELECT, UPDATE, DELETE ON " + partition + " TO " + role)
	execSQL("SET LOCAL ROLE " + role)
	if _, err := tx.Exec(ctx, chainInsert, uuid.NewString(), future); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"UPDATE audit_events SET reason = 'fixture'",
		"UPDATE audit_events SET hash = decode(repeat('ff',32),'hex')",
		"DELETE FROM audit_events",
		"TRUNCATE audit_events",
		"UPDATE " + partition + " SET reason = 'fixture'",
		"DELETE FROM " + partition,
	} {
		attempt, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := attempt.Exec(ctx, statement)
		if err := attempt.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		var pgErr *pgconn.PgError
		if !errors.As(writeErr, &pgErr) || pgErr.Code != "42501" || pgErr.Message != "AUDIT_EVENTS_APPEND_ONLY" {
			t.Fatalf("guard did not reject %q: %v", statement, writeErr)
		}
	}

	var rows int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE reason IS NULL AND hash IS NOT NULL").Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("events after rejected mutations = %d: %v", rows, err)
	}
	execSQL("RESET ROLE")
	var head int
	if err := tx.QueryRow(ctx, "SELECT chain_position FROM audit_chain_head").Scan(&head); err != nil || head != 2 {
		t.Fatalf("tail after rejected mutations = %d: %v", head, err)
	}
}

func TestAuditOwnerCanExpirePartitionWithoutBreakingRetainedChain(t *testing.T) {
	db := newAuditFixture(t, 0)
	ctx := t.Context()
	now := time.Now().UTC()
	old := time.Date(now.Year(), now.Month()-8, 1, 0, 0, 0, 0, time.UTC)
	archive := "audit_events_p" + old.Format("200601")
	retained := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.Exec(ctx, "ALTER TABLE audit_events DETACH PARTITION audit_events_p000000"); err != nil {
		t.Fatal(err)
	}
	create := fmt.Sprintf("CREATE TABLE %s PARTITION OF audit_events FOR VALUES FROM ('%s') TO ('%s')",
		pgx.Identifier{archive}.Sanitize(), old.Format(time.RFC3339), old.AddDate(0, 1, 0).Format(time.RFC3339))
	if _, err := db.Exec(ctx, create); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{old, retained} {
		if _, err := db.Exec(ctx, chainInsert, uuid.NewString(), at); err != nil {
			t.Fatal(err)
		}
	}
	before := auditExport(t, db)
	if len(before) != 2 {
		t.Fatalf("expected two fixture rows, got %d", len(before))
	}
	var dropped int
	if err := db.QueryRow(ctx, "SELECT dropped FROM manage_time_partitions(current_schema()::text, 'audit_events', 4, 180)").Scan(&dropped); err != nil || dropped != 1 {
		t.Fatalf("retention dropped %d partitions: %v", dropped, err)
	}
	after := auditExport(t, db)
	if len(after) != 1 || !bytes.Equal(before[1], after[0]) {
		t.Fatal("retention changed the surviving event")
	}
	if binary := os.Getenv("OWNSOLID_TEST_AUDIT_VERIFY"); binary != "" {
		cmd := exec.CommandContext(ctx, binary, "--file", "-", "--previous-hash", field(t, before[0], "hash"))
		cmd.Stdin = bytes.NewReader(after[0])
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("retained chain did not verify: %v: %s", err, out)
		}
	}
}
