package sso

import (
	"net/url"
	"testing"

	"github.com/google/uuid"
)

func TestM11ActualServiceReopenPreservesUnusedBrowserBoundFlowAndSingleIssuance(t *testing.T) {
	f := sourceFixture(t)
	ctx := t.Context()
	start, e := f.s.Start(ctx, f.source.ConnectorID, "admin", "", nil, StartInput{Mode: ModeLogin}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	authorization, _ := url.Parse(start.AuthorizationURL)
	f.s.Close()
	next, e := New(f.s.Identity)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(next.Close)
	next.Identity.Sources = next
	if e = next.VerifyStored(ctx); e != nil {
		t.Fatal("reopened encrypted unused flow", e)
	}
	claimed, e := next.claim(ctx, f.source.ConnectorID, "admin", authorization.Query().Get("state"), start.BrowserToken, "")
	if e != nil || !validClaimedFlow(claimed) {
		t.Fatal("matching restart lost unused browser authority", e)
	}
	// The provider output here is an explicit source fixture; the actual provider
	// exchange is covered separately. Issuance and durable one-use state are real.
	issued, e := next.commitFlow(ctx, claimed, "9007199254740993", "", uuid.NewString())
	if e != nil || issued.Token == "" {
		t.Fatal("reopened ordinary session issuance", e)
	}
	if _, e = next.Identity.Authenticate(ctx, issued.Token, "admin"); e != nil {
		t.Fatal(e)
	}
	if _, e = next.claim(ctx, f.source.ConnectorID, "admin", authorization.Query().Get("state"), start.BrowserToken, ""); e == nil {
		t.Fatal("restart permitted a second claim")
	}
	interrupted, e := next.Start(ctx, f.source.ConnectorID, "admin", "", nil, StartInput{Mode: ModeLogin}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	intent, _ := url.Parse(interrupted.AuthorizationURL)
	if _, e = next.claim(ctx, f.source.ConnectorID, "admin", intent.Query().Get("state"), interrupted.BrowserToken, ""); e != nil {
		t.Fatal(e)
	}
	// Discard the service and captured claim before exchange, as process memory
	// loss would. The new instance must not reuse the durable claimed intent.
	next.Close()
	reopened, e := New(next.Identity)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(reopened.Close)
	reopened.Identity.Sources = reopened
	refused, _, e := reopened.Complete(ctx, f.source.ConnectorID, "admin", intent.Query().Get("state"), interrupted.BrowserToken, "", "discarded-unused-code", false, uuid.NewString())
	requireCode(t, e, "SSO_FLOW_UNUSABLE")
	if refused.Token != "" {
		t.Fatal("reopened claimed flow issued a second session")
	}
	if _, e = reopened.Identity.Authenticate(ctx, issued.Token, "admin"); e != nil {
		t.Fatal("ordinary prior issuance lost after reopen", e)
	}
}
