package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// Result is decoded as an exact status-discriminated union; optional fields are not interchangeable across variants.
type Result struct {
	SchemaVersion                 int              `json:"schema_version"`
	WorkerVersion                 string           `json:"worker_version"`
	Status                        string           `json:"status"`
	JobID                         string           `json:"job_id"`
	AttemptID                     string           `json:"attempt_id"`
	DatasetVersionID              string           `json:"dataset_version_id,omitempty"`
	DatasetManifestSHA256         string           `json:"dataset_manifest_sha256,omitempty"`
	MemoryEnforcement             string           `json:"memory_enforcement,omitempty"`
	EventCount                    int64            `json:"event_count,omitempty"`
	GlobalStep                    int64            `json:"global_step,omitempty"`
	TrainingLoss                  *float64         `json:"training_loss,omitempty"`
	LossKind                      string           `json:"loss_kind,omitempty"`
	ResumeFromStep                int64            `json:"resume_from_step,omitempty"`
	ParentCheckpointReceiptSHA256 *string          `json:"parent_checkpoint_receipt_sha256,omitempty"`
	AdapterChanged                bool             `json:"adapter_changed,omitempty"`
	BaseParametersFrozen          bool             `json:"base_parameters_frozen,omitempty"`
	Bindings                      *Bindings        `json:"bindings,omitempty"`
	Checkpoints                   []Checkpoint     `json:"checkpoints,omitempty"`
	Metrics                       []Metric         `json:"metrics,omitempty"`
	Files                         []datawire.Asset `json:"files,omitempty"`
	TrainRows                     int64            `json:"train_rows,omitempty"`
	ValidationRows                int64            `json:"validation_rows,omitempty"`
	DeviceProfile                 string           `json:"device_profile,omitempty"`
	ErrorCode                     string           `json:"error_code,omitempty"`
	Partition                     *string          `json:"partition,omitempty"`
	ErrorLineNumber               *int64           `json:"error_line_number,omitempty"`
	Cause                         *string          `json:"cause,omitempty"`
}

func outputError() error { return errors.New("TRAINING_RESULT_INVALID") }

