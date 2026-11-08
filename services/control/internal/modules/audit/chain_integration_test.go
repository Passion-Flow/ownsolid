package audit_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

const auditChainFirstSchema = 53

const chainInsert = `INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id)
VALUES($1,$2,'system','fixture.audit','success',$1)`

func TestAuditChainConcurrentAppends(t *testing.T) {
	db := newAuditFixture(t, 0)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	start := make(chan struct{})
	var workers sync.WaitGroup
	const count = 1000
	at := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < count; i++ {
		workers.Go(func() {
			<-start
			id := uuid.NewString()
			var err error
			for range 3 {
				if _, err = db.Exec(ctx, chainInsert, id, at); err == nil {
					return
				}
				var sqlErr *pgconn.PgError
				// A lock timeout aborts the statement. Reuse the event ID so a
				// retry can only append the same operation.
				if !errors.As(err, &sqlErr) || sqlErr.Code != "55P03" {
					break
				}
			}
			t.Error(err)
		})
	}
	close(start)
	workers.Wait()
	var rows, gaps, head int
	err := db.QueryRow(ctx, `
		WITH ordered AS (
			SELECT chain_position, hash, prev_hash, lag(hash, 1, decode(repeat('00',32),'hex'))
			    OVER (ORDER BY chain_position) AS predecessor,
			    row_number() OVER (ORDER BY chain_position) AS expected
			FROM audit_events
		)
		SELECT count(*), count(*) FILTER (WHERE chain_position IS DISTINCT FROM expected
		    OR prev_hash IS DISTINCT FROM predecessor),
		    (SELECT chain_position FROM audit_chain_head WHERE hash = (SELECT hash FROM ordered WHERE chain_position=$1))
		FROM ordered`, count).Scan(&rows, &gaps, &head)
	if err != nil || rows != count || gaps != 0 || head != count {
		t.Fatalf("rows=%d gaps=%d head=%d: %v", rows, gaps, head, err)
	}
}

