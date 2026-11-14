package identity

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
)

func accountVersion(version int64) bool { return version > 0 && version <= MaximumAuthVersion }

func (s *Service) UpdateMe(ctx context.Context, a Auth, name, locale string, version int64, request string) (Me, error) {
	var result Me
	if !sessionActor(a) {
		return result, failure("AUTH_SESSION_EXPIRED")
	}
	if !accountVersion(version) {
		return result, failure("VALIDATION_FAILED")
	}
	name, e := security.Name(name)
	if e != nil {
		return result, e
	}
	store, e := s.Repository()
	if e != nil {
		return result, e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	selected, available := s.ResolveLocale(locale)
	if available {
		locale = selected
	}
	if !available && !localization.CanonicalLocale(locale) {
		return result, failure("VALIDATION_FAILED")
	}
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if e := s.RecheckWithin(ctx, tx, a); e != nil {
			return e
		}
		var current int64
		var savedLocale string
		if e := tx.QueryRow(ctx, identitySQL(tx, "SELECT version,locale FROM users WHERE id=$1", "SELECT version,locale FROM users WHERE id=?", `SELECT VERSION,LOCALE FROM USERS WHERE ID=?`, a.Me.User.ID)).Scan(&current, &savedLocale); e != nil {
			return e
		}
		if !available && locale != savedLocale {
			return failure("VALIDATION_FAILED")
		}
		if current != version {
			return failure("VERSION_CONFLICT")
		}
		if _, e := tx.Exec(ctx, identitySQL(tx, "UPDATE users SET display_name=$1,locale=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$3", "UPDATE users SET display_name=?,locale=?,version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?", `UPDATE USERS SET DISPLAY_NAME=?,LOCALE=?,VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE ID=?`, name, locale, a.Me.User.ID)); e != nil {
			return e
		}
		if e := audit.WriteWithin(ctx, tx, a.Me.User.ID, a.Me.User.ID, a.Me.Audience, "account.updated", "success", "user", a.Me.User.ID, "", request); e != nil {
			return e
		}
		u, e := scanUser(tx.QueryRow(ctx, identitySQL(tx, "SELECT "+userColumns+" FROM users WHERE id=$1", "SELECT "+mysqlUserColumns+" FROM users WHERE id=?", `SELECT `+damengUserColumns+` FROM USERS WHERE ID=?`, a.Me.User.ID)))
		if e != nil {
			return e
		}
		w, e := s.sessionWorkspace(ctx, tx, u.ID)
		result = Me{u, w, a.Me.Audience}
		return e
	})
	if e != nil {
		return Me{}, sessionError(e)
	}
	return result, nil
}

// GetUser is a trusted lookup behind the existing authenticated Admin entrance.
func (s *Service) GetUser(ctx context.Context, id string) (User, error) {
	if !sessionID(id) {
		return User{}, failure("RESOURCE_NOT_FOUND")
	}
	store, e := s.Repository()
	if e != nil {
		return User{}, e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	u, e := scanUser(store.QueryRow(ctx, identitySQL(store, "SELECT "+userColumns+" FROM users WHERE id=$1", "SELECT "+mysqlUserColumns+" FROM users WHERE id=?", `SELECT `+damengUserColumns+` FROM USERS WHERE ID=?`, id)))
	if errors.Is(e, sql.ErrNoRows) {
		return User{}, failure("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return User{}, sessionError(e)
	}
	return u, nil
}

func (s *Service) SetStatus(ctx context.Context, a Auth, id, status string, version int64, reason, request string) (User, error) {
	if !sessionActor(a) {
		return User{}, failure("AUTH_SESSION_EXPIRED")
	}
	if a.Me.Audience != "admin" {
		return User{}, failure("ACCESS_DENIED")
	}
	if !sessionID(id) || !accountVersion(version) || status != "active" && status != "disabled" || !recoveryReason(reason) {
		return User{}, failure("VALIDATION_FAILED")
	}
	store, e := s.Repository()
	if e != nil {
		return User{}, e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	var out User
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		ids := []string{a.Me.User.ID}
		if id != a.Me.User.ID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		targetFound := true
		for _, user := range ids {
			var locked string
			e := tx.QueryRow(ctx, identitySQL(tx, "SELECT id::text FROM users WHERE id=$1 FOR UPDATE", "SELECT id FROM users WHERE id=? FOR UPDATE", `SELECT ID FROM USERS WHERE ID=? FOR UPDATE`, user)).Scan(&locked)
			if errors.Is(e, sql.ErrNoRows) {
				if user == a.Me.User.ID {
					return failure("AUTH_SESSION_EXPIRED")
				}
				targetFound = false
				continue
			}
			if e != nil {
				return e
			}
		}
		if e := s.RecheckWithin(ctx, tx, a); e != nil {
			return e
		}
		if !targetFound {
			return failure("RESOURCE_NOT_FOUND")
		}
		u, e := scanUser(tx.QueryRow(ctx, identitySQL(tx, "SELECT "+userColumns+" FROM users WHERE id=$1", "SELECT "+mysqlUserColumns+" FROM users WHERE id=?", `SELECT `+damengUserColumns+` FROM USERS WHERE ID=?`, id)))
		if e != nil {
			return e
		}
		if u.Version != version {
			return failure("VERSION_CONFLICT")
		}
		var protected bool
		if e = tx.QueryRow(ctx, identitySQL(tx, "SELECT primary_admin_user_id=$1 FROM installation_state WHERE id=1", "SELECT primary_admin_user_id=? FROM installation_state WHERE id=1", `SELECT CASE WHEN PRIMARY_ADMIN_USER_ID=? THEN 1 ELSE 0 END FROM INSTALLATION_STATE WHERE ID=1`, id)).Scan(&protected); e != nil {
			return e
		}
		if protected && status == "disabled" {
			return failure("PROTECTED_ACCOUNT")
		}
		if u.Status == status {
			out = u
			return nil
		}
		epoch, action := 0, "account.enabled"
		if status == "disabled" {
			epoch, action = 1, "account.disabled"
		}
		if _, e = tx.Exec(ctx, identitySQL(tx, "UPDATE users SET status=$1,version=version+1,auth_epoch=auth_epoch+$2,updated_at=clock_timestamp() WHERE id=$3", "UPDATE users SET status=?,version=version+1,auth_epoch=auth_epoch+?,updated_at=UTC_TIMESTAMP(6) WHERE id=?", `UPDATE USERS SET STATUS=?,VERSION=VERSION+1,AUTH_EPOCH=AUTH_EPOCH+?,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE ID=?`, status, epoch, id)); e != nil {
			return e
		}
		if status == "disabled" {
			if e = revokeAuthorityWithin(ctx, tx, id, "account_disabled"); e != nil {
				return e
			}
		}
		if e = audit.WriteWithin(ctx, tx, a.Me.User.ID, id, "admin", action, "success", "user", id, reason, request); e != nil {
			return e
		}
		out, e = scanUser(tx.QueryRow(ctx, identitySQL(tx, "SELECT "+userColumns+" FROM users WHERE id=$1", "SELECT "+mysqlUserColumns+" FROM users WHERE id=?", `SELECT `+damengUserColumns+` FROM USERS WHERE ID=?`, id)))
		return e
	})
	if e != nil {
		return User{}, sessionError(e)
	}
	return out, nil
}
