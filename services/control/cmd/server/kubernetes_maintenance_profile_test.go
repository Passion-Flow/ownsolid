package main

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// The unavailable pool is intentional. Accepted profiles must reach the original
// native database owner instead of being rejected as a legacy-only operation.
func TestKubernetesMaintenanceRoutesExplicitMVPToNativeOwner(t *testing.T) {
	for _, profile := range []string{"releases", config.MVPPostgreSQLCPUV1Profile} {
		t.Run(profile, func(t *testing.T) {
			var c config.Config
			c.Server.ApplicationProfile = &profile
			c.Database.MaxOpenConnections = 1
			result, err := observePostgresMaintenance(context.Background(), c)
			if err == nil || err.Error() != "INSTALLATION_POOL_REQUIRES_TWO_CONNECTIONS" || result.Status != "" || result.CurrentLockHeld {
				t.Fatalf("profile did not enter the existing native owner with its pool guard: %#v %v", result, err)
			}
		})
	}
	profile := "localization"
	var c config.Config
	c.Server.ApplicationProfile = &profile
	result, err := observePostgresMaintenance(context.Background(), c)
	if err == nil || err.Error() != "POSTGRES_MAINTENANCE_RELEASES_CONTEXT_REQUIRED" || result.Status != "" {
		t.Fatalf("unsupported profile reached the database owner: %#v %v", result, err)
	}
}
