package main

import (
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// PostgreSQL Repository() returns a fresh wrapper around the same original pool.
// Its wrapper address is not pool identity. Selected construction owns this
// store/pool pair; domain owners must retain the same pool and no alternate Store.
func selectedIdentityStore(selected *selectedDatabase, service *identity.Service) (transaction.Store, error) {
	bad := errors.New("SELECTED_IDENTITY_OWNER_REQUIRED")
	if selected == nil || selected.store == nil || service == nil || selected.native != nil {
		return nil, bad
	}
	if selected.postgres != nil {
		if selected.mysql != nil || service.DB != selected.postgres || service.Storage != nil || selected.store.Dialect() != transaction.PostgreSQL {
			return nil, bad
		}
	} else if selected.mysql == nil || service.DB != nil || service.Storage != selected.store || selected.store.Dialect() != transaction.MySQL {
		return nil, bad
	}
	return selected.store, nil
}
