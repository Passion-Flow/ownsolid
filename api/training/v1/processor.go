// Package v1 defines bounded training computation contracts without business database access.
package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"

	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type Scope struct {
	TenantID    string `json:"tenant_id"`
	WorkspaceID string `json:"workspace_id"`
	JobID       string `json:"job_id"`
}

func (s Scope) Validate() error {
	if !dataset.ValidID(s.TenantID) || !dataset.ValidID(s.WorkspaceID) || !dataset.ValidID(s.JobID) {
		return errors.New("TRAINING_SCOPE_INVALID")
	}
	return nil
}

type ExecutionIdentity struct {
	Profile            string  `json:"profile"`
	Python             string  `json:"python"`
	WorkerSourceSHA256 string  `json:"worker_source_sha256"`
	ImageID            *string `json:"image_id,omitempty"`
}

func (v ExecutionIdentity) Validate(device string) error {
	valid := v.Python == PythonVersion && dataset.ValidDigest(v.WorkerSourceSHA256)
	if device == VerificationProfile {
		valid = valid && v.Profile == "native_cpu_verification" && v.ImageID == nil
	} else {
		valid = valid && device == ProductionProfile && v.Profile == ProductionProfile && v.ImageID != nil && strings.HasPrefix(*v.ImageID, "sha256:") && dataset.ValidDigest(strings.TrimPrefix(*v.ImageID, "sha256:"))
	}
	if !valid {
		return errors.New("TRAINING_EXECUTION_IDENTITY_REQUIRED")
	}
	return nil
}

type CheckpointReference struct {
	Directory     string `json:"directory"`
	ReceiptSHA256 string `json:"receipt_sha256"`
}

type ProcessorRequest struct {
	SchemaVersion         int                  `json:"schema_version"`
	Operation             string               `json:"operation"`
	TenantID              string               `json:"tenant_id"`
	WorkspaceID           string               `json:"workspace_id"`
	JobID                 string               `json:"job_id"`
	AttemptID             string               `json:"attempt_id"`
	DatasetVersionID      string               `json:"dataset_version_id"`
	DatasetManifestSHA256 string               `json:"dataset_manifest_sha256"`
	ScopeDirectory        string               `json:"scope_directory"`
	ModelDirectory        string               `json:"model_directory"`
	ModelCatalogSHA256    string               `json:"model_catalog_sha256"`
	TrainFile             string               `json:"train_file"`
	TrainSHA256           string               `json:"train_sha256"`
	ValidationFile        string               `json:"validation_file"`
	ValidationSHA256      string               `json:"validation_sha256"`
	OutputDirectory       string               `json:"output_directory"`
	Configuration         Configuration        `json:"configuration"`
	DataLimits            dataset.Limits       `json:"data_limits"`
	Resources             Resources            `json:"resources"`
	ExecutionIdentity     ExecutionIdentity    `json:"execution_identity"`
	ResumeCheckpoint      *CheckpointReference `json:"resume_checkpoint"`
	FixtureCatalogFile    *string              `json:"fixture_catalog_file"`
}

