package runtime

import (
	"errors"
	"strconv"
	"strings"
)

// Explicit bindings share the existing typed configuration/policy owners. No ambient
// process environment, shell expansion, privilege switch or new default authority.
type agentEnvironmentBinding struct {
	text   *string
	number *int
	wide   *int64
}

func applyAgentEnvironment(c *Config, values map[string]string) error {
	bad := errors.New("AGENT_ENVIRONMENT_INVALID")
	policy, trainingPolicy, preparationPolicy := c.Policy(), c.Training.Policy(), c.Preparation.Policy()
	bindings := map[string]agentEnvironmentBinding{
		"PROJECT_ONE_AGENT_CONTROL_ORIGIN":                                      {text: &c.ControlOrigin},
		"PROJECT_ONE_AGENT_CA_FILE":                                             {text: &c.CAFile},
		"PROJECT_ONE_AGENT_STATE_DIRECTORY":                                     {text: &c.StateDirectory},
		"PROJECT_ONE_AGENT_DISK_PATH":                                           {text: &c.DiskPath},
		"PROJECT_ONE_AGENT_JOIN_TOKEN_FILE":                                     {text: &c.JoinTokenFile},
		"PROJECT_ONE_AGENT_EXECUTION_PROFILE":                                   {text: &c.ExecutionProfile},
		"PROJECT_ONE_AGENT_COMPONENTS_ROOT_DIRECTORY":                           {text: &c.Components.RootDirectory},
		"PROJECT_ONE_AGENT_COMPONENTS_SERVICE_USER":                             {text: &c.Components.ServiceUser},
		"PROJECT_ONE_AGENT_INFERENCE_ROOT_DIRECTORY":                            {text: &c.Inference.RootDirectory},
		"PROJECT_ONE_AGENT_INFERENCE_DOCKER_SOCKET":                             {text: &c.Inference.DockerSocket},
		"PROJECT_ONE_AGENT_TRAINING_ROOT_DIRECTORY":                             {text: &c.Training.RootDirectory},
		"PROJECT_ONE_AGENT_TRAINING_DOCKER_SOCKET":                              {text: &c.Training.DockerSocket},
		"PROJECT_ONE_AGENT_TRAINING_IMAGE_REFERENCE":                            {text: &c.Training.ImageReference},
		"PROJECT_ONE_AGENT_TRAINING_IMAGE_ID":                                   {text: &c.Training.ImageID},
		"PROJECT_ONE_AGENT_TRAINING_WORKER_SOURCE_SHA256":                       {text: &c.Training.WorkerSourceSHA256},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_ROOT_DIRECTORY":                    {text: &c.Preparation.RootDirectory},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_DOCKER_SOCKET":                     {text: &c.Preparation.DockerSocket},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_IMAGE_REFERENCE":                   {text: &c.Preparation.ImageReference},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_IMAGE_ID":                          {text: &c.Preparation.ImageID},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_WORKER_SOURCE_SHA256":              {text: &c.Preparation.WorkerSourceSHA256},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS":                    {number: &c.Preparation.OutputSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_ENROLL_SECONDS":                              {number: &policy.EnrollSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_REMOVE_SECONDS":                              {number: &policy.RemoveSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_CONTROL_REQUEST_SECONDS":                     {number: &policy.ControlRequestSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_CONTROL_HEADER_SECONDS":                      {number: &policy.ControlHeaderSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_TLS_HANDSHAKE_SECONDS":                       {number: &policy.TLSHandshakeSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_IDLE_CONNECTION_SECONDS":                     {number: &policy.IdleConnectionSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_CONTROL_CONNECTIONS":                         {number: &policy.ControlConnections},
		"PROJECT_ONE_AGENT_RUNTIME_BACKOFF_INITIAL_SECONDS":                     {number: &policy.BackoffInitialSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_BACKOFF_MAX_SECONDS":                         {number: &policy.BackoffMaxSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_COMPONENT_TASK_SECONDS":                      {number: &policy.ComponentTaskSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_INFERENCE_TASK_SECONDS":                      {number: &policy.InferenceTaskSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_SYSTEMD_SECONDS":                             {number: &policy.SystemdSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_MAINTENANCE_SECONDS":                         {number: &policy.MaintenanceSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_METRICS_SECONDS":                             {number: &policy.MetricsSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_METRICS_HEADER_SECONDS":                      {number: &policy.MetricsHeaderSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_DOCKER_STOP_SECONDS":                         {number: &policy.DockerStopSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_DOCKER_SECONDS":                              {number: &policy.DockerSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_DOCKER_HEADER_SECONDS":                       {number: &policy.DockerHeaderSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_DOCKER_DIAL_SECONDS":                         {number: &policy.DockerDialSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_DOCKER_CONNECTIONS":                          {number: &policy.DockerConnections},
		"PROJECT_ONE_AGENT_RUNTIME_PROBE_SECONDS":                               {number: &policy.ProbeSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_PROBE_HEADER_SECONDS":                        {number: &policy.ProbeHeaderSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_PROBE_DIAL_SECONDS":                          {number: &policy.ProbeDialSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_GPU_PROBE_SECONDS":                           {number: &policy.GPUProbeSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_MODEL_LOAD_SECONDS":                          {number: &policy.ModelLoadSeconds},
		"PROJECT_ONE_AGENT_RUNTIME_MODEL_POLL_MILLISECONDS":                     {number: &policy.ModelPollMilliseconds},
		"PROJECT_ONE_AGENT_TRAINING_RUNTIME_USER_ID":                            {number: &trainingPolicy.UserID},
		"PROJECT_ONE_AGENT_TRAINING_RUNTIME_GROUP_ID":                           {number: &trainingPolicy.GroupID},
		"PROJECT_ONE_AGENT_TRAINING_RUNTIME_PIDS_LIMIT":                         {wide: &trainingPolicy.PidsLimit},
		"PROJECT_ONE_AGENT_TRAINING_RUNTIME_TEMPORARY_MEMORY_BYTES":             {wide: &trainingPolicy.TemporaryMemoryBytes},
		"PROJECT_ONE_AGENT_TRAINING_RUNTIME_SHARED_MEMORY_BYTES":                {wide: &trainingPolicy.SharedMemoryBytes},
		"PROJECT_ONE_AGENT_TRAINING_RUNTIME_LOG_MAXIMUM_BYTES":                  {wide: &trainingPolicy.LogMaximumBytes},
		"PROJECT_ONE_AGENT_TRAINING_RUNTIME_LOG_MAXIMUM_FILES":                  {number: &trainingPolicy.LogMaximumFiles},
		"PROJECT_ONE_AGENT_TRAINING_RUNTIME_POLL_MILLISECONDS":                  {number: &trainingPolicy.PollMilliseconds},
		"PROJECT_ONE_AGENT_TRAINING_RUNTIME_STOP_SECONDS":                       {number: &trainingPolicy.StopSeconds},
		"PROJECT_ONE_AGENT_TRAINING_RUNTIME_MAXIMUM_MEMORY_BYTES":               {wide: &trainingPolicy.MaximumMemoryBytes},
		"PROJECT_ONE_AGENT_TRAINING_RUNTIME_MAXIMUM_CPU_THREADS":                {wide: &trainingPolicy.MaximumCPUThreads},
		"PROJECT_ONE_AGENT_TRAINING_RUNTIME_MINIMUM_FREE_BYTES":                 {wide: &trainingPolicy.MinimumFreeBytes},
		"PROJECT_ONE_AGENT_TRAINING_RUNTIME_HOST_MEMORY_RESERVE_BYTES":          {wide: &trainingPolicy.HostMemoryReserveBytes},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_USER_ID":                   {number: &preparationPolicy.UserID},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_GROUP_ID":                  {number: &preparationPolicy.GroupID},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_PIDS_LIMIT":                {wide: &preparationPolicy.PidsLimit},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_TEMPORARY_MEMORY_BYTES":    {wide: &preparationPolicy.TemporaryMemoryBytes},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_SHARED_MEMORY_BYTES":       {wide: &preparationPolicy.SharedMemoryBytes},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_LOG_MAXIMUM_BYTES":         {wide: &preparationPolicy.LogMaximumBytes},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_LOG_MAXIMUM_FILES":         {number: &preparationPolicy.LogMaximumFiles},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_POLL_MILLISECONDS":         {number: &preparationPolicy.PollMilliseconds},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_STOP_SECONDS":              {number: &preparationPolicy.StopSeconds},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_MAXIMUM_MEMORY_BYTES":      {wide: &preparationPolicy.MaximumMemoryBytes},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_MAXIMUM_CPU_THREADS":       {wide: &preparationPolicy.MaximumCPUThreads},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_MINIMUM_FREE_BYTES":        {wide: &preparationPolicy.MinimumFreeBytes},
		"PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_HOST_MEMORY_RESERVE_BYTES": {wide: &preparationPolicy.HostMemoryReserveBytes},
	}
	trainingChanged, preparationChanged := false, false
	for key, value := range values {
		if key == registryConfigurationKey {
			continue
		}
		binding, ok := bindings[key]
		if !ok {
			return bad
		}
		switch {
		case binding.text != nil:
			*binding.text = value
		default:
			if value == "" || len(value) > 1 && value[0] == '0' {
				return bad
			}
			for _, r := range value {
				if r < '0' || r > '9' {
					return bad
				}
			}
			n, e := strconv.ParseInt(value, 10, 64)
			if e != nil || n <= 0 {
				return bad
			}
			if binding.number != nil {
				if int64(int(n)) != n {
					return bad
				}
				*binding.number = int(n)
			} else {
				*binding.wide = n
			}
		}
		trainingChanged = trainingChanged || strings.HasPrefix(key, "PROJECT_ONE_AGENT_TRAINING_RUNTIME_")
		preparationChanged = preparationChanged || strings.HasPrefix(key, "PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_")
	}
	c.Runtime = &policy
	if trainingChanged {
		c.Training.Runtime = &trainingPolicy
	}
	if preparationChanged {
		c.Preparation.Runtime = &preparationPolicy
	}
	return nil
}
