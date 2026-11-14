package sso

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/google/uuid"
)

func TestConnectorSignInTestSecretsRetainTheirAuthorityDomain(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	ring, err := LoadKeyring(fixtureKeyring(t, "diagnostic", map[string]string{"diagnostic": base64.StdEncoding.EncodeToString(key)}))
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	accounts := &identity.Service{HMACKey: make([]byte, 32)}
	if _, err = rand.Read(accounts.HMACKey); err != nil {
		t.Fatal(err)
	}
	accounts.Config.Server.AdminOrigin = "https://admin.example.invalid"
	owner := &ConnectorSignInTests{service: &Service{Identity: accounts, keys: ring}}
	row := connectorTestRow{ConnectorSignInTestResult: ConnectorSignInTestResult{ID: uuid.NewString(), ConnectorID: uuid.NewString()}, tenant: uuid.NewString(), user: uuid.NewString(), session: uuid.NewString()}
	bundle := [3]string{security.Token(), security.Token(), security.Token()}
	raw, _ := json.Marshal(bundle)
	row.sealed, err = ring.seal(secretScope{row.tenant, row.ConnectorID, row.ID, connectorTestPurpose}, raw)
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	row.stateHash = security.Digest(bundle[0])
	row.browserHash = owner.proof(row, bundle[1])
	if got, e := owner.open(row); e != nil || got != bundle {
		t.Fatal("diagnostic secrets did not reopen", e)
	}
	for _, mutate := range []func(*connectorTestRow){
		func(r *connectorTestRow) { r.tenant = uuid.NewString() }, func(r *connectorTestRow) { r.ConnectorID = uuid.NewString() },
		func(r *connectorTestRow) { r.ID = uuid.NewString() }, func(r *connectorTestRow) { r.user = uuid.NewString() },
		func(r *connectorTestRow) { r.session = uuid.NewString() }, func(r *connectorTestRow) { r.stateHash = security.Digest(security.Token()) },
	} {
		changed := row
		mutate(&changed)
		if got, e := owner.open(changed); e == nil || got != [3]string{} {
			t.Fatal("diagnostic secrets escaped their scope")
		}
	}
	if _, err = ring.open(secretScope{row.tenant, row.ConnectorID, row.ID, flowVerifierPurpose}, row.sealed); err == nil {
		t.Fatal("diagnostic bundle opened as ordinary login proof")
	}
	raw, _ = json.Marshal([]string{bundle[0], bundle[1], bundle[2], security.Token()})
	row.sealed, err = ring.seal(secretScope{row.tenant, row.ConnectorID, row.ID, connectorTestPurpose}, raw)
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.open(row); err == nil {
		t.Fatal("extended secret bundle accepted")
	}
}

func TestConnectorSignInTestCompositionAndAuthorityFailClosed(t *testing.T) {
	if owner, err := NewConnectorSignInTests(nil, nil); owner != nil || err == nil {
		t.Fatal("missing storage composed a diagnostic service")
	}
	var owner *ConnectorSignInTests
	actor := unitBindingActor()
	actor.Me.User.IsPlatformAdmin = false
	if _, err := owner.Get(context.Background(), actor, uuid.NewString(), uuid.NewString()); err == nil || err.Error() != "ACCESS_DENIED" {
		t.Fatal("missing service bypassed authority check", err)
	}
	actor.Me.User.IsPlatformAdmin = true
	actor.Me.Audience = "admin"
	if _, err := owner.Get(context.Background(), actor, uuid.NewString(), uuid.NewString()); err == nil || err.Error() != "SERVICE_NOT_READY" {
		t.Fatal("missing service fabricated a result", err)
	}
	if err := owner.Cleanup(context.Background()); err == nil {
		t.Fatal("missing service fabricated cleanup")
	}
	reported := 0
	owner.Run(context.Background(), func(error) { reported++ })
	if reported != 1 {
		t.Fatal("invalid lifecycle did not report exactly once")
	}
}
