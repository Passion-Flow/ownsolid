package http

import monitoringhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring/http"

type monitoringPolicy struct {
	Enabled                    bool `json:"enabled"`
	RequestTimeoutMilliseconds int  `json:"request_timeout_milliseconds"`
}

func (h *Handler) monitoringPublicPolicy(audience string) monitoringPolicy {
	if audience != "admin" || h.Monitoring == nil || h.Monitoring.Service == nil {
		return monitoringPolicy{}
	}
	budget := h.Monitoring.Service.RequestBudgetSeconds()
	if budget <= 0 {
		return monitoringPolicy{}
	}
	return monitoringPolicy{true, budget * 1000}
}

func (h *Handler) monitoringRequestBudget(path string) int {
	if !monitoringhttp.Owns(path) || h.Monitoring == nil || h.Monitoring.Service == nil {
		return 0
	}
	return h.Monitoring.Service.RequestBudgetSeconds()
}
