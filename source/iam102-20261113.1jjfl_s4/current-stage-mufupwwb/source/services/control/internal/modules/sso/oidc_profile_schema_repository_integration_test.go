//go:build repository_integration

package sso

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func requireOIDCProfilePostgres(t *testing.T, f sourceRepositoryFixture) {
	t.Helper()
	if f.f.Store.Dialect() != transaction.PostgreSQL {
		t.Skip("OIDC storage candidate requires the PostgreSQL fixture")
	}
}

func applyOIDCProfileSchema(t *testing.T, f sourceRepositoryFixture, direction string) {
	t.Helper()
	if direction != "up" && direction != "down" {
		t.Fatal("invalid candidate schema direction")
	}
	raw, e := os.ReadFile(filepath.Join("testdata", "oidc_profile."+direction+".sql"))
	if e != nil {
		t.Fatal("candidate schema unavailable")
	}
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	opposite := "down"
	if direction == "down" {
		opposite = "up"
	}
	expected, e := os.ReadFile(filepath.Join("testdata", "oidc_profile."+opposite+".sql"))
	if e != nil {
		t.Fatal("candidate base definitions unavailable")
	}
	if e = f.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		for _, name := range []string{"protect_sso_connector_identity", "protect_sso_binding_identity", "protect_sso_flow_transition"} {
			pattern := regexp.MustCompile(`(?s)CREATE OR REPLACE FUNCTION ` + regexp.QuoteMeta(name) + `\(\) RETURNS trigger LANGUAGE plpgsql AS \$\$(.*?)\$\$;`)
			wanted := pattern.FindSubmatch(expected)
			if len(wanted) != 2 {
				return errors.New("SSO_CANDIDATE_BASE_DEFINITION_MISSING")
			}
			var actual string
			if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT prosrc FROM pg_proc WHERE oid=to_regprocedure($1)`, name+"()")).Scan(&actual); e != nil {
				return e
			}
			if strings.TrimSpace(actual) != strings.TrimSpace(string(wanted[1])) {
				return errors.New("SSO_CANDIDATE_BASE_FUNCTION_CHANGED")
			}
		}
		_, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, string(raw)))
		return e
	}); e != nil {
		t.Fatal("candidate schema transaction failed", e)
	}
	// Product migrations run before serving. Discard prepared result descriptions
	// across a type-changing migration, as a restarted Control pool does.
	f.f.PostgreSQL.Reset()
}

func oidcProfileLegacySnapshot(t *testing.T, f sourceRepositoryFixture) [32]byte {
	t.Helper()
	var raw []byte
	query := `SELECT jsonb_build_object(
 'connectors',(SELECT jsonb_agg(to_jsonb(c)-'issuer' ORDER BY c.id) FROM sso_connectors c),
 'bindings',(SELECT jsonb_agg(to_jsonb(b)-'provider' ORDER BY b.id) FROM sso_bindings b),
 'flows',(SELECT jsonb_agg(to_jsonb(f)-'provider'-'oidc_nonce_hash' ORDER BY f.id) FROM sso_flows f),
 'intents',(SELECT jsonb_agg(to_jsonb(i) ORDER BY i.tenant_id,i.actor_user_id,i.idempotency_key) FROM sso_mutation_intents i))`
	if e := f.f.Store.QueryRow(f.ctx, transaction.SQL(transaction.PostgreSQL, query)).Scan(&raw); e != nil {
		t.Fatal("committed legacy snapshot unavailable", e)
	}
	defer clear(raw)
	return sha256.Sum256(raw)
}

func expectOIDCProfileSQLState(t *testing.T, f sourceRepositoryFixture, code, statement string, args ...any) {
	t.Helper()
	e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		_, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, statement, args...))
		return e
	})
	var native *pgconn.PgError
	if !errors.As(e, &native) || native.Code != code {
		t.Fatal("candidate did not enforce the expected PostgreSQL constraint", code)
	}
}

func TestPostgresOIDCProfileSchemaPreservesGiteaAuthority(t *testing.T) {
	f := catalogRepositoryFixture(t)
	requireOIDCProfilePostgres(t, f)
	local := catalogAuth(t, f, f.f.User, "admin", nil)
	sourced := catalogAuth(t, f, f.f.User, "admin", &f.source)
	input, key := connectorInput(f), uuid.NewString()
	created, e := f.s.CreateConnector(f.ctx, local, input, key, uuid.NewString())
	if e != nil {
		t.Fatal("legacy connector creation failed", e)
	}
	pending, e := f.s.Start(f.ctx, f.source.ConnectorID, "portal", "", nil, StartInput{Mode: ModeLogin}, uuid.NewString())
	if e != nil || pending.BrowserToken == "" {
		t.Fatal("legacy protected start failed", e)
	}
	before := oidcProfileLegacySnapshot(t, f)
	for _, direction := range []string{"up", "down", "up"} {
		applyOIDCProfileSchema(t, f, direction)
		if oidcProfileLegacySnapshot(t, f) != before {
			t.Fatal("profile migration changed retained legacy authority or encrypted data")
		}
		reopened, e := New(f.accounts)
		if e != nil {
			t.Fatal("legacy secret inventory did not survive schema restart", e)
		}
		reopened.Close()
		replayed, e := f.s.CreateConnector(f.ctx, local, input, key, uuid.NewString())
		if e != nil || !sameConnector(created, replayed) || connectorCounts(t, f, input.ID, key) != ([3]int{1, 1, 1}) {
			t.Fatal("retained connector intent replay changed after schema restart", e)
		}
		page, e := f.s.ListConnectors(f.ctx, local, 100, "")
		if e != nil || len(page.Items) != 2 {
			t.Fatal("legacy connector catalog changed after schema restart", e)
		}
		bindings, e := f.s.ListBindings(f.ctx, local)
		if e != nil || len(bindings) != 1 || bindings[0].Subject != "9007199254740993" {
			t.Fatal("legacy numeric binding changed after schema restart", e)
		}
		assertConnectorAuth(t, f, local, true)
		assertConnectorAuth(t, f, sourced, true)
	}
	fresh, e := f.s.Start(f.ctx, f.source.ConnectorID, "portal", "", nil, StartInput{Mode: ModeLogin}, uuid.NewString())
	if e != nil || fresh.AuthorizationURL == "" || fresh.BrowserToken == pending.BrowserToken {
		t.Fatal("legacy start no longer persists distinct protected authority", e)
	}
	var revision int64
	if e = f.f.Store.QueryRow(f.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT revision FROM sso_connectors WHERE id=$1`, f.source.ConnectorID)).Scan(&revision); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.UpdateConnector(f.ctx, local, f.source.ConnectorID, UpdateConnector{Revision: revision, Name: "Disabled after schema upgrade", Enabled: false, Reason: "Qualify current source revocation after profile upgrade"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("legacy disable failed after schema upgrade", e)
	}
	assertConnectorAuth(t, f, sourced, false)
	assertConnectorAuth(t, f, local, true)
}

