package sso

import (
	"github.com/google/uuid"
	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"testing"
)

func TestM11ActualBoundCallbackErrorModeAndExactOriginBeforeExchange(t *testing.T) {
	f := sourceFixture(t)
	ctx := t.Context()
	start, e := f.s.Start(ctx, f.source.ConnectorID, "admin", f.local, nil, StartInput{Mode: ModeLogin}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	authorization, _ := url.Parse(start.AuthorizationURL)
	state := authorization.Query().Get("state")
	if f.s.BoundResultMode(ctx, f.source.ConnectorID, "admin", state, start.BrowserToken, f.local) != ModeLogin {
		t.Fatal("matching fixed result mode")
	}
	for _, pair := range [][3]string{{"portal", start.BrowserToken, f.local}, {"admin", security.Token(), f.local}, {"admin", start.BrowserToken, security.Token()}} {
		if f.s.BoundResultMode(ctx, f.source.ConnectorID, pair[0], state, pair[1], pair[2]) != "" {
			t.Fatal("foreign browser/audience/account selected result")
		}
	}
	original := f.s.Identity.Config.Server.AdminOrigin
	f.s.Identity.Config.Server.AdminOrigin = "https://changed.example.invalid:9443"
	if f.s.BoundResultMode(ctx, f.source.ConnectorID, "admin", state, start.BrowserToken, f.local) != "" {
		t.Fatal("changed callback origin selected old result")
	}
	_, e = f.s.claim(ctx, f.source.ConnectorID, "admin", state, start.BrowserToken, f.local)
	requireCode(t, e, "SSO_BROWSER_CHANGED")
	var pending bool
	if e = f.s.Identity.DB.QueryRow(ctx, "SELECT state='pending' AND verifier_ciphertext IS NOT NULL FROM sso_flows WHERE state_hash=$1", security.Digest(state)).Scan(&pending); e != nil || !pending {
		t.Fatal("origin refusal consumed exchange authority", e)
	}
	f.s.Identity.Config.Server.AdminOrigin = original
	// Reading a result mode cannot claim or revive the durable flow.
	if f.s.BoundResultMode(ctx, f.source.ConnectorID, "admin", state, start.BrowserToken, f.local) != ModeLogin {
		t.Fatal("matching restart changed browser proof")
	}
	if _, e = f.s.UpdateConnector(ctx, f.a, f.source.ConnectorID, UpdateConnector{Revision: 1, Name: "Disabled bound result", Enabled: false, Reason: "Synthetic terminal callback result"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if f.s.BoundResultMode(ctx, f.source.ConnectorID, "admin", state, start.BrowserToken, f.local) != ModeLogin {
		t.Fatal("terminal flow lost fixed browser presentation")
	}
	issued, _, e := f.s.Complete(ctx, f.source.ConnectorID, "admin", state, start.BrowserToken, f.local, "discarded-old-code", false, uuid.NewString())
	requireCode(t, e, "SSO_FLOW_UNUSABLE")
	if issued.Token != "" {
		t.Fatal("result lookup issued authentication")
	}
}
