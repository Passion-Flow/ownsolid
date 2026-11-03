package prepare

import (
	"context"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

func CumulativeUpgradeCommand(command string) bool { return scope.CumulativeUpgradeCommand(command) }
func RunCumulativeUpgrade(ctx context.Context, command string, c configuration.Config, r configuration.CumulativeUpgradeRequest) (any, error) {
	return scope.RunCumulativeUpgrade(ctx, c, r, command)
}
