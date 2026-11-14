package main

import (
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestSelectedIdentityStoreUsesActualPostgreSQLPool(t *testing.T) {
	pool := releaseViewPool(t)
	foreign := releaseViewPool(t)
	store, e := transaction.FromPostgreSQL(pool)
	if e != nil {
		t.Fatal(e)
	}
	d := &selectedDatabase{postgres: pool, store: store}
	s := d.identity(config.Config{})
	wrapper, e := s.Repository()
	if e != nil || wrapper == store {
		t.Fatal("positive fixture did not create a distinct same-pool wrapper", e)
	}
	if got, e := selectedIdentityStore(d, s); e != nil || got != store {
		t.Fatal("actual selected owner refused", e)
	}
	for _, invalid := range []*identity.Service{nil, {}, {DB: foreign}, {DB: pool, Storage: wrapper}} {
		if got, e := selectedIdentityStore(d, invalid); e == nil || got != nil {
			t.Fatal("foreign or contradictory owner admitted")
		}
	}
}
