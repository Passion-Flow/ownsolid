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

func mvpDatabaseCommand(command string) bool {
	return command == databaseaccess.MVPPostgreSQLCPUV1UpgradeCommand || command == databaseaccess.MVPPostgreSQLCPUV1InspectCommand
}

// The existing main FlagSet remains the sole value parser. The new commands
// admit only their exact three path flags, without ignored or duplicate options.
func validateMVPActivationFlags(command string, flags *flag.FlagSet, args []string) error {
	if !mvpDatabaseCommand(command) {
		return nil
	}
	bad := errors.New("MVP_POSTGRESQL_ARGUMENT_INVALID")
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

// Serving first verifies the exact selected original lease/catalog. Only the
// new full profile then requires the current DML-only runtime principal. An
// operator's successful schema inspection never authorizes serving as superuser.
func verifyServingDatabase(ctx context.Context, c config.Config, selected *selectedDatabase) error {
	if e := selected.Verify(ctx); e != nil {
		return e
	}
	if c.MVPPostgreSQLCPUV1Only() {
		return database.CheckMVPPostgreSQLCPUV1RuntimeWithin(ctx, selected.store)
	}
	return nil
}
