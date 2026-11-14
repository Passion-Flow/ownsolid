package sso

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func unitPrivateFlow() flow {
	return flow{id: uuid.NewString(), tenant: uuid.NewString(), connector: uuid.NewString(), audience: "portal", mode: ModeLogin, state: flowPending, connectorRevision: 1, authVersion: 1, browserHash: security.Digest(security.Token()), previousHash: security.Digest("anonymous"), expires: time.Date(2026, 10, 19, 12, 0, 0, 123456000, time.UTC)}
}

func TestCallbackPrivateAuthorityShapesAndSubjects(t *testing.T) {
	good := unitPrivateFlow()
	if !validPrivateFlow(good) {
		t.Fatal("canonical persisted pending flow rejected")
	}
	for _, change := range []func(*flow){
		func(f *flow) { f.id = uuid.Nil.String() }, func(f *flow) { f.tenant = "foreign-untrusted-value" }, func(f *flow) { f.connector = "" }, func(f *flow) { f.audience = "system" }, func(f *flow) { f.mode = "other" }, func(f *flow) { f.state = "unknown" }, func(f *flow) { f.browserHash = nil }, func(f *flow) { f.previousHash = nil }, func(f *flow) { f.authVersion = 0 }, func(f *flow) { f.connectorRevision = identity.MaximumAuthVersion + 1 }, func(f *flow) { f.expires = f.expires.Add(time.Nanosecond) }, func(f *flow) { x := uuid.NewString(); f.claim = &x }, func(f *flow) { f.state = flowClaimed }, func(f *flow) { f.mode = ModeLink },
	} {
		bad := good
		change(&bad)
		if validPrivateFlow(bad) {
			t.Fatal("malformed private authority accepted")
		}
	}
	claim := uuid.NewString()
	good.state, good.claim = flowClaimed, &claim
	if !validClaimedFlow(claimedFlow{flow: good}) {
		t.Fatal("canonical claimed anonymous flow rejected")
	}
	a := unitBindingActor()
	reason := "Synthetic opaque link proof"
	credential := int64(1)
	good.mode, good.previousSession, good.user, good.session, good.epoch, good.credential, good.reason = ModeLink, &a.SessionID, &a.Me.User.ID, &a.SessionID, &a.Epoch, &credential, &reason
	if validClaimedFlow(claimedFlow{flow: good}) || !validClaimedFlow(claimedFlow{flow: good, previousActor: &a}) {
		t.Fatal("link claim must retain exact opaque current actor")
	}
	stale := a
	stale.Epoch++
	if validClaimedFlow(claimedFlow{flow: good, previousActor: &stale}) {
		t.Fatal("link claim allowed stale current epoch")
	}
	for _, subject := range []string{"0", "-1", "01", "+1", "1.0", "1e3", " 1", "9223372036854775808", ""} {
		if canonicalSubject(subject) {
			t.Fatal("noncanonical provider subject accepted")
		}
	}
	for _, subject := range []string{"1", "9007199254740993", "9223372036854775807"} {
		if !canonicalSubject(subject) {
			t.Fatal("canonical bounded decimal-string subject rejected")
		}
	}
}

func TestCallbackInputsAndMissingOwnerBeforeStorage(t *testing.T) {
	var missing *Service
	if out, e := missing.claim(context.Background(), "", "", "", "", ""); e == nil || e.Error() != "SSO_CONFIGURATION_REQUIRED" || !reflect.DeepEqual(out, claimedFlow{}) {
		t.Fatal("nil claim owner leaked result", e)
	}
	s := &Service{Identity: &identity.Service{}, policy: config.DefaultSSOPolicy()}
	if out, e := s.claim(context.Background(), "", "", "", "", ""); e == nil || e.Error() != "SSO_DISABLED" || !reflect.DeepEqual(out, claimedFlow{}) {
		t.Fatal("disabled claim opened storage/keys", e)
	}
	s.enabled = true
	id, token := uuid.NewString(), security.Token()
	for _, input := range [][4]string{{"bad", "portal", token, token}, {id, "system", token, token}, {id, "portal", "bad", token}, {id, "portal", token, "bad"}} {
		if out, e := s.claim(context.Background(), input[0], input[1], input[2], input[3], ""); e == nil || e.Error() != "SSO_FLOW_INVALID" || !reflect.DeepEqual(out, claimedFlow{}) {
			t.Fatal("malformed claim reached storage", e)
		}
	}
	if out, e := s.claim(context.Background(), id, "portal", token, token, ""); e == nil || e.Error() != "IDENTITY_STORAGE_REQUIRED" || !reflect.DeepEqual(out, claimedFlow{}) {
		t.Fatal("format-only claim fabricated success", e)
	}
	if out, mode, e := s.Complete(context.Background(), id, "portal", token, token, "", "synthetic-code", false, "bad-request"); e == nil || e.Error() != "SSO_FLOW_INVALID" || out != (identity.IssuedSession{}) || mode != "" {
		t.Fatal("invalid completion request consumed a claim", e)
	}
	if out, e := s.commitFlow(context.Background(), claimedFlow{}, "1", "", uuid.NewString()); e == nil || e.Error() != "SSO_FLOW_INVALID" || out != (identity.IssuedSession{}) {
		t.Fatal("malformed private completion reached storage", e)
	}
	if e := s.failFlow(context.Background(), claimedFlow{}, "untrusted provider text", uuid.NewString()); e == nil || e.Error() != "SSO_FLOW_INVALID" {
		t.Fatal("malformed failure target reached storage", e)
	}
}

func TestCallbackPublicFailureKeepsUncertainOutcomeAndRedactsDiagnostics(t *testing.T) {
	if e := publicFlowError(fail(string(transaction.CommitUnknown))); e.Error() != string(transaction.CommitUnknown) {
		t.Fatal("uncertain outcome collapsed to retryable storage failure")
	}
	if e := publicFlowError(fail("private provider diagnostic")); e.Error() != "STORAGE_UNAVAILABLE" {
		t.Fatal("provider diagnostic escaped stable allowlist")
	}
}
