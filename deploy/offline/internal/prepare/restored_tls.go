package prepare

import (
	"context"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

func RestoredTLSCommand(command string) bool {
	return command == "inspect-restored-tls" || scope.TLSProjectionCommand(command)
}
func RunRestoredTLS(ctx context.Context, command string, c configuration.Config, r configuration.TLSGenerationRequest) (any, error) {
	if command != "inspect-restored-tls" {
		return scope.RunTLSProjection(ctx, c, r, command)
	}
	return scope.InspectRestoredTLS(ctx, c, r)
}
