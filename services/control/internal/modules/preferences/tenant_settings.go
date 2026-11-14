package preferences

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// TenantSettings holds what an administrator chooses for everyone in the tenant.
type TenantSettings struct {
	// DefaultLocale is the language of people who have not chosen one; AutoLocale leaves
	// it to each browser.
	DefaultLocale string `json:"default_locale"`
	Revision      int64  `json:"revision"`
}

// TenantSettingsUpdate replaces the settings on top of a revision.
type TenantSettingsUpdate struct {
	DefaultLocale    string `json:"default_locale"`
	ExpectedRevision int64  `json:"expected_revision"`
}

func platformAdministrator(a identity.Auth) bool {
	return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin
}

func validTenantSettings(v TenantSettings) bool {
	return validLocale(v.DefaultLocale) && v.Revision >= 1 && v.Revision <= MaximumRevision
}

// GetTenantSettings reads the tenant's settings for an administrator.
func (s *Service) GetTenantSettings(ctx context.Context, a identity.Auth) (TenantSettings, error) {
	if !platformAdministrator(a) {
		return TenantSettings{}, failure("ACCESS_DENIED")
	}
	var out TenantSettings
	e := s.transaction(ctx, a, func(ctx context.Context, tx transaction.Executor, tenant string) error {
		if tx.Dialect() != transaction.PostgreSQL {
			out = TenantSettings{DefaultLocale: AutoLocale, Revision: 1}
			return nil
		}
		if e := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT default_locale,settings_revision FROM tenants WHERE id=$1", tenant)).Scan(&out.DefaultLocale, &out.Revision); e != nil {
			return e
		}
		if !validTenantSettings(out) {
			return failure("PREFERENCE_UNAVAILABLE")
		}
		return nil
	})
	if e != nil {
		return TenantSettings{}, e
	}
	return out, nil
}

// UpdateTenantSettings changes the tenant's settings. It does not take the shared
// installation lock first: two administrators saving at once would each wait for the other
// to release it. A stale revision fails with PREFERENCE_REVISION_CONFLICT.
func (s *Service) UpdateTenantSettings(ctx context.Context, a identity.Auth, input TenantSettingsUpdate, request string) (TenantSettings, error) {
	var out TenantSettings
	if !platformAdministrator(a) {
		return out, failure("ACCESS_DENIED")
	}
	if !validLocale(input.DefaultLocale) || input.ExpectedRevision < 1 || input.ExpectedRevision >= MaximumRevision {
		return out, failure("VALIDATION_FAILED")
	}
	store, e := s.repository()
	if e != nil {
		return out, preferenceError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().Portal.CommitSeconds))
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if tx.Dialect() != transaction.PostgreSQL {
			return failure("PREFERENCE_UNAVAILABLE")
		}
		if e := s.Identity.RecheckOrderedWithin(ctx, tx, a); e != nil {
			return e
		}
		var tenant string
		e := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "UPDATE tenants t SET default_locale=$1,settings_revision=t.settings_revision+1 FROM installation_state i WHERE i.id=1 AND i.bootstrap_complete AND t.id=i.tenant_id AND t.status='active' AND t.settings_revision=$2 RETURNING t.id::text,t.default_locale,t.settings_revision", input.DefaultLocale, input.ExpectedRevision)).Scan(&tenant, &out.DefaultLocale, &out.Revision)
		if errors.Is(e, sql.ErrNoRows) {
			return failure("PREFERENCE_REVISION_CONFLICT")
		}
		if e != nil {
			return e
		}
		return audit.WriteWithin(ctx, tx, a.Me.User.ID, a.Me.User.ID, a.Me.Audience, "tenant.default_locale_changed", "success", "tenant", tenant, "", request)
	})
	if e != nil {
		return TenantSettings{}, preferenceError(e)
	}
	return out, nil
}

// DefaultLocale is the tenant's language for the public runtime configuration, which pages
// read before anyone signs in. A failure answers AutoLocale: the page then follows the
// browser, which is better than no page.
func (s *Service) DefaultLocale(ctx context.Context) string {
	store, e := s.repository()
	if e != nil || store.Dialect() != transaction.PostgreSQL {
		return AutoLocale
	}
	var locale string
	e = store.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT t.default_locale FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active'")).Scan(&locale)
	if e != nil || !validLocale(locale) {
		return AutoLocale
	}
	return locale
}
