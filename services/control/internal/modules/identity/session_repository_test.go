package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
)

func TestSessionAuthorityInputsBeforeStorage(t *testing.T) {
	a := Auth{Me: Me{User: User{ID: uuid.NewString()}, Audience: "portal"}, SessionID: uuid.NewString(), TokenHash: security.Digest(security.Token()), Epoch: 1}
	if !sessionActor(a) {
		t.Fatal("canonical authority refused")
	}
	for _, change := range []func(*Auth){
		func(a *Auth) { a.Me.User.ID = uuid.Nil.String() }, func(a *Auth) { a.SessionID = "ABCDEFAB-1234-4321-ABCD-123456789ABC" }, func(a *Auth) { a.Me.Audience = "operator" }, func(a *Auth) { a.Epoch = 0 }, func(a *Auth) { a.TokenHash = a.TokenHash[:31] },
	} {
		bad := a
		change(&bad)
		if sessionActor(bad) {
			t.Fatal("malformed server context accepted")
		}
		s := &Service{}
		if e := s.RecheckWithin(context.Background(), nil, bad); code(e) != "AUTH_SESSION_EXPIRED" {
			t.Fatal(e)
		}
	}
	if _, e := (*Service)(nil).Authenticate(context.Background(), "invalid", "portal"); code(e) != "AUTH_REQUIRED" {
		t.Fatal(e)
	}
	if _, e := (&Service{}).ConfirmPassword(context.Background(), a, strings.Repeat("x", maximumConfirmationPasswordBytes+1)); code(e) != "SSO_PASSWORD_REQUIRED" {
		t.Fatal(e)
	}
}

func TestSessionMissingAuthorityAndProofFailClosed(t *testing.T) {
	s := &Service{}
	if _, _, _, e := s.Login(context.Background(), "synthetic@example.invalid", "unused", "portal", "", ""); code(e) != "IDENTITY_STORAGE_REQUIRED" {
		t.Fatal(e)
	}
	if _, _, _, e := s.Login(context.Background(), "synthetic@example.invalid", "unused", "operator", "", ""); code(e) != "AUTH_INVALID_CREDENTIALS" {
		t.Fatal(e)
	}
	if e := s.Logout(context.Background(), security.Token(), "operator", ""); code(e) != "AUTH_REQUIRED" {
		t.Fatal(e)
	}
	if _, e := s.sessionTransaction(nil); code(e) != "IDENTITY_TRANSACTION_REQUIRED" {
		t.Fatal(e)
	}
	if e := s.RecheckConfirmationWithin(context.Background(), nil, Auth{}, PasswordConfirmation{}); code(e) != "SSO_PASSWORD_REQUIRED" {
		t.Fatal(e)
	}
}