func oidcProfileConnector(t *testing.T, f sourceRepositoryFixture) string {
	t.Helper()
	id := uuid.NewString()
	sealed, e := f.s.keys.seal(secretScope{f.f.Tenant, id, id, clientSecretPurpose}, []byte(security.Token()))
	if e != nil {
		t.Fatal(e)
	}
	f.exec(t, transaction.SQL(transaction.PostgreSQL, `INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,issuer,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by) VALUES($1,$2,'OIDC storage candidate','oidc',$3,$4,'opaque:client+identifier',$5,$6,$7,true,$8)`, id, f.f.Tenant, f.accounts.Config.SSO.Providers[0].Origin, f.accounts.Config.SSO.Providers[0].Origin+"/realms/private", sealed.keyID, sealed.nonce, sealed.ciphertext, f.f.User))
	return id
}

func oidcProfileFlowStatement(t *testing.T, f sourceRepositoryFixture, id, connector string, nonce []byte) transaction.Statement {
	t.Helper()
	sealed, e := f.s.keys.seal(secretScope{f.f.Tenant, connector, id, flowVerifierPurpose}, []byte(security.Token()))
	if e != nil {
		t.Fatal("protected OIDC verifier fixture failed", e)
	}
	return transaction.SQL(transaction.PostgreSQL, `INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,expires_at,provider,oidc_nonce_hash) VALUES($1,$2,$3,1,1,'portal','login',$4,$5,$6,$7,$8,$9,clock_timestamp()+interval '5 minutes','oidc',$10)`, id, f.f.Tenant, connector, security.Digest(security.Token()), security.Digest(security.Token()), security.Digest("anonymous"), sealed.keyID, sealed.nonce, sealed.ciphertext, nonce)
}

