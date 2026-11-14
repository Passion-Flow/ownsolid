package sso

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestSSOStartInputAndActorBoundariesBeforeStorage(t *testing.T) {
	// This explicit unit object has no database or keys and cannot succeed.
	s := &Service{Identity: &identity.Service{}, enabled: true, policy: config.DefaultSSOPolicy()}
	s.Identity.Config.Server.PortalOrigin = "https://portal.example.invalid"
	s.Identity.Config.Server.AdminOrigin = "https://admin.example.invalid"
	connector, request := uuid.NewString(), uuid.NewString()
	password, reason := "Synthetic unit password", "Synthetic link confirmation"
	for _, input := range []StartInput{{}, {Mode: "other"}, {Mode: ModeLogin, Password: &password}, {Mode: ModeLogin, Reason: &reason}, {Mode: ModeLink}, {Mode: ModeLink, Password: &password}, {Mode: ModeLink, Reason: &reason}, {Mode: ModeLink, Password: &password, Reason: new(string)}} {
		if value, e := s.Start(context.Background(), connector, "portal", "", nil, input, request); e == nil || e.Error() != "VALIDATION_FAILED" || value != (StartResult{}) {
			t.Fatal("malformed start reached storage", e)
		}
	}
	for _, pair := range [][2]string{{"bad-id", "portal"}, {uuid.Nil.String(), "portal"}, {connector, "system"}} {
		if value, e := s.Start(context.Background(), pair[0], pair[1], "", nil, StartInput{Mode: ModeLogin}, request); e == nil || e.Error() != "SSO_AUTHORIZATION_INPUT_INVALID" || value != (StartResult{}) {
			t.Fatal("invalid callback identity reached storage", e)
		}
	}
	if value, e := s.Start(context.Background(), connector, "portal", "", nil, StartInput{Mode: ModeLogin}, "private-invalid-request"); e == nil || e.Error() != "VALIDATION_FAILED" || value != (StartResult{}) {
		t.Fatal("invalid request identity reached storage", e)
	}
	input := StartInput{Mode: ModeLink, Password: &password, Reason: &reason}
	actor := unitBindingActor()
	wrongAudience := actor
	wrongAudience.Me.Audience = "admin"
	for _, a := range []*identity.Auth{nil, {}, &wrongAudience} {
		if value, e := s.Start(context.Background(), connector, "portal", "", a, input, request); e == nil || e.Error() != "AUTH_REQUIRED" || value != (StartResult{}) {
			t.Fatal("malformed link actor reached authentication/storage", e)
		}
	}
	if value, e := s.Start(context.Background(), connector, "portal", "", &actor, input, request); e == nil || e.Error() != "IDENTITY_STORAGE_REQUIRED" || value != (StartResult{}) {
		t.Fatal("valid format must still require actual storage", e)
	}
}

func TestSSOStartDisabledAndMissingOwnerFailClosed(t *testing.T) {
	var missing *Service
	if value, e := missing.Start(context.Background(), "", "", "", nil, StartInput{}, ""); e == nil || e.Error() != "SSO_CONFIGURATION_REQUIRED" || value != (StartResult{}) {
		t.Fatal("missing start owner must not panic", e)
	}
	s := &Service{Identity: &identity.Service{}}
	if value, e := s.Start(context.Background(), "", "", "", nil, StartInput{}, ""); e == nil || e.Error() != "SSO_DISABLED" || value != (StartResult{}) {
		t.Fatal("disabled start must not require storage,keyring or provider", e)
	}
}
