package prepare

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

func RestoreReservationCommand(command string) bool {
	return command == "prepare-restore" || command == "reconcile-restore-reservation" || command == "abort-restore-reservation"
}

func RunRestoreReservation(ctx context.Context, command string, c configuration.Config, r configuration.RestoreRequest) (any, error) {
	return scope.RunRestoreReservation(ctx, c, r, command)
}

func RunCompatibleRestoreTarget(ctx context.Context, c configuration.Config, r configuration.RestoreRequest) (any, error) {
	return scope.InspectCompatibleRestoreTarget(ctx, c, r)
}
