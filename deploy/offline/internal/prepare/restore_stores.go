package prepare

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

func RestoreStoresCommand(command string) bool {
	return command == "restore-stores" || command == "reconcile-restored-stores" || command == "abort-restored-stores"
}
func RunRestoreStores(ctx context.Context, command string, c configuration.Config, r configuration.RestoreRequest) (any, error) {
	return scope.RunRestoreStores(ctx, c, r, command)
}
