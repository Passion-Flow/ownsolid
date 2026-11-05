package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

// passwordChangesPath is where a signed-in person changes their own password.
const passwordChangesPath = "/api/v1/me/password-changes"

// changePassword serves POST /api/v1/me/password-changes. Attempts are limited per
// account and per address with the sign-in limits, since each one tests a password.
// Success replaces this browser's session cookie; every other session has ended.
func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request, cookies httpserver.CookiePolicy, a identity.Auth, requestID string) (any, int, error) {
	s := h.Service
	var v struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if e := decode(r, &v); e != nil {
		return nil, 0, e
	}
	if v.CurrentPassword == "" || v.NewPassword == "" {
		return nil, 0, errors.New("VALIDATION_FAILED")
	}
	rates := s.Config.Policy().Rates
	audience := a.Me.Audience
	if _, e := s.Admit(r.Context(),
		identity.Limit{Purpose: "password.change.user", Identifier: a.Me.User.ID, Count: rates.LoginEmailCount, Window: config.Seconds(rates.LoginEmailWindowSeconds)},
		identity.Limit{Purpose: "password.change.ip." + audience, Identifier: peer(r), Count: rates.LoginIPCount, Window: config.Seconds(rates.LoginIPWindowSeconds)},
	); e != nil {
		return nil, 0, e
	}
	issued, e := s.ChangePassword(r.Context(), a, v.CurrentPassword, v.NewPassword, requestID)
	if e != nil {
		return nil, 0, e
	}
	cookies.Set(w, audience, httpserver.SessionCookie, issued.Token, int(s.Absolute(audience)/time.Second))
	return issued.Me, 200, nil
}
