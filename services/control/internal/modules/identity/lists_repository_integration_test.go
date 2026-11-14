//go:build repository_integration

package identity_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualIdentityListRepository(t *testing.T) {
	f := repositoryfixture.NewUnbootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dir := t.TempDir()
	path := filepath.Join(dir, "synthetic-blocklist")
	if e := os.WriteFile(path, []byte("passwordpasswordpassword\n"), 0600); e != nil {
		t.Fatal(e)
	}
	passwords, e := security.NewPasswords(path)
	if e != nil {
		t.Fatal(e)
	}
	key := make([]byte, security.MinimumHMACKeyBytes)
	if _, e = rand.Read(key); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(mail.Keyring{Active: "synthetic", Keys: map[string]string{"synthetic": base64.StdEncoding.EncodeToString(key)}})
	path = filepath.Join(dir, "synthetic-mail-keyring.json")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	keys, e := mail.LoadKeyring(path)
	if e != nil {
		t.Fatal(e)
	}
	s := &identity.Service{Storage: f.Store, Passwords: passwords, HMACKey: key}
	if f.PostgreSQL != nil {
		s.Storage, s.DB = nil, f.PostgreSQL
	}
	s.Config.Server.PortalOrigin = "https://portal.example.invalid"
	s.Config.Server.AdminOrigin = "https://admin.example.invalid"
	s.Config.SMTP.FromAddress = "platform@example.invalid"
	s.Mail = &mail.Outbox{Storage: f.Store, Keys: keys, Config: s.Config}
	password := "Synthetic list phrase " + uuid.NewString()
	email := "synthetic+" + uuid.NewString() + "@example.invalid"
	if e = s.Bootstrap(ctx, email, "Synthetic primary", password, "Synthetic installation", "Synthetic Workspace"); e != nil {
		t.Fatal(e)
	}
	var primary, workspace, phc string
	if e = f.Store.QueryRow(ctx, f.Statement("SELECT u.id::text,i.default_workspace_id::text,c.password_phc FROM users u JOIN password_credentials c ON c.user_id=u.id CROSS JOIN installation_state i WHERE u.email_canonical=$1", "SELECT u.id,i.default_workspace_id,c.password_phc FROM users u JOIN password_credentials c ON c.user_id=u.id CROSS JOIN installation_state i WHERE u.email_canonical=?", email)).Scan(&primary, &workspace, &phc); e != nil {
		t.Fatal(e)
	}
	exec := func(pg, my string, args ...any) {
		t.Helper()
		if _, e := f.Store.Exec(ctx, f.Statement(pg, my, args...)); e != nil {
			t.Fatal(e)
		}
	}
	want := func(e error, code string) {
		t.Helper()
		if e == nil || e.Error() != code {
			t.Fatalf("expected %s,got %v", code, e)
		}
	}
	events := func() int {
		t.Helper()
		var n int
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	get := func(id string) identity.User {
		t.Helper()
		u, e := s.GetUser(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	login := func(address, audience string) (identity.Auth, string) {
		t.Helper()
		_, token, _, e := s.Login(ctx, address, password, audience, "", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		a, e := s.Authenticate(ctx, token, audience)
		if e != nil {
			t.Fatal(e)
		}
		return a, token
	}
	account := func(admin bool) (string, string) {
		t.Helper()
		id, address := uuid.NewString(), "synthetic+"+uuid.NewString()+"@example.invalid"
		exec("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Synthetic actor','en','active',$4)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Synthetic actor','en','active',?)", id, address, address, admin)
		exec("INSERT INTO password_credentials(user_id,password_phc) VALUES($1,$2)", "INSERT INTO password_credentials(user_id,password_phc) VALUES(?,?)", id, phc)
		return id, address
	}
	admin, _ := login(email, "admin")
	var tenant string
	if e = f.Store.QueryRow(ctx, f.Statement("SELECT tenant_id::text FROM installation_state WHERE id=1", "SELECT tenant_id FROM installation_state WHERE id=1")).Scan(&tenant); e != nil {
		t.Fatal(e)
	}
	stamp := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	prefix := "listing-" + uuid.NewString()[:8] + "-"
	invitations := []string{}
	firstToken, firstUser := "", ""
	for n := 0; n < 101; n++ {
		id, invitation, delivery := uuid.NewString(), uuid.NewString(), uuid.NewString()
		address := prefix + fmt.Sprintf("%03d", n) + "@example.invalid"
		token := security.Token()
		if n == 0 {
			firstToken, firstUser = token, id
		}
		invitations = append(invitations, invitation)
		exec("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,created_at,updated_at) VALUES($1,$2,$3,'List \u4e16\u754c','en','active',$4,$5)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,created_at,updated_at) VALUES(?,?,?,'List \u4e16\u754c','en','active',?,?)", id, address, address, stamp, stamp)
		exec("INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES($1,$2,$3,$4,'en','pending',$5,$6,$7,$8,$9)", "INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES(?,?,?,?,'en','pending',?,?,?,?,?)", invitation, tenant, workspace, "invite-"+address, security.Digest(token), primary, stamp, stamp, stamp.Add(time.Hour))
		exec("INSERT INTO mail_deliveries(id,purpose,recipient_email,locale,user_id,state,next_attempt_at,message_id,created_at,updated_at,completed_at) VALUES($1,'password_changed',$2,'en',$3,'cancelled',$4,$5,$6,$7,$8)", "INSERT INTO mail_deliveries(id,purpose,recipient_email,locale,user_id,state,next_attempt_at,message_id,created_at,updated_at,completed_at) VALUES(?,'password_changed',?,'en',?,'cancelled',?,?,?,?,?)", delivery, address, primary, stamp, delivery+"@example.invalid", stamp, stamp, stamp)
		exec("INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id) VALUES($1,$2,'system','system.retention','success',$3)", "INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id) VALUES(?,?,'system','system.retention','success',?)", uuid.NewString(), stamp, uuid.NewString())
	}
	list := func(kind string, q url.Values) identity.List {
		t.Helper()
		out, e := s.ListForActor(ctx, admin, kind, q)
		if e != nil {
			t.Fatal(kind, e)
		}
		return out
	}
	t.Run("all_four_tied_timestamp_lists_have_bounded_stable_cursors", func(t *testing.T) {
		for _, kind := range []string{"users", "invitations", "mail-deliveries", "audit-events"} {
			q := url.Values{"limit": {"17"}}
			if kind == "users" {
				q.Set("email_prefix", prefix)
			}
			if kind == "mail-deliveries" {
				q.Set("purpose", "password_changed")
				q.Set("state", "cancelled")
			}
			if kind == "audit-events" {
				q.Set("action", "system.retention")
			}
			seen := map[string]bool{}
			pages := 0
			for {
				page := list(kind, q)
				pages++
				if kind == "users" && pages == 1 {
					address := prefix + "new-head@example.invalid"
					exec("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,$2,$3,'List \u4e16\u754c','en','active')", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES(?,?,?,'List \u4e16\u754c','en','active')", uuid.NewString(), address, address)
				}
				if len(page.Items) > 17 {
					t.Fatal("page bound exceeded")
				}
				for _, item := range page.Items {
					id := item["id"].(string)
					if seen[id] {
						t.Fatal("cursor repeated row")
					}
					seen[id] = true
					if _, e := time.Parse(time.RFC3339Nano, item[map[bool]string{true: "occurred_at", false: "created_at"}[kind == "audit-events"]].(string)); e != nil {
						t.Fatal("noncanonical public timestamp", e)
					}
				}
				if page.NextCursor == nil {
					break
				}
				q.Set("cursor", *page.NextCursor)
			}
			if len(seen) != 101 || pages != 6 {
				t.Fatal("tied seek omitted rows", kind, len(seen), pages)
			}
		}
	})
	t.Run("typed_public_projections_latest_delivery_and_effective_expiry", func(t *testing.T) {
		var latest string
		e := f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			input := mail.Input{Purpose: "invitation", Recipient: "invite-" + prefix + "000@example.invalid", Locale: "en", InvitationID: invitations[0], Generation: 1, Token: firstToken, Audience: "portal"}
			old, e := s.Mail.EnqueueWithin(ctx, tx, input)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, f.Statement("UPDATE mail_deliveries SET created_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE mail_deliveries SET created_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", old.ID)); e != nil {
				return e
			}
			input.Generation, input.Token = 2, security.Token()
			if _, e = tx.Exec(ctx, f.Statement("UPDATE invitations SET token_generation=2,version=version+1,token_hash=$1 WHERE id=$2", "UPDATE invitations SET token_generation=2,version=version+1,token_hash=? WHERE id=?", security.Digest(input.Token), invitations[0])); e != nil {
				return e
			}
			next, e := s.Mail.EnqueueWithin(ctx, tx, input)
			latest = next.ID
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		exec("UPDATE users SET version=$1 WHERE id=$2", "UPDATE users SET version=? WHERE id=?", identity.MaximumAuthVersion, firstUser)
		exact := list("users", url.Values{"email_prefix": {prefix + "000@"}})
		if len(exact.Items) != 1 || exact.Items[0]["version"] != identity.MaximumAuthVersion {
			t.Fatal("JSON-safe version precision changed")
		}
		found := false
		q := url.Values{"limit": {"100"}}
		for {
			out := list("invitations", q)
			for _, item := range out.Items {
				if item["id"] == invitations[0] {
					delivery := item["delivery"].(map[string]any)
					if delivery["id"] != latest || delivery["state"] != "queued" || delivery["error_category"] != nil {
						t.Fatal("newest real delivery summary mismatch")
					}
					found = true
				}
			}
			if out.NextCursor == nil {
				break
			}
			q.Set("cursor", *out.NextCursor)
		}
		if !found {
			t.Fatal("nonempty invitation projection missing")
		}
		found = false
		for _, item := range list("users", url.Values{"q": {strings.ToUpper(prefix) + "000@"}}).Items {
			found = found || item["id"] == firstUser
		}
		if !found {
			t.Fatal("a search by address ignores case")
		}
		if byName := list("users", url.Values{"q": {"LIST \u4e16"}, "limit": {"100"}}); len(byName.Items) == 0 || byName.Items[0]["display_name"] != "List \u4e16\u754c" {
			t.Fatal("a search reads display names")
		}
		if none := list("users", url.Values{"q": {"%"}}); len(none.Items) != 0 {
			t.Fatal("a wildcard in a search is plain text")
		}
		out := list("users", url.Values{"email_prefix": {prefix}, "limit": {"1"}})
		if out.Items[0]["display_name"] != "List \u4e16\u754c" || out.Items[0]["is_platform_admin"] != false || out.Items[0]["email_verified_at"] != nil {
			t.Fatal("typed user projection changed")
		}
		for _, kind := range []string{"users", "invitations", "mail-deliveries", "audit-events"} {
			raw, _ := json.Marshal(list(kind, url.Values{}))
			for _, field := range []string{"password_phc", "auth_epoch", "token_hash", "identifier_digest", "payload_ciphertext", "payload_nonce", "payload_key_id", "lease_token"} {
				if strings.Contains(string(raw), `"`+field+`"`) {
					t.Fatal("sensitive field exposed", kind, field)
				}
			}
		}
		expired := uuid.NewString()
		exec("INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES($1,$2,$3,$4,'en','pending',$5,$6,$7,$8,$9)", "INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES(?,?,?,?,'en','pending',?,?,?,?,?)", expired, tenant, workspace, "expired-"+uuid.NewString()+"@example.invalid", security.Digest(security.Token()), primary, stamp.Add(-time.Hour), stamp, stamp.Add(-time.Second))
		out = list("invitations", url.Values{"status": {"expired"}})
		if len(out.Items) != 1 || out.Items[0]["id"] != expired || out.Items[0]["status"] != "expired" {
			t.Fatal("effective expiry projection/filter")
		}
	})
	t.Run("literal_prefix_filters_cursor_binding_and_nondefault_UI_policy", func(t *testing.T) {
		base := "literal%_!-" + uuid.NewString()[:8]
		for _, address := range []string{base + "@example.invalid", strings.ReplaceAll(base, "%_", "xyzq") + "@example.invalid"} {
			exec("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,$2,$3,'Literal prefix','en','active')", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES(?,?,?,'Literal prefix','en','active')", uuid.NewString(), address, address)
		}
		if page := list("users", url.Values{"email_prefix": {base}}); len(page.Items) != 1 || page.Items[0]["email"] != base+"@example.invalid" {
			t.Fatal("wildcard prefix was not literal")
		}
		if page := list("users", url.Values{"email_prefix": {"' OR 1=1 --"}}); len(page.Items) != 0 {
			t.Fatal("bound prefix escaped its predicate")
		}
		first := list("users", url.Values{"email_prefix": {prefix}, "limit": {"1"}})
		_, e := s.List(ctx, "users", url.Values{"status": {"active"}, "cursor": {*first.NextCursor}})
		want(e, "INVALID_CURSOR")
		_, e = s.List(ctx, "invitations", url.Values{"cursor": {*first.NextCursor}})
		want(e, "INVALID_CURSOR")
		policy := config.DefaultRuntimePolicy()
		policy.UI.PageSize = 7
		if e = policy.Validate(); e != nil {
			t.Fatal(e)
		}
		s.Config.Runtime = &policy
		if page := list("users", url.Values{"email_prefix": {prefix}}); len(page.Items) != 7 || page.NextCursor == nil {
			t.Fatal("shared nondefault omitted-limit policy ignored")
		}
	})
	t.Run("audit_window_is_pinned_across_default_window_boundary", func(t *testing.T) {
		boundary := time.Now().UTC().Add(-time.Duration(s.Config.Policy().Identity.AuditDefaultDays)*24*time.Hour + 400*time.Millisecond).Truncate(time.Microsecond)
		old := uuid.NewString()
		request := uuid.NewString()
		for _, v := range []struct {
			id   string
			time time.Time
		}{{old, boundary}, {uuid.NewString(), time.Now().UTC()}} {
			exec("INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id) VALUES($1,$2,'admin','auth.logout','success',$3)", "INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id) VALUES(?,?,'admin','auth.logout','success',?)", v.id, v.time, request)
		}
		first := list("audit-events", url.Values{"action": {"auth.logout"}, "limit": {"1"}})
		if none := list("audit-events", url.Values{"target_id": {uuid.NewString()}}); len(none.Items) != 0 {
			t.Fatal("a resource with no events")
		}
		if first.NextCursor == nil {
			t.Fatal("audit window fixture missing cursor")
		}
		time.Sleep(650 * time.Millisecond)
		second := list("audit-events", url.Values{"action": {"auth.logout"}, "limit": {"1"}, "cursor": {*first.NextCursor}})
		if len(second.Items) != 1 || second.Items[0]["id"] != old {
			t.Fatal("default window moved between pages")
		}
		if page := list("audit-events", url.Values{"request_id": {request}, "action": {"auth.logout"}, "outcome": {"success"}}); len(page.Items) != 1 {
			t.Fatal("current bounded audit filter ignored actual window")
		}
	})
	t.Run("current_admin_role_cancellation_and_no_read_audit", func(t *testing.T) {
		portal, _ := login(email, "portal")
		_, e := s.ListForActor(ctx, portal, "users", nil)
		want(e, "ACCESS_DENIED")
		actor, address := account(true)
		stale, _ := login(address, "admin")
		exec("UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", actor)
		if get(actor).IsPlatformAdmin {
			t.Fatal("role fixture did not persist")
		}
		_, e = s.ListForActor(ctx, stale, "users", nil)
		want(e, "AUTH_SESSION_EXPIRED")
		before := events()
		list("users", url.Values{})
		if events() != before {
			t.Fatal("read invented an audit mutation")
		}
		waiting, stop := context.WithCancel(ctx)
		defer stop()
		result := make(chan error, 1)
		e = f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			var id string
			if e := tx.QueryRow(ctx, f.Statement("SELECT id::text FROM users WHERE id=$1 FOR UPDATE", "SELECT id FROM users WHERE id=? FOR UPDATE", primary)).Scan(&id); e != nil {
				return e
			}
			go func() {
				out, e := s.ListForActor(waiting, admin, "users", nil)
				if e != nil && (len(out.Items) != 0 || out.NextCursor != nil) {
					result <- fmt.Errorf("partial failed page")
					return
				}
				result <- e
			}()
			select {
			case e := <-result:
				t.Fatal("list escaped actor fence", e)
			case <-time.After(150 * time.Millisecond):
			}
			stop()
			select {
			case e := <-result:
				want(e, "STORAGE_UNAVAILABLE")
			case <-time.After(time.Second):
				t.Fatal("cancelled list did not finish")
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		exec("UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_list_revoke' WHERE id=$1", "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_list_revoke' WHERE id=?", admin.SessionID)
		_, e = s.ListForActor(ctx, admin, "users", nil)
		want(e, "AUTH_SESSION_EXPIRED")
	})
}
