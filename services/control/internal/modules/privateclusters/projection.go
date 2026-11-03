package privateclusters

import (
	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	proof "github.com/Passion-Flow/ownsolid/services/control/internal/platform/privateclusterproof"
)

type ExecutionProof struct {
	RunID                      string      `json:"run_id"`
	Profile                    string      `json:"profile"`
	ManufacturerManifestSHA256 string      `json:"manufacturer_manifest_sha256"`
	ImageDigest                string      `json:"image_digest"`
	ConfigDigest               string      `json:"config_digest"`
	ObservedImageID            string      `json:"observed_image_id"`
	NodeName                   string      `json:"node_name"`
	ContainerID                string      `json:"container_id"`
	Proof                      proof.Proof `json:"proof"`
	Logs                       string      `json:"logs"`
	Qualification              string      `json:"qualification"`
}

func (s *Service) Proof(ctx context.Context, a identity.Auth, workspace, connection, id string) (ExecutionProof, error) {
	r, e := s.GetRun(ctx, a, workspace, connection, id)
	if e != nil {
		return ExecutionProof{}, e
	}
	if r.Result == nil || *r.Result != "succeeded" || r.Remote == nil || r.Remote.Proof == nil || r.PodUID == nil {
		return ExecutionProof{}, fail("PRIVATE_CLUSTER_SUCCESS_PROOF_UNAVAILABLE")
	}
	verified, e := proof.Verify([]byte(r.Remote.Logs), r.request.Challenge, *r.PodUID)
	if e != nil || verified != *r.Remote.Proof || r.Remote.ImageID != r.request.ConfigDigest {
		return ExecutionProof{}, fail("PRIVATE_CLUSTER_STORED_PROOF_INVALID")
	}
	return ExecutionProof{r.ID, r.request.Profile, r.request.ManifestSHA, r.request.ImageDigest, r.request.ConfigDigest, r.Remote.ImageID, r.Remote.NodeName, r.Remote.ContainerID, verified, r.Remote.Logs, "Observed bounded probe execution only; no exactly-once, effective CNI, production workload, GPU or other architecture qualification."}, nil
}
