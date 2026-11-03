//go:build repository_integration

package identity_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualIdentityRateRepository(t *testing.T) {
	f := repositoryfixture.New(t)
	key := make([]byte, security.MinimumHMACKeyBytes)
	if _, e := rand.Read(key); e != nil {
		t.Fatal(e)
	}
	s := &identity.Service{HMACKey: key, Storage: f.Store}
	if f.PostgreSQL != nil {
		s.Storage, s.DB = nil, f.PostgreSQL
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	bucket := func(l identity.Limit) []byte { return security.MAC(key, "m01-rate", l.Purpose, l.Identifier) }
	count := func(l identity.Limit) int {
		t.Helper()
		var value int
		e := f.Store.QueryRow(ctx, f.Statement("SELECT attempt_count FROM rate_limit_buckets WHERE bucket_key=$1", "SELECT attempt_count FROM rate_limit_buckets WHERE bucket_key=?", bucket(l))).Scan(&value)
		if errors.Is(e, sql.ErrNoRows) {
			return 0
		}
		if e != nil {
			t.Fatal(e)
		}
		return value
	}
	allCount := func() int {
		t.Helper()
		var value int
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT COUNT(*) FROM rate_limit_buckets", "SELECT COUNT(*) FROM rate_limit_buckets")).Scan(&value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	admit := func(limits ...identity.Limit) bool {
		t.Helper()
		quiet, e := s.Admit(ctx, limits...)
		if e != nil {
			t.Fatal(e)
		}
		return quiet
	}
	limited := func(limits ...identity.Limit) {
		t.Helper()
		_, e := s.Admit(ctx, limits...)
		var rate *identity.RateError
		if !errors.As(e, &rate) || rate.RetrySeconds() <= 0 || rate.RetrySeconds() > 61 {
			t.Fatal("actual bounded rate denial missing", e)
		}
	}
	limit := func(purpose string, count int) identity.Limit {
		return identity.Limit{Purpose: purpose, Identifier: "Synthetic sensitive identifier " + uuid.NewString(), Count: count, Window: time.Minute}
	}
	t.Run("persistent_count_denial_expiry_and_private_key", func(t *testing.T) {
		l := limit("rate.persist", 2)
		admit(l)
		admit(l)
		limited(l)
		if count(l) != 2 {
			t.Fatal("denial changed durable counter")
		}
		query := f.Statement("UPDATE rate_limit_buckets SET window_started_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute' WHERE bucket_key=$1", "UPDATE rate_limit_buckets SET window_started_at=TIMESTAMPADD(MINUTE,-2,UTC_TIMESTAMP(6)),expires_at=TIMESTAMPADD(MINUTE,-1,UTC_TIMESTAMP(6)) WHERE bucket_key=?", bucket(l))
		if _, e := f.Store.Exec(ctx, query); e != nil {
			t.Fatal(e)
		}
		admit(l)
		var actual []byte
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT bucket_key FROM rate_limit_buckets WHERE bucket_key=$1", "SELECT bucket_key FROM rate_limit_buckets WHERE bucket_key=?", bucket(l))).Scan(&actual); e != nil || len(actual) != 32 || strings.Contains(string(actual), l.Identifier) || count(l) != 1 {
			t.Fatal("persistent expiry/HMAC readback", e)
		}
	})
	t.Run("quiet_cooldown_and_companion", func(t *testing.T) {
		quiet, companion := limit("rate.quiet", 2), limit("rate.companion", 4)
		quiet.Quiet, quiet.Cooldown = true, time.Minute
		if admit(quiet, companion) || !admit(quiet, companion) || count(quiet) != 1 || count(companion) != 2 {
			t.Fatal("quiet denial lost allowed companion counter")
		}
		cooldown := limit("rate.cooldown", 2)
		cooldown.Cooldown = time.Minute
		admit(cooldown)
		limited(cooldown)
		if count(cooldown) != 1 {
			t.Fatal("cooldown denial changed counter")
		}
	})
	t.Run("multi_bucket_denial_rolls_back_all_rows", func(t *testing.T) {
		full, other := limit("rate.full", 1), limit("rate.other", 3)
		admit(full)
		before := allCount()
		limited(other, full)
		if count(full) != 1 || count(other) != 0 || allCount() != before {
			t.Fatal("denied compound admission committed an earlier/new bucket")
		}
	})
	t.Run("concurrent_exact_count_and_sorted_orders", func(t *testing.T) {
		l := limit("rate.concurrent", 1)
		results := make(chan error, 2)
		start := make(chan struct{})
		for range 2 {
			go func() { <-start; _, e := s.Admit(ctx, l); results <- e }()
		}
		close(start)
		accepted, denied := 0, 0
		for range 2 {
			e := <-results
			var rate *identity.RateError
			if e == nil {
				accepted++
			} else if errors.As(e, &rate) {
				denied++
			} else {
				t.Fatal(e)
			}
		}
		if accepted != 1 || denied != 1 || count(l) != 1 {
			t.Fatal("actual concurrent limit exceeded")
		}
		first, second := limit("rate.order.a", 2), limit("rate.order.b", 2)
		start = make(chan struct{})
		for _, limits := range [][]identity.Limit{{first, second}, {second, first}} {
			go func() { <-start; _, e := s.Admit(ctx, limits...); results <- e }()
		}
		close(start)
		for range 2 {
			if e := <-results; e != nil {
				t.Fatal("opposite input orders deadlocked or lost an admit", e)
			}
		}
		if count(first) != 2 || count(second) != 2 {
			t.Fatal("ordered bucket counters lost updates")
		}
	})
	t.Run("clock_is_observed_after_contended_lock", func(t *testing.T) {
		l := limit("rate.clock", 1)
		admit(l)
		result := make(chan error, 1)
		e := f.Store.Within(ctx, false, func(txctx context.Context, tx transaction.Executor) error {
			q := f.Statement("UPDATE rate_limit_buckets SET window_started_at=clock_timestamp()-interval '1 minute',expires_at=clock_timestamp()+interval '100 milliseconds' WHERE bucket_key=$1", "UPDATE rate_limit_buckets SET window_started_at=TIMESTAMPADD(MINUTE,-1,UTC_TIMESTAMP(6)),expires_at=TIMESTAMPADD(MICROSECOND,100000,UTC_TIMESTAMP(6)) WHERE bucket_key=?", bucket(l))
			if _, e := tx.Exec(txctx, q); e != nil {
				return e
			}
			go func() { _, e := s.Admit(ctx, l); result <- e }()
			select {
			case <-result:
				t.Fatal("admission escaped the actual held bucket lock")
			case <-time.After(250 * time.Millisecond):
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		if e = <-result; e != nil || count(l) != 1 {
			t.Fatal("waiter used a stale pre-lock clock", e)
		}
	})
	t.Run("business_failure_and_cancel_share_real_rollback", func(t *testing.T) {
		l := limit("rate.rollback", 2)
		before := allCount()
		e := f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			if _, e := s.AdmitWithin(ctx, tx, l); e != nil {
				return e
			}
			// An actual FK failure after admission must abort the same transaction.
			_, e := tx.Exec(ctx, f.Statement("INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'member')", uuid.NewString(), f.Tenant, f.Workspace))
			return e
		})
		var storage *transaction.Error
		if !errors.As(e, &storage) || storage.Kind != transaction.ForeignKeyConflict || count(l) != 0 || allCount() != before {
			t.Fatal("actual downstream failure committed counters", e)
		}
		e = f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			if _, e := s.AdmitWithin(ctx, tx, l); e != nil {
				return e
			}
			return audit.WriteWithin(ctx, tx, f.User, f.User, "admin", "auth.synthetic_rate_denied", "success", "user", f.User, "", "not-canonical")
		})
		if !errors.As(e, &storage) || storage.SQLState != "22P02" && storage.Number != 3819 || count(l) != 0 || allCount() != before {
			t.Fatal("actual audit constraint failure committed counters", e)
		}
		cancelled, stop := context.WithCancel(ctx)
		e = f.Store.Within(cancelled, false, func(ctx context.Context, tx transaction.Executor) error {
			if _, e := s.AdmitWithin(ctx, tx, l); e != nil {
				return e
			}
			stop()
			return ctx.Err()
		})
		stop()
		if !errors.Is(e, context.Canceled) || count(l) != 0 || allCount() != before {
			t.Fatal("actual cancelled admission persisted", e)
		}
		if e = f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			if _, e := s.AdmitWithin(ctx, tx, l); e != nil {
				return e
			}
			return audit.WriteWithin(ctx, tx, f.User, f.User, "admin", "auth.synthetic_rate_admitted", "success", "user", f.User, "", uuid.NewString())
		}); e != nil || count(l) != 1 {
			t.Fatal("actual counter/business audit commit failed", e)
		}
	})
	t.Run("invalid_and_ambiguous_configuration_writes_nothing", func(t *testing.T) {
		before := allCount()
		l := limit("rate.invalid", 1)
		bad := *s
		bad.HMACKey = nil
		if _, e := bad.Admit(ctx, l); e == nil || strings.Contains(e.Error(), l.Identifier) {
			t.Fatal("missing key accepted or leaked input", e)
		}
		if _, e := s.Admit(ctx, l, l); e == nil {
			t.Fatal("duplicate input accepted")
		}
		cancelled, stop := context.WithCancel(ctx)
		stop()
		if _, e := s.Admit(cancelled, l); !errors.Is(e, context.Canceled) {
			t.Fatal("cancelled entry accepted", e)
		}
		if f.PostgreSQL != nil {
			bad = *s
			bad.Storage = f.Store
			if _, e := bad.Admit(ctx, l); e == nil || e.Error() != "IDENTITY_STORAGE_CONFIGURATION_CONFLICT" {
				t.Fatal("ambiguous stores accepted", e)
			}
		}
		if before != allCount() {
			t.Fatal("invalid input changed durable state")
		}
	})
}
