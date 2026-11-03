//go:build repository_integration

package sso

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func connectorInput(f sourceRepositoryFixture) CreateConnector {
	return CreateConnector{ID: uuid.NewString(), Name: "Repository connector \u4e16\u754c", Provider: ProviderGitea, Origin: f.accounts.Config.SSO.Providers[0].Origin, ClientID: uuid.NewString(), ClientSecret: security.Token(), Enabled: true, Reason: "Synthetic paired connector creation"}
}

func connectorCounts(t *testing.T, f sourceRepositoryFixture, id, key string) [3]int {
	t.Helper()
	var counts [3]int
	for i, statement := range []transaction.Statement{
		f.f.Statement(`SELECT COUNT(*) FROM sso_connectors WHERE tenant_id=$1 AND id=$2`, `SELECT COUNT(*) FROM sso_connectors WHERE tenant_id=? AND id=?`, f.f.Tenant, id),
		f.f.Statement(`SELECT COUNT(*) FROM sso_mutation_intents WHERE tenant_id=$1 AND actor_user_id=$2 AND idempotency_key=$3`, `SELECT COUNT(*) FROM sso_mutation_intents WHERE tenant_id=? AND actor_user_id=? AND idempotency_key=?`, f.f.Tenant, f.f.User, key),
		f.f.Statement(`SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND target_type='sso_connector' AND target_id=$2 AND outcome='success' AND action IN('sso.connector_created','sso.connector_updated')`, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=? AND target_type='sso_connector' AND target_id=? AND outcome='success' AND action IN('sso.connector_created','sso.connector_updated')`, f.f.Tenant, id),
	} {
		if e := f.f.Store.QueryRow(f.ctx, statement).Scan(&counts[i]); e != nil {
			t.Fatal("actual connector/intent/audit count", e)
		}
	}
	return counts
}

func connectorCipher(t *testing.T, f sourceRepositoryFixture, id string) sealedSecret {
	t.Helper()
	var sealed sealedSecret
	if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT secret_key_id,secret_nonce,secret_ciphertext FROM sso_connectors WHERE tenant_id=$1 AND id=$2`, `SELECT secret_key_id,secret_nonce,secret_ciphertext FROM sso_connectors WHERE tenant_id=? AND id=?`, f.f.Tenant, id)).Scan(&sealed.keyID, &sealed.nonce, &sealed.ciphertext); e != nil {
		t.Fatal("actual encrypted connector row", e)
	}
	return sealed
}

func sameConnector(left, right Connector) bool {
	a, ae := json.Marshal(left)
	b, be := json.Marshal(right)
	return ae == nil && be == nil && bytes.Equal(a, b)
}

// These are privileged synthetic provider-claim transitions in the owned DB.
// They do not simulate a provider response or qualify an OAuth exchange.
func connectorFlow(t *testing.T, f sourceRepositoryFixture, connector, audience, state string) identity.SessionSource {
	t.Helper()
	var revision, version int64
	if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT revision,auth_version FROM sso_connectors WHERE tenant_id=$1 AND id=$2`, `SELECT revision,auth_version FROM sso_connectors WHERE tenant_id=? AND id=?`, f.f.Tenant, connector)).Scan(&revision, &version); e != nil {
		t.Fatal(e)
	}
	id := uuid.NewString()
	sealed, e := f.s.keys.seal(secretScope{f.f.Tenant, connector, id, flowVerifierPurpose}, []byte(security.Token()))
	if e != nil {
		t.Fatal(e)
	}
	var now time.Time
	if e = f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT clock_timestamp()`, `SELECT UTC_TIMESTAMP(6)`)).Scan(&now); e != nil {
		t.Fatal(e)
	}
	f.exec(t, f.f.Statement(
		`INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,'login',$7,$8,$9,$10,$11,$12,$13,$14)`,
		`INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES(?,?,?,?,?,?,'login',?,?,?,?,?,?,?,?)`, id, f.f.Tenant, connector, revision, version, audience, security.Digest(security.Token()), security.Digest(security.Token()), security.Digest("anonymous"), sealed.keyID, sealed.nonce, sealed.ciphertext, now, now.Add(time.Duration(f.s.policy.FlowSeconds)*time.Second)))
	if state == "claimed" || state == "succeeded" {
		f.exec(t, f.f.Statement(`UPDATE sso_flows SET state='claimed',claim_id=$1,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$2`, `UPDATE sso_flows SET state='claimed',claim_id=?,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=?`, uuid.NewString(), id))
	}
	if state == "succeeded" {
		f.exec(t, f.f.Statement(`UPDATE sso_flows SET state='succeeded',authenticated_user_id=$1,finished_at=clock_timestamp() WHERE id=$2`, `UPDATE sso_flows SET state='succeeded',authenticated_user_id=?,finished_at=UTC_TIMESTAMP(6) WHERE id=?`, f.f.User, id))
	}
	return identity.SessionSource{TenantID: f.f.Tenant, ConnectorID: connector, FlowID: id, AuthVersion: version}
}

