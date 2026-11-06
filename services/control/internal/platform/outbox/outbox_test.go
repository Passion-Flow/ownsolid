package outbox_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/outbox"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const topic = "test.topic"

type harness struct {
	t     *testing.T
	pool  *pgxpool.Pool
	store transaction.Store
}

func setup(t *testing.T) harness {
	t.Helper()
	s := fixture.New(t)
	store, e := s.Repository()
	if e != nil {
		t.Fatal(e)
	}
	return harness{t, s.DB, store}
}

func (h harness) reader(limit int) *outbox.Reader {
	h.t.Helper()
	r, e := outbox.NewReader(h.store, limit)
	if e != nil {
		h.t.Fatal(e)
	}
	if e = r.Start(context.Background()); e != nil {
		h.t.Fatal(e)
	}
	return r
}

func (h harness) append(key string) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e := h.store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		return outbox.AppendWithin(ctx, q, topic, key)
	}); e != nil {
		h.t.Fatal(e)
	}
}

// begin opens a transaction that holds an xid and appends key, without committing.
func (h harness) begin(key string) pgx.Tx {
	h.t.Helper()
	ctx := context.Background()
	tx, e := h.pool.Begin(ctx)
	if e != nil {
		h.t.Fatal(e)
	}
	h.t.Cleanup(func() { tx.Rollback(context.Background()) })
	if _, e = tx.Exec(ctx, `INSERT INTO control_outbox(topic,key) VALUES($1,$2)`, topic, key); e != nil {
		h.t.Fatal(e)
	}
	return tx
}

func keys(t *testing.T, r *outbox.Reader) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, e := r.Next(ctx)
	if e != nil {
		t.Fatal(e)
	}
	out := []string{}
	for _, v := range events {
		if v.Topic != topic {
			t.Fatalf("topic %q", v.Topic)
		}
		out = append(out, v.Key)
	}
	return out
}

// collect reads until want events arrived. Any write transaction in the cluster, such as autovacuum's
// ANALYZE, holds the watermark for a moment, so a committed event is not always readable at once.
func collect(t *testing.T, r *outbox.Reader, want int) []string {
	t.Helper()
	out := []string{}
	for deadline := time.Now().Add(20 * time.Second); len(out) < want && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		out = append(out, keys(t, r)...)
	}
	return out
}

