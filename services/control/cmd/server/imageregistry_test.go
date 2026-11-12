package main

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/registryconfig"
)

func TestRegistryCompositionRequiresActiveSchemaAndBootstrap(t *testing.T) {
	c := config.Config{}
	if e := registrySchemaAdmission(c, nil); e != nil {
		t.Fatal(e)
	}
	if s, e := openImageRegistry(context.Background(), c, nil, nil); s != nil || e == nil || e.Error() != "REGISTRY_OWNER_REQUIRED" {
		t.Fatal("omitted owner lacked actual state admission", e)
	}
	c.ImageRegistry = &registryconfig.Config{StoreRoot: "/nonexistent/explicit-unit-refusal"}
	if e := registrySchemaAdmission(c, nil); e == nil || e.Error() != "REGISTRY_SCHEMA_NOT_ACTIVE" {
		t.Fatal("unselected schema admitted", e)
	}
	if s, e := openImageRegistry(context.Background(), c, nil, nil); s != nil || e == nil || e.Error() != "REGISTRY_SCHEMA_NOT_ACTIVE" {
		t.Fatal("configuration reached path acquisition before schema admission", e)
	}
	if e := validateUnbootstrappedFeatures(c); e == nil || e.Error() != "BOOTSTRAP_REQUIRED_FOR_CONFIGURED_FEATURES" {
		t.Fatal("unbootstrapped registry admitted", e)
	}
}

func TestRegistrySiteSourceRequiresWritableOwner(t *testing.T) {
	c := config.Config{ImageRegistrySiteSourceFile: "/run/ownsolid/site-source.json"}
	if err := validateUnbootstrappedFeatures(c); err == nil || err.Error() != "BOOTSTRAP_REQUIRED_FOR_CONFIGURED_FEATURES" {
		t.Fatal("unbootstrapped source was ignored", err)
	}
	if err := registrySchemaAdmission(c, nil); err == nil || err.Error() != "CONFIG_IMAGE_REGISTRY_INVALID" {
		t.Fatal("source bypassed writable registry admission", err)
	}
	if service, err := openImageRegistry(t.Context(), c, nil, nil); service != nil || err == nil || err.Error() != "CONFIG_IMAGE_REGISTRY_INVALID" {
		t.Fatal("disabled registry selected an explicit source", err)
	}
}
