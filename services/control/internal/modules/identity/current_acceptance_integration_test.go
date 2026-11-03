//go:build repository_integration

package identity_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

const currentIntegrityOperationSeconds = 20
const currentIntegrityLockSeconds = 2

type currentIntegrityOutcome struct {
	index int
	err   error
}

func currentIntegrityContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(t.Context(), currentIntegrityOperationSeconds*time.Second)
}

func currentIntegrityCounts(t *testing.T, s *identity.Service, query string, wanted ...int) {
	t.Helper()
	values := make([]int, len(wanted))
	dest := make([]any, len(wanted))
	for i := range values {
		dest[i] = &values[i]
	}
	if s.DB.QueryRow(t.Context(), query).Scan(dest...) != nil {
		t.Fatal("CURRENT_M01_STATE_READ_FAILED")
	}
	for i := range wanted {
		if values[i] != wanted[i] {
			t.Fatal("CURRENT_M01_STATE_COUNT_MISMATCH", i, values[i], wanted[i])
		}
	}
}

func currentIntegrityWaiters(t *testing.T, ctx context.Context, s *identity.Service, fragment string, wanted int) {
	t.Helper()
	deadline := time.Now().Add(currentIntegrityLockSeconds * time.Second)
	for time.Now().Before(deadline) {
		var actual int
		e := s.DB.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1", "%"+fragment+"%").Scan(&actual)
		if e != nil {
			t.Fatal("CURRENT_M01_REAL_LOCK_OBSERVATION_FAILED")
		}
		if actual == wanted {
			t.Logf("CURRENT_M01_FINAL_LOCK_BARRIER observed_waiters=%d", actual)
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("CURRENT_M01_REAL_LOCK_CONTEXT_ENDED")
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatal("CURRENT_M01_REQUIRED_FINAL_LOCK_WAITERS_NOT_REACHED", wanted)
}

func currentIntegrityHold(t *testing.T, ctx context.Context, s *identity.Service, table, id string) pgx.Tx {
	t.Helper()
	if table != "users" && table != "invitations" {
		t.Fatal("CURRENT_M01_FIXED_LOCK_TABLE_REQUIRED")
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal("CURRENT_M01_LOCK_TRANSACTION_FAILED")
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, e = tx.Exec(ctx, "SELECT id FROM "+table+" WHERE id=$1 FOR UPDATE", id); e != nil {
		t.Fatal("CURRENT_M01_LOCK_ROW_FAILED")
	}
	return tx
}

func currentIntegrityResult(t *testing.T, ctx context.Context, results <-chan currentIntegrityOutcome) currentIntegrityOutcome {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-ctx.Done():
		t.Fatal("CURRENT_M01_DOMAIN_CALL_DID_NOT_JOIN")
	}
	return currentIntegrityOutcome{}
}

func currentIntegrityActor(t *testing.T) (*identity.Service, string, identity.Auth) {
	t.Helper()
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	return s, password, fixture.Login(t, s, "admin@example.invalid", password, "admin")
}

func currentIntegrityDigest(t *testing.T, s *identity.Service, user string) string {
	t.Helper()
	// Private rows (including PHCs/token hashes/ciphertext) stay in memory. Only
	// equality is used; no raw row, password or digest is printed on failure.
	var raw string
	e := s.DB.QueryRow(t.Context(), `SELECT jsonb_build_object(
		'user',(SELECT to_jsonb(u) FROM users u WHERE id=$1),
		'credential',(SELECT to_jsonb(c) FROM password_credentials c WHERE user_id=$1),
		'sessions',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM sessions a WHERE user_id=$1),
		'resets',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM password_reset_tokens r WHERE user_id=$1),
		'mail',(SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM mail_deliveries m WHERE user_id=$1))::text`, user).Scan(&raw)
	if e != nil {
		t.Fatal("CURRENT_M01_PRIVATE_POSTIMAGE_READ_FAILED")
	}
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

func TestM01CurrentConcurrentBootstrapPostimage(t *testing.T) {
	s := fixture.New(t)
	ctx, cancel := currentIntegrityContext(t)
	defer cancel()
	emails := []string{"bootstrap-alpha@example.invalid", "bootstrap-beta@example.invalid"}
	passwords := []string{fixture.Password(), fixture.Password()}
	results := make(chan currentIntegrityOutcome, 2)
	barrier := make(chan struct{})
	for i := range emails {
		go func(index int) {
			<-barrier
			results <- currentIntegrityOutcome{index, s.Bootstrap(ctx, emails[index], "Synthetic administrator", passwords[index], "Concurrent installation", "Default")}
		}(i)
	}
	close(barrier)
	winner := -1
	for range emails {
		r := currentIntegrityResult(t, ctx, results)
		if r.err == nil {
			if winner != -1 {
				t.Fatal("CURRENT_M01_MULTIPLE_BOOTSTRAP_WINNERS")
			}
			winner = r.index
		} else {
			expect(t, r.err, "BOOTSTRAP_ALREADY_COMPLETE")
		}
	}
	if winner < 0 {
		t.Fatal("CURRENT_M01_BOOTSTRAP_WINNER_MISSING")
	}
	currentIntegrityCounts(t, s, `SELECT (SELECT count(*) FROM tenants),(SELECT count(*) FROM workspaces),(SELECT count(*) FROM users),(SELECT count(*) FROM password_credentials),(SELECT count(*) FROM workspace_memberships WHERE role='owner'),(SELECT count(*) FROM audit_events WHERE action='installation.bootstrapped')`, 1, 1, 1, 1, 1, 1)
	var user, email string
	var complete bool
	if s.DB.QueryRow(ctx, "SELECT primary_admin_user_id::text,bootstrap_complete,(SELECT email_canonical FROM users WHERE id=primary_admin_user_id) FROM installation_state WHERE id=1").Scan(&user, &complete, &email) != nil || !complete || email != emails[winner] {
		t.Fatal("CURRENT_M01_BOOTSTRAP_DESIGNATED_WINNER_MISMATCH")
	}
	before := currentIntegrityDigest(t, s, user)
	expect(t, s.Bootstrap(ctx, "replacement@example.invalid", "Replacement", fixture.Password(), "Replacement installation", "Other"), "BOOTSTRAP_ALREADY_COMPLETE")
	if currentIntegrityDigest(t, s, user) != before {
		t.Fatal("CURRENT_M01_REPEAT_BOOTSTRAP_CHANGED_AUTHORITY")
	}
	currentIntegrityCounts(t, s, `SELECT (SELECT count(*) FROM tenants),(SELECT count(*) FROM workspaces),(SELECT count(*) FROM users),(SELECT count(*) FROM password_credentials),(SELECT count(*) FROM workspace_memberships),(SELECT count(*) FROM audit_events WHERE action='installation.bootstrapped')`, 1, 1, 1, 1, 1, 1)
	fixture.Login(t, s, emails[winner], passwords[winner], "admin")
	t.Log("CURRENT_M01_BOOTSTRAP_POSTIMAGE complete_scope_credentials_owner_audit_once=true distinct_winner_preserved=true")
}

func TestM01CurrentDesignatedAdminRecoveryPostimage(t *testing.T) {
	s, password, admin := currentIntegrityActor(t)
	ctx, cancel := currentIntegrityContext(t)
	defer cancel()
	memberID, memberPassword := member(t, s, admin, "recovery-member@example.invalid")
	_, memberSession, _, e := s.Login(ctx, "recovery-member@example.invalid", memberPassword, "portal", "", uuid.NewString())
	if e != nil {
		t.Fatal("CURRENT_M01_MEMBER_SESSION_FAILED")
	}
	if s.RequestReset(ctx, "recovery-member@example.invalid", "portal", "member", uuid.NewString()) != nil || s.RequestReset(ctx, "admin@example.invalid", "portal", "admin", uuid.NewString()) != nil {
		t.Fatal("CURRENT_M01_RECOVERY_TOKEN_SETUP_FAILED")
	}
	primarySessions := map[string]string{}
	for _, audience := range []string{"portal", "admin"} {
		_, token, _, err := s.Login(ctx, "admin@example.invalid", password, audience, "", uuid.NewString())
		if err != nil {
			t.Fatal("CURRENT_M01_PRIMARY_SESSION_FAILED")
		}
		primarySessions[audience] = token
	}
	before := currentIntegrityDigest(t, s, memberID)
	newPassword := fixture.Password()
	reason := "Synthetic designated administrator recovery"
	if s.RecoverAdmin(ctx, newPassword, reason) != nil {
		t.Fatal("CURRENT_M01_DESIGNATED_RECOVERY_FAILED")
	}
	if currentIntegrityDigest(t, s, memberID) != before {
		t.Fatal("CURRENT_M01_RECOVERY_CHANGED_OTHER_USER")
	}
	for audience, token := range primarySessions {
		_, err := s.Authenticate(ctx, token, audience)
		expect(t, err, "AUTH_SESSION_EXPIRED")
	}
	if _, err := s.Authenticate(ctx, memberSession, "portal"); err != nil {
		t.Fatal("CURRENT_M01_RECOVERY_REVOKED_OTHER_USER")
	}
	var sessions, resets, mail, audits int
	if s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL),(SELECT count(*) FROM password_reset_tokens WHERE user_id=$1 AND token_hash IS NOT NULL),(SELECT count(*) FROM mail_deliveries WHERE user_id=$1 AND purpose='password_reset' AND state IN ('queued','sending')),(SELECT count(*) FROM audit_events WHERE action='operator.admin_recovered' AND audience='operator' AND actor_user_id IS NULL AND subject_user_id=$1 AND target_id=$1 AND reason=$2)`, admin.Me.User.ID, reason).Scan(&sessions, &resets, &mail, &audits) != nil || sessions+resets+mail != 0 || audits != 1 {
		t.Fatal("CURRENT_M01_RECOVERY_AUTHORITY_OR_AUDIT_MISMATCH")
	}
	currentIntegrityCounts(t, s, "SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM users WHERE is_platform_admin)", 2, 1)
	_, _, _, e = s.Login(ctx, "admin@example.invalid", password, "admin", "", uuid.NewString())
	expect(t, e, "AUTH_INVALID_CREDENTIALS")
	fixture.Login(t, s, "admin@example.invalid", newPassword, "admin")
	t.Log("CURRENT_M01_RECOVERY_POSTIMAGE designated_only=true other_user_unchanged=true primary_authority_revoked=true operator_audit_once=true")
}

func TestM01CurrentRegistrationFinalBarrier(t *testing.T) {
	s, _, admin := currentIntegrityActor(t)
	ctx, cancel := currentIntegrityContext(t)
	defer cancel()
	invite, e := s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), "registration-race@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal("CURRENT_M01_INVITATION_SETUP_FAILED")
	}
	token := fixture.InvitationToken(t, s, invite.ID)
	tx := currentIntegrityHold(t, ctx, s, "invitations", invite.ID)
	passwords := []string{fixture.Password(), fixture.Password()}
	results := make(chan currentIntegrityOutcome, 2)
	for i := range passwords {
		go func(index int) {
			results <- currentIntegrityOutcome{index, s.Register(ctx, token, "Concurrent member", passwords[index], uuid.NewString())}
		}(i)
	}
	currentIntegrityWaiters(t, ctx, s, "SELECT id::text FROM invitations WHERE id=$1 FOR UPDATE", 2)
	if tx.Commit(ctx) != nil {
		t.Fatal("CURRENT_M01_REGISTRATION_BARRIER_RELEASE_FAILED")
	}
	winner := -1
	for range passwords {
		r := currentIntegrityResult(t, ctx, results)
		if r.err == nil {
			if winner != -1 {
				t.Fatal("CURRENT_M01_MULTIPLE_REGISTRATION_WINNERS")
			}
			winner = r.index
		} else {
			expect(t, r.err, "TOKEN_UNUSABLE")
		}
	}
	if winner < 0 {
		t.Fatal("CURRENT_M01_REGISTRATION_WINNER_MISSING")
	}
	currentIntegrityCounts(t, s, `SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM password_credentials),(SELECT count(*) FROM workspace_memberships),(SELECT count(*) FROM sessions),(SELECT count(*) FROM invitations WHERE status='accepted' AND token_hash IS NULL),(SELECT count(*) FROM audit_events WHERE action='invitation.accepted')`, 2, 2, 2, 1, 1, 1)
	fixture.Login(t, s, "registration-race@example.invalid", passwords[winner], "portal")
	_, _, _, e = s.Login(ctx, "registration-race@example.invalid", passwords[1-winner], "portal", "", uuid.NewString())
	expect(t, e, "AUTH_INVALID_CREDENTIALS")
	t.Log("CURRENT_M01_REGISTRATION_FINAL_BARRIER two_waiters=true one_complete_winner=true no_auto_session=true winner_password_only=true")
}

func TestM01CurrentRevokeRegistrationFinalBarrier(t *testing.T) {
	s, _, admin := currentIntegrityActor(t)
	ctx, cancel := currentIntegrityContext(t)
	defer cancel()
	invite, e := s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), "revoke-race@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal("CURRENT_M01_INVITATION_SETUP_FAILED")
	}
	token := fixture.InvitationToken(t, s, invite.ID)
	tx := currentIntegrityHold(t, ctx, s, "invitations", invite.ID)
	results := make(chan currentIntegrityOutcome, 2)
	go func() {
		_, err := s.RevokeInvitation(ctx, admin, invite.ID, invite.Version, "Synthetic real revoke before registration", uuid.NewString())
		results <- currentIntegrityOutcome{0, err}
	}()
	currentIntegrityWaiters(t, ctx, s, "FROM invitations WHERE id=", 1)
	go func() {
		results <- currentIntegrityOutcome{1, s.Register(ctx, token, "Must not register", fixture.Password(), uuid.NewString())}
	}()
	currentIntegrityWaiters(t, ctx, s, "FROM invitations WHERE id=", 2)
	if tx.Commit(ctx) != nil {
		t.Fatal("CURRENT_M01_REVOKE_BARRIER_RELEASE_FAILED")
	}
	for range 2 {
		r := currentIntegrityResult(t, ctx, results)
		if r.index == 0 {
			if r.err != nil {
				t.Fatal("CURRENT_M01_ACTUAL_REVOKE_FAILED")
			}
		} else {
			expect(t, r.err, "TOKEN_UNUSABLE")
		}
	}
	currentIntegrityCounts(t, s, `SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM invitations WHERE status='revoked' AND token_hash IS NULL),(SELECT count(*) FROM mail_deliveries WHERE state='cancelled' AND payload_key_id IS NULL AND payload_ciphertext IS NULL),(SELECT count(*) FROM audit_events WHERE action='invitation.revoked')`, 1, 1, 1, 1)
	t.Log("CURRENT_M01_REVOKE_REGISTRATION_FINAL_BARRIER actual_revoke_first=true two_waiters=true no_new_member=true old_mail_cancelled=true")
}

func TestM01CurrentResetLoginFinalBarrier(t *testing.T) {
	s, password, admin := currentIntegrityActor(t)
	ctx, cancel := currentIntegrityContext(t)
	defer cancel()
	oldSessions := map[string]string{}
	for _, audience := range []string{"portal", "admin"} {
		_, token, _, e := s.Login(ctx, "admin@example.invalid", password, audience, "", uuid.NewString())
		if e != nil {
			t.Fatal("CURRENT_M01_SESSION_SETUP_FAILED")
		}
		oldSessions[audience] = token
	}
	if s.RequestReset(ctx, "admin@example.invalid", "portal", "reset-login", uuid.NewString()) != nil {
		t.Fatal("CURRENT_M01_RESET_SETUP_FAILED")
	}
	token := fixture.ResetToken(t, s, "admin@example.invalid")
	tx := currentIntegrityHold(t, ctx, s, "users", admin.Me.User.ID)
	newPassword := fixture.Password()
	results := make(chan currentIntegrityOutcome, 2)
	go func() {
		results <- currentIntegrityOutcome{0, s.Reset(ctx, token, newPassword, "portal", uuid.NewString())}
	}()
	currentIntegrityWaiters(t, ctx, s, "FROM users", 1)
	go func() {
		_, _, _, err := s.Login(ctx, "admin@example.invalid", password, "admin", "", uuid.NewString())
		results <- currentIntegrityOutcome{1, err}
	}()
	currentIntegrityWaiters(t, ctx, s, "FROM users", 2)
	if tx.Commit(ctx) != nil {
		t.Fatal("CURRENT_M01_RESET_LOGIN_BARRIER_RELEASE_FAILED")
	}
	for range 2 {
		r := currentIntegrityResult(t, ctx, results)
		if r.index == 0 {
			if r.err != nil {
				t.Fatal("CURRENT_M01_ACTUAL_RESET_FAILED")
			}
		} else {
			expect(t, r.err, "AUTH_INVALID_CREDENTIALS")
		}
	}
	currentIntegrityCounts(t, s, "SELECT count(*) FROM sessions WHERE revoked_at IS NULL", 0)
	for audience, old := range oldSessions {
		_, e := s.Authenticate(ctx, old, audience)
		expect(t, e, "AUTH_SESSION_EXPIRED")
	}
	fixture.Login(t, s, "admin@example.invalid", newPassword, "admin")
	t.Log("CURRENT_M01_RESET_LOGIN_FINAL_BARRIER actual_reset_first=true two_waiters=true no_stale_session=true new_password_valid=true")
}

func TestM01CurrentDistinctResetFinalBarrier(t *testing.T) {
	s, password, admin := currentIntegrityActor(t)
	ctx, cancel := currentIntegrityContext(t)
	defer cancel()
	_, oldSession, _, e := s.Login(ctx, "admin@example.invalid", password, "portal", "", uuid.NewString())
	if e != nil {
		t.Fatal("CURRENT_M01_SESSION_SETUP_FAILED")
	}
	if s.RequestReset(ctx, "admin@example.invalid", "portal", "distinct-reset", uuid.NewString()) != nil {
		t.Fatal("CURRENT_M01_RESET_SETUP_FAILED")
	}
	token := fixture.ResetToken(t, s, "admin@example.invalid")
	tx := currentIntegrityHold(t, ctx, s, "users", admin.Me.User.ID)
	passwords := []string{fixture.Password(), fixture.Password()}
	results := make(chan currentIntegrityOutcome, 2)
	for i := range passwords {
		go func(index int) {
			results <- currentIntegrityOutcome{index, s.Reset(ctx, token, passwords[index], "portal", uuid.NewString())}
		}(i)
	}
	currentIntegrityWaiters(t, ctx, s, "FROM users", 2)
	if tx.Commit(ctx) != nil {
		t.Fatal("CURRENT_M01_DISTINCT_RESET_BARRIER_RELEASE_FAILED")
	}
	winner := -1
	for range 2 {
		r := currentIntegrityResult(t, ctx, results)
		if r.err == nil {
			if winner != -1 {
				t.Fatal("CURRENT_M01_MULTIPLE_RESET_WINNERS")
			}
			winner = r.index
		} else {
			expect(t, r.err, "TOKEN_UNUSABLE")
		}
	}
	if winner < 0 {
		t.Fatal("CURRENT_M01_RESET_WINNER_MISSING")
	}
	currentIntegrityCounts(t, s, `SELECT (SELECT count(*) FROM sessions WHERE revoked_at IS NULL),(SELECT count(*) FROM password_reset_tokens WHERE token_hash IS NOT NULL),(SELECT count(*) FROM audit_events WHERE action='auth.password_reset'),(SELECT count(*) FROM mail_deliveries WHERE purpose='password_changed')`, 0, 0, 1, 1)
	_, e = s.Authenticate(ctx, oldSession, "portal")
	expect(t, e, "AUTH_SESSION_EXPIRED")
	fixture.Login(t, s, "admin@example.invalid", passwords[winner], "admin")
	_, _, _, e = s.Login(ctx, "admin@example.invalid", passwords[1-winner], "admin", "", uuid.NewString())
	expect(t, e, "AUTH_INVALID_CREDENTIALS")
	t.Log("CURRENT_M01_DISTINCT_RESET_FINAL_BARRIER two_waiters=true distinct_passwords=true one_winner_audit_notification=true old_authority_revoked=true")
}

func TestM01CurrentDisableResetFinalBarrier(t *testing.T) {
	s, _, admin := currentIntegrityActor(t)
	ctx, cancel := currentIntegrityContext(t)
	defer cancel()
	user, password := member(t, s, admin, "disable-race@example.invalid")
	_, oldSession, _, e := s.Login(ctx, "disable-race@example.invalid", password, "portal", "", uuid.NewString())
	if e != nil {
		t.Fatal("CURRENT_M01_SESSION_SETUP_FAILED")
	}
	if s.RequestReset(ctx, "disable-race@example.invalid", "portal", "disable-reset", uuid.NewString()) != nil {
		t.Fatal("CURRENT_M01_RESET_SETUP_FAILED")
	}
	token := fixture.ResetToken(t, s, "disable-race@example.invalid")
	before, e := s.GetUser(ctx, user)
	if e != nil {
		t.Fatal("CURRENT_M01_MEMBER_READ_FAILED")
	}
	var credential string
	if s.DB.QueryRow(ctx, "SELECT password_phc FROM password_credentials WHERE user_id=$1", user).Scan(&credential) != nil {
		t.Fatal("CURRENT_M01_CREDENTIAL_SNAPSHOT_FAILED")
	}
	tx := currentIntegrityHold(t, ctx, s, "users", user)
	results := make(chan currentIntegrityOutcome, 2)
	reason := "Synthetic actual disable before reset"
	go func() {
		_, err := s.SetStatus(ctx, admin, user, "disabled", before.Version, reason, uuid.NewString())
		results <- currentIntegrityOutcome{0, err}
	}()
	currentIntegrityWaiters(t, ctx, s, "FROM users", 1)
	go func() {
		results <- currentIntegrityOutcome{1, s.Reset(ctx, token, fixture.Password(), "portal", uuid.NewString())}
	}()
	currentIntegrityWaiters(t, ctx, s, "FROM users", 2)
	if tx.Commit(ctx) != nil {
		t.Fatal("CURRENT_M01_DISABLE_RESET_BARRIER_RELEASE_FAILED")
	}
	for range 2 {
		r := currentIntegrityResult(t, ctx, results)
		if r.index == 0 {
			if r.err != nil {
				t.Fatal("CURRENT_M01_ACTUAL_DISABLE_FAILED")
			}
		} else {
			expect(t, r.err, "TOKEN_UNUSABLE")
		}
	}
	after, e := s.GetUser(ctx, user)
	if e != nil || after.Status != "disabled" || after.Epoch != before.Epoch+1 {
		t.Fatal("CURRENT_M01_DISABLE_POSTIMAGE_FAILED")
	}
	var actual string
	if s.DB.QueryRow(ctx, "SELECT password_phc FROM password_credentials WHERE user_id=$1", user).Scan(&actual) != nil || actual != credential {
		t.Fatal("CURRENT_M01_DISABLED_RESET_CHANGED_CREDENTIAL")
	}
	_, e = s.Authenticate(ctx, oldSession, "portal")
	expect(t, e, "AUTH_SESSION_EXPIRED")
	var liveSessions, liveResets, liveMail, audits int
	if s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL),(SELECT count(*) FROM password_reset_tokens WHERE user_id=$1 AND token_hash IS NOT NULL),(SELECT count(*) FROM mail_deliveries WHERE user_id=$1 AND purpose='password_reset' AND state IN ('queued','sending')),(SELECT count(*) FROM audit_events WHERE action='account.disabled' AND audience='admin' AND actor_user_id=$3 AND subject_user_id=$1 AND target_id=$1 AND reason=$2)`, user, reason, admin.Me.User.ID).Scan(&liveSessions, &liveResets, &liveMail, &audits) != nil || liveSessions+liveResets+liveMail != 0 || audits != 1 {
		t.Fatal("CURRENT_M01_DISABLE_AUTHORITY_OR_AUDIT_FAILED")
	}
	t.Log("CURRENT_M01_DISABLE_RESET_FINAL_BARRIER actual_disable_first=true two_waiters=true credential_unchanged=true old_authority_revoked=true")
}

