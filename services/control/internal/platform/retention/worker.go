package retention

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Pass preserves the PostgreSQL API while sharing the real selected operation.
func Pass(ctx context.Context, db *pgxpool.Pool, policies ...config.RetentionPolicy) error {
	policy := config.DefaultRuntimePolicy().Retention
	if len(policies) > 0 {
		policy = policies[0]
	}
	store, e := transaction.FromPostgreSQL(db)
	if e != nil {
		return e
	}
	if e = maintainPartitions(ctx, store, policy); e != nil {
		return e
	}
	return PassSelected(ctx, store, policy)
}

// Run is the legacy PostgreSQL lifecycle. New server wiring uses RunSelected so
// maintenance failure is observable and joined with the owning server lifecycle.
func Run(ctx context.Context, db *pgxpool.Pool, policies ...config.RetentionPolicy) {
	policy := config.DefaultRuntimePolicy().Retention
	if len(policies) > 0 {
		policy = policies[0]
	}
	store, e := transaction.FromPostgreSQL(db)
	if e != nil {
		slog.Error("retention_worker_failed", "code", "RETENTION_STORE_REQUIRED")
		return
	}
	if e = RunSelected(ctx, store, policy); e != nil {
		code := "RETENTION_PASS_FAILED"
		var storage *transaction.Error
		if errors.As(e, &storage) {
			code = string(storage.Kind)
		}
		slog.Error("retention_worker_failed", "code", code)
	}
}

func RunSelected(ctx context.Context, store transaction.Store, policy config.RetentionPolicy) error {
	if ctx == nil {
		return errors.New("RETENTION_CONTEXT_REQUIRED")
	}
	if store == nil {
		return errors.New("RETENTION_STORE_REQUIRED")
	}
	if store.Dialect() != transaction.PostgreSQL && store.Dialect() != transaction.MySQL {
		return errors.New("RETENTION_DIALECT_UNSUPPORTED")
	}
	check := config.DefaultRuntimePolicy()
	check.Retention = policy
	if e := check.Validate(); e != nil {
		return e
	}
	ticker := time.NewTicker(config.Seconds(policy.IntervalSeconds))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			e := maintainPartitions(ctx, store, policy)
			if e == nil {
				e = PassSelected(ctx, store, policy)
			}
			if e != nil {
				if ctx.Err() != nil && (transaction.IsCancellationOnly(e)) {
					return nil
				}
				return e
			}
		}
	}
}
