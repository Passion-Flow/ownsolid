//go:build cgo && odbc_manager

package dameng

import "context"

// VerifyConfiguration compares the complete admitted principal, credential
// reference and policy, then rechecks the original protected input binding.
// It grants no pool ownership and cannot substitute for the caller's current
// native session identity or held maintenance fence.
func (s *Store) VerifyConfiguration(ctx context.Context, configuration ConnectionConfiguration) error {
	if err := odbcContext(ctx); err != nil {
		return SafeError(err)
	}
	if s == nil || !s.admitted.Load() || s.manager == nil || s.manager.connection == nil || configuration.Validate() != nil || s.configuration != configuration || s.manager.connection.configuration != configuration {
		return fail("DAMENG_STORE_CONFIGURATION_MISMATCH")
	}
	if !s.manager.accepting() {
		return fail("DAMENG_STORE_OWNER_CLOSING")
	}
	if err := s.manager.connection.verify(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return SafeError(err)
	}
	if !s.admitted.Load() || !s.manager.accepting() {
		return fail("DAMENG_STORE_OWNER_CLOSING")
	}
	return nil
}
