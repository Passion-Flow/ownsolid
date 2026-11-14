package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

const (
	adminTOTPLoginPath   = "/api/v1/auth/login/totp"
	adminTOTPEnrollPath  = "/api/v1/me/totp/enrollment"
	adminTOTPConfirmPath = "/api/v1/me/totp/enrollment/confirmation"
	adminTOTPRevokePath  = "/api/v1/me/totp/revocation"
)

func adminTOTPManagementPath(path string) bool {
	return path == adminTOTPEnrollPath || path == adminTOTPConfirmPath || path == adminTOTPRevokePath
}

func (h *Handler) browserLoginResponse(w http.ResponseWriter, cookies httpserver.CookiePolicy, audience string, issued identity.IssuedSession) (any, int, error) {
	cookies.Set(w, audience, httpserver.SessionCookie, issued.Token, int(h.Service.Absolute(audience)/time.Second))
	return map[string]any{
		"user": issued.Me.User, "workspace": issued.Me.Workspace, "audience": audience,
		"session": map[string]any{"absolute_expires_at": issued.ExpiresAt, "idle_timeout_seconds": int(h.Service.Idle(audience) / time.Second)},
	}, http.StatusOK, nil
}

func (h *Handler) adminTOTPManagement(w http.ResponseWriter, r *http.Request, cookies httpserver.CookiePolicy, a identity.Auth, requestID string) (any, int, error) {
	if r.Method != http.MethodPost || a.Me.Audience != "admin" {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	switch r.URL.Path {
	case adminTOTPEnrollPath:
		var input struct {
			Password string `json:"password"`
		}
		if err := decode(r, &input); err != nil {
			return nil, 0, err
		}
		out, err := h.Service.StartAdminTOTPEnrollment(r.Context(), a, input.Password)
		return out, http.StatusCreated, err
	case adminTOTPConfirmPath:
		var input struct {
			ID   string `json:"id"`
			Code string `json:"code"`
		}
		if err := decode(r, &input); err != nil {
			return nil, 0, err
		}
		codes, err := h.Service.ConfirmAdminTOTPEnrollment(r.Context(), a, input.ID, input.Code, requestID)
		if err != nil {
			return nil, 0, err
		}
		cookies.ClearIdentity(w, a.Me.Audience)
		return map[string][]string{"recovery_codes": codes}, http.StatusOK, nil
	case adminTOTPRevokePath:
		var input struct {
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if err := decode(r, &input); err != nil {
			return nil, 0, err
		}
		if err := h.Service.RevokeAdminTOTP(r.Context(), a, input.Password, input.Code, requestID); err != nil {
			return nil, 0, err
		}
	default:
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	cookies.ClearIdentity(w, a.Me.Audience)
	return nil, http.StatusNoContent, nil
}
