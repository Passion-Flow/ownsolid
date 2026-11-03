package prepare

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

func RestoreSQLCommand(command string) bool {
	return command == "restore-sql" || command == "reconcile-restore-sql" || command == "abort-restore-sql"
}

func RunRestoreSQL(ctx context.Context, command string, c configuration.Config, r configuration.RestoreRequest) (any, error) {
	return scope.RunRestoreSQL(ctx, c, r, command)
}
