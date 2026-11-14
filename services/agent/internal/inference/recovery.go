package inference

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// RemoveFenced executes only under the caller's exclusive agent identity lock after explicit operator fencing.
func (m *Manager) RemoveFenced(ctx context.Context, fencedNode, deployment string) (wire.InferenceRemovalReceipt, error) {
	var out wire.InferenceRemovalReceipt
	j, e := m.read()
	if e != nil {
		return out, e
	}
	if !deploymentID.MatchString(fencedNode) || j.NodeID != fencedNode || j.Command.DeploymentID != deployment || j.Command.Revision >= 9007199254740991 {
		return out, fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return out, fail("INFERENCE_IO_FAILED")
	}
	nonce[6] = (nonce[6] & 15) | 64
	nonce[8] = (nonce[8] & 63) | 128
	receipt := fmt.Sprintf("%x-%x-%x-%x-%x", nonce[0:4], nonce[4:6], nonce[6:8], nonce[8:10], nonce[10:16])
	command := j.Command
	command.Action = "remove"
	command.Revision++
	observation, e := m.Execute(ctx, fencedNode, receipt, command, nil)
	if e != nil {
		return out, e
	}
	if observation.Deployment == nil || observation.Deployment.State != "not_installed" || observation.Deployment.Managed {
		return out, fail("INFERENCE_RECOVERY_REQUIRED")
	}
	if _, e = os.Lstat(m.deploymentPath(deployment)); !os.IsNotExist(e) {
		return out, fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	// The removed journal observation alone is insufficient; recheck actual named container absence.
	if _, e = m.docker.InspectContainer(ctx, "projectone-vllm-"+deployment); e == nil || e.Error() != "INFERENCE_DOCKER_NOT_FOUND" {
		return out, fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	_, e = os.Lstat(m.commandModelPath(command))
	present := e == nil
	if e != nil && !os.IsNotExist(e) {
		return out, fail("INFERENCE_IO_FAILED")
	}
	out = wire.InferenceRemovalReceipt{Derived: command.Derived, SchemaVersion: 1, ReceiptID: receipt, NodeID: fencedNode, DeploymentID: deployment, ModelID: command.ModelID, CatalogDigest: command.CatalogDigest, GPUUUID: command.GPUUUID, LocalRevision: command.Revision, OwnedContainerAbsent: true, RuntimeCacheAbsent: true, ModelCachePresent: present, CompletedAt: time.Now().UTC()}
	if e = out.Validate(); e != nil {
		return out, e
	}
	return out, nil
}
