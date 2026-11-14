package runtime

import (
	"context"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/progress"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
)

// executeGateway starts or stops this node's model gateway under the task's lease (GW-114).
// A start activates the credential and key it generated with one call bound to the lease, so
// a task that lost its lease can activate nothing.
func executeGateway(ctx context.Context, connection *client.Client, base string, identity state.Identity, lease wire.Lease, m *inference.GatewayManager) error {
	command := *lease.Gateway
	return fencedExecution(ctx, connection, base, lease, settings.Seconds(connection.Policy().RemoveSeconds), func(bounded context.Context, taskBase string) (wire.Result, error) {
		var observation wire.GatewayObservation
		if command.Action == "start" {
			progress.Step(bounded, wire.ProgressStepStart)
			activate := func(activation context.Context, credential, request, origin string) (wire.GatewayActivated, error) {
				return connection.ActivateGateway(activation, taskBase, wire.GatewayActivation{LeaseToken: lease.LeaseToken, Credential: credential, CertificateRequest: request, Origin: origin})
			}
			observation = m.Start(bounded, identity.NodeID, identity.Scope(), command, activate)
		} else {
			progress.Step(bounded, wire.ProgressStepStop)
			observation = m.Stop(bounded, command)
		}
		result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Gateway: &observation}
		if observation.ErrorCode != "" {
			progress.Error(bounded, "The model gateway task failed: %s", observation.ErrorCode)
			result.Outcome, result.ErrorCode = "failure", observation.ErrorCode
		}
		if e := result.ValidateGateway(command); e != nil {
			return result, e
		}
		return result, nil
	})
}
