package distribution_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

func TestRestoreIndependentlyRevokesGatewayAndAPIKeyAuthorityPreservingHistoryAndUnknownCapacity(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	access := c.access(t)
	known := access.input()
	pending := access.input()
	for _, input := range []struct{ id string }{{known.RequestID}, {pending.RequestID}} {
		request := access.input()
		request.RequestID = input.id
		if _, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, request, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, success(known.RequestID), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if e := recovery.InvalidateRestored(ctx, c.s.Identity, "Synthetic restore authority acceptance"); e != nil {
		t.Fatal(e)
	}
	_, e := c.s.AuthorizeModels(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, *access.key.Secret)
	check(t, e, "GATEWAY_UNAUTHORIZED")
	_, e = c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, success(pending.RequestID), uuid.NewString())
	check(t, e, "GATEWAY_UNAUTHORIZED")
	var retained, held, reported int
	if e = c.s.DB.QueryRow(ctx, "SELECT count(*),count(*) FILTER(WHERE capacity_held),count(*) FILTER(WHERE state='reported') FROM gateway_requests").Scan(&retained, &held, &reported); e != nil || retained != 2 || held != 1 || reported != 1 {
		t.Fatal("restore changed history or invented release", e)
	}
	var credentialGone bool
	if e = c.s.DB.QueryRow(ctx, "SELECT credential_hash IS NULL AND authority_invalidated_at IS NOT NULL FROM gateway_instances WHERE id=$1", access.gateway.Gateway.ID).Scan(&credentialGone); e != nil || !credentialGone {
		t.Fatal("gateway authority retained", e)
	}
	admin := fixture.Login(t, c.s.Identity, "admin@example.invalid", c.password, "admin")
	keys, e := c.s.Keys(ctx, admin, access.alias.WorkspaceID)
	if e != nil || len(keys) != 1 || keys[0].State != "revoked" {
		t.Fatal("API key authority/history", e)
	}
	gateways, e := c.s.Gateways(ctx, admin, access.alias.WorkspaceID)
	if e != nil || len(gateways) != 1 || gateways[0].State != "invalidated" {
		t.Fatal("restored gateway metadata", e)
	}
}
