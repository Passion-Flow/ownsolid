package identity

import "context"

// BusinessAdmission is installed by the trusted serving/component constructor.
// Call only after authenticating the human or machine actor and outside an open
// business transaction: observation must commit even when admission is denied.
type BusinessAdmission interface {
	Check(context.Context) error
	VerifyStored(context.Context) error
}

// RequireBusinessIntegrity permits already accepted work to finish after expiry,
// while still refusing missing configuration, corrupt history or storage failure.
// Like new-work observation, call it before opening a business transaction.
func (s *Service) RequireBusinessIntegrity(ctx context.Context) error {
	if s == nil || s.BusinessAdmission == nil {
		return failure("LICENSE_CONFIGURATION_REQUIRED")
	}
	return s.BusinessAdmission.VerifyStored(ctx)
}

func (s *Service) RequireNewBusiness(ctx context.Context) error {
	if s == nil || s.BusinessAdmission == nil {
		return failure("LICENSE_CONFIGURATION_REQUIRED")
	}
	return s.BusinessAdmission.Check(ctx)
}
