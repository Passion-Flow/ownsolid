package databaseaccess

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// ValidateMySQLMigrationContext requires the existing protected runtime authority
// before opening an operator connection. The runtime login may not exist yet.
func ValidateMySQLMigrationContext(c config.Config, input MySQLRuntimeInput) error {
	if e := input.verify(); e != nil {
		return e
	}
	return mysqloperator.VerifyDeclaredRuntimeSelection(c, input.connection)
}

func MigrateMySQLWithRuntimeContext(ctx context.Context, kernel *mysqlstore.Store, c config.Config, catalog mysqlmigrate.Catalog, options mysqlmigrate.Options, input MySQLRuntimeInput) (mysqlmigrate.Report, error) {
	if e := ValidateMySQLMigrationContext(c, input); e != nil {
		return mysqlmigrate.Report{}, e
	}
	if e := mysqloperator.VerifyConnectorInputs(ctx, kernel, c); e != nil {
		return mysqlmigrate.Report{}, e
	}
	// Run alone owns the installation lock, journal, DDL, metadata and release.
	report, e := mysqlmigrate.Run(ctx, kernel, catalog, options, mysqlmigrate.Apply, nil)
	if e != nil {
		return mysqlmigrate.Report{}, e
	}
	if !report.Ready || !report.LockReleased {
		return mysqlmigrate.Report{}, errors.New("MYSQL_MIGRATION_NOT_VERIFIED")
	}
	if e = ValidateMySQLMigrationContext(c, input); e != nil {
		return mysqlmigrate.Report{}, e
	}
	if e = mysqloperator.VerifyConnectorInputs(ctx, kernel, c); e != nil {
		return mysqlmigrate.Report{}, e
	}
	return report, nil
}
