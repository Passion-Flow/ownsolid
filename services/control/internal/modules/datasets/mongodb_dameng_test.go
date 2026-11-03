package datasets

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Statement/session doubles check control flow only; they never establish DM
// syntax, lock behavior, driver behavior, or target database qualification.
type mongoSlotProbe struct {
	transaction.Store
	answers    []any
	queries    []transaction.Statement
	closed     bool
	closeError error
}

func (*mongoSlotProbe) Dialect() transaction.Dialect { return transaction.Dameng }
func (p *mongoSlotProbe) WithSession(ctx context.Context, f func(context.Context, transaction.Store) error) error {
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("missing deadline")
	}
	e := f(ctx, p)
	p.closed = true
	return errors.Join(e, p.closeError)
}
func (p *mongoSlotProbe) QueryRow(_ context.Context, q transaction.Statement) transaction.Row {
	p.queries = append(p.queries, q)
	if len(p.answers) == 0 {
		return mongoProbeRow{errors.New("unexpected SQL after failure")}
	}
	v := p.answers[0]
	p.answers = p.answers[1:]
	return mongoProbeRow{v}
}

type mongoProbeRow struct{ value any }

func (r mongoProbeRow) Scan(dest ...any) error {
	if e, ok := r.value.(error); ok {
		return e
	}
	reflect.ValueOf(dest[0]).Elem().Set(reflect.ValueOf(r.value))
	return nil
}
func TestMongoNativeSessionSlotBoundedFailureAndRelease(t *testing.T) {
	for _, tc := range []struct {
		name       string
		answers    []any
		called     bool
		queries    int
		want       string
		closeError error
	}{
		{"acquire error", []any{"M21_TEST", errors.New("STORAGE_UNAVAILABLE")}, false, 2, "STORAGE_UNAVAILABLE", nil},
		{"busy", []any{"M21_TEST", 1}, false, 2, "MONGODB_BUSY", nil},
		{"invalid result", []any{"M21_TEST", 4}, false, 2, "MONGODB_SLOT_UNAVAILABLE", nil},
		{"release error", []any{"M21_TEST", 0, errors.New("STORAGE_UNAVAILABLE")}, true, 3, "MONGODB_SLOT_RELEASE_UNCERTAIN", nil},
		{"release unowned", []any{"M21_TEST", 0, 4}, true, 3, "MONGODB_SLOT_RELEASE_UNCERTAIN", nil},
		{"close error", []any{"M21_TEST", 0, 0}, true, 3, "STORAGE_UNAVAILABLE", errors.New("STORAGE_UNAVAILABLE")},
		{"success", []any{"M21_TEST", 0, 0}, true, 3, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &mongoSlotProbe{answers: tc.answers, closeError: tc.closeError}
			called := false
			e := withMongoSessionSlot(context.Background(), p, config.DefaultMongoDBConfiguration(), 5, 1, func(context.Context) error {
				called = true
				if p.closed {
					t.Fatal("session closed while operation owns slot")
				}
				return nil
			})
			if called != tc.called || !p.closed || len(p.queries) != tc.queries || len(p.answers) != 0 {
				t.Fatal("session lifecycle mismatch", called, len(p.queries))
			}
			if tc.want == "" && e != nil || tc.want != "" && (e == nil || !strings.Contains(e.Error(), tc.want)) {
				t.Fatal("wrong outcome", e)
			}
		})
	}
}
func TestMongoNativeSlotSecondAdmissionAndNamespace(t *testing.T) {
	p := &mongoSlotProbe{answers: []any{"M21_TEST", 1, 0, 0}}
	policy := config.DefaultMongoDBConfiguration()
	policy.MaximumConcurrent = 2
	if e := withMongoSessionSlot(context.Background(), p, policy, 5, 1, func(context.Context) error { return nil }); e != nil || len(p.queries) != 4 {
		t.Fatal(e)
	}
	for _, schema := range []string{"M21_A", "M21_B"} {
		for slot := 0; slot < 2; slot++ {
			v := mongoNativeSlotID(schema, slot)
			if v < 0 || v > 1073741823 {
				t.Fatal("outside documented native lock range")
			}
		}
	}
	if mongoNativeSlotID("M21_A", 0) == mongoNativeSlotID("M21_A", 1) || mongoNativeSlotID("M21_A", 0) == mongoNativeSlotID("M21_B", 0) {
		t.Fatal("fixture namespaces collided")
	}
}
func TestMongoCommitUnknownDominatesCancellationAndRelease(t *testing.T) {
	for _, e := range []error{&transaction.Error{Kind: transaction.CommitUnknown}, errors.Join(context.Canceled, errors.New(string(transaction.CommitUnknown))), errors.Join(context.DeadlineExceeded, &transaction.Error{Kind: transaction.CommitUnknown})} {
		if !mongoUnknown(e) || mongoOutcome(e).Error() != string(transaction.CommitUnknown) {
			t.Fatal("uncertain mutation lost", e)
		}
	}
	p := &mongoSlotProbe{answers: []any{"M21_TEST", 0, errors.New("STORAGE_UNAVAILABLE")}}
	e := withMongoSessionSlot(context.Background(), p, config.DefaultMongoDBConfiguration(), 5, 1, func(context.Context) error { return &transaction.Error{Kind: transaction.CommitUnknown} })
	if !mongoUnknown(e) {
		t.Fatal("release concealed uncertain commit", e)
	}
}
func TestMongoNativeBoundsTimestampAndReceiptValidation(t *testing.T) {
	p := config.DefaultMongoDBConfiguration()
	for _, value := range []int{0, 1001, -1} {
		p.MaximumImportsPerDataset = value
		if _, e := mongoLimit(p); e == nil {
			t.Fatal("invalid literal limit")
		}
	}
	p.MaximumImportsPerDataset = 37
	if v, e := mongoLimit(p); e != nil || v != "37" {
		t.Fatal(v, e)
	}
	for _, v := range []time.Time{time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 0, 1, time.UTC), time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("offset", 3600))} {
		if mongoTimestamp(v) {
			t.Fatal("noncanonical timestamp")
		}
	}
	id, object := uuid.NewString(), uuid.NewString()
	r := MongoDBImport{ID: id, CreationKey: uuid.NewString(), SourceID: "source", State: "reading", AttemptCount: 1, objectID: object, attemptID: uuid.NewString(), intent: make([]byte, 32), retained: []string{object}, sourceGrant: []byte(`{}`), Request: MongoDBRequest{SourceID: "source", Reason: "Verify source receipt"}}
	if !mongoReceiptValid(r) {
		t.Fatal("valid initial receipt rejected")
	}
	partial := r
	digest := strings.Repeat("a", 64)
	partial.SHA256 = &digest
	if mongoReceiptValid(partial) {
		t.Fatal("partial proof admitted")
	}
	changed := r
	changed.retained = []string{uuid.NewString()}
	if mongoReceiptValid(changed) {
		t.Fatal("unretained object admitted")
	}
	changed = r
	changed.AttemptCount = 2
	if mongoReceiptValid(changed) {
		t.Fatal("retained attempt inventory mismatch admitted")
	}
}