var errorName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,79}$`)

func resultShape(raw []byte, out Result) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return outputError()
	}
	required := strings.Fields("schema_version worker_version status job_id attempt_id")
	optional := []string{}
	if out.Status == "failed" {
		required = append(required, "error_code")
		optional = strings.Fields("partition error_line_number cause")
	} else {
		required = append(required, strings.Fields("dataset_version_id dataset_manifest_sha256 memory_enforcement event_count global_step checkpoints")...)
		if out.Status == "trained" {
			required = append(required, strings.Fields("training_loss loss_kind resume_from_step parent_checkpoint_receipt_sha256 adapter_changed base_parameters_frozen bindings metrics files train_rows validation_rows device_profile")...)
		} else if out.Status == "cancelled" {
			optional = strings.Fields("bindings metrics")
		} else {
			return outputError()
		}
	}
	allowed := map[string]bool{}
	for _, name := range required {
		allowed[name] = true
		if _, ok := fields[name]; !ok {
			return outputError()
		}
	}
	for _, name := range optional {
		allowed[name] = true
	}
	for name := range fields {
		if !allowed[name] {
			return outputError()
		}
		if bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) && name != "parent_checkpoint_receipt_sha256" && !(out.Status == "failed" && (name == "partition" || name == "error_line_number" || name == "cause")) {
			return outputError()
		}
	}
	if metrics, present := fields["metrics"]; present {
		var entries []json.RawMessage
		if json.Unmarshal(metrics, &entries) != nil {
			return outputError()
		}
		for _, entry := range entries {
			var metric map[string]json.RawMessage
			json.Unmarshal(entry, &metric)
			if metricNulls(metric["values"]) != nil {
				return outputError()
			}
		}
	}
	return nil
}

func DecodeResult(raw []byte, r ProcessorRequest, repository string) (Result, error) {
	var out Result
	if r.Validate() != nil || int64(len(raw)) > r.Resources.MaximumResultBytes || DecodeStrict(raw, &out) != nil || resultShape(raw, out) != nil {
		return out, outputError()
	}
	if out.SchemaVersion != 1 || out.WorkerVersion != WorkerVersion || out.JobID != r.JobID || out.AttemptID != r.AttemptID {
		return out, outputError()
	}
	if out.Status == "failed" {
		if !strings.HasPrefix(out.ErrorCode, "TRAINING_") || !errorName.MatchString(out.ErrorCode) || out.Partition != nil && *out.Partition != "train" && *out.Partition != "validation" || out.ErrorLineNumber != nil && (*out.ErrorLineNumber < 1 || *out.ErrorLineNumber > r.DataLimits.MaximumRows+1) || out.Cause != nil && !errorName.MatchString(*out.Cause) {
			return out, outputError()
		}
		return out, nil
	}
	mode := "linux_cgroup_memory_cpu_fsize_wall"
	if r.Configuration.Device == VerificationProfile {
		mode = "native_cpu_fsize_wall_parent_RSS_required_verification"
	}
	if out.DatasetVersionID != r.DatasetVersionID || out.DatasetManifestSHA256 != r.DatasetManifestSHA256 || out.MemoryEnforcement != mode || out.EventCount < 0 || out.EventCount > MaximumEventSequence || out.GlobalStep < 0 || out.GlobalStep > r.Configuration.MaximumSteps || len(out.Checkpoints) > MaximumCheckpointCount || len(out.Metrics) > int(r.Configuration.MaximumSteps*2+1) {
		return out, outputError()
	}
	if out.Bindings != nil && out.Bindings.ValidateFor(r, repository) != nil {
		return out, outputError()
	}
	seen := map[int64]bool{}
	var previous int64
	for _, checkpoint := range out.Checkpoints {
		if checkpoint.GlobalStep < 1 || checkpoint.GlobalStep > out.GlobalStep || checkpoint.GlobalStep <= previous || seen[checkpoint.GlobalStep] || checkpoint.Path != "checkpoint-"+strconv.FormatInt(checkpoint.GlobalStep, 10) || !datawire.ValidDigest(checkpoint.ReceiptSHA256) {
			return out, outputError()
		}
		seen[checkpoint.GlobalStep] = true
		previous = checkpoint.GlobalStep
	}
	previous = 0
	for _, metric := range out.Metrics {
		if metric.Kind != "metric" || metric.GlobalStep < 1 || metric.GlobalStep < previous || metric.GlobalStep > out.GlobalStep || metric.Values.Validate() != nil {
			return out, outputError()
		}
		previous = metric.GlobalStep
	}
	if out.Status == "cancelled" {
		if out.GlobalStep > 0 && (out.Bindings == nil || len(out.Checkpoints) == 0) || out.GlobalStep == 0 && (out.Bindings != nil || len(out.Checkpoints) > 0 || len(out.Metrics) > 0) {
			return out, outputError()
		}
		return out, nil
	}
	if out.GlobalStep != r.Configuration.MaximumSteps || out.Bindings == nil || !out.AdapterChanged || !out.BaseParametersFrozen || out.TrainingLoss == nil || math.IsNaN(*out.TrainingLoss) || math.IsInf(*out.TrainingLoss, 0) || out.DeviceProfile != r.Configuration.Device || out.TrainRows < 1 || out.ValidationRows < 1 || out.TrainRows+out.ValidationRows > r.DataLimits.MaximumRows || out.ResumeFromStep < 0 || out.ResumeFromStep > out.GlobalStep || out.LossKind != "trainer_reported_training_loss" && out.LossKind != "saved_checkpoint_step_loss" {
		return out, outputError()
	}
	if r.ResumeCheckpoint == nil {
		if out.ResumeFromStep != 0 || out.ParentCheckpointReceiptSHA256 != nil {
			return out, outputError()
		}
	} else {
		if out.ResumeFromStep < 1 || out.ParentCheckpointReceiptSHA256 == nil || *out.ParentCheckpointReceiptSHA256 != r.ResumeCheckpoint.ReceiptSHA256 {
			return out, outputError()
		}
	}
	if out.LossKind == "saved_checkpoint_step_loss" && out.ResumeFromStep != out.GlobalStep {
		return out, outputError()
	}
	if e := out.validateInventory(r); e != nil {
		return out, e
	}
	return out, nil
}

func (r Result) validateInventory(request ProcessorRequest) error {
	if len(r.Files) < 3 || int64(len(r.Files)) > request.Resources.MaximumOutputFiles {
		return outputError()
	}
	seen := map[string]datawire.Asset{}
	groups := map[string]bool{}
	var total int64
	for _, asset := range r.Files {
		if asset.SizeBytes < 1 || asset.SizeBytes > request.Resources.MaximumOutputBytes || !datawire.ValidDigest(asset.SHA256) {
			return outputError()
		}
		if _, ok := seen[asset.Path]; ok {
			return outputError()
		}
		seen[asset.Path] = asset
		total += asset.SizeBytes
		parts := strings.Split(asset.Path, "/")
		if len(parts) == 2 && parts[0] == "adapter" {
			if parts[1] != "adapter_model.safetensors" && parts[1] != "adapter_config.json" && parts[1] != "README.md" {
				return outputError()
			}
		} else if len(parts) == 3 && parts[0] == "checkpoints" && strings.HasPrefix(parts[1], "checkpoint-") && checkpointNames[parts[2]] {
			step, e := strconv.ParseInt(strings.TrimPrefix(parts[1], "checkpoint-"), 10, 64)
			if e != nil || step < 1 || step > r.GlobalStep || parts[1] != "checkpoint-"+strconv.FormatInt(step, 10) {
				return outputError()
			}
			groups[parts[1]] = true
		} else {
			return outputError()
		}
	}
	if total > request.Resources.MaximumOutputBytes || len(groups) != len(r.Checkpoints) {
		return outputError()
	}
	for _, name := range []string{"adapter_model.safetensors", "adapter_config.json", "README.md"} {
		if _, ok := seen["adapter/"+name]; !ok {
			return outputError()
		}
	}
	for _, checkpoint := range r.Checkpoints {
		for _, name := range []string{"adapter_model.safetensors", "adapter_config.json", "optimizer.pt", "scheduler.pt", "trainer_state.json", "rng_state.pth", "projectone-checkpoint.json"} {
			asset, ok := seen["checkpoints/"+checkpoint.Path+"/"+name]
			if !ok {
				return outputError()
			}
			if name == "projectone-checkpoint.json" && (asset.SHA256 != checkpoint.ReceiptSHA256 || asset.SizeBytes > MaximumCheckpointMetadataBytes) {
				return outputError()
			}
		}
	}
	return nil
}
