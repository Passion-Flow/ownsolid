package prepare

import (
	"context"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

func BackupCommand(command string) bool {
	return command == "backup" || command == "reconcile-backup" || command == "abort-backup" || command == "inspect-backup"
}
func RunBackup(ctx context.Context, command string, c configuration.Config, r configuration.BackupRequest) (any, error) {
	return scope.RunBackup(ctx, c, r, command)
}
