package sso

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/google/uuid"
)

func TestSAMLCompletionProtectedInputDiagnostics(t *testing.T) {
	input := SAMLCompleteInput{ConnectorID: uuid.NewString(), Audience: "portal", FlowID: uuid.NewString(), Continuation: security.Token(), PreviousSession: security.Token()}
	raw, e := json.Marshal(input)
	if e != nil {
		t.Fatal(e)
	}
	for _, output := range []string{string(raw), fmt.Sprint(input), fmt.Sprintf("%+v", input), fmt.Sprintf("%#v", input)} {
		if strings.Contains(output, input.Continuation) || strings.Contains(output, input.PreviousSession) {
			t.Fatal("protected SAML browser value entered diagnostics")
		}
	}
}
func TestSAMLCompletionRequiresActualOwner(t *testing.T) {
	for _, ctx := range []context.Context{nil, context.Background()} {
		owner, e := NewSAMLCompletion(ctx, nil)
		if e == nil || owner != nil {
			t.Fatal("missing SAML dependency admitted an owner")
		}
	}
	base := &Service{Identity: &identity.Service{}}
	if (&SAMLCompletion{}).MatchesService(base) || (&SAMLCompletion{service: base}).MatchesService(nil) || (&SAMLCompletion{service: base}).MatchesService(&Service{Identity: base.Identity}) || !(&SAMLCompletion{service: base}).MatchesService(base) {
		t.Fatal("SAML construction identity was widened")
	}
	var owner *SAMLCompletion
	out, e := owner.Complete(context.Background(), SAMLCompleteInput{Audience: "portal"}, uuid.NewString())
	if e == nil || out != (identity.IssuedSession{}) {
		t.Fatal("missing owner released a session")
	}
	if e = owner.CheckSessionSource(context.Background(), nil, identity.SessionSource{}, uuid.NewString()); e == nil {
		t.Fatal("missing source owner authorized a session")
	}
	if e = owner.CheckSessionSourceWithin(context.Background(), nil, identity.SessionSource{}, uuid.NewString()); e == nil {
		t.Fatal("missing source transaction authorized a session")
	}
}
func TestSAMLCompletionVersionDomain(t *testing.T) {
	for _, value := range []int64{-1, 0, identity.MaximumAuthVersion + 1} {
		if samlCompletionVersion(value) {
			t.Fatal("unsupported authority version admitted")
		}
	}
	for _, value := range []int64{1, identity.MaximumAuthVersion} {
		if !samlCompletionVersion(value) {
			t.Fatal("supported authority version refused")
		}
	}
}
