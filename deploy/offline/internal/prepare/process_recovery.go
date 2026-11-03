package prepare

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

func ProcessRecoveryCommand(command string) bool {
	return command == "resume-restored-database" || command == "resume-restored-application"
}

func RunProcessRecovery(ctx context.Context, command string, c configuration.Config, r configuration.ProcessRecoveryRequest) (any, error) {
	if command == "resume-restored-database" {
		return scope.ResumeRestoredDatabase(ctx, c, r)
	}
	if command == "resume-restored-application" {
		return scope.ResumeRestoredApplication(ctx, c, r)
	}
	return nil, errors.New("OFFLINE_OPERATOR_PROCESS_RECOVERY_COMMAND_INVALID")
}
