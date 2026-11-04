package main

import (
	"context"
	"errors"
	"flag"
	"path/filepath"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

func schemaUpgradeCommand(command string) bool {
	return command == databaseaccess.UpgradeCommand || command == databaseaccess.UpgradeInspectCommand
}

// The existing main FlagSet remains the sole value parser. The new commands
// admit only their exact three path flags, without ignored or duplicate options.
func validateSchemaUpgradeFlags(command string, flags *flag.FlagSet, args []string) error {
	if !schemaUpgradeCommand(command) {
		return nil
	}
	bad := errors.New("SCHEMA_UPGRADE_ARGUMENT_INVALID")
	allowed := map[string]bool{"config": true, "runtime-config": true, "env-file": true}
	var invalid bool
	flags.Visit(func(f *flag.Flag) {
		value := f.Value.String()
		if !allowed[f.Name] || value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			invalid = true
		}
	})
	seen := map[string]bool{}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if allowed[name] {
			if seen[name] {
				invalid = true
			}
			seen[name] = true
		}
	}
	if invalid {
		return bad
	}
	return nil
}

// Serving first verifies the exact selected original lease/catalog, then
// requires the current DML-only runtime principal. An operator's successful
// schema inspection never authorizes serving as superuser.
func verifyServingDatabase(ctx context.Context, c config.Config, selected *selectedDatabase) error {
	if e := selected.Verify(ctx); e != nil {
		return e
	}
	if c.DatabaseEngine() != config.PostgreSQLDatabaseEngine {
		return nil
	}
	return database.CheckRuntimeWithin(ctx, selected.store)
}
