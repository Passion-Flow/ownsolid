package http

import (
	"net/http"
	artifacthttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/artifactstores/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"strings"
)

// Two full transfers bound verified readback followed by attachment delivery.
// SQL authority work keeps the existing two transaction budgets.
func artifactTransferSeconds(c config.Config) int {
	if c.ObjectStorage == nil || !c.ObjectStorage.Enabled {
		return 0
	}
	return 2*c.ObjectStorage.TransferSeconds + 2*c.Policy().HTTP.RequestSeconds
}
func artifactTransferBudget(c config.Config, r *http.Request) int {
	if !artifacthttp.Owns(r.URL.Path) {
		return 0
	}
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	start := 4
	if p[2] == "workspaces" {
		start = 5
	}
	tail := p[start:]
	if len(tail) == 3 && tail[1] == "parts" && r.Method == http.MethodPut || len(tail) == 2 && (tail[1] == "content" || tail[1] == "snapshot") && r.Method == http.MethodGet {
		return artifactTransferSeconds(c)
	}
	return 0
}
func artifactPublicPolicy(c config.Config) map[string]any {
	enabled := c.ObjectStorage != nil && c.ObjectStorage.Enabled
	return map[string]any{"enabled": enabled, "transfer_timeout_milliseconds": artifactTransferSeconds(c) * 1000}
}
