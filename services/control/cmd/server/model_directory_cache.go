package main

import (
	"errors"

	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/modelcache"
)

// The client validates protected local inputs and dials only when a directory
// request uses it. Availability is optional; SQL remains the directory authority.
// Connections belong to individual bounded requests, so there is no worker or
// connection-pool shutdown obligation to add to runtimeLifecycle.
func openModelDirectoryCache(c config.Config, h *identityhttp.Handler) error {
	if c.ModelCache.Enabled && (h == nil || h.Service == nil || h.Models == nil || h.Models.Service == nil || h.Models.Service.Identity != h.Service || h.Models.Service.DirectoryCache != nil) {
		return errors.New("MODEL_CACHE_OWNER_REQUIRED")
	}
	client, e := modelcache.New(c.ModelCache)
	if e != nil {
		return e
	}
	if client != nil {
		h.Models.Service.DirectoryCache = client
	}
	return nil
}
