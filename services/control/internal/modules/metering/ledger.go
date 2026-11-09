// Package metering records exact usage deltas in the owning PostgreSQL transaction.
package metering

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrInvalidEvent indicates an incomplete or malformed usage event.
	ErrInvalidEvent = errors.New("METERING_INVALID_EVENT")
	// ErrEventConflict indicates that the event ID already names different usage.
	ErrEventConflict = errors.New("METERING_EVENT_CONFLICT")
	// ErrInvalidPeriod indicates an invalid scope or reporting period.
	ErrInvalidPeriod = errors.New("METERING_INVALID_PERIOD")
	// ErrUnavailable indicates a ledger operation failed and may be retried.
	ErrUnavailable = errors.New("METERING_UNAVAILABLE")
)

var eventIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

// Scope is the tenant and workspace authorized by the producer's control path.
type Scope struct {
	TenantID    uuid.UUID
	WorkspaceID uuid.UUID
}

func (s Scope) valid() bool {
	return s.TenantID != uuid.Nil && s.WorkspaceID != uuid.Nil
}

// Usage contains exact nonnegative usage deltas in integral units.
type Usage struct {
	GPUMicroseconds    int64
	Tokens             int64
	StorageByteSeconds int64 // Byte-seconds.
	Requests           int64
}

func (u Usage) valid() bool {
	return u.GPUMicroseconds >= 0 && u.Tokens >= 0 && u.StorageByteSeconds >= 0 && u.Requests >= 0 && u != (Usage{})
}

// Event is an immutable delta from a trusted producer. The producer must retain
// the identity, timestamp and measurements across delivery retries.
type Event struct {
	Producer   string
	ID         string
	OccurredAt time.Time
	Usage      Usage
}

func (e Event) normalized(scope Scope) (Event, error) {
	if !scope.valid() || !eventIdentity.MatchString(e.Producer) || !eventIdentity.MatchString(e.ID) ||
		!e.Usage.valid() || e.OccurredAt.IsZero() {
		return Event{}, ErrInvalidEvent
	}
	e.OccurredAt = e.OccurredAt.UTC().Truncate(time.Microsecond)
	if e.OccurredAt.Year() < 1 || e.OccurredAt.Year() > 9999 {
		return Event{}, ErrInvalidEvent
	}
	return e, nil
}

// AppendWithin inserts an event in the same transaction as its source state.
// A false result with no error is an exact replay. The caller must roll back
// on error and acknowledge only after commit. A transaction retried after
// unknown commit must reuse the original event and scope.
func AppendWithin(ctx context.Context, tx pgx.Tx, scope Scope, event Event) (bool, error) {
	event, err := event.normalized(scope)
	if err != nil {
		return false, err
	}
	if tx == nil {
		return false, ErrUnavailable
	}
	if _, err := tx.Exec(ctx, "SET LOCAL synchronous_commit = on"); err != nil {
		return false, operationError(ctx)
	}
	u := event.Usage
	args := []any{event.Producer, event.ID, scope.TenantID, scope.WorkspaceID, event.OccurredAt,
		u.GPUMicroseconds, u.Tokens, u.StorageByteSeconds, u.Requests}
	insert, err := tx.Exec(ctx, `INSERT INTO usage_ledger
 (producer, event_id, tenant_id, workspace_id, occurred_at, gpu_microseconds, tokens, storage_byte_seconds, requests)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
 ON CONFLICT (producer,event_id) DO NOTHING`, args...)
	if err != nil {
		return false, operationError(ctx)
	}
	if insert.RowsAffected() == 1 {
		return true, nil
	}
	// In READ COMMITTED this statement sees the winner even if the prior
	// INSERT encountered an index entry invisible to its own snapshot.
	var matches bool
	err = tx.QueryRow(ctx, `SELECT tenant_id=$3 AND workspace_id=$4 AND occurred_at=$5
  AND gpu_microseconds=$6 AND tokens=$7 AND storage_byte_seconds=$8 AND requests=$9
 FROM usage_ledger WHERE producer=$1 AND event_id=$2`, args...).Scan(&matches)
	if err != nil {
		return false, operationError(ctx)
	}
	if !matches {
		return false, ErrEventConflict
	}
	return false, nil
}

// Totals contains exact base-ten integer sums for one usage reporting window.
type Totals struct {
	GPUMicroseconds    string
	Tokens             string
	StorageByteSeconds string // Byte-seconds.
	Requests           string
}

const usageTotalsProjection = `COALESCE(sum(gpu_microseconds),0)::text, COALESCE(sum(tokens),0)::text,
 COALESCE(sum(storage_byte_seconds),0)::text, COALESCE(sum(requests),0)::text`

// TotalsWithin reports one authorized workspace over the half-open [from, to)
// period. The caller must obtain scope from its current read authorization.
func TotalsWithin(ctx context.Context, tx pgx.Tx, scope Scope, from, to time.Time) (Totals, error) {
	from, to, err := reportingWindow(scope, from, to)
	if err != nil {
		return Totals{}, err
	}
	if tx == nil {
		return Totals{}, ErrUnavailable
	}
	var out Totals
	err = tx.QueryRow(ctx, `SELECT `+usageTotalsProjection+`
 FROM usage_ledger WHERE tenant_id=$1 AND workspace_id=$2 AND occurred_at >= $3 AND occurred_at < $4`,
		scope.TenantID, scope.WorkspaceID, from, to).Scan(&out.GPUMicroseconds, &out.Tokens, &out.StorageByteSeconds, &out.Requests)
	if err != nil {
		return Totals{}, operationError(ctx)
	}
	return out, nil
}

func reportingWindow(scope Scope, from, to time.Time) (time.Time, time.Time, error) {
	from = from.UTC().Truncate(time.Microsecond)
	to = to.UTC().Truncate(time.Microsecond)
	if !scope.valid() || from.IsZero() || !from.Before(to) || from.Year() < 1 || to.Year() > 9999 {
		return time.Time{}, time.Time{}, ErrInvalidPeriod
	}
	return from, to, nil
}

func operationError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnavailable
}
