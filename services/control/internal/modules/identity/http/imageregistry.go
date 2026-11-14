package http

import (
	"net/http"

	registryhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/imageregistry/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

type registryRuntimePolicy struct {
	Enabled               bool `json:"enabled"`
	RequestMilliseconds   int  `json:"request_timeout_milliseconds"`
	OperationMilliseconds int  `json:"operation_timeout_milliseconds"`
}

func registryPublicPolicy(c config.Config) registryRuntimePolicy {
	if c.ImageRegistry == nil {
		return registryRuntimePolicy{}
	}
	return registryRuntimePolicy{true, c.ImageRegistry.RequestSeconds * 1000, c.ImageRegistry.OperationSeconds * 1000}
}

func registryRequestBudget(c config.Config, r *http.Request) int {
	if c.ImageRegistry == nil || r.URL.RawPath != "" {
		return 0
	}
	if registryhttp.MachineOwns(r.URL.Path) {
		return c.ImageRegistry.RequestSeconds
	}
	if registryhttp.Owns(r.URL.Path) {
		return c.ImageRegistry.OperationSeconds
	}
	return 0
}
