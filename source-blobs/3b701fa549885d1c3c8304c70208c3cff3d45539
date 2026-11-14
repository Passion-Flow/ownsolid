//go:build repository_integration

package sso

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func bindingRepositoryFixture(t *testing.T) (sourceRepositoryFixture, string) {
	t.Helper()
	f := catalogRepositoryFixture(t)
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("private password fixture")
	}
	path := filepath.Join(dir, "blocklist.txt")
	if os.WriteFile(path, []byte("synthetic-blocked-password\n"), 0600) != nil {
		t.Fatal("private bounded blocklist fixture")
	}
	passwords, e := security.NewPasswords(path)
	if e != nil {
		t.Fatal(e)
	}
	f.accounts.Passwords = passwords
	password := "Synthetic paired binding phrase " + uuid.NewString()
	phc, e := passwords.Hash(f.ctx, password)
	if e != nil {
		t.Fatal("actual bounded local password hash", e)
	}
	f.exec(t, f.f.Statement(`INSERT INTO password_credentials(user_id,password_phc) VALUES($1,$2)`, `INSERT INTO password_credentials(user_id,password_phc) VALUES(?,?)`, f.f.User, phc))
	return f, password
}

func bindingSeed(t *testing.T, f sourceRepositoryFixture, tenant, connector, user, subject string, active bool, stamp time.Time) string {
	t.Helper()
	id := uuid.NewString()
	f.exec(t, f.f.Statement(`INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id,active,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, tenant, connector, subject, user, active, stamp, stamp))
	return id
}

func bindingCounts(t *testing.T, f sourceRepositoryFixture, id, key string) [3]int {
	t.Helper()
	var result [3]int
	for i, statement := range []transaction.Statement{
		f.f.Statement(`SELECT COUNT(*) FROM sso_bindings WHERE tenant_id=$1 AND user_id=$2 AND id=$3`, `SELECT COUNT(*) FROM sso_bindings WHERE tenant_id=? AND user_id=? AND id=?`, f.f.Tenant, f.f.User, id),
		f.f.Statement(`SELECT COUNT(*) FROM sso_mutation_intents WHERE tenant_id=$1 AND actor_user_id=$2 AND idempotency_key=$3`, `SELECT COUNT(*) FROM sso_mutation_intents WHERE tenant_id=? AND actor_user_id=? AND idempotency_key=?`, f.f.Tenant, f.f.User, key),
		f.f.Statement(`SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND actor_user_id=$2 AND target_id=$3 AND action='sso.binding_unlinked' AND outcome='success'`, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=? AND actor_user_id=? AND target_id=? AND action='sso.binding_unlinked' AND outcome='success'`, f.f.Tenant, f.f.User, id),
	} {
		if e := f.f.Store.QueryRow(f.ctx, statement).Scan(&result[i]); e != nil {
			t.Fatal("actual binding/intent/audit inventory", e)
		}
	}
	return result
}

func sameBinding(left, right Binding) bool {
	l, _ := json.Marshal(left)
	r, _ := json.Marshal(right)
	return string(l) == string(r)
}

// Explicit privileged synthetic link states exercise persistence/revocation.
// They are not provider responses or evidence of an OAuth code exchange.
func bindingLinkFlow(t *testing.T, f sourceRepositoryFixture, a identity.Auth, state string) string {
	t.Helper()
	id := uuid.NewString()
	sealed, e := f.s.keys.seal(secretScope{f.f.Tenant, f.source.ConnectorID, id, flowVerifierPurpose}, []byte(security.Token()))
	if e != nil {
		t.Fatal(e)
	}
	var now time.Time
	if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT clock_timestamp()`, `SELECT UTC_TIMESTAMP(6)`)).Scan(&now); e != nil {
		t.Fatal(e)
	}
	f.exec(t, f.f.Statement(`INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,previous_session_id,initiating_user_id,initiating_session_id,initiating_epoch,credential_version,reason,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES($1,$2,$3,1,1,$4,'link',$5,$6,$7,$8,$9,$10,$11,1,'Synthetic pending account link',$12,$13,$14,$15,$16)`, `INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,previous_session_id,initiating_user_id,initiating_session_id,initiating_epoch,credential_version,reason,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES(?,?,?,1,1,?,'link',?,?,?,?,?,?,?,1,'Synthetic pending account link',?,?,?,?,?)`, id, f.f.Tenant, f.source.ConnectorID, a.Me.Audience, security.Digest(security.Token()), security.Digest(security.Token()), security.Digest("anonymous"), a.SessionID, a.Me.User.ID, a.SessionID, a.Epoch, sealed.keyID, sealed.nonce, sealed.ciphertext, now, now.Add(time.Duration(f.s.policy.FlowSeconds)*time.Second)))
	if state == "claimed" {
		f.exec(t, f.f.Statement(`UPDATE sso_flows SET state='claimed',claim_id=$1,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$2`, `UPDATE sso_flows SET state='claimed',claim_id=?,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=?`, uuid.NewString(), id))
	}
	return id
}

func bindingOtherUser(t *testing.T, f sourceRepositoryFixture) string {
	t.Helper()
	id := uuid.NewString()
	email := "synthetic+" + id + "@example.invalid"
	f.exec(t, f.f.Statement(`INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,$2,$3,'Foreign binding user','en','active')`, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES(?,?,?,'Foreign binding user','en','active')`, id, email, email))
	return id
}

