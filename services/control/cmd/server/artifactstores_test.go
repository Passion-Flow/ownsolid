package main

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"testing"
)

func TestObjectStorageCannotAdmitUnselectedSchema(t *testing.T) {
	c := config.Config{}
	if e := artifactSchemaAdmission(c, nil); e != nil {
		t.Fatal(e)
	}
	c.ObjectStorage = &config.ObjectStorageConfiguration{Enabled: true}
	if e := artifactSchemaAdmission(c, nil); e == nil || e.Error() != "OBJECT_SCHEMA_NOT_ACTIVE" {
		t.Fatal(e)
	}
	if e := validateUnbootstrappedFeatures(c); e == nil {
		t.Fatal("unbootstrapped remote authority admitted")
	}
}
