package mysqloperator

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
)

func TestFreshTargetCannotBeFabricatedFromMissingConnectorAndLock(t *testing.T) {
	f, e := NewFresh(context.Background(), nil, nil, config.Config{}, mysqlmigrate.Catalog{}, mysqlmigrate.Options{})
	if e == nil || f != nil {
		t.Fatal("missing actual selected connector fabricated fresh target")
	}
	var missing *Fresh
	if e = missing.Verify(t.Context()); e == nil {
		t.Fatal("missing actual owner verified")
	}
	if _, e = missing.Promote(t.Context()); e == nil {
		t.Fatal("missing fresh target became installed context")
	}
}
