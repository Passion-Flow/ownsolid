package sso

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

const testAuditBarrierLock int64 = 713090112

// The second local Admin role and the post-provider subject are explicit test
// fixtures. Actual role management/provider qualification are not inferred here.
func secondAdmin(t *testing.T, f sessionFixture) sessionFixture {
	t.Helper()
	ctx := context.Background()
	email := "second-" + uuid.NewString() + "@example.invalid"
	password := fixture.Password()
	i, e := f.s.Identity.Invite(ctx, f.a, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.Identity.Register(ctx, fixture.InvitationToken(t, f.s.Identity, i.ID), "Second synthetic Admin", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	var user string
	if e = f.s.Identity.DB.QueryRow(ctx, `UPDATE users SET is_platform_admin=true WHERE email_canonical=$1 RETURNING id::text`, email).Scan(&user); e != nil {
		t.Fatal(e)
	}
	_, local, _, e := f.s.Identity.Login(ctx, email, password, "admin", "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	a, e := f.s.Identity.Authenticate(ctx, local, "admin")
	if e != nil {
		t.Fatal(e)
	}
	source := identity.SessionSource{TenantID: f.source.TenantID, ConnectorID: f.source.ConnectorID, BindingID: uuid.NewString(), FlowID: uuid.NewString(), AuthVersion: 1}
	if _, e = f.s.Identity.DB.Exec(ctx, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES($1,$2,$3,'3',$4)`, source.BindingID, source.TenantID, source.ConnectorID, user); e != nil {
		t.Fatal(e)
	}
	f.pendingFlow(t, source.FlowID)
	if _, e = f.s.Identity.DB.Exec(ctx, `UPDATE sso_flows SET state='claimed',claim_id=$2,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$1`, source.FlowID, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Identity.DB.Exec(ctx, `UPDATE sso_flows SET state='succeeded',authenticated_user_id=$2,finished_at=clock_timestamp() WHERE id=$1`, source.FlowID, user); e != nil {
		t.Fatal(e)
	}
	return sessionFixture{s: f.s, a: a, local: local, password: password, source: source}
}

func waitBlocked(t *testing.T, f sessionFixture, blocker, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var observed int
		if e := f.s.Identity.DB.QueryRow(context.Background(), `WITH RECURSIVE waiters(pid) AS (SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) UNION SELECT a.pid FROM pg_stat_activity a JOIN waiters w ON w.pid=ANY(pg_blocking_pids(a.pid))) SELECT count(*) FROM waiters`, blocker).Scan(&observed); e != nil {
			t.Fatal(e)
		}
		if observed >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("controlled transactions did not reach the expected actual database barrier")
}

func barrier(t *testing.T, f sessionFixture, action string) (pgx.Tx, int) {
	t.Helper()
	ctx := context.Background()
	if action != "sso.connector_updated" && action != "auth.sso_login_succeeded" {
		t.Fatal("fixed test audit action required")
	}
	// Both action choices are fixed test constants, never caller-controlled SQL.
	statement := `CREATE FUNCTION sso_test_audit_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='` + action + `' THEN PERFORM pg_advisory_xact_lock(` + "713090112" + `); END IF; RETURN NEW; END; $$; CREATE TRIGGER sso_test_audit_barrier BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION sso_test_audit_barrier()`
	if _, e := f.s.Identity.DB.Exec(ctx, statement); e != nil {
		t.Fatal(e)
	}
	tx, e := f.s.Identity.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { tx.Rollback(ctx) })
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, testAuditBarrierLock); e != nil {
		t.Fatal(e)
	}
	var pid int
	if e = tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); e != nil {
		t.Fatal(e)
	}
	return tx, pid
}

func TestM11ActualTwoSourcedAdminsSerializeWithoutSharedLockUpgradeDeadlock(t *testing.T) {
	f := sourceFixture(t)
	other := secondAdmin(t, f)
	ctx := context.Background()
	firstSession, secondSession := f.issue(t, ""), other.issue(t, "")
	first, e := f.s.Identity.Authenticate(ctx, firstSession.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	second, e := f.s.Identity.Authenticate(ctx, secondSession.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	locker, e := f.s.Identity.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer locker.Rollback(ctx)
	var pid int
	if e = locker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); e != nil {
		t.Fatal(e)
	}
	if _, e = locker.Exec(ctx, `SELECT id FROM sso_connectors WHERE id=$1 FOR UPDATE`, f.source.ConnectorID); e != nil {
		t.Fatal(e)
	}
	results := make(chan error, 2)
	for _, actor := range []identity.Auth{first, second} {
		go func(a identity.Auth) {
			_, e := f.s.UpdateConnector(ctx, a, f.source.ConnectorID, UpdateConnector{Revision: 1, Name: "Serialized synthetic name", Enabled: true, Reason: "Controlled two-Admin mutation"}, uuid.NewString(), uuid.NewString())
			results <- e
		}(actor)
	}
	waitBlocked(t, f, pid, 2)
	if e = locker.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	wins := 0
	for i := 0; i < 2; i++ {
		select {
		case e := <-results:
			if e == nil {
				wins++
			} else {
				requireCode(t, e, "SSO_REVISION_CONFLICT")
			}
		case <-time.After(4 * time.Second):
			t.Fatal("Admin connector mutation did not finish")
		}
	}
	if wins != 1 {
		t.Fatal("expected one exact-revision winner")
	}
	for _, token := range []string{firstSession.Token, secondSession.Token} {
		if _, e = f.s.Identity.Authenticate(ctx, token, "admin"); e != nil {
			t.Fatal("name edit revoked an unchanged source", e)
		}
	}
}

func TestM11ActualConnectorDisableWinsBeforeFinalIssuance(t *testing.T) {
	f := sourceFixture(t)
	other := secondAdmin(t, f)
	ctx := context.Background()
	claimed := startAndClaim(t, f, f.local, &f.a, "link")
	locker, pid := barrier(t, f, "sso.connector_updated")
	mutation := make(chan error, 1)
	go func() {
		_, e := f.s.UpdateConnector(ctx, other.a, f.source.ConnectorID, UpdateConnector{Revision: 1, Name: "Disabled before issue", Enabled: false, Reason: "Controlled disable before final commit"}, uuid.NewString(), uuid.NewString())
		mutation <- e
	}()
	waitBlocked(t, f, pid, 1)
	issuer := make(chan error, 1)
	go func() { _, e := f.s.commitFlow(ctx, claimed, "2", f.local, uuid.NewString()); issuer <- e }()
	// The mutation owns the connector while its atomic audit waits. Releasing
	// that real transaction commits disable before issuance can inspect it.
	if e := locker.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-mutation:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("disable did not finish")
	}
	select {
	case e := <-issuer:
		requireCode(t, e, "SSO_FLOW_UNUSABLE")
	case <-time.After(4 * time.Second):
		t.Fatal("issuer did not observe disabled connector")
	}
	var count int
	if e := f.s.Identity.DB.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE sso_flow_id=$1`, claimed.id).Scan(&count); e != nil || count != 0 {
		t.Fatal("disabled flow established a session", e)
	}
	if _, e := f.s.Identity.Authenticate(ctx, f.local, "admin"); e != nil {
		t.Fatal("local emergency source revoked", e)
	}
}

func TestM11ActualIssuedSessionIsRevokedByWaitingConnectorDisable(t *testing.T) {
	f := sourceFixture(t)
	other := secondAdmin(t, f)
	ctx := context.Background()
	claimed := startAndClaim(t, f, f.local, &f.a, "link")
	locker, pid := barrier(t, f, "auth.sso_login_succeeded")
	type outcome struct {
		session identity.IssuedSession
		err     error
	}
	issuer := make(chan outcome, 1)
	go func() {
		session, e := f.s.commitFlow(ctx, claimed, "2", f.local, uuid.NewString())
		issuer <- outcome{session, e}
	}()
	waitBlocked(t, f, pid, 1)
	mutation := make(chan error, 1)
	go func() {
		_, e := f.s.UpdateConnector(ctx, other.a, f.source.ConnectorID, UpdateConnector{Revision: 1, Name: "Disabled after issue", Enabled: false, Reason: "Controlled disable after issuance"}, uuid.NewString(), uuid.NewString())
		mutation <- e
	}()
	if e := locker.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	var issued identity.IssuedSession
	select {
	case result := <-issuer:
		if result.err != nil {
			t.Fatal(result.err)
		}
		issued = result.session
	case <-time.After(4 * time.Second):
		t.Fatal("issuer did not commit")
	}
	select {
	case e := <-mutation:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("waiting disable did not commit")
	}
	_, e := f.s.Identity.Authenticate(ctx, issued.Token, "admin")
	requireCode(t, e, "AUTH_SESSION_EXPIRED")
	var reason string
	if e = f.s.Identity.DB.QueryRow(ctx, `SELECT revoke_reason FROM sessions WHERE id=$1 AND revoked_at IS NOT NULL`, issued.SessionID).Scan(&reason); e != nil || !strings.HasPrefix(reason, "sso_connector_") {
		t.Fatal("issued session was not atomically revoked", e)
	}
	if _, e = f.s.Identity.Authenticate(ctx, other.local, "admin"); e != nil {
		t.Fatal("unrelated local Admin revoked", e)
	}
}
