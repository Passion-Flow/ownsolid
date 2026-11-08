package runtime

import (
	"context"
	"errors"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/kubernetes"
)

// executeKubernetes runs one task of the Kubernetes node profile under a renewed lease.
// RKE2 files come from control in ranges, bound to this task's lease.
func executeKubernetes(ctx context.Context, connection *client.Client, base string, lease wire.Lease, m *kubernetes.Manager, timeout time.Duration) error {
	return fencedExecution(ctx, connection, base, lease, timeout, func(bounded context.Context, taskBase string) (wire.Result, error) {
		download := func(downloadContext context.Context, artifact wire.KubernetesArtifact, destination string) error {
			return connection.KubernetesArtifact(downloadContext, taskBase+"/"+wire.KubernetesArtifactsPath, lease.LeaseToken, artifact, destination)
		}
		observation, e := m.Execute(bounded, lease.Operation, *lease.Kubernetes, download)
		result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Kubernetes: &observation}
		if e != nil {
			if observation.ErrorCode == "" {
				// The command did not fit the operation: no observation to report.
				return wire.Result{}, e
			}
			result.Outcome, result.ErrorCode = "failure", observation.ErrorCode
		}
		if result.ValidateKubernetes(*lease.Kubernetes, lease.Operation) != nil {
			return result, errors.New("AGENT_RESULT_INVALID")
		}
		return result, nil
	})
}
