package v1

import "path"

// ContainerRequest is the shared declared image filesystem ABI, not a caller-selected host path.
func (c Command) ContainerRequest() (ProcessorRequest, error) {
	if e := c.Validate(); e != nil {
		return ProcessorRequest{}, e
	}
	directory := path.Join(ContainerAttemptsDirectory, c.Scope.JobID, c.AttemptID)
	r := ProcessorRequest{SchemaVersion: 1, Operation: "train", TenantID: c.Scope.TenantID, WorkspaceID: c.Scope.WorkspaceID, JobID: c.Scope.JobID, AttemptID: c.AttemptID, DatasetVersionID: c.DatasetVersionID, DatasetManifestSHA256: c.DatasetManifestSHA256, ScopeDirectory: directory, ModelDirectory: ContainerModelDirectory, ModelCatalogSHA256: c.ModelCatalogSHA256, TrainFile: path.Join(directory, "inputs/train.jsonl"), TrainSHA256: c.Inputs.Train.SHA256, ValidationFile: path.Join(directory, "inputs/validation.jsonl"), ValidationSHA256: c.Inputs.Validation.SHA256, OutputDirectory: path.Join(directory, "output"), Configuration: c.Configuration, DataLimits: c.Recipe.DataLimits, Resources: c.Recipe.Resources, ExecutionIdentity: c.Recipe.Identity}
	if c.Resume != nil {
		r.ResumeCheckpoint = &CheckpointReference{Directory: path.Join(directory, "inputs/checkpoint"), ReceiptSHA256: c.Resume.ReceiptSHA256}
	}
	return r, r.Validate()
}
