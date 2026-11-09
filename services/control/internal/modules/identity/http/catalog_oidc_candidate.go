package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const catalogOIDCApprovalPath = "/api/v1/me/catalog-oidc/approvals"

func (h *Handler) approveCatalogOIDCCandidate(r *http.Request, audience, origin, session, requestID string) (any, int, error) {
	if r.Method != http.MethodPost || audience != "portal" || !strings.HasPrefix(origin, "https://") || r.URL.RawPath != "" {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		return nil, 0, errors.New("VALIDATION_FAILED")
	}
	a, err := h.Service.Authenticate(r.Context(), session, audience)
	if err != nil {
		return nil, 0, err
	}
	var input identity.CatalogOIDCCandidateApproval
	if err := decode(r, &input); err != nil {
		return nil, 0, err
	}
	rates := h.Service.Config.Policy().Rates
	if _, err := h.Service.Admit(r.Context(), identity.Limit{
		Purpose: "catalog_oidc.approve", Identifier: a.Me.User.ID, Count: rates.LoginEmailCount, Window: config.Seconds(rates.LoginEmailWindowSeconds),
	}); err != nil {
		return nil, 0, err
	}
	callback, err := h.Service.ApproveCatalogOIDCCandidate(r.Context(), a, input, requestID)
	if err != nil {
		return nil, 0, err
	}
	return map[string]string{"redirect_uri": callback}, http.StatusCreated, nil
}
