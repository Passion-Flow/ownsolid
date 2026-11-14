package artifactstores

import (
	"context"
	"errors"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
)

// This explicit test double must never satisfy production license composition.
type syntheticLicenseAdmission struct{}

func (syntheticLicenseAdmission) Check(context.Context) error        { return nil }
func (syntheticLicenseAdmission) VerifyStored(context.Context) error { return nil }

func TestObjectLicenseOwnerRejectsUnprovenAdmission(t *testing.T) {
	for _, owner := range []*identity.Service{nil, {}, {BusinessAdmission: syntheticLicenseAdmission{}}, {BusinessAdmission: &licensing.Service{}}} {
		if got, err := configuredLicenseOwner(owner); err == nil || got != nil {
			t.Fatal("unproven license owner accepted")
		}
	}
	for _, s := range []*Service{nil, {}, {Identity: &identity.Service{}, lifecycle: &lifecycle{}}} {
		if got, err := s.licenseOwner(); err == nil || got != nil {
			t.Fatal("unadmitted component acquired a license authority")
		}
	}
}

func TestObjectLicenseFailuresRemainSafeAndSpecific(t *testing.T) {
	for _, code := range []string{"LICENSE_CONFIGURATION_REQUIRED", "LICENSE_STORED_STATE_INVALID", "LICENSE_NEW_BUSINESS_UNAVAILABLE"} {
		if err := safe(errors.New(code)); err.Error() != code {
			t.Fatal("license recovery failure was obscured", err)
		}
	}
	if err := safe(errors.New("LICENSE_PRIVATE: synthetic private diagnostic")); err.Error() != "STORAGE_UNAVAILABLE" {
		t.Fatal("private license diagnostic leaked")
	}
}
