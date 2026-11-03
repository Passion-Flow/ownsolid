package licensing

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"time"
)

// FromConfig is the serving construction boundary. There is no unlicensed
// fallback: safe migration/bootstrap operators do not construct this service.
func FromConfig(owner *identity.Service) (*Service, error) {
	if owner == nil {
		return nil, fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	trust, e := owner.Config.LicenseTrust()
	if e != nil {
		return nil, e
	}
	return New(owner, trust, owner.Config.LicensePolicy(), time.Now)
}
