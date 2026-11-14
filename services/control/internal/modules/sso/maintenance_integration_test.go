package sso

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
)

func oldFlow(t *testing.T, f sessionFixture, terminal bool) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewString()
	sealed, e := f.s.keys.seal(secretScope{f.source.TenantID, f.source.ConnectorID, id, flowVerifierPurpose}, []byte(security.Token()))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Identity.DB.Exec(ctx, `INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES($1,$2,$3,1,1,'admin','login',$4,$5,$6,$7,$8,$9,clock_timestamp()-interval '10 days',clock_timestamp()-interval '9 days')`, id, f.source.TenantID, f.source.ConnectorID, security.Digest(security.Token()), security.Digest(security.Token()), security.Digest("anonymous"), sealed.keyID, sealed.nonce, sealed.ciphertext); e != nil {
		t.Fatal(e)
	}
	if terminal {
		if _, e = f.s.Identity.DB.Exec(ctx, `UPDATE sso_flows SET state='expired',error_code='SSO_FLOW_EXPIRED',verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL,finished_at=clock_timestamp()-interval '8 days' WHERE id=$1`, id); e != nil {
			t.Fatal(e)
		}
	}
	return id
}

func TestM11ActualBoundedExpirySecretClearingRetentionAndAuditRollback(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	expired := []string{oldFlow(t, f, false), oldFlow(t, f, false)}
	terminal := oldFlow(t, f, true)
	policy := f.s.Identity.Config.Policy()
	policy.SSO.CleanupBatchSize = 1
	policy.SSO.FlowRetentionDays = 1
	f.s.Identity.Config.Runtime = &policy
	service, e := New(f.s.Identity)
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close()
	active, e := service.Start(ctx, f.source.ConnectorID, "admin", "", nil, StartInput{Mode: "login"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = service.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = f.s.Identity.DB.QueryRow(ctx, `SELECT count(*) FROM sso_flows WHERE id=ANY($1::uuid[]) AND state='expired' AND verifier_ciphertext IS NULL`, expired).Scan(&count); e != nil || count != 1 {
		t.Fatal("expiry ignored its configured batch bound", e)
	}
	if e = f.s.Identity.DB.QueryRow(ctx, `SELECT count(*) FROM sso_flows WHERE id=$1`, terminal).Scan(&count); e != nil || count != 0 {
		t.Fatal("old unreferenced terminal metadata was not removed", e)
	}
	if e = service.Cleanup(ctx); e != nil {
		t.Fatal(e)
	}
	if e = f.s.Identity.DB.QueryRow(ctx, `SELECT count(*) FROM sso_flows WHERE id=ANY($1::uuid[]) AND state='expired' AND verifier_key_id IS NULL AND verifier_ciphertext IS NULL`, expired).Scan(&count); e != nil || count != 2 {
		t.Fatal("expiry did not clear unusable secret material", e)
	}
	if active.AuthorizationURL == "" {
		t.Fatal("live flow was not created")
	}
	if e = f.s.Identity.DB.QueryRow(ctx, `SELECT count(*) FROM sso_flows WHERE state='pending' AND expires_at>clock_timestamp() AND verifier_ciphertext IS NOT NULL`).Scan(&count); e != nil || count != 1 {
		t.Fatal("cleanup changed live flow authority", e)
	}
	failed := oldFlow(t, f, false)
	if _, e = f.s.Identity.DB.Exec(ctx, `CREATE FUNCTION reject_sso_cleanup_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='sso.flow_cleanup' THEN RAISE EXCEPTION 'synthetic cleanup audit failure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER reject_sso_cleanup_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_sso_cleanup_audit()`); e != nil {
		t.Fatal(e)
	}
	if e = service.Cleanup(ctx); e == nil {
		t.Fatal("audit failure reported cleanup success")
	}
	if e = f.s.Identity.DB.QueryRow(ctx, `SELECT count(*) FROM sso_flows WHERE id=$1 AND state='pending' AND verifier_ciphertext IS NOT NULL`, failed).Scan(&count); e != nil || count != 1 {
		t.Fatal("failed cleanup escaped atomic rollback", e)
	}
}

func TestM11ActualNondefaultRetainedBindingAdmissionAndOwnedRelink(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	policy := f.s.Identity.Config.Policy()
	policy.SSO.MaximumBindingsPerUser = 1
	f.s.Identity.Config.Runtime = &policy
	service, e := New(f.s.Identity)
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close()
	f.s.Identity.Sources = service
	f.s = service
	claimed := startAndClaim(t, f, f.local, &f.a, "link")
	_, e = service.commitFlow(ctx, claimed, "2", f.local, uuid.NewString())
	requireCode(t, e, "SSO_BINDING_LIMIT")
	if e = service.failFlow(ctx, claimed, e.Error(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	owned := startAndClaim(t, f, f.local, &f.a, "link")
	issued, e := service.commitFlow(ctx, owned, "9007199254740993", f.local, uuid.NewString())
	if e != nil {
		t.Fatal("existing ownership incorrectly counted as a new binding", e)
	}
	if _, e = service.Identity.Authenticate(ctx, issued.Token, "admin"); e != nil {
		t.Fatal(e)
	}
}
