//go:build cgo && odbc_manager && dameng_account_integration

package identity_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

// This target-only test uses the actual admitted native owner, never a SQL mock.
// The schema must be provisioned by its migration owner and retained for evidence.
// It creates synthetic application rows, but does not own DDL/schema deletion.
func TestDamengAccountCoreNative(t *testing.T) {
	configuration := os.Getenv("PROJECT_ONE_DAMENG_ACCOUNT_CONFIGURATION")
	disposable := os.Getenv("PROJECT_ONE_DAMENG_ACCOUNT_DISPOSABLE_SCHEMA")
	if configuration == "" || !strings.HasPrefix(disposable, "P1_ACCOUNT_TEST_") {
		t.Fatal("explicit protected native configuration and disposable P1_ACCOUNT_TEST_ schema required")
	}
	c, e := dameng.LoadConnectionConfiguration(configuration, "")
	if e != nil || c.Schema != disposable {
		t.Fatal("native test schema/configuration admission refused")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	native, _, e := dameng.Open(ctx, c)
	if native != nil {
		t.Cleanup(func() {
			closeCtx, done := context.WithTimeout(context.Background(), time.Duration(c.CleanupSeconds)*time.Second)
			defer done()
			status, closeError := native.Close(closeCtx)
			if closeError != nil || status.State != "closed" || status.PhysicalOwners != 0 || status.CleanupJobs != 0 {
				t.Error("native owner cleanup incomplete; retain process/schema recovery evidence")
			}
		})
	}
	if e != nil {
		t.Fatal("native owner admission failed", dameng.SafeError(e))
	}
	store, e := transaction.FromDameng(native)
	if e != nil {
		t.Fatal(e)
	}
	must := func(label string, e error) {
		t.Helper()
		if e != nil {
			t.Fatal(label, e)
		}
	}
	count := func(table string) int {
		t.Helper()
		var n int
		must("count fixed owned table", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT COUNT(*) FROM "+table)).Scan(&n))
		return n
	}
	// Fixed identifiers only; schema/user comes from admitted native configuration.
	tables := []string{"TENANTS", "WORKSPACES", "USERS", "PASSWORD_CREDENTIALS", "WORKSPACE_MEMBERSHIPS", "SESSIONS", "INVITATIONS", "PASSWORD_RESET_TOKENS", "MAIL_DELIVERIES", "AUDIT_EVENTS", "RATE_LIMIT_BUCKETS", "IDEMPOTENCY_RECORDS", "WORKSPACE_NOTES"}
	for _, table := range tables {
		if count(table) != 0 {
			t.Fatal("nonempty schema refused", table)
		}
	}
	var complete bool
	if count("INSTALLATION_STATE") != 1 {
		t.Fatal("unbootstrapped singleton required")
	}
	must("installation state", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT BOOTSTRAP_COMPLETE FROM INSTALLATION_STATE WHERE ID=1")).Scan(&complete))
	if complete {
		t.Fatal("bootstrapped schema refused")
	}
	// Required shared extension, even for password sessions; missing columns fail.
	rows, e := store.Query(ctx, transaction.SQL(transaction.Dameng, "SELECT SSO_TENANT_ID,SSO_CONNECTOR_ID,SSO_BINDING_ID,SSO_AUTH_VERSION,SSO_FLOW_ID FROM SESSIONS WHERE 1=0"))
	must("shared session-source extension", e)
	rows.Close()

	root := t.TempDir()
	blocklist := filepath.Join(root, "synthetic-blocklist")
	must("synthetic blocklist", os.WriteFile(blocklist, []byte("passwordpasswordpassword\n"), 0600))
	passwords, e := security.NewPasswords(blocklist)
	must("password owner", e)
	key := make([]byte, 32)
	_, e = rand.Read(key)
	must("synthetic key", e)
	keyring := filepath.Join(root, "synthetic-mail-keyring.json")
	raw, e := json.Marshal(mail.Keyring{Active: "test", Keys: map[string]string{"test": base64.StdEncoding.EncodeToString(key)}})
	must("synthetic key encoding", e)
	must("synthetic keyring", os.WriteFile(keyring, raw, 0600))
	keys, e := mail.LoadKeyring(keyring)
	must("keyring owner", e)
	s := &identity.Service{Storage: store, Passwords: passwords, HMACKey: key}
	s.Config.Server.AdminOrigin = "https://admin.example.invalid"
	s.Config.Server.PortalOrigin = "https://portal.example.invalid"
	s.Config.SMTP.FromAddress = "noreply@example.invalid"
	s.Mail = &mail.Outbox{Storage: store, Keys: keys, Config: s.Config}
	password := "Synthetic native credential " + uuid.NewString()
	must("bootstrap", s.Bootstrap(ctx, "operator@example.invalid", "Operator \u4e16\u754c", password, "Native test tenant", "Default \u7a7a\u95f4"))
	login := func(email, password, audience string) (identity.Auth, string) {
		t.Helper()
		_, token, _, e := s.Login(ctx, email, password, audience, "", uuid.NewString())
		must("login", e)
		a, e := s.Authenticate(ctx, token, audience)
		must("authenticate", e)
		return a, token
	}
	operator, operatorToken := login("operator@example.invalid", password, "admin")
	if _, e := s.Authenticate(ctx, operatorToken, "portal"); e == nil {
		t.Fatal("audience isolation failed")
	}
	invitationKey := uuid.NewString()
	invite, e := s.Invite(ctx, operator, "", "createInvitation", invitationKey, "member@example.invalid", "en", 0, uuid.NewString())
	must("invite", e)
	replay, e := s.Invite(ctx, operator, "", "createInvitation", invitationKey, "member@example.invalid", "en", 0, uuid.NewString())
	must("idempotency CLOB readback", e)
	if replay.ID != invite.ID || invite.Delivery == nil || replay.Delivery == nil || replay.Delivery.ID != invite.Delivery.ID || count("MAIL_DELIVERIES") != 1 {
		t.Fatal("invitation replay changed durable outcome")
	}
	tokenFromDelivery := func(id, purpose string) string {
		t.Helper()
		var keyID string
		var nonce, ciphertext []byte
		must("encrypted outbox read", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT PAYLOAD_KEY_ID,PAYLOAD_NONCE,PAYLOAD_CIPHERTEXT FROM MAIL_DELIVERIES WHERE ID=? AND PURPOSE=? AND STATE='queued'", id, purpose)).Scan(&keyID, &nonce, &ciphertext))
		plaintext, e := keys.Decrypt(keyID, id, purpose, nonce, ciphertext)
		must("encrypted outbox decrypt", e)
		var payload mail.Payload
		must("payload decode", json.Unmarshal(plaintext, &payload))
		link, e := url.Parse(payload.Link)
		must("payload URL", e)
		if link.Scheme != "https" || link.Host != "portal.example.invalid" {
			t.Fatal("configured mail origin changed")
		}
		fragment, e := url.ParseQuery(link.Fragment)
		must("payload fragment", e)
		token := fragment.Get("token")
		if !security.ValidToken(token) {
			t.Fatal("invalid synthetic token")
		}
		return token
	}
	invitationToken := tokenFromDelivery(invite.Delivery.ID, "invitation")
	must("register", s.Register(ctx, invitationToken, "Member \u4e16\u754c", password, uuid.NewString()))
	if e := s.Register(ctx, invitationToken, "Duplicate", password, uuid.NewString()); e == nil {
		t.Fatal("registration token reused")
	}
	member, memberToken := login("member@example.invalid", password, "portal")
	ws := &workspaces.Service{Identity: s}
	workspace, e := ws.Create(ctx, operator, "Scoped \u4e16\u754c", operator.Me.User.ID, uuid.NewString())
	must("workspace create", e)
	membership, e := ws.AddMember(ctx, operator, workspace.ID, "member@example.invalid", "viewer", uuid.NewString())
	must("viewer add", e)
	if _, e := ws.CreateNote(ctx, member, workspace.ID, "Denied", "text", uuid.NewString()); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal("viewer write authority widened")
	}
	membership, e = ws.ChangeMember(ctx, operator, workspace.ID, member.Me.User.ID, "member", membership.Version, "Synthetic promotion", uuid.NewString(), false)
	must("member promote", e)
	note, e := ws.CreateNote(ctx, member, workspace.ID, "Unicode \u4e16\u754c", "Native \u6587\u672c🙂", uuid.NewString())
	must("CLOB note create", e)
	if note.Text != "Native \u6587\u672c🙂" {
		t.Fatal("CLOB text readback changed")
	}
	oldVersion := note.Version
	note, e = ws.ChangeNote(ctx, member, workspace.ID, note.ID, "Empty payload", "", note.Version, uuid.NewString(), false)
	must("empty CLOB update", e)
	if note.Text != "" || note.Version != oldVersion+1 {
		t.Fatal("empty CLOB/version readback changed")
	}
	if _, e := ws.ChangeNote(ctx, member, workspace.ID, note.ID, "Stale", "text", oldVersion, uuid.NewString(), false); e == nil || e.Error() != "VERSION_CONFLICT" {
		t.Fatal("stale version accepted")
	}
	if _, e := ws.CreateNote(ctx, member, workspace.ID, "Empty insert", "", uuid.NewString()); e != nil {
		t.Fatal("empty CLOB insert", e)
	}
	notes, e := ws.Notes(ctx, member, workspace.ID, url.Values{"limit": {"1"}})
	must("notes page", e)
	if len(notes.Items) != 1 || notes.NextCursor == nil {
		t.Fatal("native notes continuation missing")
	}
	next, e := ws.Notes(ctx, member, workspace.ID, url.Values{"limit": {"1"}, "cursor": {*notes.NextCursor}})
	must("notes cursor", e)
	if len(next.Items) != 1 || next.Items[0].ID == notes.Items[0].ID {
		t.Fatal("native seek duplicated or lost note")
	}
	_, e = ws.Members(ctx, operator, workspace.ID, url.Values{"limit": {"1"}})
	must("members list", e)
	_, e = ws.List(ctx, member, url.Values{"limit": {"1"}})
	must("workspace list", e)
	for _, kind := range []string{"users", "invitations", "mail-deliveries", "audit-events"} {
		page, e := s.ListForActor(ctx, operator, kind, url.Values{"limit": {"1"}})
		must("admin list "+kind, e)
		if len(page.Items) != 1 {
			t.Fatal("native list unexpectedly empty", kind)
		}
		if page.NextCursor != nil {
			_, e = s.ListForActor(ctx, operator, kind, url.Values{"limit": {"1"}, "cursor": {*page.NextCursor}})
			must("admin continuation "+kind, e)
		}
	}
	beforeNotes, beforeAudit := count("WORKSPACE_NOTES"), count("AUDIT_EVENTS")
	// Rollback is real; the failure is an explicit application callback fault.
	rollback := &damengAccountOutcomeStore{Store: store, rollback: true}
	copyIdentity := *s
	copyIdentity.Storage = rollback
	isolated := &workspaces.Service{Identity: &copyIdentity}
	rolled, e := isolated.CreateNote(ctx, member, workspace.ID, "Rollback", "Synthetic", uuid.NewString())
	if e == nil || rolled.ID != "" || rollback.calls != 1 || count("WORKSPACE_NOTES") != beforeNotes || count("AUDIT_EVENTS") != beforeAudit {
		t.Fatal("callback failure did not roll back note and audit")
	}
	// Commit actually succeeds; the acknowledgement fault is an identified double.
	unknown := &damengAccountOutcomeStore{Store: store}
	copyIdentity.Storage = unknown
	request := uuid.NewString()
	uncertain, e := isolated.CreateNote(ctx, member, workspace.ID, "Unknown receipt", "Synthetic", request)
	if e == nil || e.Error() != string(transaction.CommitUnknown) || uncertain.ID != "" || unknown.calls != 1 {
		t.Fatal("uncertain commit leaked success or replayed")
	}
	if count("WORKSPACE_NOTES") != beforeNotes+1 || count("AUDIT_EVENTS") != beforeAudit+1 {
		t.Fatal("independent committed postimage missing")
	}
	var retained int
	must("independent operation readback", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT COUNT(*) FROM AUDIT_EVENTS WHERE REQUEST_ID=? AND ACTION='workspace.note_created' AND WORKSPACE_ID=?", request, workspace.ID)).Scan(&retained))
	if retained != 1 {
		t.Fatal("uncertain operation audit is not unique")
	}
	// Each refused native statement ends its transaction. Readback uses a new
	// connection; no query follows a poisoned statement in the same callback.
	rejectDML := func(label string, expectedCode int32, query string, args ...any) {
		t.Helper()
		e := store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			_, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, query, args...))
			return e
		})
		if !damengAccountGuardRefusal(e, expectedCode) {
			t.Fatal("expected native trigger refusal was not established", label, expectedCode)
		}
	}
	var authority string
	must("membership authority", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT AUTHORITY_ID FROM WORKSPACE_MEMBERSHIPS WHERE USER_ID=? AND WORKSPACE_ID=?", member.Me.User.ID, workspace.ID)).Scan(&authority))
	rejectDML("membership", -20011, "UPDATE WORKSPACE_MEMBERSHIPS SET AUTHORITY_ID=? WHERE USER_ID=? AND WORKSPACE_ID=?", uuid.NewString(), member.Me.User.ID, workspace.ID)
	var retainedAuthority string
	must("authority after refusal", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT AUTHORITY_ID FROM WORKSPACE_MEMBERSHIPS WHERE USER_ID=? AND WORKSPACE_ID=?", member.Me.User.ID, workspace.ID)).Scan(&retainedAuthority))
	if retainedAuthority != authority {
		t.Fatal("membership authority changed")
	}
	var noteWorkspace, retainedNoteWorkspace string
	must("note scope before refusal", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT WORKSPACE_ID FROM WORKSPACE_NOTES WHERE ID=?", note.ID)).Scan(&noteWorkspace))
	rejectDML("note scope", -20012, "UPDATE WORKSPACE_NOTES SET WORKSPACE_ID=? WHERE ID=?", operator.Me.Workspace.ID, note.ID)
	must("note scope after refusal", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT WORKSPACE_ID FROM WORKSPACE_NOTES WHERE ID=?", note.ID)).Scan(&retainedNoteWorkspace))
	if retainedNoteWorkspace != noteWorkspace {
		t.Fatal("note scope changed")
	}
	var auditID string
	var auditReason, retainedAuditReason sql.NullString
	must("audit before refusal", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT ID,REASON FROM AUDIT_EVENTS WHERE REQUEST_ID=? AND ACTION='workspace.note_created' AND WORKSPACE_ID=?", request, workspace.ID)).Scan(&auditID, &auditReason))
	rejectDML("audit event", -20013, "UPDATE AUDIT_EVENTS SET REASON='synthetic tampering' WHERE ID=?", auditID)
	must("audit after refusal", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT REASON FROM AUDIT_EVENTS WHERE ID=?", auditID)).Scan(&retainedAuditReason))
	if retainedAuditReason != auditReason {
		t.Fatal("audit reason changed")
	}
	var recipient, retainedRecipient string
	must("mail identity before refusal", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT RECIPIENT_EMAIL FROM MAIL_DELIVERIES WHERE ID=?", invite.Delivery.ID)).Scan(&recipient))
	rejectDML("mail identity", -20014, "UPDATE MAIL_DELIVERIES SET RECIPIENT_EMAIL='changed@example.invalid' WHERE ID=?", invite.Delivery.ID)
	must("mail identity after refusal", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT RECIPIENT_EMAIL FROM MAIL_DELIVERIES WHERE ID=?", invite.Delivery.ID)).Scan(&retainedRecipient))
	if retainedRecipient != recipient {
		t.Fatal("mail recipient changed")
	}
	_, e = ws.ChangeMember(ctx, operator, workspace.ID, member.Me.User.ID, "", membership.Version, "Synthetic removal", uuid.NewString(), true)
	must("member removal", e)
	if _, e := ws.GetNote(ctx, member, workspace.ID, note.ID); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatal("removed member retained note access")
	}
	must("reset request", s.RequestReset(ctx, "member@example.invalid", "portal", "127.0.0.1", uuid.NewString()))
	var delivery string
	must("reset delivery", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT ID FROM MAIL_DELIVERIES WHERE USER_ID=? AND PURPOSE='password_reset' AND STATE='queued' ORDER BY CREATED_AT DESC,ID DESC LIMIT 1", member.Me.User.ID)).Scan(&delivery))
	resetToken := tokenFromDelivery(delivery, "password_reset")
	newPassword := "Rotated native credential " + uuid.NewString()
	must("password reset", s.Reset(ctx, resetToken, newPassword, "portal", uuid.NewString()))
	if _, e := s.Authenticate(ctx, memberToken, "portal"); e == nil {
		t.Fatal("reset retained old session")
	}
	if e := s.Reset(ctx, resetToken, newPassword, "portal", uuid.NewString()); e == nil {
		t.Fatal("reset token reused")
	}
	// Exercise actual native claim/eligibility/terminal audit without SMTP.
	// The reset has queued a password_changed notice. A missing test key must
	// fail before rendering/transport and atomically erase payload plus lease.
	var notice string
	must("password change notice", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT ID FROM MAIL_DELIVERIES WHERE USER_ID=? AND PURPOSE='password_changed' AND STATE='queued'", member.Me.User.ID)).Scan(&notice))
	worker := *s.Mail
	worker.Keys = &mail.Keyring{}
	processed, e := worker.DispatchOne(ctx)
	must("native mail claim/failure completion", e)
	if !processed {
		t.Fatal("queued native notice not claimed")
	}
	var terminal int
	must("terminal mail readback", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT COUNT(*) FROM MAIL_DELIVERIES WHERE ID=? AND STATE='failed' AND ERROR_CATEGORY='mail_key_missing' AND ATTEMPT_COUNT=1 AND PAYLOAD_KEY_ID IS NULL AND PAYLOAD_NONCE IS NULL AND PAYLOAD_CIPHERTEXT IS NULL AND LEASE_TOKEN IS NULL AND LOCKED_UNTIL IS NULL AND COMPLETED_AT IS NOT NULL", notice)).Scan(&terminal))
	if terminal != 1 {
		t.Fatal("terminal notice failed to clear private state")
	}
	must("terminal mail audit", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT COUNT(*) FROM AUDIT_EVENTS WHERE TARGET_ID=? AND ACTION='mail.failed'", notice)).Scan(&terminal))
	if terminal != 1 {
		t.Fatal("terminal mail audit absent")
	}
	member, newToken := login("member@example.invalid", newPassword, "portal")
	current, e := s.GetUser(ctx, member.Me.User.ID)
	must("current user", e)
	_, e = s.SetStatus(ctx, operator, current.ID, "disabled", current.Version, "Synthetic disable", uuid.NewString())
	must("disable account", e)
	if _, e := s.Authenticate(ctx, newToken, "portal"); e == nil {
		t.Fatal("disabled account retained session")
	}
	must("operator logout", s.Logout(ctx, operatorToken, "admin", uuid.NewString()))
	if _, e := s.Authenticate(ctx, operatorToken, "admin"); e == nil {
		t.Fatal("logout retained session")
	}
	t.Log("native synthetic account/workspace/outbox/audit chain passed; SMTP, HTTP, SSO and wire commit-loss are not exercised")
}

type damengAccountOutcomeStore struct {
	transaction.Store
	rollback bool
	calls    int
}

func (s *damengAccountOutcomeStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	s.calls++
	e := s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		if e := fn(ctx, q); e != nil {
			return e
		}
		if s.rollback {
			return errors.New("SYNTHETIC_CALLBACK_FAILURE")
		}
		return nil
	})
	if e != nil {
		return e
	}
	return errors.Join(context.DeadlineExceeded, &transaction.Error{Kind: transaction.CommitUnknown})
}
