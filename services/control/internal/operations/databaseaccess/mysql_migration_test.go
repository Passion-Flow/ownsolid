package databaseaccess

import (
	"reflect"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
)

func TestMySQLMigrationMissingPrivateAuthorityReturnsZeroReport(t *testing.T) {
	c := config.Config{}
	if ValidateMySQLMigrationContext(c, MySQLRuntimeInput{}) == nil {
		t.Fatal("missing protected authority accepted")
	}
	r, e := MigrateMySQLWithRuntimeContext(t.Context(), nil, c, mysqlmigrate.Catalog{}, mysqlmigrate.Options{}, MySQLRuntimeInput{})
	if e == nil || !reflect.DeepEqual(r, mysqlmigrate.Report{}) {
		t.Fatal("missing real authority produced success")
	}
}
