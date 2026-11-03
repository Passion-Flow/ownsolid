package config

import "strconv"

func (p TrainingPolicy) environmentKey(key string) bool {
	switch key {
	case "PROJECT_ONE_TRAINING_SNAPSHOT_MAXIMUM_FILES":
		return true
	case "PROJECT_ONE_TRAINING_SNAPSHOT_MAXIMUM_BYTES":
		return true
	case "PROJECT_ONE_TRAINING_SNAPSHOT_MANIFEST_BYTES":
		return true
	case "PROJECT_ONE_TRAINING_SNAPSHOT_SECONDS":
		return true
	case "PROJECT_ONE_TRAINING_MAX_JOBS_PER_WORKSPACE":
		return true
	case "PROJECT_ONE_TRAINING_MAX_ACTIVE_PER_WORKSPACE":
		return true
	case "PROJECT_ONE_TRAINING_MAX_ATTEMPTS":
		return true
	case "PROJECT_ONE_TRAINING_POLL_MILLISECONDS":
		return true
	case "PROJECT_ONE_TRAINING_TRANSACTION_SECONDS":
		return true
	case "PROJECT_ONE_TRAINING_UPLOAD_SECONDS":
		return true
	case "PROJECT_ONE_TRAINING_MINIMUM_FREE_BYTES":
		return true
	case "PROJECT_ONE_TRAINING_MINIMUM_HOST_MEMORY_BYTES":
		return true
	case "PROJECT_ONE_TRAINING_MINIMUM_GPU_MEMORY_MIB":
		return true
	case "PROJECT_ONE_TRAINING_MINIMUM_DRIVER_MAJOR":
		return true
	case "PROJECT_ONE_TRAINING_MINIMUM_COMPUTE_MAJOR":
		return true
	case "PROJECT_ONE_TRAINING_RESOURCES_WALL_SECONDS":
		return true
	case "PROJECT_ONE_TRAINING_RESOURCES_CPU_SECONDS":
		return true
	case "PROJECT_ONE_TRAINING_RESOURCES_MEMORY_BYTES":
		return true
	case "PROJECT_ONE_TRAINING_RESOURCES_MAXIMUM_OUTPUT_BYTES":
		return true
	case "PROJECT_ONE_TRAINING_RESOURCES_MAXIMUM_OUTPUT_FILES":
		return true
	case "PROJECT_ONE_TRAINING_RESOURCES_MAXIMUM_RESULT_BYTES":
		return true
	case "PROJECT_ONE_TRAINING_RESOURCES_MAXIMUM_EVENT_BYTES":
		return true
	case "PROJECT_ONE_TRAINING_RESOURCES_OPEN_FILES":
		return true
	}
	return false
}
func (p *TrainingPolicy) applyEnvironment(key, value string) error {
	n, e := strconv.ParseInt(value, 10, 64)
	if e != nil {
		return e
	}
	switch key {
	case "PROJECT_ONE_TRAINING_SNAPSHOT_MAXIMUM_FILES":
		p.SnapshotMaximumFiles = int(n)
	case "PROJECT_ONE_TRAINING_SNAPSHOT_MAXIMUM_BYTES":
		p.SnapshotMaximumBytes = n
	case "PROJECT_ONE_TRAINING_SNAPSHOT_MANIFEST_BYTES":
		p.SnapshotManifestBytes = n
	case "PROJECT_ONE_TRAINING_SNAPSHOT_SECONDS":
		p.SnapshotSeconds = int(n)
	case "PROJECT_ONE_TRAINING_MAX_JOBS_PER_WORKSPACE":
		p.MaxJobsPerWorkspace = int(n)
	case "PROJECT_ONE_TRAINING_MAX_ACTIVE_PER_WORKSPACE":
		p.MaxActivePerWorkspace = int(n)
	case "PROJECT_ONE_TRAINING_MAX_ATTEMPTS":
		p.MaxAttempts = int(n)
	case "PROJECT_ONE_TRAINING_POLL_MILLISECONDS":
		p.PollMilliseconds = int(n)
	case "PROJECT_ONE_TRAINING_TRANSACTION_SECONDS":
		p.TransactionSeconds = int(n)
	case "PROJECT_ONE_TRAINING_UPLOAD_SECONDS":
		p.UploadSeconds = int(n)
	case "PROJECT_ONE_TRAINING_MINIMUM_FREE_BYTES":
		p.MinimumFreeBytes = n
	case "PROJECT_ONE_TRAINING_MINIMUM_HOST_MEMORY_BYTES":
		p.MinimumHostMemory = n
	case "PROJECT_ONE_TRAINING_MINIMUM_GPU_MEMORY_MIB":
		p.MinimumGPUMemoryMiB = n
	case "PROJECT_ONE_TRAINING_MINIMUM_DRIVER_MAJOR":
		p.MinimumDriverMajor = int(n)
	case "PROJECT_ONE_TRAINING_MINIMUM_COMPUTE_MAJOR":
		p.MinimumComputeMajor = int(n)
	case "PROJECT_ONE_TRAINING_RESOURCES_WALL_SECONDS":
		p.Resources.WallSeconds = n
	case "PROJECT_ONE_TRAINING_RESOURCES_CPU_SECONDS":
		p.Resources.CPUSeconds = n
	case "PROJECT_ONE_TRAINING_RESOURCES_MEMORY_BYTES":
		p.Resources.MemoryBytes = n
	case "PROJECT_ONE_TRAINING_RESOURCES_MAXIMUM_OUTPUT_BYTES":
		p.Resources.MaximumOutputBytes = n
	case "PROJECT_ONE_TRAINING_RESOURCES_MAXIMUM_OUTPUT_FILES":
		p.Resources.MaximumOutputFiles = n
	case "PROJECT_ONE_TRAINING_RESOURCES_MAXIMUM_RESULT_BYTES":
		p.Resources.MaximumResultBytes = n
	case "PROJECT_ONE_TRAINING_RESOURCES_MAXIMUM_EVENT_BYTES":
		p.Resources.MaximumEventBytes = n
	case "PROJECT_ONE_TRAINING_RESOURCES_OPEN_FILES":
		p.Resources.OpenFiles = n
	}
	return nil
}
func trainingArtifactEnvironment(key string) bool {
	switch key {
	case "PROJECT_ONE_TRAINING_ARTIFACT_ROOT":
		return true
	case "PROJECT_ONE_TRAINING_IMAGE_REFERENCE":
		return true
	case "PROJECT_ONE_TRAINING_IMAGE_ID":
		return true
	case "PROJECT_ONE_TRAINING_WORKER_SOURCE_SHA256":
		return true
	}
	return false
}
func (c *TrainingConfiguration) applyEnvironment(key, value string) {
	switch key {
	case "PROJECT_ONE_TRAINING_ARTIFACT_ROOT":
		c.ArtifactRoot = value
	case "PROJECT_ONE_TRAINING_IMAGE_REFERENCE":
		c.ImageReference = value
	case "PROJECT_ONE_TRAINING_IMAGE_ID":
		c.ImageID = value
	case "PROJECT_ONE_TRAINING_WORKER_SOURCE_SHA256":
		c.WorkerSourceSHA256 = value
	}
}
