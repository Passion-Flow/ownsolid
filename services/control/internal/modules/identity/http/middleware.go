package http

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/middleware/http"
)

type middlewarePolicy struct {
	Enabled                    bool `json:"enabled"`
	RequestTimeoutMilliseconds int  `json:"request_timeout_milliseconds"`
}

func (h *Handler) middlewarePublicPolicy() middlewarePolicy {
	if h.Middleware == nil || h.Middleware.Service == nil {
		return middlewarePolicy{}
	}
	return middlewarePolicy{true, h.Middleware.Service.RequestBudgetSeconds() * 1000}
}

// The exact route owner is shared with dispatch; no prefix alias broadens access.
func (h *Handler) middlewareRequestBudget(path string) int {
	if !http.Owns(path) || h.Middleware == nil || h.Middleware.Service == nil {
		return 0
	}
	return h.Middleware.Service.RequestBudgetSeconds()
}