func cleanPath(value string) bool {
	return utf8.ValidString(value) && len(value) <= MaximumPathBytes && filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func (r ProcessorRequest) Validate() error {
	if r.SchemaVersion != 1 || r.Operation != "train" || (Scope{r.TenantID, r.WorkspaceID, r.JobID}).Validate() != nil || !dataset.ValidID(r.AttemptID) || !dataset.ValidID(r.DatasetVersionID) {
		return errors.New("TRAINING_REQUEST_INVALID")
	}
	for _, value := range []string{r.DatasetManifestSHA256, r.ModelCatalogSHA256, r.TrainSHA256, r.ValidationSHA256} {
		if !dataset.ValidDigest(value) {
			return errors.New("TRAINING_REQUEST_INVALID")
		}
	}
	if e := r.Configuration.Validate(); e != nil {
		return e
	}
	if e := r.Resources.Validate(); e != nil {
		return e
	}
	if r.DataLimits.Validate() != nil {
		return errors.New("TRAINING_DATA_LIMITS_INVALID")
	}
	if e := r.ExecutionIdentity.Validate(r.Configuration.Device); e != nil {
		return e
	}
	if r.Resources.CPUSeconds > r.Resources.WallSeconds*r.Configuration.Threads {
		return errors.New("TRAINING_RESOURCES_INVALID")
	}
	for _, path := range []string{r.ScopeDirectory, r.ModelDirectory, r.TrainFile, r.ValidationFile, r.OutputDirectory} {
		if !cleanPath(path) {
			return errors.New("TRAINING_PATH_INVALID")
		}
	}
	if filepath.Base(r.ScopeDirectory) != r.AttemptID || filepath.Base(filepath.Dir(r.ScopeDirectory)) != r.JobID || r.TrainFile != filepath.Join(r.ScopeDirectory, "inputs/train.jsonl") || r.ValidationFile != filepath.Join(r.ScopeDirectory, "inputs/validation.jsonl") || r.OutputDirectory != filepath.Join(r.ScopeDirectory, "output") {
		return errors.New("TRAINING_PATH_INVALID")
	}
	inside := func(path, parent string) bool {
		return path == parent || strings.HasPrefix(path, parent+string(filepath.Separator))
	}
	if inside(r.ScopeDirectory, r.ModelDirectory) || r.FixtureCatalogFile == nil && inside(r.ModelDirectory, r.ScopeDirectory) {
		return errors.New("TRAINING_MODEL_INVALID")
	}
	if r.FixtureCatalogFile != nil && (r.Configuration.Device != VerificationProfile || *r.FixtureCatalogFile != filepath.Join(r.ScopeDirectory, "inputs/synthetic-catalog.json")) {
		return errors.New("TRAINING_EXECUTION_PROFILE_INVALID")
	}
	if r.ResumeCheckpoint != nil && (r.ResumeCheckpoint.Directory != filepath.Join(r.ScopeDirectory, "inputs/checkpoint") || !dataset.ValidDigest(r.ResumeCheckpoint.ReceiptSHA256)) {
		return errors.New("TRAINING_CHECKPOINT_INVALID")
	}
	return nil
}

func DecodeRequest(raw []byte) (ProcessorRequest, error) {
	var request ProcessorRequest
	if len(raw) > MaximumRequestBytes || DecodeStrict(raw, &request) != nil {
		return request, errors.New("TRAINING_REQUEST_INVALID")
	}
	if request.Configuration.Device == VerificationProfile {
		var body map[string]json.RawMessage
		var identity map[string]json.RawMessage
		if json.Unmarshal(raw, &body) != nil || json.Unmarshal(body["execution_identity"], &identity) != nil {
			return request, errors.New("TRAINING_REQUEST_INVALID")
		}
		if _, present := identity["image_id"]; present {
			return request, errors.New("TRAINING_EXECUTION_IDENTITY_REQUIRED")
		}
	}
	return request, request.Validate()
}

// WorkerSourceDigest matches the exact Python compact sorted logical-path/hash map.
func WorkerSourceDigest(files map[string]string) (string, error) {
	if len(files) == 0 {
		return "", errors.New("TRAINING_EXECUTION_IDENTITY_REQUIRED")
	}
	for path, digest := range files {
		if filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(path, "../") || strings.ContainsAny(path, "\x00\r\n\\<>&") || !strings.HasSuffix(path, ".py") || !dataset.ValidDigest(digest) {
			return "", errors.New("TRAINING_EXECUTION_IDENTITY_REQUIRED")
		}
		for _, c := range path {
			if c > 127 {
				return "", errors.New("TRAINING_EXECUTION_IDENTITY_REQUIRED")
			}
		}
	}
	raw, e := json.Marshal(files)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
