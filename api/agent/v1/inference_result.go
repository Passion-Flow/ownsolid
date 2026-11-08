package v1

import "errors"

// ValidateInference binds a bounded observation to the exact typed task intent.
func (r Result) ValidateInference(command InferenceCommand) error {
	bad := errors.New("VALIDATION_FAILED")
	if command.Validate(command.Operation()) != nil || r.Facts != nil || r.Component != nil || r.Logs != nil || r.Gateway != nil || r.Inference == nil || r.Inference.Validate() != nil {
		return bad
	}
	o := r.Inference
	if d := o.Deployment; d != nil && command.DeploymentID != "" {
		if d.DeploymentID != command.DeploymentID || d.ModelID != command.ModelID || d.CatalogDigest != command.CatalogDigest || d.GPUUUID != command.GPUUUID || d.Revision != command.Revision || !command.MatchesServingIdentity(*d) {
			return bad
		}
	}
	if r.Outcome == "failure" {
		if r.ErrorCode == "" || !KnownInferenceError(r.ErrorCode) || o.ErrorCode != r.ErrorCode {
			return bad
		}
		return nil
	}
	if r.Outcome != "success" || r.ErrorCode != "" || o.ErrorCode != "" {
		return bad
	}
	if command.DeploymentID == "" {
		return nil
	}
	d := o.Deployment
	if d == nil || d.ErrorCode != "" {
		return bad
	}
	switch command.Action {
	case "deploy", "verify":
		if !d.Managed || d.State != "running" || !d.Ready || d.Verification == nil {
			return bad
		}
	case "stop":
		if d.Ready || d.State != "stopped" && d.State != "not_installed" {
			return bad
		}
	case "remove":
		if d.Managed || d.State != "not_installed" || d.ContainerID != "" {
			return bad
		}
	}
	return nil
}
