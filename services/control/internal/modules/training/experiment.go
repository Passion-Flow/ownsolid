package training

import (
	"context"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

// AcceptedExecution binds the frozen image reference to the image ID in the
// authenticated stop and protected worker result. A nil lock or observation
// means that the attempt has no verified evidence for that field.
type AcceptedExecution struct {
	ImageReference       string
	ImageID              string
	WorkerSourceSHA256   string
	DependencyLockSHA256 *string
	HardwareObservation  *agentwire.NVIDIAGPU
}

// AcceptedExperiment is the historical record for one accepted adapter result.
// Command holds the locked inputs, configuration, image and selected GPU.
// NodeID and the GPU UUID identify placement, without asserting device facts.
type AcceptedExperiment struct {
	Command         wire.Command
	CommandSHA256   string
	NodeID          string
	ModelRepository string
	ModelRevision   string
	Packages        wire.Packages
	Execution       AcceptedExecution
	GlobalStep      int64
	ArtifactID      string
	Files           []datawire.Asset
	ResultSHA256    string
	StopSHA256      string
}

// ReadAcceptedExperiment returns a verified run for a currently authorized
// workspace actor. An absent ModelRevision means the current catalogue does
// not match the historical catalogue digest in Command.
func (s *Service) ReadAcceptedExperiment(ctx context.Context, a identity.Auth, workspace, job, requestID string) (AcceptedExperiment, error) {
	var out AcceptedExperiment
	e := s.withVerifiedAdapterExport(ctx, a, workspace, job, requestID, func(record acceptedAdapterRecord) error {
		if e := s.VerifyAcceptedAdapter(ctx, record.AcceptedAdapter); e != nil {
			return e
		}
		var modelRevision string
		if record.Command.ModelCatalogSHA256 == agentwire.ModelCatalogDigest() {
			modelRevision = agentwire.ApprovedModel().Revision
		}
		out = AcceptedExperiment{
			Command: record.Command, CommandSHA256: record.stop.CommandSHA256,
			NodeID: record.Job.CurrentNodeID, ModelRepository: record.result.Bindings.ModelRepository,
			ModelRevision: modelRevision, Packages: record.result.Bindings.Packages,
			GlobalStep: record.result.GlobalStep, ArtifactID: record.Descriptor.UploadID,
			Files: record.Descriptor.Files, ResultSHA256: record.Descriptor.ManifestSHA256,
			StopSHA256: record.stopSHA256,
			Execution: AcceptedExecution{
				ImageReference:     record.Command.Recipe.ImageReference,
				ImageID:            record.stop.ImageID,
				WorkerSourceSHA256: record.result.Bindings.ExecutionIdentity.WorkerSourceSHA256,
			},
		}
		return nil
	})
	if e != nil {
		return AcceptedExperiment{}, e
	}
	return out, nil
}
