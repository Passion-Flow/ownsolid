//go:build repository_integration

package sso

import (
	"context"
	"crypto/sha256"
	"github.com/crewjam/saml"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func samlProbeFixture(t *testing.T) (sourceRepositoryFixture, string, samlClaimProbe) {
	t.Helper()
	f, password := bindingRepositoryFixture(t)
	if f.f.PostgreSQL == nil {
		t.Fatal("isolated PostgreSQL required")
	}
	raw, e := os.ReadFile(os.Getenv("IAM_SAML_CLAIM_SQL_FILE"))
	if e != nil {
		t.Fatal("private candidate SQL unavailable")
	}
	if _, e = f.f.PostgreSQL.Exec(f.ctx, string(raw)); e != nil {
		t.Fatal("private candidate SQL", e)
	}
	crypto := newSAMLProtocolFixture(t)
	digest := sha256.Sum256(crypto.cfg.Metadata)
	f.exec(t, transaction.SQL(transaction.PostgreSQL, `INSERT INTO saml_identity_contexts(tenant_id,connector_id,issuer,sp_entity,name_id_format,metadata_digest) VALUES($1,$2,$3,$4,$5,$6)`, f.f.Tenant, f.source.ConnectorID, crypto.cfg.Issuer, crypto.cfg.SP, string(saml.PersistentNameIDFormat), digest[:]))
	return f, password, samlClaimProbe{service: f.s, ttl: time.Duration(f.s.policy.FlowSeconds) * time.Second}
}
func samlProbeConnection(t *testing.T, f sourceRepositoryFixture) *pgx.Conn {
	t.Helper()
	conn, e := pgx.ConnectConfig(f.ctx, f.f.PostgreSQL.Config().ConnConfig.Copy())
	if e != nil {
		t.Fatal("independent private diagnostic connection", e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := conn.Close(ctx); e != nil {
			t.Error("private diagnostic connection cleanup", e)
		}
	})
	return conn
}
func samlProbeCounts(t *testing.T, f sourceRepositoryFixture, id string) (string, int, [2]int) {
	t.Helper()
	var state string
	var audits int
	var authority [2]int
	if e := f.f.Store.QueryRow(f.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT state FROM saml_claim_probe_flows WHERE tenant_id=$1 AND id=$2`, f.f.Tenant, id)).Scan(&state); e != nil {
		t.Fatal(e)
	}
	if e := f.f.Store.QueryRow(f.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND target_id=$2 AND action='synthetic.saml_request_claimed'`, f.f.Tenant, id)).Scan(&audits); e != nil {
		t.Fatal(e)
	}
	for i, name := range []string{"sessions", "sso_bindings"} {
		if e := f.f.Store.QueryRow(f.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT count(*) FROM `+name)).Scan(&authority[i]); e != nil {
			t.Fatal(e)
		}
	}
	return state, audits, authority
}
func samlProbeStarted(t *testing.T, f sourceRepositoryFixture, password string, p samlClaimProbe, audience string, sourced bool) (samlProbeStart, string, string) {
	t.Helper()
	_, previous := startActor(t, f, password, audience, sourced)
	target := f.source.ConnectorID
	if sourced {
		target = catalogSeedConnector(t, f, f.f.Tenant, f.accounts.Config.SSO.Providers[0].Origin, "Separate captured-actor target", true, time.Now().UTC())
	}
	if sourced {
		f.exec(t, transaction.SQL(transaction.PostgreSQL, `INSERT INTO saml_identity_contexts(tenant_id,connector_id,issuer,sp_entity,name_id_format,metadata_digest) SELECT tenant_id,$1,issuer,sp_entity,name_id_format,metadata_digest FROM saml_identity_contexts WHERE tenant_id=$2 AND connector_id=$3`, target, f.f.Tenant, f.source.ConnectorID))
	}
	out, e := p.start(f.ctx, target, audience, previous, password, uuid.NewString())
	if e != nil {
		t.Fatal("actual password-confirmed private request", e)
	}
	return out, previous, target
}
func samlProbeRefused(t *testing.T, p samlClaimProbe, f sourceRepositoryFixture, start samlProbeStart, target, audience string) {
	t.Helper()
	out, e := p.claim(f.ctx, target, audience, start.relay, start.browser, uuid.NewString())
	if e == nil || !reflect.DeepEqual(out, samlProbeClaim{}) {
		t.Fatal("refusal released private request claim", e)
	}
	state, audits, _ := samlProbeCounts(t, f, start.id)
	if state != "pending" || audits != 0 {
		t.Fatal("refusal persisted a claim or success audit")
	}
}
func TestSAMLClaimProbeCurrentActors(t *testing.T) {
	for _, audience := range []string{"admin", "portal"} {
		for _, sourced := range []bool{false, true} {
			name := audience + "_local"
			if sourced {
				name = audience + "_different_source"
			}
			t.Run(name, func(t *testing.T) {
				f, password, p := samlProbeFixture(t)
				start, _, target := samlProbeStarted(t, f, password, p, audience, sourced)
				_, _, before := samlProbeCounts(t, f, start.id)
				out, e := p.claim(f.ctx, target, audience, start.relay, start.browser, uuid.NewString())
				if e != nil || out.id != start.id || out.request != start.request || !canonicalID(out.claim) || out.actor.Me.User.ID != f.f.User || out.tenant != f.f.Tenant || out.actor.Me.Audience != audience {
					t.Fatal("actual captured current actor request claim", e)
				}
				state, audits, after := samlProbeCounts(t, f, start.id)
				if state != "claimed" || audits != 1 || after != before {
					t.Fatal("claim must not create bindings or sessions")
				}
				// Reconstruct the owner from durable authority; do not retain an in-memory replay cache.
				reopened := samlClaimProbe{service: f.s, ttl: p.ttl}
				replay, e := reopened.claim(f.ctx, target, audience, start.relay, start.browser, uuid.NewString())
				if e == nil || !reflect.DeepEqual(replay, samlProbeClaim{}) {
					t.Fatal("durable replay accepted after owner reconstruction")
				}
			})
		}
	}
}
func TestSAMLClaimProbeScopeDenials(t *testing.T) {
	for _, kind := range []string{"browser", "relay", "audience", "target", "callback", "foreign_tenant", "wrong_password", "immutable_actor", "expired"} {
		t.Run(kind, func(t *testing.T) {
			f, password, p := samlProbeFixture(t)
			if kind == "expired" {
				p.ttl = 100 * time.Millisecond
			}
			start, previous, target := samlProbeStarted(t, f, password, p, "portal", false)
			audience := "portal"
			switch kind {
			case "browser":
				start.browser = security.Token()
			case "relay":
				start.relay = security.Token()
			case "audience":
				audience = "admin"
			case "target":
				target = catalogSeedConnector(t, f, f.f.Tenant, f.accounts.Config.SSO.Providers[0].Origin, "Wrong local target", true, time.Now().UTC())
			case "callback":
				f.accounts.Config.Server.PortalOrigin = "https://replacement.example.invalid"
			case "foreign_tenant":
				tenant := uuid.NewString()
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign private request tenant','active')`, tenant))
				foreign := catalogSeedConnector(t, f, tenant, f.accounts.Config.SSO.Providers[0].Origin, "Foreign private request connector", true, time.Now().UTC())
				other, e := p.start(f.ctx, foreign, "portal", previous, password, uuid.NewString())
				if e == nil || other != (samlProbeStart{}) {
					t.Fatal("foreign tenant start accepted")
				}
				// Duplicate otherwise-valid captured fields only in the isolated foreign tenant.
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `INSERT INTO saml_identity_contexts(tenant_id,connector_id,id,issuer,sp_entity,name_id_format,metadata_digest) SELECT $1,$2,id,issuer,sp_entity,name_id_format,metadata_digest FROM saml_identity_contexts WHERE tenant_id=$3 AND connector_id=$4`, tenant, foreign, f.f.Tenant, f.source.ConnectorID))
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `INSERT INTO saml_claim_probe_flows(id,tenant_id,connector_id,audience,callback,request_id,user_id,session_id,state,browser_hash,relay_hash,previous_hash,epoch,credential_version,connector_revision,auth_version,created_at,expires_at,context_id,context_revision,metadata_digest) SELECT id,$1,$2,audience,callback,request_id,user_id,session_id,state,browser_hash,relay_hash,previous_hash,epoch,credential_version,connector_revision,auth_version,created_at,expires_at,context_id,context_revision,metadata_digest FROM saml_claim_probe_flows WHERE tenant_id=$3 AND id=$4`, tenant, foreign, f.f.Tenant, start.id))

				target = foreign
			case "wrong_password":
				out, e := p.start(f.ctx, target, "portal", previous, security.Token(), uuid.NewString())
				if e == nil || out != (samlProbeStart{}) {
					t.Fatal("wrong password confirmed a link")
				}
				return
			case "immutable_actor":
				if _, e := f.f.PostgreSQL.Exec(f.ctx, `UPDATE saml_claim_probe_flows SET previous_hash=$1 WHERE tenant_id=$2 AND id=$3`, security.Digest(security.Token()), f.f.Tenant, start.id); e == nil {
					t.Fatal("captured actor rewritten")
				}
				return
			case "expired":
				// Wait against the actual database clock; no assertion expiry is fabricated.
				if _, e := f.f.PostgreSQL.Exec(f.ctx, `SELECT pg_sleep(0.15)`); e != nil {
					t.Fatal(e)
				}
			}
			samlProbeRefused(t, p, f, start, target, audience)
		})
	}
}
func TestSAMLClaimProbeRevokedAuthority(t *testing.T) {
	for _, kind := range []string{"logout", "idle", "absolute", "disabled_user", "epoch", "credential", "source_connector", "source_binding", "source_version", "target_revision", "target_disabled", "installation_tenant"} {
		t.Run(kind, func(t *testing.T) {
			f, password, p := samlProbeFixture(t)
			start, previous, target := samlProbeStarted(t, f, password, p, "portal", true)
			switch kind {
			case "logout":
				if e := f.accounts.Logout(f.ctx, previous, "portal", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
			case "idle":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sessions SET created_at=clock_timestamp()-interval '31 days',last_seen_at=clock_timestamp()-interval '30 days' WHERE token_hash=$1`, security.Digest(previous)))
			case "absolute":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sessions SET created_at=clock_timestamp()-interval '1 hour',last_seen_at=clock_timestamp(),absolute_expires_at=clock_timestamp()-interval '1 second' WHERE token_hash=$1`, security.Digest(previous)))
			case "disabled_user":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE users SET status='disabled' WHERE id=$1`, f.f.User))
			case "epoch":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1`, f.f.User))
			case "credential":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE password_credentials SET version=version+1 WHERE user_id=$1`, f.f.User))
			case "source_connector":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE tenant_id=$1 AND id=$2`, f.f.Tenant, f.source.ConnectorID))
			case "source_binding":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_bindings SET active=false,revision=revision+1 WHERE tenant_id=$1 AND id=$2`, f.f.Tenant, f.source.BindingID))
			case "source_version":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_connectors SET revision=revision+1,auth_version=auth_version+1 WHERE tenant_id=$1 AND id=$2`, f.f.Tenant, f.source.ConnectorID))
			case "target_revision":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_connectors SET revision=revision+1 WHERE tenant_id=$1 AND id=$2`, f.f.Tenant, target))
			case "target_disabled":
				f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE tenant_id=$1 AND id=$2`, f.f.Tenant, target))
			case "installation_tenant":
				callbackAuthorityFault(t, f, previous, "tenant")
			}
			samlProbeRefused(t, p, f, start, target, "portal")
		})
	}
}
func TestSAMLClaimProbeConcurrentAndCancellation(t *testing.T) {
	for _, cancelWorker := range []bool{false, true} {
		name := "two_claimers"
		if cancelWorker {
			name = "canceled_actual_row_wait"
		}
		t.Run(name, func(t *testing.T) {
			f, password, p := samlProbeFixture(t)
			start, _, target := samlProbeStarted(t, f, password, p, "portal", false)
			observer := samlProbeConnection(t, f)
			holder, e := samlProbeConnection(t, f).Begin(f.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer holder.Rollback(context.Background())
			var id string
			if e = holder.QueryRow(f.ctx, `SELECT id::text FROM saml_claim_probe_flows WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, f.f.Tenant, start.id).Scan(&id); e != nil {
				t.Fatal(e)
			}
			var pid int
			if e = holder.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
			defer cancel()
			type result struct {
				out samlProbeClaim
				e   error
			}
			done := make(chan result, 2)
			workers := 2
			if cancelWorker {
				workers = 1
			}
			for range workers {
				go func() {
					out, e := p.claim(ctx, target, "portal", start.relay, start.browser, uuid.NewString())
					done <- result{out, e}
				}()
			}
			deadline := time.Now().Add(3 * time.Second)
			observed := false
			for time.Now().Before(deadline) {
				var count int
				if e = observer.QueryRow(f.ctx, `SELECT count(*) FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))`, pid).Scan(&count); e != nil {
					t.Fatal(e)
				}
				if count > 0 {
					observed = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !observed {
				t.Fatal("claim never reached actual PostgreSQL row wait")
			}
			if cancelWorker {
				cancel()
			} else if e = holder.Commit(f.ctx); e != nil {
				t.Fatal(e)
			}
			wins, refused := 0, 0
			for range workers {
				select {
				case got := <-done:
					if got.e == nil && canonicalID(got.out.claim) {
						wins++
					} else if got.e != nil && reflect.DeepEqual(got.out, samlProbeClaim{}) {
						refused++
					} else {
						t.Fatal("invalid worker outcome")
					}
				case <-time.After(6 * time.Second):
					t.Fatal("claim worker did not drain")
				}
			}
			if cancelWorker {
				if wins != 0 || refused != 1 {
					t.Fatal("cancellation leaked claim")
				}
				holder.Rollback(f.ctx)
				samlProbeRefusedState(t, f, start)
				out, e := p.claim(f.ctx, target, "portal", start.relay, start.browser, uuid.NewString())
				if e != nil || out.id != start.id {
					t.Fatal("explicit retry after cancellation", e)
				}
			} else if wins != 1 || refused != 1 {
				t.Fatal("exclusive claim did not select one worker")
			}
		})
	}
}
func samlProbeRefusedState(t *testing.T, f sourceRepositoryFixture, start samlProbeStart) {
	t.Helper()
	state, audits, _ := samlProbeCounts(t, f, start.id)
	if state != "pending" || audits != 0 {
		t.Fatal("failed transaction changed durable claim")
	}
}
func TestSAMLClaimProbeAuditRollback(t *testing.T) {
	f, password, p := samlProbeFixture(t)
	start, _, target := samlProbeStarted(t, f, password, p, "portal", false)
	statement := `CREATE FUNCTION saml_claim_probe_audit_refusal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='synthetic.saml_request_claimed' THEN RAISE EXCEPTION 'private audit fault'; END IF; RETURN NEW; END; $$; CREATE TRIGGER saml_claim_probe_audit_refusal BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION saml_claim_probe_audit_refusal()`
	if _, e := f.f.PostgreSQL.Exec(f.ctx, statement); e != nil {
		t.Fatal(e)
	}
	samlProbeRefused(t, p, f, start, target, "portal")
	if _, e := f.f.PostgreSQL.Exec(f.ctx, `DROP TRIGGER saml_claim_probe_audit_refusal ON audit_events`); e != nil {
		t.Fatal(e)
	}
	out, e := p.claim(f.ctx, target, "portal", start.relay, start.browser, uuid.NewString())
	if e != nil || out.id != start.id {
		t.Fatal("explicit repair after audit rollback", e)
	}
}
func TestSAMLClaimProbeCommitLoss(t *testing.T) {
	for _, mode := range []commitfault.Mode{commitfault.DropBeforeCommit, commitfault.DropCommitAcknowledgement} {
		name := "before_commit"
		committed := false
		if mode == commitfault.DropCommitAcknowledgement {
			name = "after_server_commit"
			committed = true
		}
		t.Run(name, func(t *testing.T) {
			f, password, p := samlProbeFixture(t)
			start, _, target := samlProbeStarted(t, f, password, p, "portal", false)
			store, proxy := connectorFaultStore(t, f)
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			f.accounts.Storage, f.accounts.DB = store, nil
			if e := proxy.Arm(mode); e != nil {
				t.Fatal(e)
			}
			out, e := p.claim(f.ctx, target, "portal", start.relay, start.browser, uuid.NewString())
			f.accounts.Storage, f.accounts.DB = originalStore, originalDB
			if e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(out, samlProbeClaim{}) {
				t.Fatal("actual lost COMMIT released private handle", e)
			}
			select {
			case seen := <-proxy.Outcomes():
				if seen.Mode != mode || !seen.Suppressed || seen.Forwarded != committed || seen.ServerOK != committed {
					t.Fatal("wrong actual wire fault")
				}
			case <-f.ctx.Done():
				t.Fatal("actual wire outcome absent")
			}
			if proxy.Commits() != 1 {
				t.Fatal("claim automatically resent after unknown COMMIT")
			}
			state, audits, _ := samlProbeCounts(t, f, start.id)
			if committed && (state != "claimed" || audits != 1) || !committed && (state != "pending" || audits != 0) {
				t.Fatal("claim/audit actual commit split")
			}
		})
	}
}
func TestSAMLClaimProbeSourceBeforeSession(t *testing.T) {
	f, password, p := samlProbeFixture(t)
	start, _, target := samlProbeStarted(t, f, password, p, "portal", true)
	observer := samlProbeConnection(t, f)
	holder, e := samlProbeConnection(t, f).Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer holder.Rollback(context.Background())
	var id string
	if e = holder.QueryRow(f.ctx, `SELECT id::text FROM sso_connectors WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, f.f.Tenant, f.source.ConnectorID).Scan(&id); e != nil {
		t.Fatal(e)
	}
	var pid int
	if e = holder.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 6*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		out, e := p.claim(ctx, target, "portal", start.relay, start.browser, uuid.NewString())
		if e == nil || !reflect.DeepEqual(out, samlProbeClaim{}) {
			done <- fail("PRIVATE_CLAIM_NOT_REFUSED")
			return
		}
		done <- nil
	}()
	deadline := time.Now().Add(3 * time.Second)
	observed := false
	for time.Now().Before(deadline) {
		var n int
		if e = observer.QueryRow(f.ctx, `SELECT count(*) FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))`, pid).Scan(&n); e != nil {
			t.Fatal(e)
		}
		if n > 0 {
			observed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !observed {
		t.Fatal("claim did not wait for actual source connector")
	}
	revoker, stop := context.WithTimeout(f.ctx, time.Second)
	defer stop()
	if _, e = holder.Exec(revoker, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='private source-order probe' WHERE sso_tenant_id=$1 AND sso_connector_id=$2 AND revoked_at IS NULL`, f.f.Tenant, f.source.ConnectorID); e != nil {
		t.Fatal("claim held session before source connector", e)
	}
	if _, e = holder.Exec(revoker, `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE tenant_id=$1 AND id=$2`, f.f.Tenant, f.source.ConnectorID); e != nil {
		t.Fatal(e)
	}
	if e = holder.Commit(revoker); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("source revocation interleaving did not drain")
	}
	samlProbeRefusedState(t, f, start)
}
