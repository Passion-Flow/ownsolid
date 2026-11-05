// Package preferences owns the authenticated user's persisted theme, density and language,
// and the language an administrator sets for the tenant.
package preferences

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

const MaximumRevision int64 = 9007199254740991

// Themes a user can choose. SystemTheme leaves the choice to the browser's operating
// system, so it can differ between the user's machines.
const (
	SystemTheme = "system"
	LightTheme  = "light"
	DarkTheme   = "dark"
)

// Densities a user can choose.
const (
	ComfortableDensity = "comfortable"
	CompactDensity     = "compact"
)

// AutoLocale means no language was chosen: the tenant's default, then the browser's languages.
const AutoLocale = "auto"

type Service struct {
	DB       *pgxpool.Pool
	Identity *identity.Service
}
type Preference struct {
	Theme     string    `json:"theme"`
	Density   string    `json:"density"`
	Locale    string    `json:"locale"`
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UpdateRequest changes the fields it names and keeps the others, so two browsers that
// change different fields do not overwrite each other.
type UpdateRequest struct {
	Theme            *string `json:"theme,omitempty"`
	Density          *string `json:"density,omitempty"`
	Locale           *string `json:"locale,omitempty"`
	ExpectedRevision int64   `json:"expected_revision"`
}

func validTheme(theme string) bool {
	return theme == SystemTheme || theme == LightTheme || theme == DarkTheme
}
func validDensity(density string) bool {
	return density == ComfortableDensity || density == CompactDensity
}
func validLocale(locale string) bool {
	return locale == AutoLocale || locale == "en" || locale == "zh-CN"
}
func (r UpdateRequest) valid() bool {
	return (r.Theme != nil || r.Density != nil || r.Locale != nil) &&
		(r.Theme == nil || validTheme(*r.Theme)) &&
		(r.Density == nil || validDensity(*r.Density)) &&
		(r.Locale == nil || validLocale(*r.Locale)) &&
		r.ExpectedRevision >= 1 && r.ExpectedRevision < MaximumRevision
}
func (r UpdateRequest) applyTo(p Preference) Preference {
	if r.Theme != nil {
		p.Theme = *r.Theme
	}
	if r.Density != nil {
		p.Density = *r.Density
	}
	if r.Locale != nil {
		p.Locale = *r.Locale
	}
	return p
}

func failure(code string) error { return errors.New(code) }

// current reads and locks the user's row. Chains without the density and locale columns
// (CMP-201, CMP-202) store only a fixed theme and read as comfortable and automatic.
func current(ctx context.Context, tx transaction.Executor, user string) (Preference, error) {
	p := Preference{Density: ComfortableDensity, Locale: AutoLocale}
	var e error
	if tx.Dialect() == transaction.PostgreSQL {
		e = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT theme,density,locale,revision,updated_at FROM user_preferences WHERE user_id=$1 FOR UPDATE", user)).Scan(&p.Theme, &p.Density, &p.Locale, &p.Revision, &p.UpdatedAt)
	} else {
		e = tx.QueryRow(ctx, preferenceSQL(tx, "", "SELECT theme,revision,updated_at FROM user_preferences WHERE user_id=? FOR UPDATE", "SELECT theme,revision,updated_at FROM user_preferences WHERE user_id=? FOR UPDATE", user)).Scan(&p.Theme, &p.Revision, &p.UpdatedAt)
	}
	if errors.Is(e, sql.ErrNoRows) {
		return Preference{}, failure("PREFERENCE_UNAVAILABLE")
	}
	if e != nil {
		return Preference{}, e
	}
	if !validTheme(p.Theme) || !validDensity(p.Density) || !validLocale(p.Locale) || p.Revision < 1 || p.Revision > MaximumRevision || p.UpdatedAt.IsZero() {
		return Preference{}, failure("PREFERENCE_UNAVAILABLE")
	}
	p.UpdatedAt = p.UpdatedAt.UTC()
	return p, nil
}
func (s *Service) Get(ctx context.Context, a identity.Auth) (Preference, error) {
	var p Preference
	e := s.transaction(ctx, a, func(ctx context.Context, tx transaction.Executor, _ string) error {
		var e error
		p, e = current(ctx, tx, a.Me.User.ID)
		return e
	})
	if e != nil {
		return Preference{}, e
	}
	return p, nil
}
func (s *Service) Update(ctx context.Context, a identity.Auth, input UpdateRequest, request string) (Preference, error) {
	var p Preference
	if !input.valid() {
		return p, failure("VALIDATION_FAILED")
	}
	e := s.transaction(ctx, a, func(ctx context.Context, tx transaction.Executor, tenant string) error {
		var e error
		p, e = updateWithin(ctx, tx, a, tenant, input, request)
		return e
	})
	if e != nil {
		return Preference{}, e
	}
	return p, nil
}

// updateWithin is called only after the current identity and installation locks.
// A driver error immediately exits the transaction; no statement is retried on a
// native connection whose outcome may be poisoned or unknown.
func updateWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, tenant string, input UpdateRequest, request string) (Preference, error) {
	var p Preference
	e := func() error {
		old, e := current(ctx, tx, a.Me.User.ID)
		if e != nil {
			return e
		}
		if old.Revision != input.ExpectedRevision {
			return failure("PREFERENCE_REVISION_CONFLICT")
		}
		next := input.applyTo(old)
		switch tx.Dialect() {
		case transaction.MySQL, transaction.Dameng:
			if next.Theme == SystemTheme || next.Density != ComfortableDensity || next.Locale != AutoLocale {
				return failure("PREFERENCE_UNAVAILABLE")
			}
			changed, e := tx.Exec(ctx, preferenceSQL(tx, "", "UPDATE user_preferences SET theme=?,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE user_id=? AND revision=?", "UPDATE user_preferences SET theme=?,revision=revision+1,updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE user_id=? AND revision=?", next.Theme, a.Me.User.ID, old.Revision))
			if e != nil {
				return e
			}
			if changed != 1 {
				return failure("PREFERENCE_REVISION_CONFLICT")
			}
			p, e = current(ctx, tx, a.Me.User.ID)
			if e != nil {
				return e
			}
		case transaction.PostgreSQL:
			if e = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "UPDATE user_preferences SET theme=$1,density=$2,locale=$3,revision=revision+1,updated_at=clock_timestamp() WHERE user_id=$4 AND revision=$5 RETURNING theme,density,locale,revision,updated_at", next.Theme, next.Density, next.Locale, a.Me.User.ID, old.Revision)).Scan(&p.Theme, &p.Density, &p.Locale, &p.Revision, &p.UpdatedAt); e != nil {
				return e
			}
			p.UpdatedAt = p.UpdatedAt.UTC()
			if next.Locale != old.Locale {
				if e = followLocaleWithin(ctx, tx, a.Me.User.ID, tenant, next.Locale); e != nil {
					return e
				}
			}
		default:
			return failure("STORAGE_UNAVAILABLE")
		}
		if p.Theme != next.Theme || p.Density != next.Density || p.Locale != next.Locale || p.Revision != old.Revision+1 {
			return failure("PREFERENCE_UNAVAILABLE")
		}
		changed, e := tx.Exec(ctx, preferenceSQL(tx, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,'preference.updated','success','user_preference',$2,'User appearance preference',$5)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'preference.updated','success','user_preference',?,'User appearance preference',?)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,?,?,'preference.updated','success','user_preference',?,'User appearance preference',?)`, preferenceAuditArgs(tx, uuid.NewString(), a.Me.User.ID, tenant, a.Me.Audience, request)...))
		if e != nil {
			return e
		}
		if changed != 1 {
			return failure("STORAGE_UNAVAILABLE")
		}
		return nil
	}()
	if e != nil {
		return Preference{}, e
	}
	return p, nil
}

// followLocaleWithin keeps the account's email language with the choice. Going back to
// automatic takes the tenant's default; a browser's languages cannot reach an email, so with
// no default the account keeps the language it has.
func followLocaleWithin(ctx context.Context, tx transaction.Executor, user, tenant, locale string) error {
	_, e := tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE users u SET locale=c.locale,version=u.version+1,updated_at=clock_timestamp() FROM (SELECT CASE WHEN $1<>'auto' THEN $1 ELSE default_locale END AS locale FROM tenants WHERE id=$2) c WHERE u.id=$3 AND c.locale<>'auto' AND u.locale<>c.locale`, locale, tenant, user))
	return e
}

