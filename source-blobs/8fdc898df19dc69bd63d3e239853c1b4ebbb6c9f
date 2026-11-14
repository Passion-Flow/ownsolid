package identity

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
)

func TestProfileInputsRequireCurrentAuthority(t *testing.T) {
	ctx := context.Background()
	s := &Service{}
	if _, e := s.UpdateMe(ctx, Auth{}, "Synthetic", "en", 1, ""); code(e) != "AUTH_SESSION_EXPIRED" {
		t.Fatal(e)
	}
	a := Auth{Me: Me{User: User{ID: uuid.NewString()}, Audience: "portal"}, SessionID: uuid.NewString(), Epoch: 1, TokenHash: security.Digest(security.Token())}
	for _, v := range []int64{0, -1, MaximumAuthVersion + 1} {
		if _, e := s.UpdateMe(ctx, a, "Synthetic", "en", v, ""); code(e) != "VALIDATION_FAILED" {
			t.Fatal(e)
		}
	}
	if _, e := s.GetUser(ctx, "not-an-id"); code(e) != "RESOURCE_NOT_FOUND" {
		t.Fatal(e)
	}
	if _, e := s.GetUser(ctx, uuid.NewString()); code(e) != "IDENTITY_STORAGE_REQUIRED" {
		t.Fatal(e)
	}
}

func TestAccountStatusAudienceAndVersionBounds(t *testing.T) {
	ctx := context.Background()
	s := &Service{}
	a := Auth{Me: Me{User: User{ID: uuid.NewString(), IsPlatformAdmin: true}, Audience: "portal"}, SessionID: uuid.NewString(), Epoch: 1, TokenHash: security.Digest(security.Token())}
	if _, e := s.SetStatus(ctx, a, uuid.NewString(), "disabled", 1, "Synthetic reason", ""); code(e) != "ACCESS_DENIED" {
		t.Fatal(e)
	}
	a.Me.Audience = "admin"
	for _, v := range []struct {
		id, status, reason string
		version            int64
	}{{"invalid", "disabled", "Synthetic", 1}, {uuid.NewString(), "unknown", "Synthetic", 1}, {uuid.NewString(), "disabled", "", 1}, {uuid.NewString(), "disabled", "Synthetic", MaximumAuthVersion + 1}} {
		if _, e := s.SetStatus(ctx, a, v.id, v.status, v.version, v.reason, ""); code(e) != "VALIDATION_FAILED" {
			t.Fatal(e)
		}
	}
	if _, e := s.SetStatus(ctx, a, uuid.NewString(), "disabled", 1, "Synthetic reason", ""); code(e) != "IDENTITY_STORAGE_REQUIRED" {
		t.Fatal(e)
	}
}
