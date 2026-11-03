package identity

import (
	"bytes"
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"sort"
	"time"
)

// Both owned schemas store attempt_count as a signed32-bit SQL integer.
const maximumRateAttempts = 1<<31 - 1

type RateError struct{ RetryAfter int }

func (e *RateError) Error() string     { return "RATE_LIMITED" }
func (e *RateError) RetrySeconds() int { return e.RetryAfter }

type Limit struct {
	Purpose, Identifier string
	Count               int
	Window, Cooldown    time.Duration
	Quiet               bool
}

// AdmitInTransaction keeps a verified business admission and its rate counter atomic.
func (s *Service) AdmitInTransaction(ctx context.Context, tx pgx.Tx, limits ...Limit) error {
	_, e := s.AdmitWithin(ctx, transaction.PostgreSQLTransaction(tx), limits...)
	return e
}

func (s *Service) Admit(ctx context.Context, limits ...Limit) (bool, error) {
	items, e := s.rateItems(limits)
	if e != nil {
		return false, e
	}
	store, e := s.identityStore()
	if e != nil {
		return false, e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	var quiet bool
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var e error
		quiet, e = s.admitItems(ctx, tx, items)
		return e
	})
	return quiet, e
}
func (s *Service) admitTx(ctx context.Context, tx pgx.Tx, limits []Limit) (bool, error) {
	return s.AdmitWithin(ctx, transaction.PostgreSQLTransaction(tx), limits...)
}

// AdmitWithin borrows the caller's real transaction. Propagate errors so both
// business state and every admission counter share commit/rollback authority.
func (s *Service) AdmitWithin(ctx context.Context, tx transaction.Executor, limits ...Limit) (bool, error) {
	items, e := s.rateItems(limits)
	if e != nil {
		return false, e
	}
	if tx == nil {
		return false, failure("IDENTITY_RATE_TRANSACTION_REQUIRED")
	}
	return s.admitItems(ctx, tx, items)
}

type rateItem struct {
	key    []byte
	limit  Limit
	expiry time.Time
	last   *time.Time
	count  int
}

func (s *Service) rateItems(limits []Limit) ([]rateItem, error) {
	if s == nil || len(s.HMACKey) < security.MinimumHMACKeyBytes || len(s.HMACKey) > security.MaximumHMACKeyBytes {
		return nil, failure("IDENTITY_RATE_KEY_INVALID")
	}
	items := make([]rateItem, 0, len(limits))
	for _, l := range limits {
		if l.Purpose == "" || l.Count < 1 || l.Count > maximumRateAttempts || l.Window < time.Microsecond || l.Window%time.Microsecond != 0 || l.Cooldown < 0 || l.Cooldown > l.Window || l.Cooldown%time.Microsecond != 0 {
			return nil, failure("IDENTITY_RATE_LIMIT_INVALID")
		}
		items = append(items, rateItem{key: security.MAC(s.HMACKey, "m01-rate", l.Purpose, l.Identifier), limit: l})
	}
	sort.Slice(items, func(i, j int) bool { return bytes.Compare(items[i].key, items[j].key) < 0 })
	for i := 1; i < len(items); i++ {
		if bytes.Equal(items[i-1].key, items[i].key) {
			return nil, failure("IDENTITY_RATE_DUPLICATE_BUCKET")
		}
	}
	return items, nil
}

