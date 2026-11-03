package training

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"path/filepath"

	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// Trainer state is pinned third-party metadata, never an executable checkpoint object or a human export.
func verifyTrainerState(raw []byte, step int64, c wire.Configuration) error {
	keys := []string{"best_global_step", "best_metric", "best_model_checkpoint", "epoch", "eval_steps", "global_step", "is_hyper_param_search", "is_local_process_zero", "is_world_process_zero", "log_history", "logging_steps", "max_steps", "num_input_tokens_seen", "num_train_epochs", "save_steps", "stateful_callbacks", "total_flos", "train_batch_size", "trial_name", "trial_params"}
	values, e := wire.ExactObject(raw, keys)
	if e != nil {
		return fail("TRAINING_CHECKPOINT_INVALID")
	}
	for _, key := range []string{"best_global_step", "best_metric", "best_model_checkpoint", "trial_name", "trial_params"} {
		if !bytes.Equal(bytes.TrimSpace(values[key]), []byte("null")) {
			return fail("TRAINING_CHECKPOINT_INVALID")
		}
	}
	for key, wanted := range map[string]int64{"global_step": step, "max_steps": c.MaximumSteps, "logging_steps": 1, "save_steps": c.CheckpointSteps, "train_batch_size": c.BatchSize, "num_input_tokens_seen": 0} {
		var actual int64
		if wire.DecodeStrict(values[key], &actual) != nil || actual != wanted {
			return fail("TRAINING_CHECKPOINT_INVALID")
		}
	}
	for key, wanted := range map[string]bool{"is_hyper_param_search": false, "is_local_process_zero": true, "is_world_process_zero": true} {
		var actual bool
		if wire.DecodeStrict(values[key], &actual) != nil || actual != wanted {
			return fail("TRAINING_CHECKPOINT_INVALID")
		}
	}
	for _, key := range []string{"epoch", "total_flos", "num_train_epochs", "eval_steps"} {
		var value float64
		if json.Unmarshal(values[key], &value) != nil || bytes.Equal(values[key], []byte("null")) || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fail("TRAINING_CHECKPOINT_INVALID")
		}
	}
	var logs []json.RawMessage
	if json.Unmarshal(values["log_history"], &logs) != nil || len(logs) < 1 || int64(len(logs)) > c.MaximumSteps+1 {
		return fail("TRAINING_CHECKPOINT_INVALID")
	}
	var previous int64
	for _, entry := range logs {
		var fields map[string]json.RawMessage
		if json.Unmarshal(entry, &fields) != nil || fields == nil {
			return fail("TRAINING_CHECKPOINT_INVALID")
		}
		var current int64
		if json.Unmarshal(fields["step"], &current) != nil || current < previous || current < 1 || current > step {
			return fail("TRAINING_CHECKPOINT_INVALID")
		}
		previous = current
		for key, raw := range fields {
			switch key {
			case "step", "loss", "learning_rate", "grad_norm", "epoch", "train_runtime", "train_samples_per_second", "train_steps_per_second", "total_flos", "train_loss":
			default:
				return fail("TRAINING_CHECKPOINT_INVALID")
			}
			var value float64
			if json.Unmarshal(raw, &value) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || math.IsNaN(value) || math.IsInf(value, 0) {
				return fail("TRAINING_CHECKPOINT_INVALID")
			}
		}
	}
	if previous != step {
		return fail("TRAINING_CHECKPOINT_INVALID")
	}
	callbacks, e := wire.ExactObject(values["stateful_callbacks"], []string{"TrainerControl"})
	if e != nil {
		return fail("TRAINING_CHECKPOINT_INVALID")
	}
	control, e := wire.ExactObject(callbacks["TrainerControl"], []string{"args", "attributes"})
	if e != nil {
		return fail("TRAINING_CHECKPOINT_INVALID")
	}
	if _, e = wire.ExactObject(control["attributes"], []string{}); e != nil {
		return fail("TRAINING_CHECKPOINT_INVALID")
	}
	args, e := wire.ExactObject(control["args"], []string{"should_epoch_stop", "should_evaluate", "should_log", "should_save", "should_training_stop"})
	if e != nil {
		return fail("TRAINING_CHECKPOINT_INVALID")
	}
	for _, raw := range args {
		var flag bool
		if json.Unmarshal(raw, &flag) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fail("TRAINING_CHECKPOINT_INVALID")
		}
	}
	return nil
}

// VerifyPublishedOutput independently checks prepared bytes before the caller's fresh reference/audit transaction.
// Optimizer, scheduler, RNG and training_args remain opaque hash-verified files; Go never executes their serialization.
func (s *Store) VerifyPublishedOutput(ctx context.Context, d UploadDescriptor, shape BaseShape, c wire.Configuration, repository string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkBase(); e != nil {
		return e
	}
	if e := s.verifyPublished(ctx, d); e != nil {
		return e
	}
	read := func(name string) ([]byte, error) {
		file, e := s.checkedFile(filepath.Join(publishedDirectory(d), name), wire.MaximumCheckpointMetadataBytes)
		if e != nil {
			return nil, e
		}
		defer file.Close()
		raw, e := io.ReadAll(io.LimitReader(file, wire.MaximumCheckpointMetadataBytes+1))
		if e != nil || len(raw) > wire.MaximumCheckpointMetadataBytes {
			return nil, fail("TRAINING_STORAGE_DATA_INVALID")
		}
		return raw, nil
	}
	metadata, e := read("adapter_config.json")
	if e != nil {
		return e
	}
	if e = wire.ValidateAdapterConfiguration(metadata, c, repository); e != nil {
		return e
	}
	tensors, e := s.checkedFile(filepath.Join(publishedDirectory(d), "adapter_model.safetensors"), s.maximumBytes)
	if e != nil {
		return e
	}
	e = VerifyAdapterTensors(ctx, tensors, shape, c, s.maximumBytes)
	tensors.Close()
	if e != nil {
		return e
	}
	for _, asset := range d.Files {
		if asset.Path == "README.md" {
			raw, e := read(asset.Path)
			if e != nil {
				return e
			}
			if !bytes.Equal(raw, wire.AdapterReadme(repository)) {
				return fail("TRAINING_ADAPTER_INVALID")
			}
		}
	}
	if d.Kind == "checkpoint" {
		raw, e := read("trainer_state.json")
		if e != nil {
			return e
		}
		if e = verifyTrainerState(raw, d.GlobalStep, c); e != nil {
			return e
		}
		raw, e = read("projectone-checkpoint.json")
		if e != nil {
			return e
		}
		if rawDigest(raw) != d.ManifestSHA256 {
			return fail("TRAINING_CHECKPOINT_INVALID")
		}
	}
	return nil
}
