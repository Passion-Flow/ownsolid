//go:build training_artifact_integration && model_lifecycle_admission_integration && training_operator_integration

package training_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

type cumulativeSSORecovery struct{ keyring, connector, binding, token, state, browser string }

// Provider output here is an explicit source fixture. Real Gitea consent/code
// validation is a separate actual-provider gate. Encryption, shared issuance,
// logical/file restore, current source checks and native serving are real.
func prepareCumulativeSSORecovery(t *testing.T, g preparationScenario) *cumulativeSSORecovery {
	t.Helper()
	accounts := g.f.s.Identity
	if !accounts.Config.SSORuntime() {
		return nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), config.Seconds(accounts.Config.Policy().SSO.OperationSeconds))
	defer cancel()
	private := t.TempDir()
	os.Chmod(private, 0700)
	keyring := filepath.Join(private, "source-sso-keyring.json")
	if e := sso.InitializeKeyring(keyring, ""); e != nil {
		t.Fatal(e)
	}
	accounts.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: keyring, Providers: []config.SSOProvider{{Origin: "https://provider.recovery.example.invalid", Issuer: "https://provider.recovery.example.invalid", DialAddresses: []string{"127.0.0.1"}}}}
	service, e := sso.New(accounts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(service.Close)
	accounts.Sources = service
	c, e := service.CreateConnector(ctx, g.f.admin, sso.CreateConnector{ID: uuid.NewString(), Name: "Cumulative restored source", Provider: "gitea", Origin: accounts.Config.SSO.Providers[0].Origin, ClientID: uuid.NewString(), ClientSecret: security.Token(), Enabled: true, Reason: "Synthetic matched restore source"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	binding := uuid.NewString()
	tenant, user := g.f.admin.Me.Workspace.TenantID, g.f.admin.Me.User.ID
	if _, e = accounts.DB.Exec(ctx, "INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES($1,$2,$3,'9007199254740993',$4)", binding, tenant, c.ID, user); e != nil {
		t.Fatal(e)
	}
	start, e := service.Start(ctx, c.ID, "admin", "", nil, sso.StartInput{Mode: "login"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	authorization, _ := url.Parse(start.AuthorizationURL)
	var flow string
	if e = accounts.DB.QueryRow(ctx, "SELECT id::text FROM sso_flows WHERE state_hash=$1", security.Digest(authorization.Query().Get("state"))).Scan(&flow); e != nil {
		t.Fatal(e)
	}
	if _, e = accounts.DB.Exec(ctx, "UPDATE sso_flows SET state='claimed',claim_id=$2,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$1", flow, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = accounts.DB.Exec(ctx, "UPDATE sso_flows SET state='succeeded',authenticated_user_id=$2,finished_at=clock_timestamp() WHERE id=$1", flow, user); e != nil {
		t.Fatal(e)
	}
	store, e := accounts.Repository()
	if e != nil {
		t.Fatal(e)
	}
	var issued identity.IssuedSession
	if e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := accounts.LockSessionUsersWithin(ctx, q, user, "", "admin"); e != nil {
			return e
		}
		var e error
		issued, e = accounts.IssueSessionWithin(ctx, q, user, g.f.admin.Epoch, "admin", "", uuid.NewString(), &identity.SessionSource{TenantID: tenant, ConnectorID: c.ID, BindingID: binding, AuthVersion: c.AuthVersion, FlowID: flow})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = accounts.Authenticate(ctx, issued.Token, "admin"); e != nil {
		t.Fatal(e)
	}
	pending, e := service.Start(ctx, c.ID, "portal", "", nil, sso.StartInput{Mode: "login"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	authorization, _ = url.Parse(pending.AuthorizationURL)
	return &cumulativeSSORecovery{keyring: keyring, connector: c.ID, binding: binding, token: issued.Token, state: authorization.Query().Get("state"), browser: pending.BrowserToken}
}

func copyCumulativeSSOKeyring(t *testing.T, state *cumulativeSSORecovery, backup string) {
	t.Helper()
	if state == nil {
		return
	}
	raw, e := os.ReadFile(state.keyring)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(backup, "sso-keyring.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
}

func restoreCumulativeSSOKeyring(t *testing.T, state *cumulativeSSORecovery, h nativeTrainingHarness, backup string) {
	t.Helper()
	if state == nil {
		return
	}
	private := t.TempDir()
	os.Chmod(private, 0700)
	keyring := filepath.Join(private, "restored-sso-keyring.json")
	raw, e := os.ReadFile(filepath.Join(backup, "sso-keyring.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(keyring, raw, 0600); e != nil {
		t.Fatal(e)
	}
	configRaw, e := os.ReadFile(h.Configuration)
	if e != nil {
		t.Fatal(e)
	}
	var values map[string]any
	if json.Unmarshal(configRaw, &values) != nil {
		t.Fatal("protected native configuration")
	}
	values["sso"].(map[string]any)["keyring_file"] = keyring
	configRaw, e = json.Marshal(values)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(h.Configuration, configRaw, 0600); e != nil {
		t.Fatal(e)
	}
	state.keyring = keyring
}

func verifyCumulativeSSORecovery(t *testing.T, state *cumulativeSSORecovery, g preparationScenario, pool *pgxpool.Pool) {
	t.Helper()
	if state == nil {
		return
	}
	accounts := *g.f.s.Identity
	accounts.DB = pool
	accounts.Storage = nil
	accounts.Config.SSO.KeyringFile = state.keyring
	service, e := sso.New(&accounts)
	if e != nil {
		t.Fatal("matching restored encrypted source", e)
	}
	defer service.Close()
	accounts.Sources = service
	ctx := t.Context()
	if _, e = accounts.Authenticate(ctx, state.token, "admin"); e == nil {
		t.Fatal("restored sourced session authenticated")
	}
	issued, _, e := service.Complete(ctx, state.connector, "portal", state.state, state.browser, "", "discarded-old-callback", false, uuid.NewString())
	if e == nil || issued.Token != "" {
		t.Fatal("restored callback regained exchange or session authority")
	}
	var retained, live int
	if e = pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM sso_bindings WHERE id=$1 AND connector_id=$2 AND subject='9007199254740993' AND active),(SELECT count(*) FROM sso_flows WHERE state IN ('pending','claimed') OR verifier_key_id IS NOT NULL)", state.binding, state.connector).Scan(&retained, &live); e != nil || retained != 1 || live != 0 {
		t.Fatal("restored source history/fence", retained, live, e)
	}
	fresh := fixture.Login(t, &accounts, "admin@example.invalid", g.f.password, "admin")
	bindings, e := service.ListBindings(ctx, fresh)
	if e != nil || len(bindings) != 1 || bindings[0].ID != state.binding {
		t.Fatal("fresh local recovery lost binding history", e)
	}
	wrong := filepath.Join(filepath.Dir(state.keyring), "wrong-keyring.json")
	if e = sso.InitializeKeyring(wrong, ""); e != nil {
		t.Fatal(e)
	}
	accounts.Config.SSO.KeyringFile = wrong
	if rejected, e := sso.New(&accounts); e == nil {
		rejected.Close()
		t.Fatal("wrong restored keyring accepted ciphertext")
	}
	accounts.Config.SSO.Enabled = false
	accounts.Config.SSO.KeyringFile = filepath.Join(filepath.Dir(wrong), "missing-disabled-keyring.json")
	disabled, e := sso.New(&accounts)
	if e != nil {
		t.Fatal("explicit disabled recovery required absent keys", e)
	}
	defer disabled.Close()
	accounts.Sources = disabled
	fixture.Login(t, &accounts, "admin@example.invalid", g.f.password, "admin")
	if _, e = accounts.Authenticate(ctx, state.token, "admin"); e == nil {
		t.Fatal("disabled recovery admitted sourced authority")
	}
	t.Log("Cumulative native four-store restore retained encrypted connector/binding/immutable subject; separately protected matching keyring; old sourced session/callback fenced; wrong key refused; fresh local and explicit missing-key recovery passed")
}
