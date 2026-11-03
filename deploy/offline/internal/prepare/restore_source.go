package prepare

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

func RestoreSourceCommand(command string) bool {
	return command == "inspect-restore-source" || command == "inspect-restore-target"
}

func RunRestoreSource(ctx context.Context, c configuration.Config, r configuration.RestoreSourceRequest) (any, error) {
	return scope.InspectRestoreSource(ctx, c, r)
}

func RunRestoreTarget(ctx context.Context, c configuration.Config, r configuration.RestoreSourceRequest) (any, error) {
	return scope.InspectRestoreTarget(ctx, c, r)
}
