package distribution_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"strings"
	"testing"
)

func (c scenario) user(t *testing.T, email string) identity.Auth {
	t.Helper()
	ctx := context.Background()
	invitation, e := c.s.Identity.Invite(ctx, c.a, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	password := fixture.Password()
	if e = c.s.Identity.Register(ctx, fixture.InvitationToken(t, c.s.Identity, invitation.ID), "Synthetic API user", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	return fixture.Login(t, c.s.Identity, email, password, "portal")
}
func (c scenario) published(t *testing.T) distribution.Alias {
	t.Helper()
	d := c.readyDeployment(t)
	g, e := c.s.Provision(context.Background(), c.a, c.input(), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	alias, e := c.s.Publish(context.Background(), c.a, distribution.PublishRequest{WorkspaceID: c.a.Me.Workspace.ID, GatewayID: g.Gateway.ID, Name: "public-alias", DeploymentID: d.ID, DeploymentRevision: d.Revision, Reason: "Synthetic publication"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return alias
}
func keyInput(alias distribution.Alias) distribution.KeyRequest {
	return distribution.KeyRequest{WorkspaceID: alias.WorkspaceID, AliasID: alias.ID, Label: "Synthetic key \u4e2d\u6587", TTLSeconds: 3600, Reason: "Synthetic API access"}
}

func TestAPIKeySecretOnceCreatorIsolationAndPersistentRevocation(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	alias := c.published(t)
	member := c.user(t, "member@example.invalid")
	other := c.user(t, "other@example.invalid")
	key := uuid.NewString()
	input := keyInput(alias)
	minted, e := c.s.MintKey(ctx, member, input, key, uuid.NewString())
	if e != nil || !minted.SecretAvailable || minted.Secret == nil || !minted.Created {
		t.Fatal("one-time API key", e)
	}
	if !strings.HasPrefix(*minted.Secret, minted.Key.Prefix+".") {
		t.Fatal("opaque prefix binding")
	}
	again, e := c.s.MintKey(ctx, member, input, key, uuid.NewString())
	if e != nil || again.SecretAvailable || again.Secret != nil || again.Created || again.Key.ID != minted.Key.ID {
		t.Fatal("replay disclosed or replaced API key", e)
	}
	changed := input
	changed.TTLSeconds++
	_, e = c.s.MintKey(ctx, member, changed, key, uuid.NewString())
	check(t, e, "IDEMPOTENCY_CONFLICT")
	var raw string
	if e = c.s.DB.QueryRow(ctx, "SELECT row_to_json(k)::text FROM api_keys k WHERE id=$1", minted.Key.ID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	secret := strings.Split(*minted.Secret, ".")[2]
	if strings.Contains(raw, secret) {
		t.Fatal("raw secret persisted")
	}
	public, _ := json.Marshal(minted.Key)
	if strings.Contains(string(public), secret) || strings.Contains(string(public), "secret_hash") {
		t.Fatal("metadata disclosed secret")
	}
	own, e := c.s.Keys(ctx, member, alias.WorkspaceID)
	if e != nil || len(own) != 1 || own[0].State != "active" {
		t.Fatal("own key inventory", e)
	}
	others, e := c.s.Keys(ctx, other, alias.WorkspaceID)
	if e != nil || len(others) != 0 {
		t.Fatal("creator isolation", e)
	}
	_, e = c.s.RevokeKey(ctx, other, alias.WorkspaceID, minted.Key.ID, "Synthetic unauthorized revoke", uuid.NewString())
	check(t, e, "RESOURCE_NOT_FOUND")
	revoked, e := c.s.RevokeKey(ctx, member, alias.WorkspaceID, minted.Key.ID, "Synthetic owner revoke", uuid.NewString())
	if e != nil || revoked.State != "revoked" {
		t.Fatal("persistent owner revoke", e)
	}
	_, e = c.s.RevokeKey(ctx, c.a, alias.WorkspaceID, minted.Key.ID, "Synthetic administrative retry", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var audits int
	if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action IN ('gateway.api_key_created','gateway.api_key_revoked')", minted.Key.ID).Scan(&audits); e != nil || audits != 2 {
		t.Fatal("audited idempotent lifecycle", e)
	}
}

func TestAPIKeyConfiguredTTLQuotaExpiryAndAliasDisable(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	policy := config.DefaultRuntimePolicy()
	policy.Distribution.KeyTTLSeconds = 600
	policy.Distribution.KeyMaxTTLSeconds = 3600
	policy.Distribution.KeyMaxActive = 1
	c.s.Identity.Config.Runtime = &policy
	alias := c.published(t)
	input := keyInput(alias)
	input.TTLSeconds = 0
	minted, e := c.s.MintKey(ctx, c.p, input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	duration := minted.Key.ExpiresAt.Sub(minted.Key.CreatedAt).Seconds()
	if duration < 599 || duration > 602 {
		t.Fatal("configured TTL not applied", duration)
	}
	_, e = c.s.MintKey(ctx, c.p, input, uuid.NewString(), uuid.NewString())
	check(t, e, "RESOURCE_LIMIT_REACHED")
	if _, e = c.s.DB.Exec(ctx, "UPDATE api_keys SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", minted.Key.ID); e != nil {
		t.Fatal(e)
	}
	keys, e := c.s.Keys(ctx, c.p, alias.WorkspaceID)
	if e != nil || len(keys) != 1 || keys[0].State != "expired" {
		t.Fatal("persisted historical expiry", e)
	}
	input.TTLSeconds = 3601
	_, e = c.s.MintKey(ctx, c.p, input, uuid.NewString(), uuid.NewString())
	check(t, e, "VALIDATION_FAILED")
	if _, e = c.s.DisableAlias(ctx, c.a, alias.WorkspaceID, alias.ID, alias.Revision, "Synthetic disable", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	input.TTLSeconds = 0
	_, e = c.s.MintKey(ctx, c.p, input, uuid.NewString(), uuid.NewString())
	check(t, e, "MODEL_ALIAS_UNAVAILABLE")
}

func TestAPIKeyAuditRollbackLeavesNoAuthorityOrOneTimeSecret(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	alias := c.published(t)
	if _, e := c.s.DB.Exec(ctx, `CREATE FUNCTION synthetic_key_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='gateway.api_key_created' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER synthetic_key_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION synthetic_key_audit_failure();`); e != nil {
		t.Fatal(e)
	}
	out, e := c.s.MintKey(ctx, c.p, keyInput(alias), uuid.NewString(), uuid.NewString())
	if e == nil || out.Secret != nil || out.Key.ID != "" {
		t.Fatal("rolled back key or secret returned")
	}
	var count int
	if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM api_keys").Scan(&count); e != nil || count != 0 {
		t.Fatal("audit failure left credential", e)
	}
}