// VerifyStored checks retained seed/integrity state; it never creates defaults.
func (s *Service) VerifyStored(ctx context.Context) error {
	store, e := s.repository()
	if e != nil {
		return preferenceError(e)
	}
	if ctx == nil {
		return failure("STORAGE_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().Portal.CommitSeconds))
	defer cancel()
	return preferenceError(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if _, e := installedTenant(ctx, q); e != nil {
			return e
		}
		var invalid bool
		e := q.QueryRow(ctx, preferenceSQL(q, `SELECT EXISTS(SELECT 1 FROM users u LEFT JOIN user_preferences p ON p.user_id=u.id WHERE p.user_id IS NULL OR p.theme NOT IN('system','light','dark') OR p.density NOT IN('comfortable','compact') OR p.locale NOT IN('auto','en','zh-CN') OR p.revision NOT BETWEEN 1 AND 9007199254740991 OR p.updated_at IS NULL) OR EXISTS(SELECT 1 FROM user_preferences p LEFT JOIN users u ON u.id=p.user_id WHERE u.id IS NULL)`, `SELECT EXISTS(SELECT 1 FROM users u LEFT JOIN user_preferences p ON p.user_id=u.id WHERE p.user_id IS NULL OR p.theme NOT IN('light','dark') OR p.revision NOT BETWEEN 1 AND 9007199254740991 OR p.updated_at IS NULL) OR EXISTS(SELECT 1 FROM user_preferences p LEFT JOIN users u ON u.id=p.user_id WHERE u.id IS NULL)`, `SELECT CASE WHEN EXISTS(SELECT 1 FROM users u LEFT JOIN user_preferences p ON p.user_id=u.id WHERE p.user_id IS NULL OR p.theme NOT IN('light','dark') OR p.revision NOT BETWEEN 1 AND 9007199254740991 OR p.updated_at IS NULL) OR EXISTS(SELECT 1 FROM user_preferences p LEFT JOIN users u ON u.id=p.user_id WHERE u.id IS NULL) THEN 1 ELSE 0 END FROM DUAL`)).Scan(&invalid)
		if e != nil {
			return e
		}
		if invalid {
			return failure("PREFERENCE_UNAVAILABLE")
		}
		return nil
	}))
}

// Positional audits repeat the exact actor as target; no SQL rewriting occurs.
func preferenceAuditArgs(q transaction.Executor, id, user, tenant, audience, request string) []any {
	if q.Dialect() == transaction.MySQL || q.Dialect() == transaction.Dameng {
		return []any{id, user, tenant, audience, user, request}
	}
	return []any{id, user, tenant, audience, request}
}
