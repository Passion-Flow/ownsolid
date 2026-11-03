package prepare

import (
	"context"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

func JournalCapacityCommand(command string) bool { return command == "extend-journal-capacity" }
func RunJournalCapacity(ctx context.Context, c configuration.Config, r configuration.JournalCapacityRequest) (any, error) {
	return scope.ExtendJournalCapacity(ctx, c, r)
}
