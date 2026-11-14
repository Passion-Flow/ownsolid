package identity

import "github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

// Repository is for trusted component construction,not client driver/scope input.
// It borrows the owning business pool and grants no independent configuration.
func (s *Service) Repository() (transaction.Store, error) {
	if s == nil {
		return nil, failure("IDENTITY_STORAGE_REQUIRED")
	}
	return s.identityStore()
}
