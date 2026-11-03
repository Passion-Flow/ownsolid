package inference

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"runtime"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

type Config struct {
	RootDirectory string `json:"root_directory"`
	DockerSocket  string `json:"docker_socket"`
}
type engine interface {
	InspectApprovedImage(context.Context) (Image, error)
	CheckEngine(context.Context) error
	EnsureNetwork(context.Context, string) (string, error)
	InspectContainer(context.Context, string) (Container, error)
	CreateContainer(context.Context, string, string, string, string, string, string) (string, error)
	StartContainer(context.Context, string) error
	StopContainer(context.Context, string) error
	RemoveContainer(context.Context, string) error
	RemoveUnusedNetwork(context.Context, string, ...string) error
	VerifyNetworkEndpoint(context.Context, string, string, string) error
	ContainerFailure(context.Context, string) error
}
type probes interface {
	Health(context.Context) error
	Generate(context.Context) (wire.GenerationVerification, error)
}
type Manager struct {
	config         Config
	runtime        *settings.Policy
	stateDirectory string
	docker         engine
	probes         probes
	hostAvailable  func() error
	gpu            func(context.Context) ([]wire.NVIDIAGPU, []string, error)
}
type journal struct {
	SchemaVersion          int                          `json:"schema_version"`
	NodeID                 string                       `json:"node_id"`
	TaskID                 string                       `json:"task_id"`
	Command                wire.InferenceCommand        `json:"command"`
	CommandDigest          string                       `json:"command_digest"`
	Phase                  string                       `json:"phase"`
	ImageID                string                       `json:"image_id,omitempty"`
	NetworkID              string                       `json:"network_id,omitempty"`
	ContainerID            string                       `json:"container_id,omitempty"`
	Stages                 []string                     `json:"stages"`
	CachePublicationIntent bool                         `json:"cache_publication_intent,omitempty"`
	Verification           *wire.GenerationVerification `json:"verification,omitempty"`
	CommittedTaskID        string                       `json:"committed_task_id,omitempty"`
	ErrorCode              string                       `json:"error_code,omitempty"`
}

var phases = map[string]bool{"preparing": true, "creating": true, "loading": true, "installed": true, "stopping": true, "stopped": true, "removing": true, "removed": true, "failed": true}