func TestM01CurrentRealConstraintMatrix(t *testing.T) {
	s, _, admin := currentIntegrityActor(t)
	ctx, cancel := currentIntegrityContext(t)
	defer cancel()
	refuse := func(query, state, constraint string, args ...any) {
		t.Helper()
		_, e := s.DB.Exec(ctx, query, args...)
		var server *pgconn.PgError
		if !errors.As(e, &server) || server.Code != state || constraint != "" && server.ConstraintName != constraint {
			t.Fatal("CURRENT_M01_REAL_CONSTRAINT_REFUSAL_MISMATCH", state)
		}
	}
	refuse("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) SELECT $1,email_canonical,email_display,display_name,locale,status FROM users LIMIT 1", "23505", "users_email_canonical_key", uuid.NewString())
	invite, e := s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), "constraint-pending@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal("CURRENT_M01_CONSTRAINT_INVITATION_SETUP_FAILED")
	}
	refuse(`INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) SELECT $1,tenant_id,workspace_id,email_canonical,locale,status,$2,created_by,created_at,updated_at,expires_at FROM invitations WHERE id=$3`, "23505", "invitations_pending_email_idx", uuid.NewString(), security.Digest(security.Token()), invite.ID)
	otherTenant, otherWorkspace := uuid.NewString(), uuid.NewString()
	if _, e = s.DB.Exec(ctx, "INSERT INTO tenants(id,name,status) VALUES($1,'Other synthetic tenant','active')", otherTenant); e != nil {
		t.Fatal("CURRENT_M01_SECOND_TENANT_SETUP_FAILED")
	}
	if _, e = s.DB.Exec(ctx, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other synthetic Workspace','active')", otherWorkspace, otherTenant); e != nil {
		t.Fatal("CURRENT_M01_SECOND_WORKSPACE_SETUP_FAILED")
	}
	refuse("INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')", "23503", "", admin.Me.User.ID, admin.Me.Workspace.TenantID, otherWorkspace)
	refuse("INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')", "23503", "", uuid.NewString(), admin.Me.Workspace.TenantID, admin.Me.Workspace.ID)
	refuse("INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'invalid_role')", "23514", "", admin.Me.User.ID, otherTenant, otherWorkspace)
	if _, e = s.DB.Exec(ctx, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')", admin.Me.User.ID, otherTenant, otherWorkspace); e != nil {
		t.Fatal("CURRENT_M01_VALID_MEMBERSHIP_DID_NOT_COMMIT")
	}
	refuse("UPDATE users SET auth_epoch=0 WHERE id=$1", "23514", "", admin.Me.User.ID)
	refuse("UPDATE users SET status='invalid_state' WHERE id=$1", "23514", "", admin.Me.User.ID)
	refuse("UPDATE mail_deliveries SET state='sent',completed_at=clock_timestamp() WHERE invitation_id=$1", "23514", "", invite.ID)
	if s.RequestReset(ctx, "admin@example.invalid", "portal", "constraint-reset", uuid.NewString()) != nil {
		t.Fatal("CURRENT_M01_CONSTRAINT_RESET_SETUP_FAILED")
	}
	refuse("UPDATE password_reset_tokens SET consumed_at=clock_timestamp(),revoked_at=clock_timestamp(),token_hash=NULL", "23514", "")
	var columns int
	if s.DB.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND is_nullable='NO' AND (table_name,column_name) IN (('users','auth_epoch'),('users','email_canonical'),('workspace_memberships','tenant_id'),('workspace_memberships','workspace_id'),('password_credentials','user_id'),('invitations','status'))`).Scan(&columns) != nil || columns != 6 {
		t.Fatal("CURRENT_M01_REQUIRED_COLUMN_CONSTRAINTS_MISSING")
	}
	currentIntegrityCounts(t, s, `SELECT (SELECT count(*) FROM invitations WHERE status='pending'),(SELECT count(*) FROM users),(SELECT count(*) FROM workspace_memberships)`, 1, 1, 2)
	t.Log("CURRENT_M01_REAL_CONSTRAINT_MATRIX duplicate_email_pending=true intended_index=true composite_and_user_FK=true role_state_epoch_mail_reset_checks=true valid_commit=true required_columns=6")
}
