package http

import (
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	monitoringhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring/http"
)

func TestMonitoringDisabledAndPortalPolicyFailClosed(t *testing.T) {
	for _, h := range []*Handler{{}, {Monitoring: &monitoringhttp.Handler{}}, {Monitoring: &monitoringhttp.Handler{Service: &monitoring.Service{}}}} {
		for _, audience := range []string{"admin", "portal", ""} {
			p := h.monitoringPublicPolicy(audience)
			if p.Enabled || p.RequestTimeoutMilliseconds != 0 {
				t.Fatal("unavailable policy advertised", p)
			}
		}
		for _, path := range []string{monitoringhttp.Path, monitoringhttp.Path + "/alias", "/api/v1/admin/workspaces"} {
			if h.monitoringRequestBudget(path) != 0 {
				t.Fatal("unavailable route owns deadline")
			}
		}
	}
}
