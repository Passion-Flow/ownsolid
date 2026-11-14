package distribution_test

import (
	"context"
	"net/url"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

func rotation(overlap int) distribution.RotateRequest {
	return distribution.RotateRequest{OverlapSeconds: overlap, Reason: "Synthetic rotation"}
}

// keyCalls is one page of a key's call log.
func (c scenario) keyCalls(t *testing.T, key string, extra url.Values) distribution.Page[distribution.RequestRecord] {
	t.Helper()
	values := url.Values{"api_key_id": {key}}
	for name, value := range extra {
		values[name] = value
	}
	page, e := c.s.RequestPage(context.Background(), c.a, c.a.Me.Workspace.ID, values)
	if e != nil {
		t.Fatal(e)
	}
	return page
}

func TestRotationKeepsScopeAndOwnerAndOverlapsTheOldKey(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	a := c.access(t)
	old := a.key.Key
	idempotency := uuid.NewString()
	before := time.Now().UTC()
	rotated, e := c.s.RotateKey(ctx, c.a, old.WorkspaceID, old.ID, rotation(3600), idempotency, uuid.NewString())
	if e != nil || !rotated.Created || !rotated.SecretAvailable || rotated.Secret == nil {
		t.Fatal("successor with its secret once", e)
	}
	next := rotated.Key
	if next.ID == old.ID || next.AliasID != old.AliasID || next.Label != old.Label || next.CreatedBy != a.owner.Me.User.ID || next.State != "active" {
		t.Fatalf("the successor changed more than the secret: %+v", next)
	}
	if next.RotatedFrom == nil || *next.RotatedFrom != old.ID || next.ReplacedBy != nil {
		t.Fatal("the successor does not name the key it replaced")
	}
	if lifetime := next.ExpiresAt.Sub(next.CreatedAt).Seconds(); lifetime < 3599 || lifetime > 3602 {
		t.Fatal("the successor did not keep the old key's lifetime", lifetime)
	}
	previous, e := c.s.Key(ctx, c.a, old.WorkspaceID, old.ID)
	if e != nil || previous.State != "active" || previous.ReplacedBy == nil || *previous.ReplacedBy != next.ID {
		t.Fatal("the old key does not name its successor or stopped early", e)
	}
	if previous.ExpiresAt.After(old.ExpiresAt) || previous.ExpiresAt.After(before.Add(3601*time.Second)) {
		t.Fatal("the overlap extended the old key")
	}
	for _, secret := range []string{*a.key.Secret, *rotated.Secret} {
		if _, e = c.s.AuthorizeModels(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, secret); e != nil {
			t.Fatal("a key stopped working during the overlap", e)
		}
	}

	again, e := c.s.RotateKey(ctx, c.a, old.WorkspaceID, old.ID, rotation(3600), idempotency, uuid.NewString())
	if e != nil || again.Created || again.SecretAvailable || again.Secret != nil || again.Key.ID != next.ID {
		t.Fatal("a repeated rotation disclosed or replaced the successor", e)
	}
	_, e = c.s.RotateKey(ctx, c.a, old.WorkspaceID, old.ID, rotation(60), idempotency, uuid.NewString())
	check(t, e, "IDEMPOTENCY_CONFLICT")
	_, e = c.s.RotateKey(ctx, c.a, old.WorkspaceID, old.ID, rotation(3600), uuid.NewString(), uuid.NewString())
	check(t, e, "API_KEY_ALREADY_ROTATED")

	immediate, e := c.s.RotateKey(ctx, c.a, next.WorkspaceID, next.ID, rotation(0), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	stopped, e := c.s.Key(ctx, c.a, next.WorkspaceID, next.ID)
	if e != nil || stopped.State != "revoked" {
		t.Fatal("an overlap of zero did not stop the key at once", e)
	}
	_, e = c.s.AuthorizeModels(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, *rotated.Secret)
	check(t, e, "API_KEY_INVALID")
	if _, e = c.s.AuthorizeModels(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, *immediate.Secret); e != nil {
		t.Fatal("the second successor does not work", e)
	}
	var audits int
	if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='gateway.api_key_rotated' AND target_id IN ($1,$2)", old.ID, next.ID).Scan(&audits); e != nil || audits != 2 {
		t.Fatal("each rotation is audited once on the key it replaced", audits, e)
	}
	if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='gateway.api_key_created' AND target_id IN ($1,$2)", next.ID, immediate.Key.ID).Scan(&audits); e != nil || audits != 2 {
		t.Fatal("each successor is audited as created", audits, e)
	}
}

