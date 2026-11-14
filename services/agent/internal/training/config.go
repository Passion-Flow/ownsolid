// Package training owns the fixed offline training container lifecycle, not computation.
package training

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

const ImageWorkingDirectory = "/opt/projectone/runtimes/training"
const WorkerFile = ImageWorkingDirectory + "/train.py"
const MaximumDockerRequestBytes = 65536
const MaximumDockerResponseBytes = 262144
const EngineAPI = "/v1.45"

func fail(code string) error { return errors.New(code) }

// Policy is loaded once at the agent configuration boundary; all changes require restart.
type Policy struct {
	UserID                 int   `json:"user_id"`
	GroupID                int   `json:"group_id"`
	PidsLimit              int64 `json:"pids_limit"`
	TemporaryMemoryBytes   int64 `json:"temporary_memory_bytes"`
	SharedMemoryBytes      int64 `json:"shared_memory_bytes"`
	LogMaximumBytes        int64 `json:"log_maximum_bytes"`
	LogMaximumFiles        int   `json:"log_maximum_files"`
	PollMilliseconds       int   `json:"poll_milliseconds"`
	StopSeconds            int   `json:"stop_seconds"`
	MaximumMemoryBytes     int64 `json:"maximum_memory_bytes"`
	MaximumCPUThreads      int64 `json:"maximum_cpu_threads"`
	MinimumFreeBytes       int64 `json:"minimum_free_bytes"`
	HostMemoryReserveBytes int64 `json:"host_memory_reserve_bytes"`
}

func DefaultPolicy() Policy {
	return Policy{UserID: 10001, GroupID: 10001, PidsLimit: 128, TemporaryMemoryBytes: 268435456, SharedMemoryBytes: 67108864, LogMaximumBytes: 1048576, LogMaximumFiles: 2, PollMilliseconds: 500, StopSeconds: 15, MaximumMemoryBytes: 8589934592, MaximumCPUThreads: 8, MinimumFreeBytes: 2147483648, HostMemoryReserveBytes: 1073741824}
}
func (p Policy) Validate() error {
	if p.HostMemoryReserveBytes < 134217728 || p.HostMemoryReserveBytes > 4294967296 {
		return fail("TRAINING_AGENT_CONFIGURATION_INVALID")
	}
	if p.UserID < 1 || p.UserID > 2147483647 || p.GroupID < 1 || p.GroupID > 2147483647 || p.PidsLimit < 32 || p.PidsLimit > 1024 || p.TemporaryMemoryBytes < 16777216 || p.TemporaryMemoryBytes > 1073741824 || p.SharedMemoryBytes < 16777216 || p.SharedMemoryBytes > 1073741824 || p.LogMaximumBytes < 65536 || p.LogMaximumBytes > 16777216 || p.LogMaximumFiles < 1 || p.LogMaximumFiles > 5 || p.PollMilliseconds < 100 || p.PollMilliseconds > 5000 || p.StopSeconds < 1 || p.StopSeconds > 60 || p.MaximumMemoryBytes < 134217728 || p.MaximumMemoryBytes > 17179869184 || p.MaximumCPUThreads < 1 || p.MaximumCPUThreads > 8 || p.MinimumFreeBytes < 1048576 || p.MinimumFreeBytes > 1099511627776 {
		return fail("TRAINING_AGENT_CONFIGURATION_INVALID")
	}
	return nil
}
func (p Policy) User() string { return strconv.Itoa(p.UserID) + ":" + strconv.Itoa(p.GroupID) }

type Config struct {
	RootDirectory      string  `json:"root_directory"`
	DockerSocket       string  `json:"docker_socket"`
	ImageReference     string  `json:"image_reference"`
	ImageID            string  `json:"image_id"`
	WorkerSourceSHA256 string  `json:"worker_source_sha256"`
	Runtime            *Policy `json:"runtime,omitempty"`
}

func (c *Config) UnmarshalJSON(raw []byte) error {
	type plain Config
	var decoded plain
	if wire.DecodeStrict(json.RawMessage(raw), &decoded) != nil {
		return fail("TRAINING_AGENT_CONFIGURATION_INVALID")
	}
	*c = Config(decoded)
	return nil
}

func cleanPath(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && len(value) <= wire.MaximumPathBytes && !strings.ContainsAny(value, "\x00\r\n")
}
func (c Config) Policy() Policy {
	if c.Runtime == nil {
		return DefaultPolicy()
	}
	return *c.Runtime
}
func (c Config) Validate() error {
	if !cleanPath(c.RootDirectory) || c.RootDirectory == "/" || !cleanPath(c.DockerSocket) || !strings.HasPrefix(c.ImageReference, "projectone/training@sha256:") || !datawire.ValidDigest(strings.TrimPrefix(c.ImageReference, "projectone/training@sha256:")) || !strings.HasPrefix(c.ImageID, "sha256:") || !datawire.ValidDigest(strings.TrimPrefix(c.ImageID, "sha256:")) || !datawire.ValidDigest(c.WorkerSourceSHA256) {
		return fail("TRAINING_AGENT_CONFIGURATION_INVALID")
	}
	return c.Policy().Validate()
}
func (c Config) Bind(command wire.Command) error {
	p := c.Policy()
	if c.Validate() != nil || command.Validate() != nil || command.Recipe.ImageReference != c.ImageReference || command.Recipe.Identity.ImageID == nil || *command.Recipe.Identity.ImageID != c.ImageID || command.Recipe.Identity.WorkerSourceSHA256 != c.WorkerSourceSHA256 || command.Recipe.Resources.MemoryBytes > p.MaximumMemoryBytes || command.Configuration.Threads > p.MaximumCPUThreads || command.ModelCatalogSHA256 == "" {
		return fail("TRAINING_AGENT_RECIPE_MISMATCH")
	}
	return nil
}
