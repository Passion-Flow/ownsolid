// Package outbox is the transactional outbox of the control plane (ADR 0028). A writer appends an
// event in the transaction of its change, so an event exists if and only if the change commits.
// Every reader then sees every event, at least once and in no promised order across keys, so
// consumers treat an event as a hint to look again. PostgreSQL only.
package outbox

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Event says something changed about key.
type Event struct {
	Topic string
	Key   string
}

// AppendWithin records an event in the caller's transaction.
func AppendWithin(ctx context.Context, q transaction.Executor, topic, key string) error {
	if q.Dialect() != transaction.PostgreSQL {
		return errors.New("OUTBOX_UNSUPPORTED")
	}
	_, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO control_outbox(topic,key) VALUES($1,$2)`, topic, key))
	return e
}

// Trim deletes events older than retention and returns how many. A reader further behind than
// that loses them, which costs its consumers one look that a later check makes up for.
func Trim(ctx context.Context, q transaction.Executor, retention time.Duration) (int64, error) {
	if q.Dialect() != transaction.PostgreSQL {
		return 0, errors.New("OUTBOX_UNSUPPORTED")
	}
	return q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `DELETE FROM control_outbox WHERE site_id=local_site_id() AND cell_id=local_cell_id() AND created_at<clock_timestamp()-make_interval(secs=>$1)`, retention.Seconds()))
}

// Reader follows the outbox from where it started. It holds its position in memory, so a restarted
// process does not replay what it missed: events are hints.
type Reader struct {
	store transaction.Executor
	limit int

	mu       sync.Mutex
	started  bool
	position string
	id       string
	lag      time.Duration
}

// NewReader returns a reader that reads up to limit events at a time.
func NewReader(store transaction.Executor, limit int) (*Reader, error) {
	if store == nil || store.Dialect() != transaction.PostgreSQL || limit < 1 {
		return nil, errors.New("OUTBOX_READER_INVALID")
	}
	return &Reader{store: store, limit: limit}, nil
}

// Start places the reader at the watermark. Events of transactions still running there are read
// once they commit. Calling it again changes nothing.
func (r *Reader) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return nil
	}
	if e := r.store.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT pg_snapshot_xmin(pg_current_snapshot())::text`)).Scan(&r.position); e != nil {
		return e
	}
	r.id = "00000000-0000-0000-0000-000000000000"
	r.started = true
	return nil
}

// Next returns the next events and moves past them, in transaction order and in no particular order
// within one transaction. Only transactions older than every running one are read: a transaction
// that started earlier can commit later, and a reader that passed it would never see its events. A
// failed read leaves the position alone.
func (r *Reader) Next(ctx context.Context) ([]Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return nil, errors.New("OUTBOX_READER_NOT_STARTED")
	}
	rows, e := r.store.Query(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT topic,key,transaction_id::text,id::text,EXTRACT(EPOCH FROM clock_timestamp()-created_at)::float8 FROM control_outbox WHERE site_id=local_site_id() AND cell_id=local_cell_id() AND (transaction_id,id)>($1::text::xid8,$2::uuid) AND transaction_id<pg_snapshot_xmin(pg_current_snapshot()) ORDER BY transaction_id,id LIMIT $3`, r.position, r.id, r.limit))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Event
	position, id := r.position, r.id
	var age float64
	for rows.Next() {
		var v Event
		if e = rows.Scan(&v.Topic, &v.Key, &position, &id, &age); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	r.position, r.id = position, id
	if len(out) > 0 {
		r.lag = time.Duration(age * float64(time.Second))
	}
	return out, nil
}

// Lag is how old the newest event was when it was read.
func (r *Reader) Lag() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lag
}
