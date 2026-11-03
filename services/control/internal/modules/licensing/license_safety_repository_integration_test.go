//go:build repository_integration

package licensing_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

func pairedLicensePeer(t *testing.T, f pairedLicenseScenario) identity.Auth {
	t.Helper()
	id := uuid.NewString()
	email := "synthetic+" + id + "@example.invalid"
	pairedLicenseExec(t, f, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Synthetic peer Admin','en','active',true)`, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Synthetic peer Admin','en','active',true)`, id, email, email)
	return pairedLicenseAuth(t, f, id, "admin")
}
func pairedLicenseObserved(t *testing.T, f pairedLicenseScenario) time.Time {
	t.Helper()
	var value time.Time
	if e := f.fixture.Store.QueryRow(f.ctx, f.fixture.Statement(`SELECT observed_at FROM platform_license_state WHERE id=1`, `SELECT observed_at FROM platform_license_state WHERE id=1`)).Scan(&value); e != nil {
		t.Fatal(e)
	}
	return value
}
func pairedLicenseSnapshot(t *testing.T, f pairedLicenseScenario) string {
	t.Helper()
	raw, _ := json.Marshal([]any{pairedLicenseStored(t, f), pairedLicenseObserved(t, f), pairedLicenseCount(t, f, "license.imported"), pairedLicenseCount(t, f, "license.import_rejected"), pairedLicenseCount(t, f, "license.diagnostics_exported")})
	return wire.Digest(raw)
}
func TestActualPairedLicenseCurrentAuthorityRefusals(t *testing.T) {
	for _, kind := range []string{"revoked_session", "demoted_admin", "disabled_user", "wrong_token", "wrong_user", "portal_audience"} {
		t.Run(kind, func(t *testing.T) {
			f := pairedLicenseFixture(t)
			a := f.admin
			want := "AUTH_SESSION_EXPIRED"
			switch kind {
			case "revoked_session":
				pairedLicenseExec(t, f, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_license_test' WHERE id=$1`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_license_test' WHERE id=?`, a.SessionID)
			case "demoted_admin":
				pairedLicenseExec(t, f, `UPDATE users SET is_platform_admin=false WHERE id=$1`, `UPDATE users SET is_platform_admin=false WHERE id=?`, a.Me.User.ID)
			case "disabled_user":
				pairedLicenseExec(t, f, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=?`, a.Me.User.ID)
			case "wrong_token":
				a.TokenHash = append([]byte(nil), a.TokenHash...)
				a.TokenHash[0] ^= 1
			case "wrong_user":
				peer := pairedLicensePeer(t, f)
				a.Me.User.ID = peer.Me.User.ID
			case "portal_audience":
				a = pairedLicenseAuth(t, f, f.fixture.User, "portal")
				want = "ACCESS_DENIED"
			}
			before := pairedLicenseSnapshot(t, f)
			f.clock.set(f.base.Add(time.Second))
			state, e := f.service.StatusAdmin(f.ctx, a)
			expect(t, e, want)
			if state.DeploymentID != "" {
				t.Fatal("refused actor obtained private state")
			}
			result, e := f.service.Import(f.ctx, a, input(t, f.scenario, claims(f.scenario), ""), uuid.NewString())
			expect(t, e, want)
			if result.ImportedLicenseID != "" {
				t.Fatal("refused actor obtained an import receipt")
			}
			download, e := f.service.Diagnostics(f.ctx, a, uuid.NewString())
			expect(t, e, want)
			if len(download.Bytes) != 0 || pairedLicenseSnapshot(t, f) != before {
				t.Fatal("refused actor changed License observation/history/audit")
			}
		})
	}
}

type pairedLicenseSource struct {
	provider  *sso.Service
	connector sso.Connector
	actor     identity.Auth
	source    identity.SessionSource
}

func pairedLicenseSourcedActor(t *testing.T, f pairedLicenseScenario) pairedLicenseSource {
	t.Helper()
	importLicense(t, f.scenario, claims(f.scenario), "")
	directory := t.TempDir()
	if e := os.Chmod(directory, 0700); e != nil {
		t.Fatal(e)
	}
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]any{"schema_version": 1, "active_key_id": "synthetic", "keys": map[string]string{"synthetic": base64.StdEncoding.EncodeToString(key)}})
	path := filepath.Join(directory, "sso-keyring.json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	f.owner.HMACKey = make([]byte, 32)
	if _, e := rand.Read(f.owner.HMACKey); e != nil {
		t.Fatal(e)
	}
	f.owner.Config.Server.PortalOrigin, f.owner.Config.Server.AdminOrigin = "https://portal.example.invalid", "https://admin.example.invalid"
	f.owner.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: path, Providers: []config.SSOProvider{{Origin: "https://provider.example.invalid", Issuer: "https://provider.example.invalid", DialAddresses: []string{"127.0.0.1"}}}}
	provider, e := sso.New(f.owner)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(provider.Close)
	f.owner.Sources = provider
	connector, e := provider.CreateConnector(f.ctx, f.admin, sso.CreateConnector{ID: uuid.NewString(), Name: "Synthetic License source \u4e16\u754c", Provider: sso.ProviderGitea, Origin: "https://provider.example.invalid", ClientID: uuid.NewString(), ClientSecret: uuid.NewString() + uuid.NewString(), Enabled: true, Reason: "Isolated License source prerequisite"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("actual encrypted connector prerequisite", e)
	}
	source := identity.SessionSource{TenantID: f.fixture.Tenant, ConnectorID: connector.ID, BindingID: uuid.NewString(), FlowID: uuid.NewString(), AuthVersion: 1}
	pairedLicenseExec(t, f, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES($1,$2,$3,'9007199254740993',$4)`, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES(?,?,?,'9007199254740993',?)`, source.BindingID, source.TenantID, source.ConnectorID, f.fixture.User)
	state, browser, cookie := sha256.Sum256([]byte(uuid.NewString())), sha256.Sum256([]byte(uuid.NewString())), sha256.Sum256([]byte(uuid.NewString()))
	// Explicit synthetic already-authenticated provider history. No provider call
	// or PKCE decryption is claimed;real transitions/FKs/source checks stay enabled.
	pairedLicenseExec(t, f, `INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,expires_at) VALUES($1,$2,$3,1,1,'admin','login',$4,$5,$6,'synthetic',$7,$8,clock_timestamp()+interval '1 hour')`, `INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,expires_at) VALUES(?,?,?,1,1,'admin','login',?,?,?,'synthetic',?,?,TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)))`, source.FlowID, source.TenantID, source.ConnectorID, state[:], browser[:], cookie[:], make([]byte, 12), make([]byte, 59))
	pairedLicenseExec(t, f, `UPDATE sso_flows SET state='claimed',claim_id=$1,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$2`, `UPDATE sso_flows SET state='claimed',claim_id=?,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=?`, uuid.NewString(), source.FlowID)
	pairedLicenseExec(t, f, `UPDATE sso_flows SET state='succeeded',authenticated_user_id=$1,finished_at=clock_timestamp() WHERE id=$2`, `UPDATE sso_flows SET state='succeeded',authenticated_user_id=?,finished_at=UTC_TIMESTAMP(6) WHERE id=?`, f.fixture.User, source.FlowID)
	var issued identity.IssuedSession
	e = f.fixture.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = f.owner.IssueSessionWithin(ctx, q, f.fixture.User, 1, "admin", "", uuid.NewString(), &source)
		return e
	})
	if e != nil {
		t.Fatal("actual sourced License session issuance", e)
	}
	actor, e := f.owner.Authenticate(f.ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	return pairedLicenseSource{provider, connector, actor, source}
}
func TestActualPairedLicenseSourceRevocationAndLocalRecovery(t *testing.T) {
	f := pairedLicenseFixture(t)
	source := pairedLicenseSourcedActor(t, f)
	state, e := f.service.StatusAdmin(f.ctx, source.actor)
	if e != nil || state.State != licensing.Active {
		t.Fatal("actual current sourced License authority", e)
	}
	_, e = source.provider.UpdateConnector(f.ctx, f.admin, source.connector.ID, sso.UpdateConnector{Revision: 1, Name: source.connector.Name, Enabled: false, Reason: "Isolated source revocation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	before := pairedLicenseSnapshot(t, f)
	_, e = f.service.StatusAdmin(f.ctx, source.actor)
	expect(t, e, "AUTH_SESSION_EXPIRED")
	if pairedLicenseSnapshot(t, f) != before {
		t.Fatal("revoked source changed License state")
	}
	if _, e := f.service.StatusAdmin(f.ctx, f.admin); e != nil {
		t.Fatal("local administrator recovery after sourced revocation", e)
	}
}

type licensePauseStore struct {
	transaction.Store
	calls                      atomic.Int32
	revokerReady, licenseReady chan struct{}
	release                    <-chan struct{}
}
type licensePauseExecutor struct {
	transaction.Executor
	owner     *licensePauseStore
	operation int32
	queries   int
}
type licensePauseRow struct {
	transaction.Row
	owner *licensePauseStore
	ctx   context.Context
}

func (s *licensePauseStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	operation := s.calls.Add(1)
	return s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, &licensePauseExecutor{Executor: q, owner: s, operation: operation})
	})
}
func (q *licensePauseExecutor) QueryRow(ctx context.Context, statement transaction.Statement) transaction.Row {
	q.queries++
	if q.operation == 2 && q.queries == 3 {
		select {
		case q.owner.licenseReady <- struct{}{}:
		case <-ctx.Done():
			return licenseFaultRow{&transaction.Error{Kind: transaction.Timeout}}
		}
	}
	row := q.Executor.QueryRow(ctx, statement)
	if q.operation == 1 && q.queries == 4 {
		return licensePauseRow{row, q.owner, ctx}
	}
	return row
}
func (r licensePauseRow) Scan(dest ...any) error {
	if e := r.Row.Scan(dest...); e != nil {
		return e
	}
	select {
	case r.owner.revokerReady <- struct{}{}:
	case <-r.ctx.Done():
		return &transaction.Error{Kind: transaction.Timeout}
	}
	select {
	case <-r.owner.release:
		return nil
	case <-r.ctx.Done():
		return &transaction.Error{Kind: transaction.Timeout}
	}
}

type licenseFaultRow struct{ error }

func (r licenseFaultRow) Scan(...any) error { return r.error }
func TestActualPairedLicenseSourceRevocationOrderingAvoidsSessionCycle(t *testing.T) {
	f := pairedLicenseFixture(t)
	source := pairedLicenseSourcedActor(t, f)
	peer := pairedLicensePeer(t, f)
	release := make(chan struct{})
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	defer unlock()
	observed := &licensePauseStore{Store: f.fixture.Store, revokerReady: make(chan struct{}, 1), licenseReady: make(chan struct{}, 1), release: release}
	f.owner.Storage = observed
	defer func() { f.owner.Storage = f.fixture.Store }()
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	revoked := make(chan error, 1)
	go func() {
		_, e := source.provider.UpdateConnector(ctx, peer, source.connector.ID, sso.UpdateConnector{Revision: 1, Name: source.connector.Name, Enabled: false, Reason: "Actual concurrent License source revocation"}, uuid.NewString(), uuid.NewString())
		revoked <- e
	}()
	select {
	case <-observed.revokerReady:
	case e := <-revoked:
		t.Fatal("actual revoker did not hold connector", e)
	case <-ctx.Done():
		t.Fatal("revoker lock deadline")
	}
	denied := make(chan error, 1)
	go func() { _, e := f.service.StatusAdmin(ctx, source.actor); denied <- e }()
	select {
	case <-observed.licenseReady:
	case e := <-denied:
		t.Fatal("License did not reach source fence before session lock", e)
	case <-ctx.Done():
		t.Fatal("source fence observation deadline")
	}
	unlock()
	select {
	case e := <-revoked:
		if e != nil {
			t.Fatal("real revoker blocked behind License session", e)
		}
	case <-ctx.Done():
		t.Fatal("source revocation/session cycle")
	}
	select {
	case e := <-denied:
		expect(t, e, "AUTH_SESSION_EXPIRED")
	case <-ctx.Done():
		t.Fatal("License did not reject committed revocation")
	}
	f.owner.Storage = f.fixture.Store
	if _, e := f.service.StatusAdmin(f.ctx, f.admin); e != nil {
		t.Fatal("local recovery after actual source ordering", e)
	}
}

func TestActualPairedLicenseCompetingRenewalsAndConfiguredCapacity(t *testing.T) {
	t.Run("competing_renewals", func(t *testing.T) {
		f := pairedLicenseFixture(t)
		first := claims(f.scenario)
		importLicense(t, f.scenario, first, "")
		peer := pairedLicensePeer(t, f)
		actors := []identity.Auth{f.admin, peer}
		inputs := []licensing.ImportInput{}
		for range 2 {
			next := first
			next.LicenseID = uuid.NewString()
			next.ReplacesLicenseID = first.LicenseID
			next.ExpiresAt = f.base.Add(30 * time.Second).Format(time.RFC3339)
			next.GraceUntil = f.base.Add(40 * time.Second).Format(time.RFC3339)
			inputs = append(inputs, input(t, f.scenario, next, first.LicenseID))
		}
		type outcome struct {
			value licensing.ImportResult
			e     error
		}
		ready := make(chan struct{})
		done := make(chan outcome, 2)
		for index := range 2 {
			go func() {
				<-ready
				value, e := f.service.Import(f.ctx, actors[index], inputs[index], uuid.NewString())
				done <- outcome{value, e}
			}()
		}
		close(ready)
		success, denials := 0, 0
		for range 2 {
			select {
			case result := <-done:
				if result.e == nil {
					success++
					if result.value.Repeated {
						t.Fatal("competing new renewal was reported repeated")
					}
				} else {
					expect(t, result.e, "LICENSE_CURRENT_CHANGED")
					denials++
				}
			case <-f.ctx.Done():
				t.Fatal("competing actual License renewal deadline")
			}
		}
		if success != 1 || denials != 1 || pairedLicenseCount(t, f, "license.imported") != 2 || pairedLicenseCount(t, f, "license.import_rejected") != 1 {
			t.Fatal("competing renewal lost history or denial audit")
		}
	})
	t.Run("configured_capacity", func(t *testing.T) {
		f := pairedLicenseFixture(t)
		policy := wire.DefaultPolicy()
		policy.MaximumImports = 1
		limited, e := licensing.New(f.owner, f.trust, policy, f.clock.now)
		if e != nil {
			t.Fatal(e)
		}
		first := claims(f.scenario)
		if _, e := limited.Import(f.ctx, f.admin, input(t, f.scenario, first, ""), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		before := pairedLicenseStored(t, f)
		next := first
		next.LicenseID = uuid.NewString()
		next.ReplacesLicenseID = first.LicenseID
		next.ExpiresAt = f.base.Add(30 * time.Second).Format(time.RFC3339)
		next.GraceUntil = f.base.Add(40 * time.Second).Format(time.RFC3339)
		_, e = limited.Import(f.ctx, f.admin, input(t, f.scenario, next, first.LicenseID), uuid.NewString())
		expect(t, e, "LICENSE_IMPORT_CAPACITY_REQUIRED")
		if pairedLicenseStored(t, f) != before || pairedLicenseCount(t, f, "license.imported") != 1 || pairedLicenseCount(t, f, "license.import_rejected") != 1 {
			t.Fatal("nondefault License capacity changed accepted history")
		}
	})
}

type licenseExecFaultStore struct {
	transaction.Store
	failAt, observeAt int
	ready             chan struct{}
}
type licenseExecFaultExecutor struct {
	transaction.Executor
	owner  licenseExecFaultStore
	writes int
}

func (s licenseExecFaultStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, &licenseExecFaultExecutor{Executor: q, owner: s})
	})
}
func (q *licenseExecFaultExecutor) Exec(ctx context.Context, statement transaction.Statement) (int64, error) {
	q.writes++
	if q.writes == q.owner.observeAt {
		select {
		case q.owner.ready <- struct{}{}:
		case <-ctx.Done():
			return 0, &transaction.Error{Kind: transaction.Cancelled}
		}
	}
	if q.writes == q.owner.failAt {
		return 0, errors.New("IDENTIFIED_TEST_ONLY_LICENSE_SQL_FAULT")
	}
	return q.Executor.Exec(ctx, statement)
}
func TestActualPairedLicenseAtomicSQLAuditAndDenialRollback(t *testing.T) {
	for index, name := range []string{"observation", "history", "success_audit", "second_observation", "denial_audit", "diagnostics_audit"} {
		t.Run(name, func(t *testing.T) {
			f := pairedLicenseFixture(t)
			before := pairedLicenseSnapshot(t, f)
			f.clock.set(f.base.Add(time.Second))
			failAt := index + 1
			if name == "denial_audit" || name == "diagnostics_audit" {
				failAt = 2
			}
			f.owner.Storage = licenseExecFaultStore{Store: f.fixture.Store, failAt: failAt}
			defer func() { f.owner.Storage = f.fixture.Store }()
			c := claims(f.scenario)
			if name == "denial_audit" {
				c.DeploymentID = uuid.NewString()
			}
			if name == "diagnostics_audit" {
				download, e := f.service.Diagnostics(f.ctx, f.admin, uuid.NewString())
				expect(t, e, "STORAGE_UNAVAILABLE")
				if len(download.Bytes) != 0 {
					t.Fatal("failed diagnostics returned bytes")
				}
			} else {
				value, e := f.service.Import(f.ctx, f.admin, input(t, f.scenario, c, ""), uuid.NewString())
				expect(t, e, "STORAGE_UNAVAILABLE")
				if value.ImportedLicenseID != "" {
					t.Fatal("failed import returned receipt")
				}
			}
			if pairedLicenseSnapshot(t, f) != before {
				t.Fatal("SQL/final-audit failure escaped actual rollback")
			}
			f.owner.Storage = f.fixture.Store
			if name == "diagnostics_audit" {
				if _, e := f.service.Diagnostics(f.ctx, f.admin, uuid.NewString()); e != nil || pairedLicenseCount(t, f, "license.diagnostics_exported") != 1 {
					t.Fatal("fresh diagnostics recovery", e)
				}
			} else {
				_, e := f.service.Import(f.ctx, f.admin, input(t, f.scenario, c, ""), uuid.NewString())
				if name == "denial_audit" {
					expect(t, e, "LICENSE_DEPLOYMENT_MISMATCH")
					if pairedLicenseCount(t, f, "license.import_rejected") != 1 {
						t.Fatal("fresh denial recovery lost audit")
					}
				} else if e != nil || pairedLicenseCount(t, f, "license.imported") != 1 {
					t.Fatal("fresh import recovery", e)
				}
			}
		})
	}
}

func pairedLicenseHold(t *testing.T, f pairedLicenseScenario, statement transaction.Statement) func() {
	t.Helper()
	// Cleanup must still join the actual transaction after t.Context is cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	ready, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- f.fixture.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var id string
			if e := q.QueryRow(ctx, statement).Scan(&id); e != nil {
				return e
			}
			close(ready)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	var once sync.Once
	unlock := func() {
		once.Do(func() {
			close(release)
			select {
			case e := <-done:
				if e != nil && ctx.Err() == nil {
					t.Error("actual License lock holder", e)
				}
			case <-ctx.Done():
				t.Error("License holder failed to join")
			}
			cancel()
		})
	}
	t.Cleanup(unlock)
	select {
	case <-ready:
	case e := <-done:
		cancel()
		t.Fatal("actual License hold startup", e)
	case <-ctx.Done():
		t.Fatal("actual License hold startup deadline")
	}
	return unlock
}
func TestActualPairedLicenseCancellationRollsBackAndRecovers(t *testing.T) {
	f := pairedLicenseFixture(t)
	before := pairedLicenseSnapshot(t, f)
	f.clock.set(f.base.Add(time.Second))
	unlock := pairedLicenseHold(t, f, f.fixture.Statement(`SELECT id::text FROM tenants WHERE id=$1 FOR UPDATE`, `SELECT id FROM tenants WHERE id=? FOR UPDATE`, f.fixture.Tenant))
	ready := make(chan struct{}, 1)
	f.owner.Storage = licenseExecFaultStore{Store: f.fixture.Store, observeAt: 3, ready: ready}
	defer func() { f.owner.Storage = f.fixture.Store }()
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done := make(chan error, 1)
	c := claims(f.scenario)
	attempt := input(t, f.scenario, c, "")
	go func() { _, e := f.service.Import(ctx, f.admin, attempt, uuid.NewString()); done <- e }()
	select {
	case <-ready:
	case e := <-done:
		t.Fatal("actual writes did not reach held audit FK", e)
	case <-f.ctx.Done():
		t.Fatal("License audit observation deadline")
	}
	cancel()
	select {
	case e := <-done:
		expect(t, e, "STORAGE_CANCELLED")
	case <-time.After(3 * time.Second):
		t.Fatal("actual License cancellation did not join")
	}
	if pairedLicenseSnapshot(t, f) != before {
		t.Fatal("cancelled import committed history/clock/audit")
	}
	unlock()
	f.owner.Storage = f.fixture.Store
	if _, e := f.service.Import(f.ctx, f.admin, attempt, uuid.NewString()); e != nil || pairedLicenseCount(t, f, "license.imported") != 1 {
		t.Fatal("fresh import after actual cancellation", e)
	}
}

type licenseDeadlineStore struct {
	transaction.Store
	seen chan time.Duration
}

func (s licenseDeadlineStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("IDENTIFIED_TEST_MISSING_OWNED_DEADLINE")
	}
	s.seen <- time.Until(deadline)
	return s.Store.Within(ctx, readOnly, fn)
}
func TestActualPairedLicenseOwnedBackgroundDeadlineAndShorterCaller(t *testing.T) {
	f := pairedLicenseFixture(t)
	policy := config.DefaultRuntimePolicy()
	policy.HTTP.ReadHeaderSeconds, policy.HTTP.RequestSeconds = 1, 1
	f.owner.Config.Runtime = &policy
	limited, e := licensing.New(f.owner, f.trust, wire.DefaultPolicy(), f.clock.now)
	if e != nil {
		t.Fatal(e)
	}
	before := pairedLicenseSnapshot(t, f)
	f.clock.set(f.base.Add(time.Second))
	unlock := pairedLicenseHold(t, f, f.fixture.Statement(`SELECT deployment_id::text FROM platform_license_state WHERE id=1 FOR UPDATE`, `SELECT deployment_id FROM platform_license_state WHERE id=1 FOR UPDATE`))
	seen := make(chan time.Duration, 2)
	f.owner.Storage = licenseDeadlineStore{Store: f.fixture.Store, seen: seen}
	defer func() { f.owner.Storage = f.fixture.Store }()
	for _, short := range []bool{false, true} {
		ctx := context.Background()
		cancel := func() {}
		maximum := 1100 * time.Millisecond
		if short {
			ctx, cancel = context.WithTimeout(f.ctx, 100*time.Millisecond)
			maximum = 150 * time.Millisecond
		}
		start := time.Now()
		_, e := limited.StatusAdmin(ctx, f.admin)
		cancel()
		if e == nil || (e.Error() != "STORAGE_TIMEOUT" && e.Error() != "STORAGE_LOCK_TIMEOUT") || time.Since(start) > 2*time.Second {
			t.Fatal("owned/shorter License request deadline missing", e)
		}
		if value := <-seen; value <= 0 || value > maximum {
			t.Fatal("transaction did not inherit the declared shorter deadline")
		}
	}
	if pairedLicenseSnapshot(t, f) != before {
		t.Fatal("timed-out License observation changed durable state")
	}
	unlock()
	f.owner.Storage = f.fixture.Store
	state, e := limited.StatusAdmin(f.ctx, f.admin)
	if e != nil || !state.ObservedAt.Equal(f.base.Add(time.Second)) {
		t.Fatal("fresh observation after bounded deadline", e)
	}
}