func equal(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestReaderSeesCommittedEventsOnceInCommitOrder(t *testing.T) {
	h := setup(t)
	r := h.reader(10)
	h.append("a")
	h.append("b")
	if got := collect(t, r, 2); !equal(got, "a", "b") {
		t.Fatal(got)
	}
	if got := keys(t, r); len(got) != 0 {
		t.Fatalf("read twice: %v", got)
	}
	h.append("c")
	if got := collect(t, r, 1); !equal(got, "c") {
		t.Fatal(got)
	}
}

func TestEveryReaderSeesEveryEvent(t *testing.T) {
	h := setup(t)
	first, second := h.reader(10), h.reader(10)
	h.append("a")
	if got := collect(t, first, 1); !equal(got, "a") {
		t.Fatal(got)
	}
	if got := collect(t, second, 1); !equal(got, "a") {
		t.Fatal(got)
	}
}

func TestRollbackLeavesNoEvent(t *testing.T) {
	h := setup(t)
	r := h.reader(10)
	tx := h.begin("rolled back")
	if e := tx.Rollback(context.Background()); e != nil {
		t.Fatal(e)
	}
	h.append("kept")
	if got := collect(t, r, 1); !equal(got, "kept") {
		t.Fatal(got)
	}
}

// This is the bug the watermark exists for: a reader that followed write order alone would read
// "late" first, move past "early" and never see it.
func TestATransactionThatCommitsLateIsNotSkipped(t *testing.T) {
	h := setup(t)
	r := h.reader(10)
	ctx := context.Background()
	early := h.begin("early")
	h.append("late")
	if got := keys(t, r); len(got) != 0 {
		t.Fatalf("read past a running transaction: %v", got)
	}
	if e := early.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if got := collect(t, r, 2); !equal(got, "early", "late") {
		t.Fatal(got)
	}
}

func TestAReaderStartedDuringATransactionStillSeesIt(t *testing.T) {
	h := setup(t)
	running := h.begin("running")
	r := h.reader(10)
	if got := keys(t, r); len(got) != 0 {
		t.Fatal(got)
	}
	if e := running.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	if got := collect(t, r, 1); !equal(got, "running") {
		t.Fatal(got)
	}
}

func TestAStartedReaderDoesNotReplay(t *testing.T) {
	h := setup(t)
	earlier := h.reader(10)
	h.append("before")
	// Once a reader has seen it, the watermark has passed it for good.
	if got := collect(t, earlier, 1); !equal(got, "before") {
		t.Fatal(got)
	}
	r := h.reader(10)
	if got := keys(t, r); len(got) != 0 {
		t.Fatalf("replayed: %v", got)
	}
	if e := r.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	h.append("after")
	if got := collect(t, r, 1); !equal(got, "after") {
		t.Fatal(got)
	}
}

// One transaction's events come in no particular order, and the limit may fall among them.
func TestEventsOfOneTransactionAreReadAcrossTheLimit(t *testing.T) {
	h := setup(t)
	r := h.reader(2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e := h.store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		for _, key := range []string{"a", "b", "c", "d", "e"} {
			if e := outbox.AppendWithin(ctx, q, topic, key); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	got := collect(t, r, 5)
	sort.Strings(got)
	if !equal(got, "a", "b", "c", "d", "e") {
		t.Fatal(got)
	}
}

func TestAFailedReadKeepsThePosition(t *testing.T) {
	h := setup(t)
	r := h.reader(10)
	h.append("a")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := r.Next(cancelled); e == nil {
		t.Fatal("a read with no database reported success")
	}
	if got := collect(t, r, 1); !equal(got, "a") {
		t.Fatalf("lost across a failed read: %v", got)
	}
}

// A logical restore brings back rows whose transaction IDs the new server has not reached. They must
// not be read, and above all must not carry the reader past the real events that follow.
func TestRestoredRowsAboveTheWatermarkDoNotCarryTheReaderAway(t *testing.T) {
	h := setup(t)
	r := h.reader(10)
	if _, e := h.pool.Exec(context.Background(), `INSERT INTO control_outbox(transaction_id,topic,key) VALUES('9000000000000000',$1,'restored')`, topic); e != nil {
		t.Fatal(e)
	}
	h.append("real")
	if got := collect(t, r, 1); !equal(got, "real") {
		t.Fatal(got)
	}
}

func TestTrimDeletesOnlyWhatAged(t *testing.T) {
	h := setup(t)
	r := h.reader(10)
	h.append("old")
	h.append("new")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, e := h.pool.Exec(ctx, `UPDATE control_outbox SET created_at=clock_timestamp()-interval '1 hour' WHERE key='old'`); e != nil {
		t.Fatal(e)
	}
	if e := h.store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		n, e := outbox.Trim(ctx, q, 5*time.Minute)
		if e == nil && n != 1 {
			t.Errorf("trimmed %d", n)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if got := collect(t, r, 1); !equal(got, "new") {
		t.Fatal(got)
	}
}

func TestLagReportsTheAgeOfTheNewestEventRead(t *testing.T) {
	h := setup(t)
	r := h.reader(10)
	h.append("a")
	if _, e := h.pool.Exec(context.Background(), `UPDATE control_outbox SET created_at=clock_timestamp()-interval '1 minute'`); e != nil {
		t.Fatal(e)
	}
	collect(t, r, 1)
	if lag := r.Lag(); lag < 59*time.Second || lag > 90*time.Second {
		t.Fatalf("lag %v", lag)
	}
}

func TestReaderRefusesMisuse(t *testing.T) {
	if _, e := outbox.NewReader(nil, 1); e == nil {
		t.Fatal("reader without a store")
	}
	h := setup(t)
	if _, e := outbox.NewReader(h.store, 0); e == nil {
		t.Fatal("reader without a limit")
	}
	r, e := outbox.NewReader(h.store, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Next(context.Background()); e == nil || e.Error() != "OUTBOX_READER_NOT_STARTED" {
		t.Fatal(e)
	}
}