func New(config Config, stateDirectory string, policies ...settings.Policy) (*Manager, error) {
	policy := settings.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	if e := policy.Validate(); e != nil {
		return nil, e
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return nil, fail("INFERENCE_PLATFORM_UNSUPPORTED")
	}
	if config.RootDirectory == stateDirectory || strings.HasPrefix(config.RootDirectory, stateDirectory+"/") || strings.HasPrefix(stateDirectory, config.RootDirectory+"/") {
		return nil, fail("INFERENCE_CONFIGURATION_INVALID")
	}
	if e := secureRoot(config.RootDirectory, true); e != nil {
		return nil, e
	}
	if e := state.Directory(stateDirectory); e != nil {
		return nil, e
	}
	if e := ProtectedDockerSocket(config.DockerSocket); e != nil {
		return nil, e
	}
	docker, e := NewDocker(config.DockerSocket, policy)
	if e != nil {
		return nil, e
	}
	return &Manager{runtime: &policy, config: config, stateDirectory: stateDirectory, docker: docker, probes: localVerifier(policy), gpu: func(ctx context.Context) ([]wire.NVIDIAGPU, []string, error) { return ProbeNVIDIA(ctx, policy) }, hostAvailable: CheckAvailableHostMemory}, nil
}
func (m *Manager) Close() {
	if docker, ok := m.docker.(*Docker); ok {
		docker.Close()
	}
	if probe, ok := m.probes.(*verifier); ok {
		probe.Close()
	}
}
func (m *Manager) journalPath() string {
	return filepath.Join(m.stateDirectory, "inference-journal.json")
}
func (m *Manager) modelPath() string {
	return filepath.Join(m.config.RootDirectory, "models", wire.ModelCatalogDigest())
}
func (m *Manager) deploymentPath(id string) string {
	return filepath.Join(m.config.RootDirectory, "deployments", id)
}
func (m *Manager) cachePath(id string) string { return filepath.Join(m.deploymentPath(id), "cache") }
func (m *Manager) save(value journal) error   { return replaceJSON(m.journalPath(), value) }
func (m *Manager) read() (journal, error) {
	var value journal
	if e := protectedJSON(m.journalPath(), &value); e != nil {
		return value, e
	}
	if value.SchemaVersion != 1 || !deploymentID.MatchString(value.NodeID) || !deploymentID.MatchString(value.TaskID) || value.Command.DeploymentID == "" || value.Command.Validate(value.Command.Operation()) != nil || value.CommandDigest != commandDigest(value.Command) || !phases[value.Phase] || len(value.Stages) > 3 || !wire.KnownInferenceError(value.ErrorCode) {
		return value, fail("INFERENCE_STATE_INVALID")
	}
	if value.CommittedTaskID != "" && !deploymentID.MatchString(value.CommittedTaskID) {
		return value, fail("INFERENCE_STATE_INVALID")
	}
	if value.CachePublicationIntent && len(value.Stages) == 0 {
		return value, fail("INFERENCE_STATE_INVALID")
	}
	if value.ContainerID != "" && !containerID.MatchString(value.ContainerID) || value.NetworkID != "" && !containerID.MatchString(value.NetworkID) {
		return value, fail("INFERENCE_STATE_INVALID")
	}
	recipe := wire.ApprovedInferenceRecipe()
	if value.ImageID != "" && value.ImageID != recipe.ImageConfigDigest && value.ImageID != recipe.ImageManifestDigest {
		return value, fail("INFERENCE_STATE_INVALID")
	}
	for _, stage := range value.Stages {
		if !validStagePath(m.config.RootDirectory, stage) {
			return value, fail("INFERENCE_STATE_INVALID")
		}
	}
	if value.Verification != nil && value.Verification.Validate() != nil {
		return value, fail("INFERENCE_STATE_INVALID")
	}
	return value, nil
}
func safeError(e error) error {
	if e == nil {
		return nil
	}
	if wire.KnownInferenceError(e.Error()) {
		return e
	}
	return fail("INFERENCE_OPERATION_FAILED")
}
func commandObservation(j journal) wire.DeploymentObservation {
	out := wire.DeploymentObservation{DeploymentID: j.Command.DeploymentID, ModelID: j.Command.ModelID, CatalogDigest: j.Command.CatalogDigest, GPUUUID: j.Command.GPUUUID, Revision: j.Command.Revision, State: "unknown", ContainerID: j.ContainerID, ImageID: j.ImageID, Verification: j.Verification, ErrorCode: j.ErrorCode}
	if j.Command.Derived != nil {
		out.ModelKind = wire.DerivedModelKind
		out.ServingRecipeSHA256 = j.Command.Derived.ServingRecipeSHA256
	}
	return out
}
func sameCommandIdentity(a, b wire.InferenceCommand) bool {
	if (a.Derived == nil) != (b.Derived == nil) {
		return false
	}
	if a.Derived != nil && a.Derived.ServingRecipeSHA256 != b.Derived.ServingRecipeSHA256 {
		return false
	}
	return a.DeploymentID == b.DeploymentID && a.ModelID == b.ModelID && a.CatalogDigest == b.CatalogDigest && a.GPUUUID == b.GPUUUID
}
func (m *Manager) Observe(ctx context.Context, node string) wire.InferenceObservation {
	out := wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, ObservedAt: time.Now().UTC()}
	gpus, _, e := m.gpu(ctx)
	if e != nil {
		out.ProbeErrorCode = safeError(e).Error()
	} else {
		out.GPUs = gpus
	}
	if _, e = os.Lstat(m.journalPath()); os.IsNotExist(e) {
		if out.ProbeErrorCode == "" {
			if e = m.docker.CheckEngine(ctx); e == nil {
				_, e = m.docker.InspectApprovedImage(ctx)
			}
			if e != nil {
				out.ProbeErrorCode = safeError(e).Error()
			}
		}
		return out
	}
	j, e := m.read()
	if e != nil {
		out.ErrorCode = safeError(e).Error()
		return out
	}
	observation := commandObservation(j)
	out.Deployment = &observation
	if j.NodeID != node {
		out.ErrorCode = "INFERENCE_OWNERSHIP_CONFLICT"
		observation.ErrorCode = out.ErrorCode
		return out
	}
	if j.Phase == "removed" {
		observation.State = "not_installed"
		observation.ContainerID = ""
		observation.ImageID = ""
		observation.Verification = nil
		observation.ErrorCode = ""
		return out
	}
	if j.Phase == "stopped" && j.ContainerID == "" {
		_, exists, e := m.findContainer(ctx, j)
		if e != nil || exists {
			observation.ErrorCode = "INFERENCE_OWNERSHIP_CONFLICT"
			return out
		}
		observation.State = "not_installed"
		observation.ImageID = ""
		observation.Verification = nil
		observation.ErrorCode = ""
		return out
	}
	container, e := m.ownedContainer(ctx, j)
	if e != nil {
		observation.ErrorCode = safeError(e).Error()
		return out
	}
	observation.Managed = true
	observation.ContainerID = container.ID
	observation.ImageID = container.Image
	if !container.State.Running {
		observation.State = "stopped"
		if container.State.OOMKilled && j.Phase != "stopped" {
			observation.State = "failed"
			observation.ErrorCode = "INFERENCE_OOM"
		}
		return out
	}
	observation.State = "loading"
	if e = m.probes.Health(ctx); e != nil {
		observation.ErrorCode = safeError(e).Error()
		return out
	}
	observation.State = "running"
	started, e := time.Parse(time.RFC3339Nano, container.State.StartedAt)
	if e == nil && j.Verification != nil && !started.After(j.Verification.ObservedAt) && out.ErrorCode == "" && out.ProbeErrorCode == "" && j.ErrorCode == "" {
		observation.Ready = true
	}
	return out
}

// JournalBytesForEvidence excludes credentials, model contents and raw generation text.
func (m *Manager) JournalBytesForEvidence() ([]byte, error) {
	value, e := m.read()
	if e != nil {
		return nil, e
	}
	return json.Marshal(value)
}

func (m *Manager) commandModelPath(c wire.InferenceCommand) string {
	return filepath.Join(m.config.RootDirectory, "models", c.CatalogDigest)
}
func (m *Manager) commandContainerConfiguration(j journal) (ContainerConfig, error) {
	if j.Command.Derived == nil {
		return ContainerConfiguration(j.Command.DeploymentID, j.NodeID, j.Command.GPUUUID, m.modelPath(), m.cachePath(j.Command.DeploymentID), j.NetworkID)
	}
	c := j.Command
	c.Action = "deploy"
	return DerivedContainerConfiguration(c, j.NodeID, m.commandModelPath(c), m.cachePath(c.DeploymentID), j.NetworkID)
}
