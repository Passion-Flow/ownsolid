package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

type Invitation struct {
	ID        string        `json:"id"`
	Email     string        `json:"email"`
	Locale    string        `json:"locale"`
	Status    string        `json:"status"`
	Version   int64         `json:"version"`
	ExpiresAt time.Time     `json:"expires_at"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Delivery  *mail.Summary `json:"delivery"`
}

const invitationColumns = `id::text,email_canonical,locale,CASE WHEN status='pending' AND expires_at<=clock_timestamp() THEN 'expired' ELSE status END,version,expires_at,created_at,updated_at`
const mysqlInvitationColumns = `id,email_canonical,locale,CASE WHEN status='pending' AND expires_at<=UTC_TIMESTAMP(6) THEN 'expired' ELSE status END,version,expires_at,created_at,updated_at`
const damengInvitationColumns = `ID,EMAIL_CANONICAL,LOCALE,CASE WHEN STATUS='pending' AND EXPIRES_AT<=CURRENT_TIMESTAMP(6) THEN 'expired' ELSE STATUS END,VERSION,EXPIRES_AT,CREATED_AT,UPDATED_AT`
const createInvitation = "createInvitation"
const resendInvitation = "resendInvitation"

func scanInvitation(row transaction.Row) (Invitation, error) {
	var i Invitation
	e := row.Scan(&i.ID, &i.Email, &i.Locale, &i.Status, &i.Version, &i.ExpiresAt, &i.CreatedAt, &i.UpdatedAt)
	return i, e
}
func (s *Service) invitation(ctx context.Context, tx transaction.Executor, id string) (Invitation, error) {
	i, e := scanInvitation(tx.QueryRow(ctx, identitySQL(tx, "SELECT "+invitationColumns+" FROM invitations WHERE id=$1", "SELECT "+mysqlInvitationColumns+" FROM invitations WHERE id=?", `SELECT `+damengInvitationColumns+` FROM INVITATIONS WHERE ID=?`, id)))
	if e != nil {
		return Invitation{}, e
	}
	i.Delivery, e = mail.LatestWithin(ctx, tx, id)
	return i, e
}
func (s *Service) invitationStore(delivery bool) (transaction.Store, error) {
	store, e := s.Repository()
	if e != nil {
		return nil, e
	}
	if delivery && (s.Mail == nil || s.Mail.Keys == nil || len(s.HMACKey) < security.MinimumHMACKeyBytes || len(s.HMACKey) > security.MaximumHMACKeyBytes) {
		return nil, failure("IDENTITY_INVITATION_CONFIGURATION_REQUIRED")
	}
	return store, nil
}

// invitationScope fences the exact Workspace against concurrent archival. Use
// fresh eligibility after waiting;never lock every row of a joined scope query.
func (s *Service) invitationScope(ctx context.Context, tx transaction.Executor, tenant, workspace string, active bool, unusable string) error {
	var actual, status string
	e := tx.QueryRow(ctx, identitySQL(tx, "SELECT tenant_id::text,status FROM workspaces WHERE id=$1 FOR SHARE", "SELECT tenant_id,status FROM workspaces WHERE id=? FOR SHARE", `SELECT TENANT_ID,STATUS FROM WORKSPACES WHERE ID=? FOR UPDATE`, workspace)).Scan(&actual, &status)
	if errors.Is(e, sql.ErrNoRows) {
		return failure(unusable)
	}
	if e != nil {
		return e
	}
	var eligible bool
	e = tx.QueryRow(ctx, identitySQL(tx, `SELECT EXISTS(SELECT 1 FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' AND i.tenant_id=$1)`, `SELECT EXISTS(SELECT 1 FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' AND i.tenant_id=?)`, `SELECT CASE WHEN EXISTS(SELECT 1 FROM INSTALLATION_STATE I JOIN TENANTS T ON T.ID=I.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND T.STATUS='active' AND I.TENANT_ID=?) THEN 1 ELSE 0 END FROM DUAL`, tenant)).Scan(&eligible)
	if e != nil {
		return e
	}
	if !eligible || actual != tenant || active && status != "active" {
		return failure(unusable)
	}
	return nil
}
func (s *Service) invitationLocation(ctx context.Context, q transaction.Executor, id string) (string, string, error) {
	var tenant, workspace string
	e := q.QueryRow(ctx, identitySQL(q, "SELECT tenant_id::text,workspace_id::text FROM invitations WHERE id=$1", "SELECT tenant_id,workspace_id FROM invitations WHERE id=?", `SELECT TENANT_ID,WORKSPACE_ID FROM INVITATIONS WHERE ID=?`, id)).Scan(&tenant, &workspace)
	if errors.Is(e, sql.ErrNoRows) {
		e = failure("RESOURCE_NOT_FOUND")
	}
	return tenant, workspace, e
}
func (s *Service) ValidateInvitation(ctx context.Context, token string) (map[string]any, error) {
	if !security.ValidToken(token) {
		return nil, failure("TOKEN_UNUSABLE")
	}
	store, e := s.invitationStore(false)
	if e != nil {
		return nil, e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	var email, locale string
	var exp time.Time
	e = store.QueryRow(ctx, identitySQL(store,
		`SELECT v.email_canonical,v.locale,v.expires_at FROM invitations v JOIN workspaces w ON w.id=v.workspace_id AND w.tenant_id=v.tenant_id JOIN tenants t ON t.id=v.tenant_id JOIN installation_state i ON i.tenant_id=v.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND v.token_hash=$1 AND v.status='pending' AND v.expires_at>clock_timestamp() AND w.status='active' AND t.status='active'`,
		`SELECT v.email_canonical,v.locale,v.expires_at FROM invitations v JOIN workspaces w ON w.id=v.workspace_id AND w.tenant_id=v.tenant_id JOIN tenants t ON t.id=v.tenant_id JOIN installation_state i ON i.tenant_id=v.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND v.token_hash=? AND v.status='pending' AND v.expires_at>UTC_TIMESTAMP(6) AND w.status='active' AND t.status='active'`, `SELECT V.EMAIL_CANONICAL,V.LOCALE,V.EXPIRES_AT FROM INVITATIONS V JOIN WORKSPACES W ON W.ID=V.WORKSPACE_ID AND W.TENANT_ID=V.TENANT_ID JOIN TENANTS T ON T.ID=V.TENANT_ID JOIN INSTALLATION_STATE I ON I.TENANT_ID=V.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND V.TOKEN_HASH=? AND V.STATUS='pending' AND V.EXPIRES_AT>CURRENT_TIMESTAMP(6) AND W.STATUS='active' AND T.STATUS='active'`, security.Digest(token))).Scan(&email, &locale, &exp)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, failure("TOKEN_UNUSABLE")
	}
	if e != nil {
		return nil, sessionError(e)
	}
	return map[string]any{"email": email, "locale": locale, "expires_at": exp}, nil
}
func (s *Service) Invite(ctx context.Context, a Auth, id, operation, key, email, locale string, version int64, request string) (Invitation, error) {
	if !sessionActor(a) {
		return Invitation{}, failure("AUTH_SESSION_EXPIRED")
	}
	if a.Me.Audience != "admin" {
		return Invitation{}, failure("ACCESS_DENIED")
	}
	if !sessionID(key) || operation != createInvitation && operation != resendInvitation || operation == createInvitation && (id != "" || version != 0) || operation == resendInvitation && (!sessionID(id) || !accountVersion(version) || email != "" || locale != "") {
		return Invitation{}, failure("VALIDATION_FAILED")
	}
	if operation == createInvitation {
		var e error
		email, _, e = security.Email(email)
		if e != nil {
			return Invitation{}, e
		}
		selected, available := s.ResolveLocale(locale)
		if !available {
			return Invitation{}, failure("VALIDATION_FAILED")
		}
		locale = selected
	}
	store, e := s.invitationStore(true)
	if e != nil {
		return Invitation{}, e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	payload, _ := json.Marshal([]any{id, email, locale, version})
	hash := sha256.Sum256(payload)
	var result Invitation
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if e := s.RecheckWithin(ctx, tx, a); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, identitySQL(tx, "DELETE FROM idempotency_records WHERE actor_user_id=$1 AND operation_id=$2 AND idempotency_key=$3 AND expires_at<=clock_timestamp()", "DELETE FROM idempotency_records WHERE actor_user_id=? AND operation_id=? AND idempotency_key=? AND expires_at<=UTC_TIMESTAMP(6)", `DELETE FROM IDEMPOTENCY_RECORDS WHERE ACTOR_USER_ID=? AND OPERATION_ID=? AND IDEMPOTENCY_KEY=? AND EXPIRES_AT<=CURRENT_TIMESTAMP(6)`, a.Me.User.ID, operation, key)); e != nil {
			return e
		}
		var saved, body []byte
		e := tx.QueryRow(ctx, identitySQL(tx, "SELECT request_hash,response_body FROM idempotency_records WHERE actor_user_id=$1 AND operation_id=$2 AND idempotency_key=$3", "SELECT request_hash,response_body FROM idempotency_records WHERE actor_user_id=? AND operation_id=? AND idempotency_key=?", `SELECT REQUEST_HASH,RESPONSE_BODY FROM IDEMPOTENCY_RECORDS WHERE ACTOR_USER_ID=? AND OPERATION_ID=? AND IDEMPOTENCY_KEY=?`, a.Me.User.ID, operation, key)).Scan(&saved, &body)
		if e == nil {
			if !bytes.Equal(saved, hash[:]) {
				return failure("IDEMPOTENCY_CONFLICT")
			}
			return json.Unmarshal(body, &result)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if _, e = s.AdmitWithin(ctx, tx, Limit{Purpose: "admin.invite", Identifier: a.Me.User.ID, Count: s.Config.Policy().Rates.AdminInviteCount, Window: config.Seconds(s.Config.Policy().Rates.AdminInviteWindowSeconds)}); e != nil {
			return e
		}
		var tenant, workspace string
		if operation == createInvitation {
			e = tx.QueryRow(ctx, identitySQL(tx, "SELECT tenant_id::text,default_workspace_id::text FROM installation_state WHERE id=1 AND bootstrap_complete", "SELECT tenant_id,default_workspace_id FROM installation_state WHERE id=1 AND bootstrap_complete=1", `SELECT TENANT_ID,DEFAULT_WORKSPACE_ID FROM INSTALLATION_STATE WHERE ID=1 AND BOOTSTRAP_COMPLETE=1`)).Scan(&tenant, &workspace)
			if errors.Is(e, sql.ErrNoRows) {
				return failure("SERVICE_NOT_READY")
			}
			if e != nil {
				return e
			}
		} else {
			tenant, workspace, e = s.invitationLocation(ctx, tx, id)
			if e != nil {
				return e
			}
		}
		if e = s.invitationScope(ctx, tx, tenant, workspace, true, "RESOURCE_NOT_FOUND"); e != nil {
			return e
		}
		generation, action, httpStatus := 1, "invitation.created", 201
		if operation == resendInvitation {
			var locked string
			e = tx.QueryRow(ctx, identitySQL(tx, "SELECT id::text FROM invitations WHERE id=$1 FOR UPDATE", "SELECT id FROM invitations WHERE id=? FOR UPDATE", `SELECT ID FROM INVITATIONS WHERE ID=? FOR UPDATE`, id)).Scan(&locked)
			if e != nil {
				return e
			}
			i, e := s.invitation(ctx, tx, id)
			if e != nil {
				return e
			}
			if i.Version != version {
				return failure("VERSION_CONFLICT")
			}
			if i.Status != "pending" && i.Status != "expired" {
				return failure("INVITATION_STATE_CONFLICT")
			}
			email, locale, action, httpStatus = i.Email, i.Locale, "invitation.resent", 200
			if e = tx.QueryRow(ctx, identitySQL(tx, "SELECT token_generation+1 FROM invitations WHERE id=$1", "SELECT token_generation+1 FROM invitations WHERE id=?", `SELECT TOKEN_GENERATION+1 FROM INVITATIONS WHERE ID=?`, id)).Scan(&generation); e != nil {
				return e
			}
		} else {
			var current bool
			if e = tx.QueryRow(ctx, identitySQL(tx, "SELECT default_workspace_id=$1 FROM installation_state WHERE id=1", "SELECT default_workspace_id=? FROM installation_state WHERE id=1", `SELECT CASE WHEN DEFAULT_WORKSPACE_ID=? THEN 1 ELSE 0 END FROM INSTALLATION_STATE WHERE ID=1`, workspace)).Scan(&current); e != nil {
				return e
			}
			if !current {
				return failure("SERVICE_NOT_READY")
			}
		}
		var exists bool
		if e = tx.QueryRow(ctx, identitySQL(tx, "SELECT EXISTS(SELECT 1 FROM users WHERE email_canonical=$1)", "SELECT EXISTS(SELECT 1 FROM users WHERE email_canonical=?)", `SELECT CASE WHEN EXISTS(SELECT 1 FROM USERS WHERE EMAIL_CANONICAL=?) THEN 1 ELSE 0 END FROM DUAL`, email)).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return failure("ACCOUNT_EXISTS")
		}
		rows, e := tx.Query(ctx, identitySQL(tx, `SELECT id::text FROM invitations WHERE email_canonical=$1 AND status='pending' AND expires_at<=clock_timestamp() AND id::text<>$2 AND tenant_id=$3 ORDER BY id FOR UPDATE`, `SELECT id FROM invitations WHERE email_canonical=? AND status='pending' AND expires_at<=UTC_TIMESTAMP(6) AND id<>? AND tenant_id=? ORDER BY id FOR UPDATE`, `SELECT ID FROM INVITATIONS WHERE EMAIL_CANONICAL=? AND STATUS='pending' AND EXPIRES_AT<=CURRENT_TIMESTAMP(6) AND ID<>COALESCE(?, '-') AND TENANT_ID=? ORDER BY ID FOR UPDATE`, email, id, tenant))
		if e != nil {
			return e
		}
		var expired []string
		for rows.Next() {
			var x string
			if e = rows.Scan(&x); e != nil {
				rows.Close()
				return e
			}
			expired = append(expired, x)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, x := range expired {
			if _, e = tx.Exec(ctx, identitySQL(tx, "UPDATE invitations SET status='expired',token_hash=NULL,version=version+1,updated_at=clock_timestamp() WHERE id=$1", "UPDATE invitations SET status='expired',token_hash=NULL,version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?", `UPDATE INVITATIONS SET STATUS='expired',TOKEN_HASH=NULL,VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE ID=?`, x)); e != nil {
				return e
			}
			if e = mail.CancelInvitationWithin(ctx, tx, x); e != nil {
				return e
			}
			if e = audit.WriteWithin(ctx, tx, a.Me.User.ID, "", "admin", "invitation.expired", "success", "invitation", x, "", request); e != nil {
				return e
			}
		}
		if e = tx.QueryRow(ctx, identitySQL(tx, "SELECT EXISTS(SELECT 1 FROM invitations WHERE email_canonical=$1 AND status='pending' AND id::text<>$2)", "SELECT EXISTS(SELECT 1 FROM invitations WHERE email_canonical=? AND status='pending' AND id<>?)", `SELECT CASE WHEN EXISTS(SELECT 1 FROM INVITATIONS WHERE EMAIL_CANONICAL=? AND STATUS='pending' AND ID<>COALESCE(?, '-')) THEN 1 ELSE 0 END FROM DUAL`, email, id)).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return failure("INVITATION_EXISTS")
		}
		token := security.Token()
		if operation == createInvitation {
			id = uuid.NewString()
			_, e = tx.Exec(ctx, identitySQL(tx, `INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES($1,$2,$3,$4,$5,'pending',$6,$7,clock_timestamp(),clock_timestamp(),clock_timestamp()+$8::bigint*interval '1 second')`, `INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES(?,?,?,?,?,'pending',?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)))`, `INSERT INTO INVITATIONS(ID,TENANT_ID,WORKSPACE_ID,EMAIL_CANONICAL,LOCALE,STATUS,TOKEN_HASH,CREATED_BY,CREATED_AT,UPDATED_AT,EXPIRES_AT) VALUES(?,?,?,?,?,'pending',?,?,CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)+NUMTODSINTERVAL(?,'SECOND'))`, id, tenant, workspace, email, locale, security.Digest(token), a.Me.User.ID, s.Config.Policy().Identity.InvitationTTLSeconds))
		} else {
			if e = mail.CancelInvitationWithin(ctx, tx, id); e != nil {
				return e
			}
			_, e = tx.Exec(ctx, identitySQL(tx, `UPDATE invitations SET status='pending',token_hash=$1,token_generation=$2,version=version+1,updated_at=clock_timestamp(),expires_at=clock_timestamp()+$3::bigint*interval '1 second' WHERE id=$4`, `UPDATE invitations SET status='pending',token_hash=?,token_generation=?,version=version+1,updated_at=UTC_TIMESTAMP(6),expires_at=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)) WHERE id=?`, `UPDATE INVITATIONS SET STATUS='pending',TOKEN_HASH=?,TOKEN_GENERATION=?,VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6),EXPIRES_AT=CURRENT_TIMESTAMP(6)+NUMTODSINTERVAL(?,'SECOND') WHERE ID=?`, security.Digest(token), generation, s.Config.Policy().Identity.InvitationTTLSeconds, id))
		}
		if e != nil {
			var se *transaction.Error
			if errors.As(e, &se) && se.Kind == transaction.UniqueConflict {
				return failure("INVITATION_EXISTS")
			}
			return e
		}
		if _, e = s.Mail.EnqueueWithin(ctx, tx, mail.Input{Purpose: "invitation", Recipient: email, Locale: locale, InvitationID: id, Generation: generation, Token: token, Audience: "portal"}); e != nil {
			return e
		}
		if e = audit.WriteWithin(ctx, tx, a.Me.User.ID, "", "admin", action, "success", "invitation", id, "", request); e != nil {
			return e
		}
		result, e = s.invitation(ctx, tx, id)
		if e != nil {
			return e
		}
		body, e = json.Marshal(result)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, identitySQL(tx, `INSERT INTO idempotency_records(actor_user_id,operation_id,idempotency_key,request_hash,response_status,response_body,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp(),clock_timestamp()+$7::bigint*interval '1 second')`, `INSERT INTO idempotency_records(actor_user_id,operation_id,idempotency_key,request_hash,response_status,response_body,created_at,expires_at) VALUES(?,?,?,?,?,?,UTC_TIMESTAMP(6),TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)))`, `INSERT INTO IDEMPOTENCY_RECORDS(ACTOR_USER_ID,OPERATION_ID,IDEMPOTENCY_KEY,REQUEST_HASH,RESPONSE_STATUS,RESPONSE_BODY,CREATED_AT,EXPIRES_AT) VALUES(?,?,?,?,?,?,CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)+NUMTODSINTERVAL(?,'SECOND'))`, a.Me.User.ID, operation, key, hash[:], httpStatus, identityJSON(tx, body), s.Config.Policy().Identity.IdempotencySeconds))
		return e
	})
	if e != nil {
		return Invitation{}, sessionError(e)
	}
	return result, nil
}
func (s *Service) RevokeInvitation(ctx context.Context, a Auth, id string, version int64, reason, request string) (Invitation, error) {
	if !sessionActor(a) {
		return Invitation{}, failure("AUTH_SESSION_EXPIRED")
	}
	if a.Me.Audience != "admin" {
		return Invitation{}, failure("ACCESS_DENIED")
	}
	if !sessionID(id) || !accountVersion(version) || !recoveryReason(reason) {
		return Invitation{}, failure("VALIDATION_FAILED")
	}
	store, e := s.invitationStore(false)
	if e != nil {
		return Invitation{}, e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	var result Invitation
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if e := s.RecheckWithin(ctx, tx, a); e != nil {
			return e
		}
		tenant, workspace, e := s.invitationLocation(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = s.invitationScope(ctx, tx, tenant, workspace, false, "RESOURCE_NOT_FOUND"); e != nil {
			return e
		}
		var locked string
		if e = tx.QueryRow(ctx, identitySQL(tx, "SELECT id::text FROM invitations WHERE id=$1 FOR UPDATE", "SELECT id FROM invitations WHERE id=? FOR UPDATE", `SELECT ID FROM INVITATIONS WHERE ID=? FOR UPDATE`, id)).Scan(&locked); e != nil {
			return e
		}
		i, e := s.invitation(ctx, tx, id)
		if e != nil {
			return e
		}
		if i.Version != version {
			return failure("VERSION_CONFLICT")
		}
		if i.Status != "pending" && i.Status != "expired" {
			return failure("INVITATION_STATE_CONFLICT")
		}
		if _, e = tx.Exec(ctx, identitySQL(tx, "UPDATE invitations SET status='revoked',token_hash=NULL,version=version+1,updated_at=clock_timestamp(),revoked_at=clock_timestamp() WHERE id=$1", "UPDATE invitations SET status='revoked',token_hash=NULL,version=version+1,updated_at=UTC_TIMESTAMP(6),revoked_at=UTC_TIMESTAMP(6) WHERE id=?", `UPDATE INVITATIONS SET STATUS='revoked',TOKEN_HASH=NULL,VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6),REVOKED_AT=CURRENT_TIMESTAMP(6) WHERE ID=?`, id)); e != nil {
			return e
		}
		if e = mail.CancelInvitationWithin(ctx, tx, id); e != nil {
			return e
		}
		if e = audit.WriteWithin(ctx, tx, a.Me.User.ID, "", "admin", "invitation.revoked", "success", "invitation", id, reason, request); e != nil {
			return e
		}
		result, e = s.invitation(ctx, tx, id)
		return e
	})
	if e != nil {
		return Invitation{}, sessionError(e)
	}
	return result, nil
}
func (s *Service) Register(ctx context.Context, token, name, password, request string) error {
	i, e := s.ValidateInvitation(ctx, token)
	if e != nil {
		return e
	}
	name, e = security.Name(name)
	if e != nil {
		return e
	}
	store, e := s.invitationStore(false)
	if e != nil {
		return e
	}
	password, e = s.passwordPolicy(ctx, password, i["email"].(string))
	if e != nil {
		return e
	}
	phc, e := s.Passwords.Hash(ctx, password)
	if e != nil {
		return e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var id, tenant, workspace string
		e := tx.QueryRow(ctx, identitySQL(tx, "SELECT id::text,tenant_id::text,workspace_id::text FROM invitations WHERE token_hash=$1", "SELECT id,tenant_id,workspace_id FROM invitations WHERE token_hash=?", `SELECT ID,TENANT_ID,WORKSPACE_ID FROM INVITATIONS WHERE TOKEN_HASH=?`, security.Digest(token))).Scan(&id, &tenant, &workspace)
		if errors.Is(e, sql.ErrNoRows) {
			return failure("TOKEN_UNUSABLE")
		}
		if e != nil {
			return e
		}
		if e = s.invitationScope(ctx, tx, tenant, workspace, true, "TOKEN_UNUSABLE"); e != nil {
			return e
		}
		var locked string
		if e = tx.QueryRow(ctx, identitySQL(tx, "SELECT id::text FROM invitations WHERE id=$1 FOR UPDATE", "SELECT id FROM invitations WHERE id=? FOR UPDATE", `SELECT ID FROM INVITATIONS WHERE ID=? FOR UPDATE`, id)).Scan(&locked); e != nil {
			return e
		}
		var email, locale string
		e = tx.QueryRow(ctx, identitySQL(tx, `SELECT email_canonical,locale FROM invitations WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND token_hash=$4 AND status='pending' AND expires_at>clock_timestamp()`, `SELECT email_canonical,locale FROM invitations WHERE id=? AND tenant_id=? AND workspace_id=? AND token_hash=? AND status='pending' AND expires_at>UTC_TIMESTAMP(6)`, `SELECT EMAIL_CANONICAL,LOCALE FROM INVITATIONS WHERE ID=? AND TENANT_ID=? AND WORKSPACE_ID=? AND TOKEN_HASH=? AND STATUS='pending' AND EXPIRES_AT>CURRENT_TIMESTAMP(6)`, id, tenant, workspace, security.Digest(token))).Scan(&email, &locale)
		if errors.Is(e, sql.ErrNoRows) {
			return failure("TOKEN_UNUSABLE")
		}
		if e != nil {
			return e
		}
		user := uuid.NewString()
		if _, e = tx.Exec(ctx, identitySQL(tx, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,email_verified_at) VALUES($1,$2,$3,$4,$5,'active',clock_timestamp())`, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,email_verified_at) VALUES(?,?,?,?,?,'active',UTC_TIMESTAMP(6))`, `INSERT INTO USERS(ID,EMAIL_CANONICAL,EMAIL_DISPLAY,DISPLAY_NAME,LOCALE,STATUS,EMAIL_VERIFIED_AT) VALUES(?,?,?,?,?,'active',CURRENT_TIMESTAMP(6))`, user, email, email, name, locale)); e != nil {
			var se *transaction.Error
			if errors.As(e, &se) && se.Kind == transaction.UniqueConflict {
				return failure("TOKEN_UNUSABLE")
			}
			return e
		}
		if _, e = tx.Exec(ctx, identitySQL(tx, "INSERT INTO password_credentials(user_id,password_phc) VALUES($1,$2)", "INSERT INTO password_credentials(user_id,password_phc) VALUES(?,?)", `INSERT INTO PASSWORD_CREDENTIALS(USER_ID,PASSWORD_PHC) VALUES(?,?)`, user, phc)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, identityMembershipSQL(tx, user, tenant, workspace, "member")); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, identitySQL(tx, "UPDATE invitations SET status='accepted',token_hash=NULL,accepted_user_id=$1,accepted_at=clock_timestamp(),updated_at=clock_timestamp(),version=version+1 WHERE id=$2", "UPDATE invitations SET status='accepted',token_hash=NULL,accepted_user_id=?,accepted_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6),version=version+1 WHERE id=?", `UPDATE INVITATIONS SET STATUS='accepted',TOKEN_HASH=NULL,ACCEPTED_USER_ID=?,ACCEPTED_AT=CURRENT_TIMESTAMP(6),UPDATED_AT=CURRENT_TIMESTAMP(6),VERSION=VERSION+1 WHERE ID=?`, user, id)); e != nil {
			return e
		}
		if e = mail.CancelInvitationWithin(ctx, tx, id); e != nil {
			return e
		}
		return audit.WriteWithin(ctx, tx, "", user, "portal", "invitation.accepted", "success", "invitation", id, "", request)
	})
	return sessionError(e)
}
