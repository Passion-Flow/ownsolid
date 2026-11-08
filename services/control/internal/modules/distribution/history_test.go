package distribution_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/google/uuid"
)

func TestDistributionPaginationPreservesHistoryAndActorWorkspaceScopes(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	a := c.access(t)
	other := c.user(t, "history-other@example.invalid")
	for i := 0; i < 3; i++ {
		input := a.input()
		if _, e := c.s.Admit(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, input, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if i < 2 {
			if _, e := c.s.Report(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, success(input.RequestID), uuid.NewString()); e != nil {
				t.Fatal(e)
			}
		}
	}
	first, e := c.s.RequestPage(ctx, a.owner, a.alias.WorkspaceID, url.Values{"limit": {"1"}})
	if e != nil || len(first.Items) != 1 || first.NextCursor == nil || !first.Items[0].CapacityHeld || first.Items[0].Outcome != nil {
		t.Fatal("new unknown admission remains null", e)
	}
	second, e := c.s.RequestPage(ctx, a.owner, a.alias.WorkspaceID, url.Values{"limit": {"1"}, "cursor": {*first.NextCursor}})
	if e != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.Items[0].Outcome == nil || second.Items[0].Outcome.Usage.TotalTokens != 9 {
		t.Fatal("keyset page actual usage", e)
	}
	third, e := c.s.RequestPage(ctx, a.owner, a.alias.WorkspaceID, url.Values{"limit": {"1"}, "cursor": {*second.NextCursor}})
	if e != nil || len(third.Items) != 1 || third.NextCursor != nil || third.Items[0].ID == second.Items[0].ID {
		t.Fatal("last history page", e)
	}
	if _, e = c.s.RequestPage(ctx, other, a.alias.WorkspaceID, url.Values{"cursor": {*first.NextCursor}}); e == nil {
		t.Fatal("foreign actor cursor accepted")
	}
	own, e := c.s.RequestPage(ctx, other, a.alias.WorkspaceID, url.Values{})
	if e != nil || len(own.Items) != 0 {
		t.Fatal("request ownership", e)
	}
	_, e = c.s.Request(ctx, other, a.alias.WorkspaceID, first.Items[0].ID)
	check(t, e, "RESOURCE_NOT_FOUND")
	_, e = c.s.Key(ctx, other, a.alias.WorkspaceID, a.key.Key.ID)
	check(t, e, "RESOURCE_NOT_FOUND")
	_, e = c.s.Gateway(ctx, a.owner, a.alias.WorkspaceID, a.gateway.Gateway.ID)
	check(t, e, "ACCESS_DENIED")
	if _, e = c.s.Request(ctx, c.a, a.alias.WorkspaceID, third.Items[0].ID); e != nil {
		t.Fatal("admin retained history", e)
	}
	foreign, e := c.s.Workspaces.Create(ctx, c.a, "Foreign history Workspace", c.a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.RequestPage(ctx, c.a, foreign.ID, url.Values{"cursor": {*first.NextCursor}}); e == nil {
		t.Fatal("foreign scope cursor")
	}
	_, e = c.s.Request(ctx, c.a, foreign.ID, first.Items[0].ID)
	check(t, e, "RESOURCE_NOT_FOUND")
	for _, q := range []url.Values{{"limit": {"0"}}, {"limit": {"101"}}, {"limit": {"1", "2"}}, {"cursor": {"bad"}}, {"actor_id": {other.Me.User.ID}}} {
		if _, e = c.s.RequestPage(ctx, c.a, a.alias.WorkspaceID, q); e == nil {
			t.Fatal("invalid pagination accepted")
		}
	}
	if _, e = c.s.RevokeGateway(ctx, c.a, a.alias.WorkspaceID, a.gateway.Gateway.ID, "Synthetic retained gateway", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		v, e := c.s.Provision(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = c.s.RevokeGateway(ctx, c.a, a.alias.WorkspaceID, v.Gateway.ID, "Synthetic history", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	gateways, e := c.s.GatewayPage(ctx, c.a, a.alias.WorkspaceID, url.Values{"limit": {"1"}})
	if e != nil || gateways.NextCursor == nil {
		t.Fatal("gateway history cursor", e)
	}
	aliases, e := c.s.AliasPage(ctx, a.owner, a.alias.WorkspaceID, url.Values{})
	if e != nil || len(aliases.Items) != 1 || aliases.Items[0].ClientOrigin != c.input().ClientOrigin || aliases.Items[0].Available {
		t.Fatal("unavailable alias retains endpoint metadata", e)
	}
	if _, e = c.s.DisableAlias(ctx, c.a, a.alias.WorkspaceID, a.alias.ID, a.alias.Revision, "Synthetic disable", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	portal, e := c.s.AliasPage(ctx, a.owner, a.alias.WorkspaceID, url.Values{})
	if e != nil || len(portal.Items) != 0 {
		t.Fatal("portal disabled alias hidden", e)
	}
	retained, e := c.s.Alias(ctx, c.a, a.alias.WorkspaceID, a.alias.ID)
	if e != nil || retained.ClientOrigin == "" {
		t.Fatal("retained alias endpoint", e)
	}
}

func TestProvisionRejectsUnsafeAdvertisedOriginsAndBindsIntent(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	for _, origin := range []string{"", "http://gateway.example.invalid", "https://user:secret@example.invalid", "https://gateway.example.invalid/", "https://gateway.example.invalid:0", "https://gateway.example.invalid:65536", "https://gateway.example.invalid?", "https://gateway.example.invalid?secret=value", "https://gateway.example.invalid#fragment", "https://gateway.example.invalid\\untrusted", " https://gateway.example.invalid"} {
		input := c.input()
		input.ClientOrigin = origin
		_, e := c.s.Provision(ctx, c.a, input, uuid.NewString(), uuid.NewString())
		check(t, e, "VALIDATION_FAILED")
	}
	input := c.input()
	input.ClientOrigin = "https://nondefault.example.invalid:22443"
	intent := uuid.NewString()
	v, e := c.s.Provision(ctx, c.a, input, intent, uuid.NewString())
	if e != nil || v.Gateway.ClientOrigin == nil || *v.Gateway.ClientOrigin != input.ClientOrigin {
		t.Fatal("nondefault origin", e)
	}
	input.ClientOrigin = "https://another.example.invalid"
	_, e = c.s.Provision(ctx, c.a, input, intent, uuid.NewString())
	check(t, e, "IDEMPOTENCY_CONFLICT")
}
