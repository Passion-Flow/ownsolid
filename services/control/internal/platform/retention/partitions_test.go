package retention_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/retention"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func partitions(t *testing.T, s *identity.Service) []string {
	t.Helper()
	rows, e := s.DB.Query(context.Background(), `SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid WHERE i.inhparent='audit_events'::regclass ORDER BY 1`)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			t.Fatal(e)
		}
		out = append(out, name)
	}
	return out
}

func month(offset int) string {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month()+time.Month(offset), 1, 0, 0, 0, 0, time.UTC).Format("200601")
}

func insertAudit(t *testing.T, s *identity.Service, at time.Time) error {
	t.Helper()
	_, e := s.DB.Exec(context.Background(), `INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id) VALUES($1,$2,'system','test.event','success',$1)`, uuid.NewString(), at)
	return e
}

func TestMigrationLeavesMonthsAheadAndAWriteLandsInItsMonth(t *testing.T) {
	s := fixture.New(t)
	got := partitions(t, s)
	if !strings.HasSuffix(got[len(got)-1], month(4)) || got[0] != "audit_events_p000000" {
		t.Fatalf("partitions %v, want the last at %s", got, month(4))
	}
	future := time.Now().UTC().AddDate(0, 3, 0)
	if e := insertAudit(t, s, future); e != nil {
		t.Fatal(e)
	}
	var home string
	if e := s.DB.QueryRow(context.Background(), `SELECT tableoid::regclass::text FROM audit_events WHERE occurred_at=$1`, future).Scan(&home); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(home, future.Format("200601")) {
		t.Fatalf("row landed in %s", home)
	}
}

func TestAWriteBeyondThePartitionsFails(t *testing.T) {
	s := fixture.New(t)
	e := insertAudit(t, s, time.Now().UTC().AddDate(5, 0, 0))
	if e == nil || !strings.Contains(e.Error(), "no partition") {
		t.Fatalf("%v", e)
	}
}

func TestAPassCreatesWhatIsMissing(t *testing.T) {
	s := fixture.New(t)
	ctx := context.Background()
	for _, offset := range []int{3, 4} {
		if _, e := s.DB.Exec(ctx, fmt.Sprintf(`DROP TABLE audit_events_p%s`, month(offset))); e != nil {
			t.Fatal(e)
		}
	}
	if e := retention.Pass(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	if got := partitions(t, s); !strings.HasSuffix(got[len(got)-1], month(3)) && !strings.HasSuffix(got[len(got)-1], month(4)) {
		t.Fatalf("partitions %v", got)
	}
	if e := insertAudit(t, s, time.Now().UTC().AddDate(0, 3, 0)); e != nil {
		t.Fatal(e)
	}
}

// attachExpired replaces the first partition with one that ended in 2020, holding one row.
func attachExpired(t *testing.T, s *identity.Service) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []string{
		`ALTER TABLE audit_events DETACH PARTITION audit_events_p000000`,
		`DROP TABLE audit_events_p000000`,
		`CREATE TABLE audit_events_p202001 (LIKE audit_events INCLUDING DEFAULTS INCLUDING CONSTRAINTS)`,
		`ALTER TABLE audit_events ATTACH PARTITION audit_events_p202001 FOR VALUES FROM ('2020-01-01 00:00:00+00') TO ('2020-02-01 00:00:00+00')`,
	} {
		if _, e := s.DB.Exec(ctx, statement); e != nil {
			t.Fatal(statement, e)
		}
	}
	if e := insertAudit(t, s, time.Date(2020, 1, 15, 0, 0, 0, 0, time.UTC)); e != nil {
		t.Fatal(e)
	}
}

