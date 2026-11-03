package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
)

func TestInvitationInputsPrecedeStorage(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	a := Auth{Me: Me{User: User{ID: uuid.NewString()}, Audience: "admin"}, SessionID: uuid.NewString(), Epoch: 1, TokenHash: security.Digest(security.Token())}
	for _, v := range []struct {
		id, op, key, email, locale string
		version                    int64
	}{
		{"", "unknown", uuid.NewString(), "synthetic@example.invalid", "en", 0},
		{"", createInvitation, strings.ToUpper(uuid.NewString()), "synthetic@example.invalid", "en", 0},
		{"", createInvitation, uuid.Nil.String(), "synthetic@example.invalid", "en", 0},
		{uuid.NewString(), createInvitation, uuid.NewString(), "synthetic@example.invalid", "en", 0},
		{uuid.NewString(), resendInvitation, uuid.NewString(), "", "", 0},
		{uuid.NewString(), resendInvitation, uuid.NewString(), "override@example.invalid", "en", 1},
		{"", createInvitation, uuid.NewString(), "invalid", "en", 0},
		{"", createInvitation, uuid.NewString(), "synthetic@example.invalid", "unknown", 0},
	} {
		if _, e := s.Invite(ctx, a, v.id, v.op, v.key, v.email, v.locale, v.version, ""); code(e) != "VALIDATION_FAILED" {
			t.Fatal(e)
		}
	}
	if _, e := s.RevokeInvitation(ctx, a, uuid.NewString(), 1, "", ""); code(e) != "VALIDATION_FAILED" {
		t.Fatal(e)
	}
	if _, e := s.ValidateInvitation(ctx, "invalid"); code(e) != "TOKEN_UNUSABLE" {
		t.Fatal(e)
	}
	if e := s.Register(ctx, "invalid", "Name", "unused", ""); code(e) != "TOKEN_UNUSABLE" {
		t.Fatal(e)
	}
}
func TestInvitationAuthorityAndMissingStorage(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	if _, e := s.Invite(ctx, Auth{}, "", createInvitation, uuid.NewString(), "synthetic@example.invalid", "en", 0, ""); code(e) != "AUTH_SESSION_EXPIRED" {
		t.Fatal(e)
	}
	a := Auth{Me: Me{User: User{ID: uuid.NewString(), IsPlatformAdmin: true}, Audience: "portal"}, SessionID: uuid.NewString(), Epoch: 1, TokenHash: security.Digest(security.Token())}
	if _, e := s.Invite(ctx, a, "", createInvitation, uuid.NewString(), "synthetic@example.invalid", "en", 0, ""); code(e) != "ACCESS_DENIED" {
		t.Fatal(e)
	}
	if _, e := s.RevokeInvitation(ctx, a, uuid.NewString(), 1, "Synthetic", ""); code(e) != "ACCESS_DENIED" {
		t.Fatal(e)
	}
	a.Me.Audience = "admin"
	if _, e := s.Invite(ctx, a, "", createInvitation, uuid.NewString(), "synthetic@example.invalid", "en", 0, ""); code(e) != "IDENTITY_STORAGE_REQUIRED" {
		t.Fatal(e)
	}
	if _, e := s.ValidateInvitation(ctx, security.Token()); code(e) != "IDENTITY_STORAGE_REQUIRED" {
		t.Fatal(e)
	}
}
