package main

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Actual pgx pools and repository wrappers, with every possible network dial
// refused and counted. This verifies composition only, not SQL or filesystem I/O.
func releaseViewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	c, e := pgxpool.ParseConfig("host=127.0.0.1 port=1 user=synthetic_release_owner dbname=synthetic_release_owner sslmode=disable")
	if e != nil {
		t.Fatal(e)
	}
	c.MinConns = 0
	c.MinIdleConns = 0
	var calls atomic.Int32
	c.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, errors.New("RELEASE_TEST_NETWORK_FORBIDDEN")
	}
	p, e := pgxpool.NewWithConfig(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		p.Close()
		if calls.Load() != 0 {
			t.Error("composition attempted a database connection")
		}
	})
	return p
}

func TestReleaseViewPostgreSQLCompositionUsesActualPoolOwner(t *testing.T) {
	pool := releaseViewPool(t)
	store, e := transaction.FromPostgreSQL(pool)
	if e != nil {
		t.Fatal(e)
	}
	selected := &selectedDatabase{postgres: pool, store: store}
	service := selected.identity(config.Config{})
	wrapper, e := service.Repository()
	if e != nil {
		t.Fatal(e)
	}
	if wrapper == store {
		t.Fatal("positive control did not create separate real wrappers")
	}
	got, e := releaseViewStore(selected, service)
	if e != nil || got != store {
		t.Fatal("same PostgreSQL pool was rejected", e)
	}
	if service.DB != pool || service.Storage != nil {
		t.Fatal("existing PostgreSQL ownership was changed")
	}
}

func TestReleaseViewCompositionRefusesForeignOrConflictingPool(t *testing.T) {
	pool, foreign := releaseViewPool(t), releaseViewPool(t)
	store, e := transaction.FromPostgreSQL(pool)
	if e != nil {
		t.Fatal(e)
	}
	selected := &selectedDatabase{postgres: pool, store: store}
	for _, service := range []*identity.Service{nil, {}, {DB: foreign}, {DB: pool, Storage: store}, {Storage: store}} {
		if got, e := releaseViewStore(selected, service); e == nil || got != nil {
			t.Fatal("foreign or ambiguous owner accepted")
		}
	}
	for _, selection := range []*selectedDatabase{nil, {}, {postgres: pool}, {store: store}} {
		if got, e := releaseViewStore(selection, &identity.Service{DB: pool}); e == nil || got != nil {
			t.Fatal("unowned selection accepted")
		}
	}
}