func TestActualSSOBindingInventoryScopeRetentionAndBounds(t *testing.T) {
	f := catalogRepositoryFixture(t)
	admin := catalogAuth(t, f, f.f.User, "admin", nil)
	portal := catalogAuth(t, f, f.f.User, "portal", nil)
	sourced := catalogAuth(t, f, f.f.User, "admin", &f.source)
	stamp := time.Date(2024, 1, 2, 3, 4, 5, 123456000, time.UTC)
	ids := []string{bindingSeed(t, f, f.f.Tenant, f.source.ConnectorID, f.f.User, "9007199254740997", true, stamp), bindingSeed(t, f, f.f.Tenant, f.source.ConnectorID, f.f.User, "9223372036854775807", false, stamp)}
	sort.Strings(ids)
	ids = append(ids, f.source.BindingID)
	other := bindingOtherUser(t, f)
	bindingSeed(t, f, f.f.Tenant, f.source.ConnectorID, other, "9007199254740999", true, stamp)
	foreign := uuid.NewString()
	f.exec(t, f.f.Statement(`INSERT INTO tenants(id,name,status) VALUES($1,'Foreign binding tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Foreign binding tenant','active')`, foreign))
	connector := catalogSeedConnector(t, f, foreign, f.accounts.Config.SSO.Providers[0].Origin, "Foreign binding connector", true, stamp)
	bindingSeed(t, f, foreign, connector, f.f.User, "9007199254741001", true, stamp)
	policy := f.accounts.Config.Policy()
	policy.SSO.MaximumBindingsPerUser = 1
	f.accounts.Config.Runtime = &policy
	service, e := New(f.accounts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(service.Close)
	f.s, f.accounts.Sources = service, service
	before := catalogReadCounts(t, f)
	for _, a := range []identity.Auth{admin, portal, sourced} {
		items, e := f.s.ListBindings(f.ctx, a)
		if e != nil || len(items) != len(ids) {
			t.Fatal("actual account inventory must retain rows beyond lowered creation limit", e)
		}
		for i, item := range items {
			if item.ID != ids[i] || item.ConnectorID != f.source.ConnectorID || !validBinding(item) {
				t.Fatal("binding tenant/user/order/DTO scope mismatch")
			}
		}
	}
	if catalogReadCounts(t, f) != before {
		t.Fatal("successful binding inventory mutated storage")
	}
	f.accounts.Config.SSO.Enabled, f.accounts.Config.SSO.KeyringFile = false, "/missing-synthetic-disabled-keyring"
	recovery, e := New(f.accounts)
	if e != nil {
		t.Fatal("keyless disabled local recovery", e)
	}
	t.Cleanup(recovery.Close)
	f.accounts.Sources = recovery
	if items, e := recovery.ListBindings(f.ctx, portal); e != nil || len(items) != len(ids) {
		t.Fatal("disabled inventory should retain local account history without provider keys", e)
	}
}

func TestActualSSOBindingInventoryOverflowFailsEmpty(t *testing.T) {
	f := catalogRepositoryFixture(t)
	a := catalogAuth(t, f, f.f.User, "portal", nil)
	e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		for i := range maximumBindingInventory {
			_, e := q.Exec(ctx, f.f.Statement(`INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES($1,$2,$3,$4,$5)`, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES(?,?,?,?,?)`, uuid.NewString(), f.f.Tenant, f.source.ConnectorID, strconv.Itoa(i+1), f.f.User))
			if e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal("privileged protocol-overflow fixture", e)
	}
	if items, e := f.s.ListBindings(f.ctx, a); e == nil || e.Error() != "STORAGE_UNAVAILABLE" || items == nil || len(items) != 0 {
		t.Fatal("oversized inventory must not expose partial binding results", e)
	}
}

func TestActualSSOBindingUnlinkAtomicIntentAndRevocation(t *testing.T) {
	f, password := bindingRepositoryFixture(t)
	local := catalogAuth(t, f, f.f.User, "admin", nil)
	ordinaryPortal := catalogAuth(t, f, f.f.User, "portal", nil)
	sourced := catalogAuth(t, f, f.f.User, "admin", &f.source)
	portalSource := connectorFlow(t, f, f.source.ConnectorID, "portal", "succeeded")
	portalSource.BindingID = f.source.BindingID
	portal := catalogAuth(t, f, f.f.User, "portal", &portalSource)
	stamp := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	otherConnector := catalogSeedConnector(t, f, f.f.Tenant, f.accounts.Config.SSO.Providers[0].Origin, "Retained other binding connector", true, stamp)
	otherSource := connectorFlow(t, f, otherConnector, "portal", "succeeded")
	otherSource.BindingID = connectorBinding(t, f, otherConnector)
	other := catalogAuth(t, f, f.f.User, "portal", &otherSource)
	pending, claimed := bindingLinkFlow(t, f, local, "pending"), bindingLinkFlow(t, f, ordinaryPortal, "claimed")
	foreignUser := bindingOtherUser(t, f)
	foreignActor := catalogAuth(t, f, foreignUser, "portal", nil)
	foreignPending := bindingLinkFlow(t, f, foreignActor, "pending")
	input, key := UnlinkBinding{Revision: 1, Password: password, Reason: "Explicit synthetic account unlink"}, uuid.NewString()
	bad := input
	bad.Password = security.Token()
	if out, e := f.s.Unlink(f.ctx, local, f.source.BindingID, bad, key, uuid.NewString()); e == nil || e.Error() != "SSO_PASSWORD_REQUIRED" || out != (Binding{}) || bindingCounts(t, f, f.source.BindingID, key) != ([3]int{1, 0, 0}) {
		t.Fatal("wrong current password changed binding/intent/audit", e)
	}
	result, e := f.s.Unlink(f.ctx, local, f.source.BindingID, input, key, uuid.NewString())
	if e != nil || result.Active || result.Revision != 2 || !validBinding(result) || bindingCounts(t, f, result.ID, key) != ([3]int{1, 1, 1}) {
		t.Fatal("actual atomic confirmed unlink", e)
	}
	assertConnectorAuth(t, f, sourced, false)
	assertConnectorAuth(t, f, portal, false)
	for _, actor := range []identity.Auth{local, ordinaryPortal, other} {
		assertConnectorAuth(t, f, actor, true)
	}
	for _, id := range []string{pending, claimed, foreignPending} {
		var state string
		var erased bool
		if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT state,verifier_ciphertext IS NULL FROM sso_flows WHERE id=$1`, `SELECT state,verifier_ciphertext IS NULL FROM sso_flows WHERE id=?`, id)).Scan(&state, &erased); e != nil || id == foreignPending && (state != "pending" || erased) || id != foreignPending && (state != "failed" || !erased) {
			t.Fatal("own pending/claimed versus foreign flow invalidation scope", e)
		}
	}
	replay, e := f.s.Unlink(f.ctx, local, result.ID, input, key, uuid.NewString())
	if e != nil || !sameBinding(result, replay) || bindingCounts(t, f, result.ID, key) != ([3]int{1, 1, 1}) {
		t.Fatal("explicit accepted unlink replay duplicated state", e)
	}
	changed := input
	changed.Reason = "Different explicit unlink intent"
	if out, e := f.s.Unlink(f.ctx, local, result.ID, changed, key, uuid.NewString()); e == nil || e.Error() != "IDEMPOTENCY_CONFLICT" || out != (Binding{}) {
		t.Fatal("changed accepted intent bypassed fingerprint", e)
	}
	if out, e := f.s.Unlink(f.ctx, sourced, result.ID, input, key, uuid.NewString()); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || out != (Binding{}) {
		t.Fatal("revoked sourced authority replayed an accepted unlink", e)
	}
	if out, e := f.s.Unlink(f.ctx, local, result.ID, input, uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "SSO_REVISION_CONFLICT" || out != (Binding{}) {
		t.Fatal("inactive/stale binding accepted a second unlink", e)
	}
	f.accounts.Config.SSO.Enabled, f.accounts.Config.SSO.KeyringFile = false, "/missing-synthetic-disabled-keyring"
	recovery, e := New(f.accounts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(recovery.Close)
	f.accounts.Sources = recovery
	if replay, e := recovery.Unlink(f.ctx, local, result.ID, input, key, uuid.NewString()); e != nil || !sameBinding(result, replay) {
		t.Fatal("keyless disabled local accepted-intent recovery", e)
	}
}

func TestActualSSOBindingSourcedPortalUnlinkLocalRecovery(t *testing.T) {
	f, password := bindingRepositoryFixture(t)
	local := catalogAuth(t, f, f.f.User, "portal", nil)
	source := connectorFlow(t, f, f.source.ConnectorID, "portal", "succeeded")
	source.BindingID = f.source.BindingID
	a := catalogAuth(t, f, f.f.User, "portal", &source)
	input, key := UnlinkBinding{Revision: 1, Password: password, Reason: "Explicit sourced Portal unlink"}, uuid.NewString()
	result, e := f.s.Unlink(f.ctx, a, f.source.BindingID, input, key, uuid.NewString())
	if e != nil || result.Active || result.Revision != 2 {
		t.Fatal("actual sourced Portal unlink did not commit", e)
	}
	assertConnectorAuth(t, f, a, false)
	assertConnectorAuth(t, f, local, true)
	if out, e := f.s.Unlink(f.ctx, a, result.ID, input, key, uuid.NewString()); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || out != (Binding{}) {
		t.Fatal("self-revoked Portal session bypassed current authority", e)
	}
	replayed, e := f.s.Unlink(f.ctx, local, result.ID, input, key, uuid.NewString())
	if e != nil || !sameBinding(result, replayed) || bindingCounts(t, f, result.ID, key) != ([3]int{1, 1, 1}) {
		t.Fatal("ordinary local recovery must replay one accepted unlink", e)
	}
}
