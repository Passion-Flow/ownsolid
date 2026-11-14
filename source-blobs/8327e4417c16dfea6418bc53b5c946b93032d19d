package http

import "net/http"

type privateClusterPolicy struct {
	Enabled                      bool  `json:"enabled"`
	OperationTimeoutMilliseconds int   `json:"operation_timeout_milliseconds"`
	MaximumResponseBytes         int64 `json:"maximum_response_bytes"`
}

func (h *Handler) privateClusterPublicPolicy() privateClusterPolicy {
	if h.PrivateClusters == nil || h.PrivateClusters.Service == nil {
		return privateClusterPolicy{}
	}
	budget := h.PrivateClusters.Service.OperationBudgetSeconds()
	if budget <= 0 || h.PrivateClusters.Service.MaximumResponseBytes() <= 0 {
		return privateClusterPolicy{}
	}
	return privateClusterPolicy{true, budget * 1000, h.PrivateClusters.Service.MaximumResponseBytes()}
}

// These routes reduce or observe already fenced effects. The ordinary dispatcher
// still authenticates, verifies Origin/CSRF and invokes current module authority.
// A body cannot select another operation or supply an executable specification.
func privateClusterRecoveryMutation(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost || r.URL.RawPath != "" {
		return false
	}
	if licensePath(r.URL.Path, "/api/v1/admin/private-clusters/connections/:id/disable") {
		return true
	}
	for _, root := range []string{"/api/v1/admin/private-clusters", "/api/v1/workspaces/:id/private-clusters"} {
		for _, action := range []string{"reconcile", "cancel", "cleanup"} {
			if licensePath(r.URL.Path, root+"/connections/:id/runs/:id/"+action) {
				return true
			}
		}
	}
	return false
}
