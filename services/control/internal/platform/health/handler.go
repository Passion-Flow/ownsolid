package health

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func Check(ctx context.Context, db *pgxpool.Pool, method, path string) (any, int, error) {
	store, e := transaction.FromPostgreSQL(db)
	if e != nil && path != "/health/live" {
		return nil, 0, errors.New("SERVICE_NOT_READY")
	}
	return CheckSelected(ctx, store, func(ctx context.Context) error { return database.Check(ctx, db) }, method, path)
}

// CheckSelected accepts only the actual owning repository and schema verifier.
// A missing selected verifier never makes readiness pass.
func CheckSelected(ctx context.Context, store transaction.Store, verifySchema func(context.Context) error, method, path string) (any, int, error) {
	if method != "GET" {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if path == "/health/live" {
		return map[string]string{"status": "ok"}, 200, nil
	}
	if path == "/health/ready" {
		var complete bool
		if store == nil || verifySchema == nil || verifySchema(ctx) != nil {
			return nil, 0, errors.New("SERVICE_NOT_READY")
		}
		if store.Dialect() != transaction.PostgreSQL && store.Dialect() != transaction.MySQL {
			return nil, 0, errors.New("SERVICE_NOT_READY")
		}
		if store.QueryRow(ctx, transaction.SQL(store.Dialect(), "SELECT bootstrap_complete FROM installation_state WHERE id=1")).Scan(&complete) != nil || !complete {
			return nil, 0, errors.New("SERVICE_NOT_READY")
		}
		return map[string]string{"status": "ok"}, 200, nil
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}