func TestExpiredMonthsAreDroppedWholeAndNewerRowsStay(t *testing.T) {
	s := fixture.New(t)
	ctx := context.Background()
	attachExpired(t, s)
	if e := insertAudit(t, s, time.Now().UTC()); e != nil {
		t.Fatal(e)
	}
	if e := retention.Pass(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	for _, name := range partitions(t, s) {
		if name == "audit_events_p202001" {
			t.Fatal("expired partition kept")
		}
	}
	var old, recent int
	if e := s.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE occurred_at<'2021-01-01'),count(*) FILTER(WHERE occurred_at>='2021-01-01') FROM audit_events`).Scan(&old, &recent); e != nil || old != 0 || recent == 0 {
		t.Fatal(old, recent, e)
	}
}

func TestARowInsideItsRetentionKeepsItsWholeMonth(t *testing.T) {
	s := fixture.New(t)
	ctx := context.Background()
	attachExpired(t, s)
	policy := config.DefaultRuntimePolicy().Retention
	policy.AuditDays = 3650
	if time.Since(time.Date(2020, 2, 1, 0, 0, 0, 0, time.UTC)) > 3650*24*time.Hour {
		t.Skip("the fixed month has aged past the longest retention")
	}
	if e := retention.Pass(ctx, s.DB, policy); e != nil {
		t.Fatal(e)
	}
	var old int
	if e := s.DB.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE occurred_at<'2021-01-01'`).Scan(&old); e != nil || old != 1 {
		t.Fatal(old, e)
	}
}

func TestADropBlockedByALongReaderIsRetriedAndLetsWritersThrough(t *testing.T) {
	s := fixture.New(t)
	ctx := context.Background()
	attachExpired(t, s)
	holder, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer holder.Rollback(ctx)
	if _, e = holder.Exec(ctx, `SELECT count(*) FROM audit_events`); e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	policy := config.DefaultRuntimePolicy().Retention
	policy.TimeoutSeconds = 10
	if e = retention.Pass(ctx, s.DB, policy); e != nil {
		t.Fatal("a held lock must not fail the pass:", e)
	}
	if took := time.Since(started); took > 8*time.Second {
		t.Fatalf("waited %v", took)
	}
	if !contains(partitions(t, s), "audit_events_p202001") {
		t.Fatal("dropped under a held lock")
	}
	if e = holder.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	if e = retention.Pass(ctx, s.DB, policy); e != nil {
		t.Fatal(e)
	}
	if contains(partitions(t, s), "audit_events_p202001") {
		t.Fatal("not retried")
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestTheFunctionRefusesWhatItDoesNotOwn(t *testing.T) {
	s := fixture.New(t)
	ctx := context.Background()
	var schema string
	if e := s.DB.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); e != nil {
		t.Fatal(e)
	}
	for name, c := range map[string]struct {
		schema, table string
		ahead, keep   int
		want          string
	}{
		"other table":   {schema, "users", 3, 0, "TIME_PARTITION_TABLE_UNKNOWN"},
		"quoted table":  {schema, `audit_events"; DROP TABLE users; --`, 3, 0, "TIME_PARTITION_TABLE_UNKNOWN"},
		"other schema":  {"pg_catalog", "audit_events", 3, 0, "TIME_PARTITION_SCOPE_INVALID"},
		"negative keep": {schema, "audit_events", 3, -1, "TIME_PARTITION_ARGUMENTS_INVALID"},
		"too far ahead": {schema, "audit_events", 25, 0, "TIME_PARTITION_ARGUMENTS_INVALID"},
		"nothing ahead": {schema, "audit_events", 0, 0, "TIME_PARTITION_ARGUMENTS_INVALID"},
	} {
		_, e := s.DB.Exec(ctx, `SELECT manage_time_partitions($1,$2,$3,$4)`, c.schema, c.table, c.ahead, c.keep)
		if e == nil || !strings.Contains(e.Error(), c.want) {
			t.Errorf("%s: %v", name, e)
		}
	}
}

func TestPartitionsDoNotDisturbTheSchemaCheck(t *testing.T) {
	s := fixture.New(t)
	if e := retention.Pass(context.Background(), s.DB); e != nil {
		t.Fatal(e)
	}
	// A partition added at run time must not read as a changed catalog.
	if _, e := s.DB.Exec(context.Background(), `CREATE TABLE audit_events_p209901 (LIKE audit_events INCLUDING DEFAULTS INCLUDING CONSTRAINTS)`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(context.Background(), `ALTER TABLE audit_events ATTACH PARTITION audit_events_p209901 FOR VALUES FROM ('2099-01-01 00:00:00+00') TO ('2099-02-01 00:00:00+00')`); e != nil {
		t.Fatal(e)
	}
	if e := database.Check(context.Background(), s.DB); e != nil {
		t.Fatal(e)
	}
}
