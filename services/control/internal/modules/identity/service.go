package identity

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

type Service struct {
	BusinessAdmission BusinessAdmission
	Locales           *localization.Registry
	DB                *pgxpool.Pool
	Storage           transaction.Store
	Config            config.Config
	Passwords         *security.Passwords
	Mail              *mail.Outbox
	HMACKey           []byte
	Sources           SourceGuard
	adminTOTP         *mail.Keyring
}

func (s *Service) transact(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return failure("STORAGE_UNAVAILABLE")
	}
	defer tx.Rollback(ctx)
	if e = fn(tx); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return failure("STORAGE_UNAVAILABLE")
	}
	return nil
}
func (s *Service) Idle(audience string) time.Duration {
	if audience == "admin" {
		return config.Seconds(s.Config.Policy().Identity.AdminIdleSeconds)
	}
	return config.Seconds(s.Config.Policy().Identity.PortalIdleSeconds)
}
func (s *Service) Absolute(audience string) time.Duration {
	if audience == "admin" {
		return config.Seconds(s.Config.Policy().Identity.AdminAbsoluteSeconds)
	}
	return config.Seconds(s.Config.Policy().Identity.PortalAbsoluteSeconds)
}

// lockActor retains the actual PostgreSQL transaction entrance used by legacy modules.
func (s *Service) lockActor(ctx context.Context, tx pgx.Tx, a Auth) error {
	q, e := s.sessionTransaction(tx)
	if e != nil {
		return e
	}
	return s.RecheckWithin(ctx, q, a)
}

func interval(d time.Duration) string {
	return strconv.FormatInt(int64(d/time.Second), 10) + " seconds"
}

func (s *Service) passwordPolicy(ctx context.Context, password, email string) (string, error) {
	store, e := s.Repository()
	if e != nil {
		return "", e
	}
	if s.Passwords == nil {
		return "", failure("IDENTITY_PASSWORD_CONFIGURATION_REQUIRED")
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	var installation string
	if e = store.QueryRow(ctx, identitySQL(store, `SELECT t.name FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete`, `SELECT t.name FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1`, `SELECT T.NAME FROM INSTALLATION_STATE I JOIN TENANTS T ON T.ID=I.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1`)).Scan(&installation); e != nil {
		return "", sessionError(e)
	}
	return s.Passwords.Policy(password, email, installation)
}

// Recheck preserves the identity/session write fence for scoped business modules.
func (s *Service) Recheck(ctx context.Context, tx pgx.Tx, a Auth) error {
	return s.lockActor(ctx, tx, a)
}

func (s *Service) LocaleRegistry() (*localization.Registry, error) {
	if s.Locales != nil {
		return s.Locales, nil
	}
	if s.Config.Localization.PackageDirectory != "" || len(s.Config.Localization.RequiredLocales) > 0 {
		return nil, errors.New("LOCALIZATION_UNAVAILABLE")
	}
	return localization.Default()
}
func (s *Service) ResolveLocale(value string) (string, bool) {
	registry, e := s.LocaleRegistry()
	if e != nil {
		return "", false
	}
	return registry.Resolve(value)
}
