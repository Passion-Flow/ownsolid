package http

import (
	"reflect"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestFullMVPCallbackOriginsPreserveExactDeploymentAndBoundedProfiles(t *testing.T) {
	for _, profile := range []string{"platform", config.MVPPostgreSQLCPUV1Profile, "account"} {
		c := config.Config{}
		c.Server.ApplicationProfile = &profile
		c.Server.PortalOrigin, c.Server.AdminOrigin = "https://portal.example.invalid:8443", "https://127.0.0.1:9443"
		got := ssoCallbackOrigins(c)
		if profile == "account" {
			if len(got) != 0 {
				t.Fatal("bounded account exposed later callback graph")
			}
		} else if !reflect.DeepEqual(got, []string{c.Server.PortalOrigin, c.Server.AdminOrigin}) {
			t.Fatal("full selected origins lost or rewritten", got)
		}
	}
	profile := config.MVPPostgreSQLCPUV1Profile
	c := config.Config{}
	c.Server.ApplicationProfile = &profile
	c.Server.PortalOrigin, c.Server.AdminOrigin = "https://localhost:8443", "https://localhost:8443"
	if got := ssoCallbackOrigins(c); !reflect.DeepEqual(got, []string{c.Server.PortalOrigin}) {
		t.Fatal("duplicate exact origin", got)
	}
	c.Server.AdminOrigin = "http://localhost:8443"
	if got := ssoCallbackOrigins(c); len(got) != 0 {
		t.Fatal("provider HTTPS restriction was weakened", got)
	}
}