func TestAuditChainExportAcrossPartitions(t *testing.T) {
	db := newAuditFixture(t, 0)
	ctx := t.Context()
	now := time.Now().UTC()
	boundary := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	first := uuid.NewString()
	if _, err := db.Exec(ctx, chainInsert, first, boundary.Add(-time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, chainInsert+" ON CONFLICT DO NOTHING", first, boundary.Add(-time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.WriteAnonymous(ctx, tx, "system", "fixture.rollback", "failure", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{boundary, boundary.Add(time.Microsecond)} {
		if _, err := db.Exec(ctx, chainInsert, uuid.NewString(), at); err != nil {
			t.Fatal(err)
		}
	}
	var partitions int
	if err := db.QueryRow(ctx, "SELECT count(DISTINCT tableoid) FROM audit_events").Scan(&partitions); err != nil || partitions != 2 {
		t.Fatalf("expected two physical partitions, got %d: %v", partitions, err)
	}
	lines := auditExport(t, db)
	if len(lines) != 3 || field(t, lines[1], "position") != "2" ||
		field(t, lines[1], "prev_hash") != field(t, lines[0], "hash") ||
		field(t, lines[1], "occurred_at") != boundary.Format("2006-01-02T15:04:05.000000Z") {
		t.Fatal("monthly boundary, rollback or conflict handling broke the chain")
	}

	binary := os.Getenv("OWNSOLID_TEST_AUDIT_VERIFY")
	if binary == "" {
		t.Log("set OWNSOLID_TEST_AUDIT_VERIFY to test the built standalone executable")
		return
	}
	last := field(t, lines[2], "hash")
	run := func(name string, input [][]byte, want string, args ...string) {
		t.Run(name, func(t *testing.T) {
			cmd := exec.CommandContext(ctx, binary, append([]string{"--file", "-", "--output", "json", "--expected-last-hash", last}, args...)...)
			cmd.Stdin = bytes.NewReader(bytes.Join(input, []byte("\n")))
			out, err := cmd.CombinedOutput()
			if (want == "") != (err == nil) || (want != "" && !strings.Contains(string(out), want)) {
				t.Fatalf("verify returned %v: %s", err, out)
			}
		})
	}
	run("intact", lines, "")
	run("retained", lines[1:], "", "--previous-hash", field(t, lines[0], "hash"))
	run("middle_deleted", [][]byte{lines[0], lines[2]}, `"line":2,"field":"position"`)
	run("tail_deleted", lines[:2], "trusted checkpoint")
	if _, err := db.Exec(ctx, "UPDATE audit_events SET metadata=$1 WHERE chain_position=2", `{"fixture":true}`); err != nil {
		t.Fatal(err)
	}
	run("middle_changed_in_database", auditExport(t, db), `"line":2,"field":"hash"`)
}

func TestAuditChainPreservesEarlierRowsOnUpgrade(t *testing.T) {
	db := newAuditFixture(t, auditChainFirstSchema-1)
	ctx := t.Context()
	at := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Exec(ctx, chainInsert, uuid.NewString(), at); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, chainInsert, uuid.NewString(), at); err != nil {
		t.Fatal(err)
	}
	lines := auditExport(t, db)
	if len(lines) != 1 || field(t, lines[0], "position") != "1" || field(t, lines[0], "prev_hash") != hex.EncodeToString(make([]byte, 32)) {
		t.Fatal("upgrade did not preserve the coverage boundary")
	}
	var legacy int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE chain_position IS NULL").Scan(&legacy); err != nil || legacy != 1 {
		t.Fatalf("pre-upgrade event count = %d: %v", legacy, err)
	}
}

func TestAuditChainUpgradeBindsPlacementWithoutRewritingV1(t *testing.T) {
	db := newAuditFixture(t, auditChainFirstSchema)
	ctx := t.Context()
	at := time.Now().UTC().Truncate(time.Microsecond)
	for i := range 2 {
		if i == 1 {
			if err := database.Migrate(ctx, db); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(ctx, chainInsert, uuid.NewString(), at); err != nil {
			t.Fatal(err)
		}
	}
	lines := auditExport(t, db)
	if len(lines) != 2 || field(t, lines[0], "format") != "ownsolid.audit.v1" ||
		field(t, lines[1], "format") != "ownsolid.audit.v2" ||
		field(t, lines[1], "prev_hash") != field(t, lines[0], "hash") ||
		field(t, lines[1], "site_id") == "" || field(t, lines[1], "cell_id") == "" {
		t.Fatal("upgrade broke continuity or omitted placement scope")
	}
	binary := os.Getenv("OWNSOLID_TEST_AUDIT_VERIFY")
	if binary == "" {
		t.Log("set OWNSOLID_TEST_AUDIT_VERIFY to test the built standalone executable")
		return
	}
	verify := func(input [][]byte) ([]byte, error) {
		cmd := exec.CommandContext(ctx, binary, "--file", "-", "--output", "json")
		cmd.Stdin = bytes.NewReader(bytes.Join(input, []byte("\n")))
		return cmd.CombinedOutput()
	}
	if out, err := verify(lines); err != nil {
		t.Fatalf("mixed chain did not verify: %v: %s", err, out)
	}
	if _, err := db.Exec(ctx, "UPDATE audit_events SET site_id='fixture-changed' WHERE chain_position=2"); err != nil {
		t.Fatal(err)
	}
	if out, err := verify(auditExport(t, db)); err == nil || !strings.Contains(string(out), `"line":2,"field":"hash"`) {
		t.Fatalf("modified placement was accepted: %v: %s", err, out)
	}
}

func TestAuditChainCanonicalMetadata(t *testing.T) {
	db := newAuditFixture(t, 0)
	var same bool
	err := db.QueryRow(t.Context(), `SELECT audit_chain_metadata($1::jsonb) = audit_chain_metadata($2::jsonb)`,
		`{"y": [null, false, true, "", "\u4e16"], "long": 9007199254740993.00, "x": -0.0}`,
		`{"x": 0, "long": 9007199254740993, "y": [null, false, true, "", "\u4e16"]}`).Scan(&same)
	if err != nil || !same {
		t.Fatalf("equivalent JSONB did not canonicalize identically: %v", err)
	}
}

func auditExport(t *testing.T, db *pgxpool.Pool) [][]byte {
	t.Helper()
	query, err := os.ReadFile("../../../../../tools/audit-verify/export.sql")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(t.Context(), string(query))
	if err != nil {
		t.Fatal(err)
	}
	export, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		t.Fatal(err)
	}
	return export
}

func field(t *testing.T, row []byte, name string) string {
	t.Helper()
	var decoded map[string]string
	if err := json.Unmarshal(row, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded[name]
}
