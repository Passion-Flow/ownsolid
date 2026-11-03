package config

import "strconv"

func (p DatasetPolicy) environmentKey(key string) bool {
	switch key {
	case "PROJECT_ONE_DATASETS_SNAPSHOT_MAXIMUM_FILES":
		return true
	case "PROJECT_ONE_DATASETS_SNAPSHOT_MAXIMUM_BYTES":
		return true
	case "PROJECT_ONE_DATASETS_SNAPSHOT_MANIFEST_BYTES":
		return true
	case "PROJECT_ONE_DATASETS_SNAPSHOT_SECONDS":
		return true
	case "PROJECT_ONE_DATASETS_UPLOAD_SECONDS":
		return true
	case "PROJECT_ONE_DATASETS_COMMIT_SECONDS":
		return true
	case "PROJECT_ONE_DATASETS_RETRY_DELAY_SECONDS":
		return true
	case "PROJECT_ONE_DATASETS_POLL_MILLISECONDS":
		return true
	case "PROJECT_ONE_DATASETS_LEASE_SECONDS":
		return true
	case "PROJECT_ONE_DATASETS_MAX_ATTEMPTS":
		return true
	case "PROJECT_ONE_DATASETS_CONCURRENCY":
		return true
	case "PROJECT_ONE_DATASETS_CHILD_STOP_SECONDS":
		return true
	case "PROJECT_ONE_DATASETS_MEMORY_POLL_MILLISECONDS":
		return true
	case "PROJECT_ONE_DATASETS_MINIMUM_FREE_BYTES":
		return true
	case "PROJECT_ONE_DATASETS_MAX_DATASETS_PER_WORKSPACE":
		return true
	case "PROJECT_ONE_DATASETS_MAX_ACTIVE_DRAFTS":
		return true
	case "PROJECT_ONE_DATASETS_MAX_VERSIONS_PER_DATASET":
		return true
	case "PROJECT_ONE_DATASETS_VALIDATION_BASIS_POINTS":
		return true
	case "PROJECT_ONE_DATASETS_LIMITS_MAXIMUM_BYTES":
		return true
	case "PROJECT_ONE_DATASETS_LIMITS_MAXIMUM_ROWS":
		return true
	case "PROJECT_ONE_DATASETS_LIMITS_MAXIMUM_LINE_BYTES":
		return true
	case "PROJECT_ONE_DATASETS_LIMITS_MAXIMUM_TEXT_BYTES":
		return true
	case "PROJECT_ONE_DATASETS_LIMITS_MAXIMUM_SOURCE_BYTES":
		return true
	case "PROJECT_ONE_DATASETS_LIMITS_MAXIMUM_ERRORS":
		return true
	case "PROJECT_ONE_DATASETS_RESOURCES_WALL_SECONDS":
		return true
	case "PROJECT_ONE_DATASETS_RESOURCES_CPU_SECONDS":
		return true
	case "PROJECT_ONE_DATASETS_RESOURCES_MEMORY_BYTES":
		return true
	case "PROJECT_ONE_DATASETS_RESOURCES_MAXIMUM_RESULT_BYTES":
		return true
	}
	return false
}
func (p *DatasetPolicy) applyEnvironment(key, value string) error {
	n, e := strconv.ParseInt(value, 10, 64)
	if e != nil {
		return e
	}
	switch key {
	case "PROJECT_ONE_DATASETS_SNAPSHOT_MAXIMUM_FILES":
		p.SnapshotMaximumFiles = int(n)
	case "PROJECT_ONE_DATASETS_SNAPSHOT_MAXIMUM_BYTES":
		p.SnapshotMaximumBytes = n
	case "PROJECT_ONE_DATASETS_SNAPSHOT_MANIFEST_BYTES":
		p.SnapshotManifestBytes = n
	case "PROJECT_ONE_DATASETS_SNAPSHOT_SECONDS":
		p.SnapshotSeconds = int(n)
	case "PROJECT_ONE_DATASETS_UPLOAD_SECONDS":
		p.UploadSeconds = int(n)
	case "PROJECT_ONE_DATASETS_COMMIT_SECONDS":
		p.CommitSeconds = int(n)
	case "PROJECT_ONE_DATASETS_RETRY_DELAY_SECONDS":
		p.RetryDelaySeconds = int(n)
	case "PROJECT_ONE_DATASETS_POLL_MILLISECONDS":
		p.PollMilliseconds = int(n)
	case "PROJECT_ONE_DATASETS_LEASE_SECONDS":
		p.LeaseSeconds = int(n)
	case "PROJECT_ONE_DATASETS_MAX_ATTEMPTS":
		p.MaxAttempts = int(n)
	case "PROJECT_ONE_DATASETS_CONCURRENCY":
		p.Concurrency = int(n)
	case "PROJECT_ONE_DATASETS_CHILD_STOP_SECONDS":
		p.ChildStopSeconds = int(n)
	case "PROJECT_ONE_DATASETS_MEMORY_POLL_MILLISECONDS":
		p.MemoryPollMilliseconds = int(n)
	case "PROJECT_ONE_DATASETS_MINIMUM_FREE_BYTES":
		p.MinimumFreeBytes = n
	case "PROJECT_ONE_DATASETS_MAX_DATASETS_PER_WORKSPACE":
		p.MaxDatasetsPerWorkspace = int(n)
	case "PROJECT_ONE_DATASETS_MAX_ACTIVE_DRAFTS":
		p.MaxActiveDrafts = int(n)
	case "PROJECT_ONE_DATASETS_MAX_VERSIONS_PER_DATASET":
		p.MaxVersionsPerDataset = int(n)
	case "PROJECT_ONE_DATASETS_VALIDATION_BASIS_POINTS":
		p.ValidationBasisPoints = int(n)
	case "PROJECT_ONE_DATASETS_LIMITS_MAXIMUM_BYTES":
		p.Limits.MaximumBytes = n
	case "PROJECT_ONE_DATASETS_LIMITS_MAXIMUM_ROWS":
		p.Limits.MaximumRows = n
	case "PROJECT_ONE_DATASETS_LIMITS_MAXIMUM_LINE_BYTES":
		p.Limits.MaximumLineBytes = n
	case "PROJECT_ONE_DATASETS_LIMITS_MAXIMUM_TEXT_BYTES":
		p.Limits.MaximumTextBytes = n
	case "PROJECT_ONE_DATASETS_LIMITS_MAXIMUM_SOURCE_BYTES":
		p.Limits.MaximumSourceBytes = n
	case "PROJECT_ONE_DATASETS_LIMITS_MAXIMUM_ERRORS":
		p.Limits.MaximumErrors = n
	case "PROJECT_ONE_DATASETS_RESOURCES_WALL_SECONDS":
		p.Resources.WallSeconds = n
	case "PROJECT_ONE_DATASETS_RESOURCES_CPU_SECONDS":
		p.Resources.CpuSeconds = n
	case "PROJECT_ONE_DATASETS_RESOURCES_MEMORY_BYTES":
		p.Resources.MemoryBytes = n
	case "PROJECT_ONE_DATASETS_RESOURCES_MAXIMUM_RESULT_BYTES":
		p.Resources.MaximumResultBytes = n
	}
	return nil
}
func datasetPathEnvironment(key string) bool {
	return key == "PROJECT_ONE_DATASETS_ARTIFACT_ROOT" || key == "PROJECT_ONE_DATASETS_PYTHON_EXECUTABLE" || key == "PROJECT_ONE_DATASETS_PROCESSOR_FILE"
}