func (s *Service) admitItems(ctx context.Context, tx transaction.Executor, items []rateItem) (bool, error) {
	if len(items) == 0 {
		return false, nil
	}
	if tx.Dialect() == transaction.Dameng {
		// Serialize first insertion, including absent bucket rows. A native SQL
		// conflict poisons this transaction; it must never be caught and reused.
		// Existing actor locks precede this singleton, then canonical bucket locks.
		var installation int
		if e := tx.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT ID FROM INSTALLATION_STATE WHERE ID=1 FOR UPDATE")).Scan(&installation); e != nil {
			return false, e
		}
	}
	clock := identitySQL(tx, "SELECT clock_timestamp()", "SELECT UTC_TIMESTAMP(6)", `SELECT CURRENT_TIMESTAMP(6) FROM DUAL`)
	var now time.Time
	if e := tx.QueryRow(ctx, clock).Scan(&now); e != nil {
		return false, e
	}
	for index := range items {
		i := &items[index]
		l := i.limit
		if _, e := tx.Exec(ctx, identitySQL(tx,
			`INSERT INTO rate_limit_buckets(bucket_key,window_started_at,expires_at,attempt_count) VALUES($1,$2,$3,0) ON CONFLICT(bucket_key) DO NOTHING`,
			`INSERT INTO rate_limit_buckets(bucket_key,window_started_at,expires_at,attempt_count) VALUES(?,?,?,0) ON DUPLICATE KEY UPDATE bucket_key=rate_limit_buckets.bucket_key`, `MERGE INTO RATE_LIMIT_BUCKETS B USING (SELECT CAST(? AS VARBINARY(32)) BUCKET_KEY,CAST(? AS TIMESTAMP(6)) WINDOW_STARTED_AT,CAST(? AS TIMESTAMP(6)) EXPIRES_AT FROM DUAL) V ON (B.BUCKET_KEY=V.BUCKET_KEY) WHEN NOT MATCHED THEN INSERT (BUCKET_KEY,WINDOW_STARTED_AT,EXPIRES_AT,ATTEMPT_COUNT) VALUES(V.BUCKET_KEY,V.WINDOW_STARTED_AT,V.EXPIRES_AT,0)`, i.key, now, now.Add(l.Window))); e != nil {
			return false, e
		}
		if e := tx.QueryRow(ctx, identitySQL(tx,
			"SELECT expires_at,attempt_count,last_admitted_at FROM rate_limit_buckets WHERE bucket_key=$1 FOR UPDATE",
			"SELECT expires_at,attempt_count,last_admitted_at FROM rate_limit_buckets WHERE bucket_key=? FOR UPDATE", `SELECT EXPIRES_AT,ATTEMPT_COUNT,LAST_ADMITTED_AT FROM RATE_LIMIT_BUCKETS WHERE BUCKET_KEY=? FOR UPDATE`, i.key)).Scan(&i.expiry, &i.count, &i.last); e != nil {
			return false, e
		}
	}
	// A waiter must evaluate expiry against the clock after acquiring all locks.
	if e := tx.QueryRow(ctx, clock).Scan(&now); e != nil {
		return false, e
	}
	quiet := false
	for _, i := range items {
		l, expiry, count, last := i.limit, i.expiry, i.count, i.last
		if !now.Before(expiry) {
			count = 0
			last = nil
			expiry = now.Add(l.Window)
			if _, e := tx.Exec(ctx, resetRateBucket(tx, i.key, now, expiry)); e != nil {
				return false, e
			}
		}
		if count >= l.Count || (last != nil && now.Sub(*last) < l.Cooldown) {
			if l.Quiet {
				quiet = true
				continue
			}
			return false, &RateError{RetryAfter: int(expiry.Sub(now)/time.Second) + 1}
		}
		if _, e := tx.Exec(ctx, incrementRateBucket(tx, i.key, now)); e != nil {
			return false, e
		}
	}
	return quiet, nil
}

func resetRateBucket(tx transaction.Executor, key []byte, now, expiry time.Time) transaction.Statement {
	if tx.Dialect() == transaction.Dameng {
		return transaction.SQL(transaction.Dameng, "UPDATE RATE_LIMIT_BUCKETS SET WINDOW_STARTED_AT=?,EXPIRES_AT=?,ATTEMPT_COUNT=0,LAST_ADMITTED_AT=NULL WHERE BUCKET_KEY=?", now, expiry, key)
	}
	if tx.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, "UPDATE rate_limit_buckets SET window_started_at=?,expires_at=?,attempt_count=0,last_admitted_at=NULL WHERE bucket_key=?", now, expiry, key)
	}
	return transaction.SQL(transaction.PostgreSQL, "UPDATE rate_limit_buckets SET window_started_at=$2,expires_at=$3,attempt_count=0,last_admitted_at=NULL WHERE bucket_key=$1", key, now, expiry)
}

func incrementRateBucket(tx transaction.Executor, key []byte, now time.Time) transaction.Statement {
	if tx.Dialect() == transaction.Dameng {
		return transaction.SQL(transaction.Dameng, "UPDATE RATE_LIMIT_BUCKETS SET ATTEMPT_COUNT=ATTEMPT_COUNT+1,LAST_ADMITTED_AT=? WHERE BUCKET_KEY=?", now, key)
	}
	if tx.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, "UPDATE rate_limit_buckets SET attempt_count=attempt_count+1,last_admitted_at=? WHERE bucket_key=?", now, key)
	}
	return transaction.SQL(transaction.PostgreSQL, "UPDATE rate_limit_buckets SET attempt_count=attempt_count+1,last_admitted_at=$2 WHERE bucket_key=$1", key, now)
}
