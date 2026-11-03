package config

import (
	"errors"
	"path/filepath"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"strings"
)

// DatasetConfiguration supplies trusted installed artifacts; uploaded data never selects code or storage paths.
type DatasetConfiguration struct {
	ArtifactRoot     string `yaml:"artifact_root"`
	PythonExecutable string `yaml:"python_executable"`
	ProcessorFile    string `yaml:"processor_file"`
}

func (c DatasetConfiguration) Validate(modelRoot string) error {
	values := []string{c.ArtifactRoot, c.PythonExecutable, c.ProcessorFile}
	present := 0
	for _, value := range values {
		if value != "" {
			present++
			if !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsAny(value, "\x00\r\n") {
				return errors.New("CONFIG_DATASETS_PATH_INVALID")
			}
		}
	}
	if present == 0 {
		return nil
	}
	if present != len(values) {
		return errors.New("CONFIG_DATASETS_PATHS_REQUIRED")
	}
	inside := func(parent, child string) bool {
		return parent == child || strings.HasPrefix(child, parent+string(filepath.Separator))
	}
	if c.ArtifactRoot == string(filepath.Separator) || inside(c.ArtifactRoot, c.PythonExecutable) || inside(c.ArtifactRoot, c.ProcessorFile) || c.PythonExecutable == c.ProcessorFile {
		return errors.New("CONFIG_DATASETS_PATH_CONFLICT")
	}
	if modelRoot != "" && (inside(c.ArtifactRoot, modelRoot) || inside(modelRoot, c.ArtifactRoot)) {
		return errors.New("CONFIG_DATASETS_PATH_CONFLICT")
	}
	return nil
}

type DatasetPolicy struct {
	SnapshotMaximumFiles    int                `yaml:"snapshot_maximum_files"`
	SnapshotMaximumBytes    int64              `yaml:"snapshot_maximum_bytes"`
	SnapshotManifestBytes   int64              `yaml:"snapshot_manifest_bytes"`
	SnapshotSeconds         int                `yaml:"snapshot_seconds"`
	UploadSeconds           int                `yaml:"upload_seconds"`
	CommitSeconds           int                `yaml:"commit_seconds"`
	RetryDelaySeconds       int                `yaml:"retry_delay_seconds"`
	Limits                  datawire.Limits    `yaml:"limits"`
	Resources               datawire.Resources `yaml:"resources"`
	PollMilliseconds        int                `yaml:"poll_milliseconds"`
	LeaseSeconds            int                `yaml:"lease_seconds"`
	MaxAttempts             int                `yaml:"max_attempts"`
	Concurrency             int                `yaml:"concurrency"`
	ChildStopSeconds        int                `yaml:"child_stop_seconds"`
	MemoryPollMilliseconds  int                `yaml:"memory_poll_milliseconds"`
	MinimumFreeBytes        int64              `yaml:"minimum_free_bytes"`
	MaxDatasetsPerWorkspace int                `yaml:"max_datasets_per_workspace"`
	MaxActiveDrafts         int                `yaml:"max_active_drafts"`
	MaxVersionsPerDataset   int                `yaml:"max_versions_per_dataset"`
	ValidationBasisPoints   int                `yaml:"validation_basis_points"`
}

// DefaultDatasetPolicy is the single owner of deployment defaults; shared wire ceilings are generated independently.
func DefaultDatasetPolicy() DatasetPolicy {
	return DatasetPolicy{SnapshotMaximumFiles: 10000, SnapshotMaximumBytes: 1073741824, SnapshotManifestBytes: 8388608, SnapshotSeconds: 120, UploadSeconds: 30, CommitSeconds: 10, RetryDelaySeconds: 2, Limits: datawire.Limits{MaximumBytes: 16777216, MaximumRows: 2000, MaximumLineBytes: 65536, MaximumTextBytes: 16384, MaximumSourceBytes: 512, MaximumErrors: 200}, Resources: datawire.Resources{WallSeconds: 15, CpuSeconds: 10, MemoryBytes: 536870912, MaximumResultBytes: 33554432}, PollMilliseconds: 500, LeaseSeconds: 60, MaxAttempts: 3, Concurrency: 1, ChildStopSeconds: 2, MemoryPollMilliseconds: 100, MinimumFreeBytes: 67108864, MaxDatasetsPerWorkspace: 100, MaxActiveDrafts: 100, MaxVersionsPerDataset: 100, ValidationBasisPoints: 2000}
}
func (p DatasetPolicy) Validate() error {
	if p.SnapshotMaximumFiles < 1 || p.SnapshotMaximumFiles > 100000 || p.SnapshotMaximumBytes < 1048576 || p.SnapshotMaximumBytes > 1099511627776 || p.SnapshotManifestBytes < 4096 || p.SnapshotManifestBytes > 67108864 || p.SnapshotSeconds < 1 || p.SnapshotSeconds > 3600 {
		return errors.New("CONFIG_DATASETS_SNAPSHOT_BOUNDS_INVALID")
	}
	if p.Limits.Validate() != nil || p.Resources.Validate() != nil {
		return errors.New("CONFIG_DATASETS_PROCESSOR_INVALID")
	}
	if p.Resources.MaximumResultBytes < p.Limits.MaximumBytes+p.Limits.MaximumRows*256 {
		return errors.New("CONFIG_DATASETS_RESULT_BUDGET_INVALID")
	}
	if p.UploadSeconds < 1 || p.UploadSeconds > 300 {
		return errors.New("CONFIG_DATASETS_UPLOAD_BOUNDS_INVALID")
	}
	if p.CommitSeconds < 1 || p.CommitSeconds > 60 || p.RetryDelaySeconds < 1 || p.RetryDelaySeconds > 60 {
		return errors.New("CONFIG_DATASETS_WORKER_BOUNDS_INVALID")
	}
	if p.PollMilliseconds < 100 || p.PollMilliseconds > 5000 || p.LeaseSeconds < 30 || p.LeaseSeconds > 600 || int64(p.LeaseSeconds) < p.Resources.WallSeconds+int64(p.ChildStopSeconds)+int64(p.CommitSeconds) || p.MaxAttempts < 1 || p.MaxAttempts > 5 || p.Concurrency < 1 || p.Concurrency > 2 || p.ChildStopSeconds < 1 || p.ChildStopSeconds > 10 || p.MemoryPollMilliseconds < 50 || p.MemoryPollMilliseconds > 1000 || p.MinimumFreeBytes < 4096 || p.MinimumFreeBytes > 9007199254740991 {
		return errors.New("CONFIG_DATASETS_WORKER_BOUNDS_INVALID")
	}
	if p.MaxDatasetsPerWorkspace < 1 || p.MaxDatasetsPerWorkspace > 1000 || p.MaxActiveDrafts < 1 || p.MaxActiveDrafts > 1000 || p.MaxVersionsPerDataset < 1 || p.MaxVersionsPerDataset > 1000 || p.ValidationBasisPoints < 100 || p.ValidationBasisPoints > 5000 {
		return errors.New("CONFIG_DATASETS_POLICY_BOUNDS_INVALID")
	}
	return nil
}