func connectorBinding(t *testing.T, f sourceRepositoryFixture, connector string) string {
	t.Helper()
	id := uuid.NewString()
	f.exec(t, f.f.Statement(`INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES($1,$2,$3,$4,$5)`, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES(?,?,?,?,?)`, id, f.f.Tenant, connector, "9007199254740995", f.f.User))
	return id
}

func assertConnectorAuth(t *testing.T, f sourceRepositoryFixture, a identity.Auth, valid bool) {
	t.Helper()
	e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error { return f.accounts.RecheckWithin(ctx, q, a) })
	if valid && e != nil || !valid && (e == nil || e.Error() != "AUTH_SESSION_EXPIRED") {
		t.Fatal("actual source/ordinary authority outcome", e)
	}
}

func TestActualSSOConnectorCreateReplayAndEncryption(t *testing.T) {
	f := catalogRepositoryFixture(t)
	a := catalogAuth(t, f, f.f.User, "admin", nil)
	input, key := connectorInput(f), uuid.NewString()
	created, e := f.s.CreateConnector(f.ctx, a, input, key, uuid.NewString())
	if e != nil || created.ID != input.ID || created.ClientID != input.ClientID || created.Name != input.Name || !created.Enabled || created.Revision != 1 || created.AuthVersion != 1 || !connectorTimestamp(created.CreatedAt) || !connectorTimestamp(created.UpdatedAt) {
		t.Fatal("actual paired creation", e)
	}
	if counts := connectorCounts(t, f, input.ID, key); counts != ([3]int{1, 1, 1}) {
		t.Fatal("create resource/intent/audit did not share commit", counts)
	}
	sealed := connectorCipher(t, f, input.ID)
	plain, e := f.s.keys.open(secretScope{f.f.Tenant, input.ID, input.ID, clientSecretPurpose}, sealed)
	if e != nil || string(plain) != input.ClientSecret || bytes.Contains(sealed.ciphertext, []byte(input.ClientSecret)) {
		t.Fatal("actual encrypted-at-rest secret", e)
	}
	if _, e = f.s.keys.open(secretScope{uuid.NewString(), input.ID, input.ID, clientSecretPurpose}, sealed); e == nil {
		t.Fatal("stored secret lost tenant associated-data scope")
	}
	var raw []byte
	if e = f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT result FROM sso_mutation_intents WHERE tenant_id=$1 AND actor_user_id=$2 AND idempotency_key=$3`, `SELECT result FROM sso_mutation_intents WHERE tenant_id=? AND actor_user_id=? AND idempotency_key=?`, f.f.Tenant, f.f.User, key)).Scan(&raw); e != nil || bytes.Contains(raw, []byte(input.ClientSecret)) || bytes.Contains(raw, []byte("ciphertext")) {
		t.Fatal("intent exposed secret material", e)
	}
	replayed, e := f.s.CreateConnector(f.ctx, a, input, key, uuid.NewString())
	if e != nil || !sameConnector(created, replayed) || connectorCounts(t, f, input.ID, key) != ([3]int{1, 1, 1}) {
		t.Fatal("exact create replay changed data/audit/result", e)
	}
	changed := input
	changed.ClientSecret = security.Token()
	if out, e := f.s.CreateConnector(f.ctx, a, changed, key, uuid.NewString()); e == nil || e.Error() != "IDEMPOTENCY_CONFLICT" || out != (Connector{}) {
		t.Fatal("changed create intent accepted", e)
	}
	if out, e := f.s.CreateConnector(f.ctx, a, input, uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "SSO_CONNECTOR_ID_CONFLICT" || out != (Connector{}) {
		t.Fatal("duplicate global connector ID accepted", e)
	}
	if connectorCounts(t, f, input.ID, key) != ([3]int{1, 1, 1}) {
		t.Fatal("failed create left extra state")
	}
}

func TestActualSSOConnectorUpdateRotationRevocationAndRecovery(t *testing.T) {
	f := catalogRepositoryFixture(t)
	local := catalogAuth(t, f, f.f.User, "admin", nil)
	sourced := catalogAuth(t, f, f.f.User, "admin", &f.source)
	portalSource := connectorFlow(t, f, f.source.ConnectorID, "portal", "succeeded")
	portalSource.BindingID = f.source.BindingID
	portal := catalogAuth(t, f, f.f.User, "portal", &portalSource)
	otherID := catalogSeedConnector(t, f, f.f.Tenant, f.accounts.Config.SSO.Providers[0].Origin, "Other retained connector", true, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC))
	otherSource := connectorFlow(t, f, otherID, "portal", "succeeded")
	otherSource.BindingID = connectorBinding(t, f, otherID)
	other := catalogAuth(t, f, f.f.User, "portal", &otherSource)
	pending := connectorFlow(t, f, f.source.ConnectorID, "admin", "pending")
	claimed := connectorFlow(t, f, f.source.ConnectorID, "portal", "claimed")
	input := UpdateConnector{Revision: 1, Name: "Renamed \u4e16\u754c", Enabled: true, Reason: "Synthetic name revision"}
	key := uuid.NewString()
	updated, e := f.s.UpdateConnector(f.ctx, local, f.source.ConnectorID, input, key, uuid.NewString())
	if e != nil || updated.Revision != 2 || updated.AuthVersion != 1 || updated.Name != input.Name {
		t.Fatal("name-only revision/auth version", e)
	}
	for _, flow := range []string{pending.FlowID, claimed.FlowID} {
		var state, code string
		var cleared bool
		if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT state,error_code,(verifier_key_id IS NULL AND verifier_nonce IS NULL AND verifier_ciphertext IS NULL) FROM sso_flows WHERE id=$1`, `SELECT state,error_code,(verifier_key_id IS NULL AND verifier_nonce IS NULL AND verifier_ciphertext IS NULL) FROM sso_flows WHERE id=?`, flow)).Scan(&state, &code, &cleared); e != nil || state != "failed" || code != "SSO_FLOW_UNUSABLE" || !cleared {
			t.Fatal("pending/claimed verifier invalidation", e)
		}
	}
	assertConnectorAuth(t, f, sourced, true)
	assertConnectorAuth(t, f, portal, true)
	assertConnectorAuth(t, f, other, true)
	before := connectorCounts(t, f, updated.ID, key)
	replay, e := f.s.UpdateConnector(f.ctx, local, updated.ID, input, key, uuid.NewString())
	if e != nil || !sameConnector(updated, replay) || connectorCounts(t, f, updated.ID, key) != before {
		t.Fatal("name-only replay added a revision/intent/audit", e)
	}
	if out, e := f.s.UpdateConnector(f.ctx, local, updated.ID, input, uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "SSO_REVISION_CONFLICT" || out != (Connector{}) {
		t.Fatal("stale revision accepted", e)
	}
	client, secret := uuid.NewString(), security.Token()
	rotation := UpdateConnector{Revision: updated.Revision, Name: updated.Name, Enabled: true, ClientID: &client, ClientSecret: &secret, Reason: "Synthetic credential rotation"}
	rotated, e := f.s.UpdateConnector(f.ctx, local, updated.ID, rotation, uuid.NewString(), uuid.NewString())
	if e != nil || rotated.Revision != 3 || rotated.AuthVersion != 2 || rotated.ClientID != client {
		t.Fatal("actual credential rotation", e)
	}
	plain, e := f.s.keys.open(secretScope{f.f.Tenant, rotated.ID, rotated.ID, clientSecretPurpose}, connectorCipher(t, f, rotated.ID))
	if e != nil || string(plain) != secret {
		t.Fatal("rotated stored credential", e)
	}
	assertConnectorAuth(t, f, sourced, false)
	assertConnectorAuth(t, f, portal, false)
	assertConnectorAuth(t, f, other, true)
	assertConnectorAuth(t, f, local, true)
	newSource := connectorFlow(t, f, rotated.ID, "admin", "succeeded")
	newSource.BindingID = f.source.BindingID
	current := catalogAuth(t, f, f.f.User, "admin", &newSource)
	disable := UpdateConnector{Revision: rotated.Revision, Name: rotated.Name, Enabled: false, Reason: "Synthetic connector disable"}
	disabled, e := f.s.UpdateConnector(f.ctx, local, rotated.ID, disable, uuid.NewString(), uuid.NewString())
	if e != nil || disabled.Revision != 4 || disabled.AuthVersion != 3 || disabled.Enabled {
		t.Fatal("actual connector disable", e)
	}
	assertConnectorAuth(t, f, current, false)
	assertConnectorAuth(t, f, other, true)
	assertConnectorAuth(t, f, local, true)
	f.accounts.Config.SSO.Enabled = false
	f.accounts.Config.SSO.KeyringFile = "/missing-synthetic-disabled-keyring"
	recovery, e := New(f.accounts)
	if e != nil {
		t.Fatal("disabled recovery opened unavailable keys", e)
	}
	defer recovery.Close()
	f.accounts.Sources = recovery
	safe := UpdateConnector{Revision: disabled.Revision, Name: "Recovery disabled connector", Enabled: false, Reason: "Synthetic local emergency recovery"}
	result, e := recovery.UpdateConnector(f.ctx, local, disabled.ID, safe, uuid.NewString(), uuid.NewString())
	if e != nil || result.Revision != 5 || result.AuthVersion != 3 || result.Enabled {
		t.Fatal("local recovery disable required unavailable keys", e)
	}
}