func TestPostgresOIDCProfileSchemaEnforcesOpaqueIdentityAndNonce(t *testing.T) {
	f := catalogRepositoryFixture(t)
	requireOIDCProfilePostgres(t, f)
	applyOIDCProfileSchema(t, f, "up")
	connector := oidcProfileConnector(t, f)
	insertBinding := `INSERT INTO sso_bindings(id,tenant_id,connector_id,user_id,subject,provider) VALUES($1,$2,$3,$4,$5,$6)`
	for _, subject := range []string{"Opaque:Subject_001", "opaque:Subject_001", "00001", strings.Repeat("s", 255)} {
		f.exec(t, transaction.SQL(transaction.PostgreSQL, insertBinding, uuid.NewString(), f.f.Tenant, connector, f.f.User, subject, "oidc"))
	}
	for name, test := range map[string]struct{ subject, code string }{"empty": {"", "23514"}, "non_ascii": {"\u4e2d", "23514"}, "oversized": {strings.Repeat("s", 256), "22001"}} {
		t.Run(name, func(t *testing.T) {
			expectOIDCProfileSQLState(t, f, test.code, insertBinding, uuid.NewString(), f.f.Tenant, connector, f.f.User, test.subject, "oidc")
		})
	}
	foreign := uuid.NewString()
	f.exec(t, transaction.SQL(transaction.PostgreSQL, `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign OIDC schema tenant','active')`, foreign))
	expectOIDCProfileSQLState(t, f, "23503", insertBinding, uuid.NewString(), foreign, connector, f.f.User, "foreign", "oidc")
	expectOIDCProfileSQLState(t, f, "23503", insertBinding, uuid.NewString(), f.f.Tenant, connector, f.f.User, "123", "gitea")
	expectOIDCProfileSQLState(t, f, "P0001", `UPDATE sso_connectors SET issuer=$1,revision=revision+1,auth_version=auth_version+1 WHERE id=$2`, f.accounts.Config.SSO.Providers[0].Origin+"/other", connector)
	nonce := security.Digest(security.Token())
	id := uuid.NewString()
	f.exec(t, oidcProfileFlowStatement(t, f, id, connector, nonce))
	reused := oidcProfileFlowStatement(t, f, uuid.NewString(), connector, nonce)
	e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error { _, e := q.Exec(ctx, reused); return e })
	var native *pgconn.PgError
	if !errors.As(e, &native) || native.Code != "23505" {
		t.Fatal("OIDC nonce reuse was accepted")
	}
	expectOIDCProfileSQLState(t, f, "P0001", `UPDATE sso_flows SET state='claimed',claim_id=$1,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL,oidc_nonce_hash=$2 WHERE id=$3`, uuid.NewString(), security.Digest(security.Token()), id)
	raw, e := os.ReadFile(filepath.Join("testdata", "oidc_profile.down.sql"))
	if e != nil {
		t.Fatal(e)
	}
	expectOIDCProfileSQLState(t, f, "P0001", string(raw))
	var retained bool
	if e = f.f.Store.QueryRow(f.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT EXISTS(SELECT 1 FROM sso_flows WHERE id=$1 AND provider='oidc' AND oidc_nonce_hash=$2 AND state='pending')`, id, nonce)).Scan(&retained); e != nil || !retained {
		t.Fatal("refused downgrade changed protected OIDC state", e)
	}
}

func TestPostgresOIDCProfileNonceRaceCommitsOneFlow(t *testing.T) {
	f := catalogRepositoryFixture(t)
	requireOIDCProfilePostgres(t, f)
	applyOIDCProfileSchema(t, f, "up")
	connector := oidcProfileConnector(t, f)
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	observer, e := pgx.ConnectConfig(ctx, f.f.PostgreSQL.Config().ConnConfig.Copy())
	if e != nil {
		t.Fatal("independent nonce observer connection unavailable")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = observer.Close(cleanup)
	}()
	nonce := security.Digest(security.Token())
	first := oidcProfileFlowStatement(t, f, uuid.NewString(), connector, nonce)
	second := oidcProfileFlowStatement(t, f, uuid.NewString(), connector, nonce)
	held, release := make(chan struct{}), make(chan struct{})
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	pidReady := make(chan int32, 1)
	var workers sync.WaitGroup
	var released sync.Once
	defer func() { released.Do(func() { close(release) }); cancel(); workers.Wait() }()
	workers.Add(1)
	go func() {
		defer workers.Done()
		firstDone <- f.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			if _, e := q.Exec(ctx, first); e != nil {
				return e
			}
			close(held)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-held:
	case e := <-firstDone:
		t.Fatal("first nonce contender did not acquire its row", e)
	case <-ctx.Done():
		t.Fatal("first nonce contender exceeded its bound")
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		secondDone <- f.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var pid int32
			if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT pg_backend_pid()`)).Scan(&pid); e != nil {
				return e
			}
			pidReady <- pid
			_, e := q.Exec(ctx, second)
			return e
		})
	}()
	var pid int32
	select {
	case pid = <-pidReady:
	case e := <-secondDone:
		t.Fatal("second nonce contender did not enter its transaction", e)
	case <-ctx.Done():
		t.Fatal("second nonce contender exceeded its bound")
	}
	for {
		var blocked bool
		if e := observer.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND state='active' AND wait_event_type='Lock')`, pid).Scan(&blocked); e != nil {
			t.Fatal("actual nonce wait observation failed", e)
		}
		if blocked {
			break
		}
		select {
		case e := <-secondDone:
			t.Fatal("second nonce insert escaped the actual unique-key fence", e)
		case <-ctx.Done():
			t.Fatal("nonce wait observation exceeded its bound")
		case <-time.After(10 * time.Millisecond):
		}
	}
	released.Do(func() { close(release) })
	if e := <-firstDone; e != nil {
		t.Fatal("first nonce flow did not commit", e)
	}
	e = <-secondDone
	var native *pgconn.PgError
	if !errors.As(e, &native) || native.Code != "23505" {
		t.Fatal("second nonce flow did not retain its unique refusal")
	}
	var count int
	if e = f.f.Store.QueryRow(f.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT count(*) FROM sso_flows WHERE tenant_id=$1 AND connector_id=$2 AND oidc_nonce_hash=$3`, f.f.Tenant, connector, nonce)).Scan(&count); e != nil || count != 1 {
		t.Fatal("nonce race committed duplicate or missing authority", e)
	}
}
