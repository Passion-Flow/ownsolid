package prepare

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

func RestoreDatabaseCommand(command string) bool {
	return command == "qualify-restored-database" || command == "reconcile-restored-database" || command == "abort-restored-database"
}
func RunRestoreDatabase(ctx context.Context, command string, c configuration.Config, r configuration.RestoreRequest) (any, error) {
	return scope.RunRestoreDatabase(ctx, c, r, command)
}
