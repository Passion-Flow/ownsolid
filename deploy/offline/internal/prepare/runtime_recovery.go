package prepare

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

func RuntimeAbsenceRecoveryCommand(command string) bool { return command == "recover-absent-runtime" }

func RunRuntimeAbsenceRecovery(ctx context.Context, c configuration.Config, r configuration.RuntimeAbsenceRecoveryRequest) (any, error) {
	return scope.RecoverAbsentRuntime(ctx, c, r)
}
