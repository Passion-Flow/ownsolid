package config

import (
	"errors"
	"path/filepath"
	"strings"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// TrainingConfiguration selects manufactured immutable artifacts, never user-supplied code.
// An empty configuration disables training dispatch. Changes require a control restart.
type TrainingConfiguration struct {
	ArtifactRoot       string `yaml:"artifact_root"`
	ImageReference     string `yaml:"image_reference"`
	ImageID            string `yaml:"image_id"`
	WorkerSourceSHA256 string `yaml:"worker_source_sha256"`
}

func (c TrainingConfiguration) Enabled() bool { return c.ArtifactRoot != "" }
func (c TrainingConfiguration) Validate(otherRoots ...string) error {
	if c == (TrainingConfiguration{}) {
		return nil
	}
	if !filepath.IsAbs(c.ArtifactRoot) || filepath.Clean(c.ArtifactRoot) != c.ArtifactRoot || c.ArtifactRoot == string(filepath.Separator) || strings.ContainsAny(c.ArtifactRoot, "\x00\r\n") {
		return errors.New("CONFIG_TRAINING_PATH_INVALID")
	}
	for _, root := range otherRoots {
		if root != "" && (root == c.ArtifactRoot || strings.HasPrefix(root, c.ArtifactRoot+string(filepath.Separator)) || strings.HasPrefix(c.ArtifactRoot, root+string(filepath.Separator))) {
			return errors.New("CONFIG_TRAINING_PATH_CONFLICT")
		}
	}
	// Only a local manufactured image selected by immutable content is executable.
	if !strings.HasPrefix(c.ImageReference, "projectone/training@sha256:") || !datawire.ValidDigest(strings.TrimPrefix(c.ImageReference, "projectone/training@sha256:")) || !strings.HasPrefix(c.ImageID, "sha256:") || !datawire.ValidDigest(strings.TrimPrefix(c.ImageID, "sha256:")) || !datawire.ValidDigest(c.WorkerSourceSHA256) {
		return errors.New("CONFIG_TRAINING_ARTIFACT_IDENTITY_REQUIRED")
	}
	return nil
}

type TrainingPolicy struct {
	SnapshotMaximumFiles  int            `yaml:"snapshot_maximum_files"`
	SnapshotMaximumBytes  int64          `yaml:"snapshot_maximum_bytes"`
	SnapshotManifestBytes int64          `yaml:"snapshot_manifest_bytes"`
	SnapshotSeconds       int            `yaml:"snapshot_seconds"`
	Resources             wire.Resources `yaml:"resources"`
	MaxJobsPerWorkspace   int            `yaml:"max_jobs_per_workspace"`
	MaxActivePerWorkspace int            `yaml:"max_active_per_workspace"`
	MaxAttempts           int            `yaml:"max_attempts"`
	PollMilliseconds      int            `yaml:"poll_milliseconds"`
	TransactionSeconds    int            `yaml:"transaction_seconds"`
	UploadSeconds         int            `yaml:"upload_seconds"`
	MinimumFreeBytes      int64          `yaml:"minimum_free_bytes"`
	MinimumHostMemory     int64          `yaml:"minimum_host_memory_bytes"`
	MinimumGPUMemoryMiB   int64          `yaml:"minimum_gpu_memory_mib"`
	MinimumDriverMajor    int            `yaml:"minimum_driver_major"`
	MinimumComputeMajor   int            `yaml:"minimum_compute_major"`
}

func DefaultTrainingPolicy() TrainingPolicy {
	return TrainingPolicy{SnapshotMaximumFiles: 20000, SnapshotMaximumBytes: 17179869184, SnapshotManifestBytes: 16777216, SnapshotSeconds: 300, Resources: wire.DefaultResources(), MaxJobsPerWorkspace: 1000, MaxActivePerWorkspace: 10, MaxAttempts: 5, PollMilliseconds: 1000, TransactionSeconds: 10, UploadSeconds: 30, MinimumFreeBytes: 2147483648, MinimumHostMemory: 12884901888, MinimumGPUMemoryMiB: 8192, MinimumDriverMajor: 580, MinimumComputeMajor: 8}
}
func (p TrainingPolicy) Validate() error {
	if p.SnapshotMaximumFiles < 1 || p.SnapshotMaximumFiles > 1000000 || p.SnapshotMaximumBytes < 1048576 || p.SnapshotMaximumBytes > 1099511627776 || p.SnapshotManifestBytes < 65536 || p.SnapshotManifestBytes > 67108864 || p.SnapshotSeconds < 1 || p.SnapshotSeconds > 3600 {
		return errors.New("CONFIG_TRAINING_POLICY_INVALID")
	}
	if p.Resources.Validate() != nil || p.UploadSeconds < 1 || p.UploadSeconds > 300 || p.MaxJobsPerWorkspace < 1 || p.MaxJobsPerWorkspace > 100000 || p.MaxActivePerWorkspace < 1 || p.MaxActivePerWorkspace > 1000 || p.MaxActivePerWorkspace > p.MaxJobsPerWorkspace || p.MaxAttempts < 1 || p.MaxAttempts > 1000 || p.PollMilliseconds < 100 || p.PollMilliseconds > 60000 || p.TransactionSeconds < 1 || p.TransactionSeconds > 60 || p.MinimumFreeBytes < p.Resources.MaximumOutputBytes || p.MinimumFreeBytes > 1099511627776 || p.MinimumHostMemory < p.Resources.MemoryBytes || p.MinimumHostMemory > 1099511627776 || p.MinimumGPUMemoryMiB < 8192 || p.MinimumGPUMemoryMiB > 1048576 || p.MinimumDriverMajor < 580 || p.MinimumDriverMajor > 999 || p.MinimumComputeMajor < 8 || p.MinimumComputeMajor > 99 {
		return errors.New("CONFIG_TRAINING_POLICY_INVALID")
	}
	return nil
}