func TestRotationRefusesWhatTheCallerMayNotRotate(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	a := c.access(t)
	old := a.key.Key
	other := c.user(t, "other-rotator@example.invalid")
	_, e := c.s.RotateKey(ctx, other, old.WorkspaceID, old.ID, rotation(60), uuid.NewString(), uuid.NewString())
	check(t, e, "RESOURCE_NOT_FOUND")
	for _, input := range []distribution.RotateRequest{rotation(-1), rotation(distribution.MaximumRotationOverlapSeconds + 1), {OverlapSeconds: 60}} {
		_, e = c.s.RotateKey(ctx, a.owner, old.WorkspaceID, old.ID, input, uuid.NewString(), uuid.NewString())
		check(t, e, "VALIDATION_FAILED")
	}
	_, e = c.s.RotateKey(ctx, a.owner, old.WorkspaceID, uuid.NewString(), rotation(60), uuid.NewString(), uuid.NewString())
	check(t, e, "RESOURCE_NOT_FOUND")
	own, e := c.s.RotateKey(ctx, a.owner, old.WorkspaceID, old.ID, rotation(distribution.MaximumRotationOverlapSeconds), uuid.NewString(), uuid.NewString())
	if e != nil || own.Key.CreatedBy != a.owner.Me.User.ID {
		t.Fatal("an owner cannot rotate their own key", e)
	}
	if _, e = c.s.RevokeKey(ctx, c.a, old.WorkspaceID, own.Key.ID, "Synthetic revoke", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e = c.s.RotateKey(ctx, c.a, old.WorkspaceID, own.Key.ID, rotation(60), uuid.NewString(), uuid.NewString())
	check(t, e, "API_KEY_NOT_ACTIVE")
}

func TestConcurrentRotationsLeaveOneSuccessor(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	a := c.access(t)
	old := a.key.Key
	const rotators = 6
	results := make(chan error, rotators)
	var start sync.WaitGroup
	start.Add(1)
	for range rotators {
		go func() {
			start.Wait()
			_, e := c.s.RotateKey(ctx, c.a, old.WorkspaceID, old.ID, rotation(60), uuid.NewString(), uuid.NewString())
			results <- e
		}()
	}
	start.Done()
	won := 0
	for range rotators {
		e := <-results
		switch {
		case e == nil:
			won++
		case e.Error() != "API_KEY_ALREADY_ROTATED":
			t.Fatal("a concurrent rotation failed for another reason", e)
		}
	}
	var successors int
	if e := c.s.DB.QueryRow(ctx, "SELECT count(*) FROM api_keys WHERE rotated_from=$1", old.ID).Scan(&successors); e != nil || won != 1 || successors != 1 {
		t.Fatal("concurrent rotations made more than one successor", won, successors, e)
	}
}

func TestAKeysCallLogHoldsOnlyItsCalls(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	a := c.access(t)
	second, e := c.s.MintKey(ctx, a.owner, keyInput(a.alias), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	admit := func(secret string) string {
		t.Helper()
		input := wire.AdmissionRequest{RequestID: uuid.NewString(), APIKey: secret, Model: a.alias.Name, MaximumOutputTokens: 16}
		if _, e := c.s.Admit(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, input, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if _, e := c.s.Report(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, success(input.RequestID), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		return input.RequestID
	}
	first := []string{admit(*a.key.Secret), admit(*a.key.Secret)}
	other := admit(*second.Secret)

	page := c.keyCalls(t, a.key.Key.ID, url.Values{"limit": {"1"}})
	if len(page.Items) != 1 || page.Items[0].ID != first[1] || page.NextCursor == nil {
		t.Fatal("the first page of a key's calls", page.Items)
	}
	rest := c.keyCalls(t, a.key.Key.ID, url.Values{"limit": {"1"}, "cursor": {*page.NextCursor}})
	if len(rest.Items) != 1 || rest.Items[0].ID != first[0] || rest.NextCursor != nil {
		t.Fatal("the second page of a key's calls", rest.Items)
	}
	only := c.keyCalls(t, second.Key.ID, nil)
	if len(only.Items) != 1 || only.Items[0].ID != other || only.Items[0].APIKeyID != second.Key.ID {
		t.Fatal("another key's calls leaked into a key's log", only.Items)
	}
	_, e = c.s.RequestPage(ctx, c.a, c.a.Me.Workspace.ID, url.Values{"api_key_id": {second.Key.ID}, "cursor": {*page.NextCursor}})
	check(t, e, "INVALID_CURSOR")
	for _, bad := range []url.Values{{"api_key_id": {"not-a-key"}}, {"api_key_id": {a.key.Key.ID, second.Key.ID}}} {
		_, e = c.s.RequestPage(ctx, c.a, c.a.Me.Workspace.ID, bad)
		check(t, e, "VALIDATION_FAILED")
	}
	stranger := c.user(t, "log-stranger@example.invalid")
	theirs, e := c.s.RequestPage(ctx, stranger, c.a.Me.Workspace.ID, url.Values{"api_key_id": {a.key.Key.ID}})
	if e == nil && len(theirs.Items) != 0 {
		t.Fatal("a person read another person's key's calls")
	}
	all, e := c.s.RequestPage(ctx, c.a, c.a.Me.Workspace.ID, url.Values{})
	if e != nil || len(all.Items) != 3 {
		t.Fatal("the unfiltered list changed", len(all.Items), e)
	}
}

func TestRotationAndCallLogOverHTTPSMatchTheContract(t *testing.T) {
	c := setup(t)
	a := c.access(t)
	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, 200, "")
	base := "/api/v1/admin/distribution/workspaces/" + c.a.Me.Workspace.ID
	path := base + "/keys/" + a.key.Key.ID + "/rotate"
	body := map[string]any{"overlap_seconds": 3600, "reason": "Synthetic HTTP rotation"}
	admin.Write("POST", path, body, 400, "Problem") // no Idempotency-Key
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	admin.Write("POST", path, map[string]any{"overlap_seconds": 3600}, 400, "Problem")
	admin.Write("POST", path, map[string]any{"overlap_seconds": 3600, "reason": "x", "label": "extra"}, 400, "Problem")
	rotated := admin.Write("POST", path, body, 201, "MintedDistributionKeyResponse")
	if rotated["secret_available"] != true || rotated["secret"] == nil {
		t.Fatal("the successor's secret was not returned once")
	}
	replay := admin.Write("POST", path, body, 200, "MintedDistributionKeyResponse")
	if replay["secret"] != nil || replay["secret_available"] != false {
		t.Fatal("a replay returned the secret again")
	}
	admin.Headers["Idempotency-Key"] = uuid.NewString()
	refused := admin.Write("POST", path, body, 409, "Problem")
	if refused["code"] != "API_KEY_ALREADY_ROTATED" {
		t.Fatal("a second rotation", refused["code"])
	}
	successor := rotated["key"].(map[string]any)
	if successor["rotated_from"] != a.key.Key.ID {
		t.Fatal("the successor does not name the key it replaced")
	}
	old := admin.Call("GET", base+"/keys/"+a.key.Key.ID, nil, 200, "DistributionKeyResponse")
	if old["replaced_by"] != successor["id"] {
		t.Fatal("the old key does not name its successor")
	}
	admin.Call("GET", base+"/requests?api_key_id="+a.key.Key.ID+"&limit=5", nil, 200, "GatewayRequestPageResponse")
	admin.Call("GET", base+"/requests?api_key_id=nope", nil, 400, "Problem")
}
