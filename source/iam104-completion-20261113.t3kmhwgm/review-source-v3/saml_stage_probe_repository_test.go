//go:build repository_integration

package sso

import (
	"crypto/sha256"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/beevik/etree"
	"github.com/google/uuid"
)

func samlStageFixture(t *testing.T) (sourceRepositoryFixture, string, samlClaimProbe, *samlProtocolFixture) {
	t.Helper()
	f, password, p := samlProbeFixture(t)
	crypto := newSAMLProtocolFixture(t)
	digest := sha256.Sum256(crypto.cfg.Metadata)
	f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_saml_profiles SET metadata_digest=$1,revision=revision+1,trust_until=$4 WHERE tenant_id=$2 AND connector_id=$3`, digest[:], f.f.Tenant, p.connector, crypto.cfg.TrustedUntil))
	return f, password, p, crypto
}
func samlStageClaim(t *testing.T, f sourceRepositoryFixture, password string, p samlClaimProbe) (samlProbeClaim, samlProbeStart) {
	t.Helper()
	start, _, target := samlProbeStarted(t, f, password, p, "portal", false)
	claim, e := p.claim(f.ctx, target, "portal", start.relay, start.browser, uuid.NewString())
	if e != nil {
		t.Fatal("actual current-actor request claim", e)
	}
	return claim, start
}
func samlStageSigned(t *testing.T, crypto *samlProtocolFixture, claim samlProbeClaim, assertion string, adjust func(*etree.Element)) identityProof {
	t.Helper()
	cfg := crypto.cfg
	cfg.ACS = claim.callback
	a := crypto.assertion()
	a.RemoveAttr("ID")
	a.CreateAttr("ID", assertion)
	sc := a.SelectElement("Subject").SelectElement("SubjectConfirmation").SelectElement("SubjectConfirmationData")
	if sc == nil {
		t.Fatal("signed fixture bearer element unavailable")
	}
	sc.RemoveAttr("InResponseTo")
	sc.CreateAttr("InResponseTo", claim.request)
	sc.RemoveAttr("Recipient")
	sc.CreateAttr("Recipient", claim.callback)
	if adjust != nil {
		adjust(a)
	}
	response := crypto.response(crypto.sign(t, a, rsaSHA256))
	response.RemoveAttr("InResponseTo")
	response.CreateAttr("InResponseTo", claim.request)
	response.RemoveAttr("Destination")
	response.CreateAttr("Destination", claim.callback)
	proof, e := verifySAMLProbeClaim(claim, cfg, encoded(t, response))
	if e != nil {
		t.Fatal("strict signed response for actual claimed request", e)
	}
	return proof
}
func samlStageCounts(t *testing.T, f sourceRepositoryFixture, id string) (string, [3]int) {
	t.Helper()
	var state string
	var counts [3]int
	if e := f.f.Store.QueryRow(f.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT state FROM sso_saml_requests WHERE tenant_id=$1 AND id=$2`, f.f.Tenant, id)).Scan(&state); e != nil {
		t.Fatal(e)
	}
	for i, query := range []string{`SELECT count(*) FROM sso_saml_results WHERE tenant_id=$1 AND flow_id=$2`, `SELECT count(*) FROM sso_saml_identities WHERE tenant_id=$1 AND $2::uuid IS NOT NULL`, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND target_id=$2 AND action='synthetic.saml_result_staged'`} {
		if e := f.f.Store.QueryRow(f.ctx, transaction.SQL(transaction.PostgreSQL, query, f.f.Tenant, id)).Scan(&counts[i]); e != nil {
			t.Fatal(e)
		}
	}
	return state, counts
}
func TestSAMLStageProbeExactRequestAndDeadline(t *testing.T) {
	for _, shorter := range []string{"request", "assertion"} {
		t.Run(shorter, func(t *testing.T) {
			f, password, p, crypto := samlStageFixture(t)
			if shorter == "request" {
				p.ttl = 20 * time.Second
			}
			claim, _ := samlStageClaim(t, f, password, p)
			proof := samlStageSigned(t, crypto, claim, "_"+uuid.NewString(), func(a *etree.Element) {
				if shorter == "assertion" {
					a.SelectElement("AuthnStatement").CreateAttr("SessionNotOnOrAfter", crypto.now.Add(15*time.Second).Format(time.RFC3339))
				}
			})
			_, _, before := samlProbeCounts(t, f, claim.id)
			out, e := p.stage(f.ctx, claim, proof, uuid.NewString())
			if e != nil || out.flow != claim.id || !security.ValidToken(out.continuation) || !canonicalID(out.identity) {
				t.Fatal("atomic signed-result staging", e)
			}
			expected := proof.ExpiresAt
			if claim.expires.Before(expected) {
				expected = claim.expires
			}
			if !out.expires.Equal(expected) {
				t.Fatal("result outlived shortest authority")
			}
			state, counts := samlStageCounts(t, f, claim.id)
			_, _, after := samlProbeCounts(t, f, claim.id)
			if state != "verified" || counts != ([3]int{1, 1, 1}) || after != before {
				t.Fatal("staging split or granted local authority")
			}
			var digest []byte
			if e = f.f.Store.QueryRow(f.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT continuation_hash FROM sso_saml_results WHERE tenant_id=$1 AND flow_id=$2`, f.f.Tenant, claim.id)).Scan(&digest); e != nil || reflect.DeepEqual(digest, []byte(out.continuation)) || !reflect.DeepEqual(digest, security.Digest(out.continuation)) {
				t.Fatal("continuation stored in plaintext", e)
			}
			reopened := samlClaimProbe{service: f.s, ttl: p.ttl}
			retry, e := reopened.stage(f.ctx, claim, proof, uuid.NewString())
			if e == nil || retry != (samlStagedProbe{}) {
				t.Fatal("staged request replayed after owner reconstruction")
			}
		})
	}
}
func TestSAMLStageProbeSignedScopeAndRevocation(t *testing.T) {
	for _, kind := range []string{"different_request", "profile_revision", "profile_disabled", "source_logout", "credential_version", "target_revision", "callback_changed", "expired_proof", "foreign_tenant"} {
		t.Run(kind, func(t *testing.T) {
			f, password, p, crypto := samlStageFixture(t)
			claim, start := samlStageClaim(t, f, password, p)
			proof := samlStageSigned(t, crypto, claim, "_"+uuid.NewString(), nil)
			switch kind {
			case "different_request":
				other, _ := samlStageClaim(t, f, password, p)
				proof = samlStageSigned(t, crypto, other, "_"+uuid.NewString(), nil)
			case "profile_revision":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_saml_profiles SET revision=revision+1 WHERE tenant_id=$1 AND connector_id=$2`, f.f.Tenant, claim.connector))
			case "profile_disabled":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_saml_profiles SET enabled=false,revision=revision+1 WHERE tenant_id=$1 AND connector_id=$2`, f.f.Tenant, claim.connector))
			case "source_logout":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='private staged source revoked' WHERE id=$1`, claim.actor.SessionID))
			case "credential_version":
				callbackAuthorityFault(t, f, "", "credential")
			case "target_revision":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_connectors SET revision=revision+1 WHERE tenant_id=$1 AND id=$2`, f.f.Tenant, claim.connector))
			case "callback_changed":
				f.accounts.Config.Server.PortalOrigin = "https://replacement.example.invalid"
			case "expired_proof":
				// Reverify a correctly signed assertion under a later fake verifier clock,
				// then independently force its short accepted deadline past the DB clock.
				proof = samlStageSigned(t, crypto, claim, "_"+uuid.NewString(), func(a *etree.Element) {
					a.SelectElement("AuthnStatement").CreateAttr("SessionNotOnOrAfter", crypto.now.Add(time.Second).Format(time.RFC3339))
				})
				if _, e := f.f.PostgreSQL.Exec(f.ctx, `SELECT pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM $1::timestamptz-clock_timestamp())))`, proof.ExpiresAt.Add(20*time.Millisecond)); e != nil {
					t.Fatal(e)
				}
			case "foreign_tenant":
				claim.tenant = uuid.NewString()
			}
			out, e := p.stage(f.ctx, claim, proof, uuid.NewString())
			if e == nil || out != (samlStagedProbe{}) {
				t.Fatal("signed scope/revocation refusal released continuation", e)
			}
			state, counts := samlStageCounts(t, f, start.id)
			if state != "claimed" || counts != ([3]int{}) {
				t.Fatal("failed staging persisted external identity/result/audit")
			}
		})
	}
}
func TestSAMLStageProbeAssertionReplayAcrossRequests(t *testing.T) {
	f, password, p, crypto := samlStageFixture(t)
	first, _ := samlStageClaim(t, f, password, p)
	second, _ := samlStageClaim(t, f, password, p)
	assertion := "_" + uuid.NewString()
	one := samlStageSigned(t, crypto, first, assertion, nil)
	two := samlStageSigned(t, crypto, second, assertion, nil)
	if _, e := p.stage(f.ctx, first, one, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	out, e := p.stage(f.ctx, second, two, uuid.NewString())
	if e == nil || out != (samlStagedProbe{}) {
		t.Fatal("same scoped signed assertion ID accepted on a second request")
	}
	state, counts := samlStageCounts(t, f, second.id)
	if state != "claimed" || counts != ([3]int{0, 1, 0}) {
		t.Fatal("duplicate assertion partially persisted")
	}
	fresh := samlStageSigned(t, crypto, second, "_"+uuid.NewString(), nil)
	if _, e := p.stage(f.ctx, second, fresh, uuid.NewString()); e != nil {
		t.Fatal("explicit fresh signed assertion retry", e)
	}
}
func TestSAMLStageProbeAuditAndConcurrent(t *testing.T) {
	for _, kind := range []string{"audit_rollback", "two_stagers"} {
		t.Run(kind, func(t *testing.T) {
			f, password, p, crypto := samlStageFixture(t)
			claim, _ := samlStageClaim(t, f, password, p)
			proof := samlStageSigned(t, crypto, claim, "_"+uuid.NewString(), nil)
			if kind == "audit_rollback" {
				sql := `CREATE FUNCTION saml_stage_probe_audit_refusal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='synthetic.saml_result_staged' THEN RAISE EXCEPTION 'private stage audit fault'; END IF; RETURN NEW; END; $$; CREATE TRIGGER saml_stage_probe_audit_refusal BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION saml_stage_probe_audit_refusal()`
				if _, e := f.f.PostgreSQL.Exec(f.ctx, sql); e != nil {
					t.Fatal(e)
				}
				out, e := p.stage(f.ctx, claim, proof, uuid.NewString())
				if e == nil || out != (samlStagedProbe{}) {
					t.Fatal("audit failure released continuation")
				}
				state, counts := samlStageCounts(t, f, claim.id)
				if state != "claimed" || counts != ([3]int{}) {
					t.Fatal("audit failure partially staged result")
				}
				if _, e = f.f.PostgreSQL.Exec(f.ctx, `DROP TRIGGER saml_stage_probe_audit_refusal ON audit_events`); e != nil {
					t.Fatal(e)
				}
				if _, e = p.stage(f.ctx, claim, proof, uuid.NewString()); e != nil {
					t.Fatal("stage explicit audit repair", e)
				}
				return
			}
			begin := make(chan struct{})
			type result struct {
				out samlStagedProbe
				e   error
			}
			done := make(chan result, 2)
			for range 2 {
				go func() { <-begin; out, e := p.stage(f.ctx, claim, proof, uuid.NewString()); done <- result{out, e} }()
			}
			close(begin)
			wins, refused := 0, 0
			for range 2 {
				select {
				case r := <-done:
					if r.e == nil && security.ValidToken(r.out.continuation) {
						wins++
					} else if r.e != nil && r.out == (samlStagedProbe{}) {
						refused++
					} else {
						t.Fatal("invalid concurrent stage result")
					}
				case <-time.After(6 * time.Second):
					t.Fatal("concurrent stage did not drain")
				}
			}
			state, counts := samlStageCounts(t, f, claim.id)
			if wins != 1 || refused != 1 || state != "verified" || counts != ([3]int{1, 1, 1}) {
				t.Fatal("concurrent staging failed single issuance")
			}
		})
	}
}
func TestSAMLStageProbeCommitLoss(t *testing.T) {
	for _, mode := range []commitfault.Mode{commitfault.DropBeforeCommit, commitfault.DropCommitAcknowledgement} {
		name := "before_commit"
		committed := false
		if mode == commitfault.DropCommitAcknowledgement {
			name = "after_server_commit"
			committed = true
		}
		t.Run(name, func(t *testing.T) {
			f, password, p, crypto := samlStageFixture(t)
			claim, _ := samlStageClaim(t, f, password, p)
			proof := samlStageSigned(t, crypto, claim, "_"+uuid.NewString(), nil)
			store, proxy := connectorFaultStore(t, f)
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			f.accounts.Storage, f.accounts.DB = store, nil
			if e := proxy.Arm(mode); e != nil {
				t.Fatal(e)
			}
			out, e := p.stage(f.ctx, claim, proof, uuid.NewString())
			f.accounts.Storage, f.accounts.DB = originalStore, originalDB
			if e == nil || e.Error() != string(transaction.CommitUnknown) || out != (samlStagedProbe{}) {
				t.Fatal("lost stage COMMIT released continuation", e)
			}
			select {
			case seen := <-proxy.Outcomes():
				if seen.Mode != mode || !seen.Suppressed || seen.Forwarded != committed || seen.ServerOK != committed {
					t.Fatal("wrong stage wire observation")
				}
			case <-f.ctx.Done():
				t.Fatal("stage wire outcome absent")
			}
			if proxy.Commits() != 1 {
				t.Fatal("stage automatically resent unknown COMMIT")
			}
			state, counts := samlStageCounts(t, f, claim.id)
			if committed && (state != "verified" || counts != ([3]int{1, 1, 1})) || !committed && (state != "claimed" || counts != ([3]int{})) {
				t.Fatal("actual stage transaction split")
			}
		})
	}
}

func TestSAMLStageProbeActualForeignProof(t *testing.T) {
	var foreign identityProof
	t.Run("verify_under_actual_foreign_owner", func(t *testing.T) {
		f, password, p, crypto := samlStageFixture(t)
		claim, _ := samlStageClaim(t, f, password, p)
		foreign = samlStageSigned(t, crypto, claim, "_"+uuid.NewString(), nil)
	})
	f, password, p, _ := samlStageFixture(t)
	claim, _ := samlStageClaim(t, f, password, p)
	if foreign.source.tenant == claim.tenant || foreign.AssertionID == "" {
		t.Fatal("independent foreign proof unavailable")
	}
	out, e := p.stage(f.ctx, claim, foreign, uuid.NewString())
	if e == nil || out != (samlStagedProbe{}) {
		t.Fatal("correctly signed foreign owner proof accepted locally", e)
	}
	state, counts := samlStageCounts(t, f, claim.id)
	if state != "claimed" || counts != ([3]int{}) {
		t.Fatal("foreign proof persisted local result")
	}
}
