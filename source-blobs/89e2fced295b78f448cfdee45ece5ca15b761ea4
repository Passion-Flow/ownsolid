package artifactstores

import (
	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func configuredLicenseOwner(i *identity.Service) (*licensing.Service, error) {
	if i == nil {
		return nil, fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	l, ok := i.BusinessAdmission.(*licensing.Service)
	if !ok || l == nil || !l.UsesIdentity(i) {
		return nil, fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	return l, nil
}

func (s *Service) licenseOwner() (*licensing.Service, error) {
	if s == nil || s.lifecycle == nil {
		return nil, fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	l, err := configuredLicenseOwner(s.Identity)
	if err != nil || l != s.lifecycle.license {
		return nil, fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	return l, nil
}

// observeLicense commits preliminary current authority before license observation.
// The caller's original business transaction repeats every authority fence.
// No license pool operation runs while that transaction holds a connection.
func (s *Service) observeLicense(ctx context.Context, a identity.Auth, workspace string, newBusiness bool) error {
	if err := s.within(ctx, a, workspace, "", func(context.Context, transaction.Executor, workspaces.Workspace) error { return nil }); err != nil {
		return err
	}
	l, err := s.licenseOwner()
	if err != nil {
		return err
	}
	if newBusiness {
		return l.Check(ctx)
	}
	return l.VerifyStored(ctx)
}

func (s *Service) admitNewBusiness(ctx context.Context, a identity.Auth, workspace string) error {
	return s.observeLicense(ctx, a, workspace, true)
}

func (s *Service) admitAcceptedWork(ctx context.Context, a identity.Auth, workspace string) error {
	return s.observeLicense(ctx, a, workspace, false)
}

func (s *Service) verifyLicenseIntegrity(ctx context.Context) error {
	l, err := s.licenseOwner()
	if err != nil {
		return err
	}
	return l.VerifyStored(ctx)
}
