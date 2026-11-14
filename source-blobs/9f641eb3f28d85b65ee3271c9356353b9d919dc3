package main

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestMiddlewareRuntimeAdmissionRequiresActualOwner(t *testing.T) {
	var c config.Config
	if out, e := openMiddleware(context.Background(), c, nil, nil); e != nil || out != nil {
		t.Fatal("omitted component constructed an owner", e)
	}
	c.MiddlewareEnvironmentFile = "/private/policy.env"
	if out, e := openMiddleware(context.Background(), c, nil, nil); e == nil || out != nil {
		t.Fatal("orphan policy admitted")
	}
	c.MiddlewareConfigFile = "/private/configuration.json"
	for _, ctx := range []context.Context{nil, context.Background()} {
		if out, e := openMiddleware(ctx, c, nil, nil); e == nil || out != nil || e.Error() != "MIDDLEWARE_IDENTITY_REPOSITORY_REQUIRED" {
			t.Fatal("missing actual owner admitted", e)
		}
	}
	if e := validateUnbootstrappedFeatures(c); e == nil || e.Error() != "BOOTSTRAP_REQUIRED_FOR_CONFIGURED_FEATURES" {
		t.Fatal("uninstalled configured component admitted", e)
	}
}
