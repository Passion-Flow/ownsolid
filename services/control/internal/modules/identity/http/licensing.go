package http

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

// Exact canonical segments only. The original handler still authorizes every
// exception. New routes default to requiring admission, never to recovery.
func licensePath(path, pattern string) bool {
	actual, expected := strings.Split(path, "/"), strings.Split(pattern, "/")
	if len(actual) != len(expected) {
		return false
	}
	for i, part := range expected {
		if part == ":id" {
			if !wire.ValidID(actual[i]) {
				return false
			}
		} else if actual[i] != part {
			return false
		}
	}
	return true
}

func authenticationRecovery(r *http.Request) bool {
	if r.URL.RawPath != "" || r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/api/v1/auth/login", "/api/v1/auth/logout", "/api/v1/auth/invitation-validations", "/api/v1/auth/register", "/api/v1/auth/password-reset-requests", "/api/v1/auth/password-resets":
		return true
	}
	// SSO owns exact mode decoding; linking additionally admits after real login.
	return licensePath(r.URL.Path, "/api/v1/auth/sso/:id/starts")
}

func inspectAction(r *http.Request, out any) error {
	raw, e := io.ReadAll(io.LimitReader(r.Body, httpserver.MaxJSONBytes+1))
	if e != nil {
		return errors.New("VALIDATION_FAILED")
	}
	if len(raw) > httpserver.MaxJSONBytes {
		return errors.New("PAYLOAD_TOO_LARGE")
	}
	// Clone does not copy Body. Replace the inspection stream and restore the
	// exact bounded original bytes for the real handler, using the same decoder.
	copy := r.Clone(r.Context())
	copy.Body = io.NopCloser(bytes.NewReader(raw))
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return httpserver.DecodeJSON(copy, out)
}

func recoveryMutation(r *http.Request) (bool, error) {
	if r.URL.RawPath != "" {
		return false, nil
	}
	path := r.URL.Path
	if privateClusterRecoveryMutation(r) {
		return true, nil
	}
	if r.Method == http.MethodPatch {
		if path == "/api/v1/me" || path == "/api/v1/preferences" {
			return true, nil
		}
		if licensePath(path, "/api/v1/admin/users/:id/status") {
			var input struct {
				Status  string `json:"status"`
				Version int64  `json:"version"`
				Reason  string `json:"reason"`
			}
			e := inspectAction(r, &input)
			return input.Status == "disabled", e
		}
	}
	for _, scope := range []string{"/api/v1/admin/artifact-objects", "/api/v1/workspaces/:id/artifact-objects"} {
		if r.Method == http.MethodPost {
			for _, tail := range []string{"/expire", "/:id/abort", "/:id/delete", "/:id/reconcile", "/:id/ingestions/:id/cancel"} {
				if licensePath(path, scope+tail) {
					return true, nil
				}
			}
		}
		if r.Method == http.MethodDelete && (licensePath(path, scope+"/staging") || licensePath(path, scope+"/:id/grants/:id")) {
			return true, nil
		}
	}
	if r.Method != http.MethodPost {
		return false, nil
	}
	if path == "/api/v1/admin/license" {
		return true, nil
	}
	for _, action := range []string{"remove", "reconcile-removal", "revoke", "release-offline", "release-deployment", "abandon", "discard"} {
		if licensePath(path, "/api/v1/admin/image-registry/pins/:id/"+action) {
			return true, nil
		}
	}
	for _, pattern := range []string{
		"/api/v1/notifications/:id/read",
		"/api/v1/account/sso/bindings/:id/unlink",
		"/api/v1/admin/invitations/:id/revocations",
		"/api/v1/admin/node-join-tokens/:id/revocations",
		"/api/v1/admin/nodes/:id/inference/observations",
		"/api/v1/admin/model-imports/:id/cancel",
		"/api/v1/admin/models/:id/retire",
	} {
		if licensePath(path, pattern) {
			return true, nil
		}
	}
	for _, scope := range []string{"/api/v1/admin", "/api/v1/workspaces/:id"} {
		for _, tail := range []string{
			"/datasets/:id/drafts/:id/cancel", "/datasets/:id/jobs/:id/cancel",
			"/training/jobs/:id/cancellations", "/model-preparations/:id/cancellations",
			"/workflows/:id/runs/:id/cancellations",
		} {
			if licensePath(path, scope+tail) {
				return true, nil
			}
		}
	}
	for _, scope := range []string{"/api/v1/admin/distribution/workspaces/:id", "/api/v1/workspaces/:id/distribution"} {
		for _, tail := range []string{
			"/keys/:id/revoke", "/gateways/:id/revoke", "/aliases/:id/disable",
			"/requests/:id/recoveries", "/requests/:id/recoveries/:id/fence", "/requests/:id/recoveries/:id/confirm",
		} {
			if licensePath(path, scope+tail) {
				return true, nil
			}
		}
	}
	if licensePath(path, "/api/v1/admin/nodes/:id/components/node-exporter/actions") {
		var input components.Request
		e := inspectAction(r, &input)
		return input.Action == "stop" || input.Action == "remove", e
	}
	if licensePath(path, "/api/v1/admin/deployments/:id/actions") {
		var input deployments.ActionRequest
		e := inspectAction(r, &input)
		return input.Action == "stop" || input.Action == "remove" || input.Action == "observe" || input.Action == "verify", e
	}
	return false, nil
}

func (h *Handler) licenseAdmission(r *http.Request, audience, session string) error {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || authenticationRecovery(r) {
		return nil
	}
	a, e := h.Service.Authenticate(r.Context(), session, audience)
	if e != nil {
		return e
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/admin/") && !a.Me.User.IsPlatformAdmin {
		return errors.New("ACCESS_DENIED")
	}
	safe, e := recoveryMutation(r)
	if e != nil || safe {
		return e
	}
	return h.Service.RequireNewBusiness(r.Context())
}

func (h *Handler) LicenseInitializationError() error { return h.licenseInitializationError }
