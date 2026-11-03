package training

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

const MinimumTrainingDriverMajor = 580
const MinimumTrainingComputeCapability = 8
const MinimumTrainingGPUMemoryMiB int64 = 8192

type Transport interface {
	TrainingFile(context.Context, string, string, string, datawire.Asset, string) error
	TrainingEvents(context.Context, string, string, wire.Command, [][]byte) ([]wire.EventAcceptance, error)
	BeginTrainingUpload(context.Context, string, string, string, []byte) (wire.Upload, error)
	TrainingChunk(context.Context, string, string, string, wire.ChunkRequest, []byte) (wire.ChunkAcceptance, error)
	FinishTrainingUpload(context.Context, string, string, wire.Upload) (wire.OutputAcceptance, error)
	TrainingStop(context.Context, string, string, wire.Command, wire.StopReceipt, []byte) (wire.StopAcceptance, error)
}
type Manager struct {
	Config       Config
	Lifecycle    Lifecycle
	AgentPolicy  settings.Policy
	preflight    func(context.Context, wire.Command) error
	prepareModel func(context.Context, Config, Journal, FileDownload) error
}

func New(config Config, stateDirectory string, policy settings.Policy) (*Manager, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return nil, fail("TRAINING_PLATFORM_UNSUPPORTED")
	}
	if config.Validate() != nil || policy.Validate() != nil || state.Directory(stateDirectory) != nil || secureRoot(config.RootDirectory, 0, true) != nil {
		return nil, fail("TRAINING_AGENT_CONFIGURATION_INVALID")
	}
	if e := ValidateAgentPolicy(config, policy); e != nil {
		return nil, e
	}
	if config.RootDirectory == stateDirectory || strings.HasPrefix(config.RootDirectory, stateDirectory+"/") || strings.HasPrefix(stateDirectory, config.RootDirectory+"/") {
		return nil, fail("TRAINING_AGENT_CONFIGURATION_INVALID")
	}
	if inference.ProtectedDockerSocket(config.DockerSocket) != nil {
		return nil, fail("TRAINING_DOCKER_SOCKET_INVALID")
	}
	docker, e := NewDocker(config.DockerSocket, policy)
	if e != nil {
		return nil, e
	}
	m := &Manager{Config: config, AgentPolicy: policy, Lifecycle: Lifecycle{Config: config, Engine: docker, Store: JournalStore{Directory: stateDirectory}}, prepareModel: prepareModel}
	m.preflight = m.productionPreflight
	return m, nil
}
func (m *Manager) Close() {
	if d, ok := m.Lifecycle.Engine.(*Docker); ok {
		d.Close()
	}
}
func (m *Manager) CheckInstallation(ctx context.Context) error {
	if e := m.Lifecycle.Engine.CheckEngine(ctx); e != nil {
		return e
	}
	_, e := m.Lifecycle.Engine.InspectImage(ctx, m.Config)
	return e
}
func (m *Manager) productionPreflight(ctx context.Context, command wire.Command) error {
	if runtime.NumCPU() < int(command.Configuration.Threads) {
		return fail("TRAINING_HOST_CPU_UNAVAILABLE")
	}
	file, e := os.Open("/proc/meminfo")
	if e != nil {
		return fail("TRAINING_HOST_MEMORY_LOW")
	}
	body, e := io.ReadAll(io.LimitReader(file, 65537))
	file.Close()
	if e != nil || len(body) > 65536 {
		return fail("TRAINING_HOST_MEMORY_LOW")
	}
	required := command.Recipe.Resources.MemoryBytes + m.Config.Policy().HostMemoryReserveBytes + m.Config.Policy().TemporaryMemoryBytes + m.Config.Policy().SharedMemoryBytes
	if validateHostMemory(string(body), required) != nil {
		return fail("TRAINING_HOST_MEMORY_LOW")
	}
	if e := m.CheckInstallation(ctx); e != nil {
		return e
	}
	gpus, busy, e := inference.ProbeNVIDIA(ctx, m.AgentPolicy)
	if e != nil {
		return fail("TRAINING_GPU_UNAVAILABLE")
	}
	for _, gpu := range busy {
		if gpu == command.GPUUUID {
			return fail("TRAINING_GPU_BUSY")
		}
	}
	found := false
	for _, gpu := range gpus {
		if gpu.UUID != command.GPUUUID {
			continue
		}
		found = true
		driver, e := strconv.Atoi(strings.Split(gpu.DriverVersion, ".")[0])
		if e != nil || driver < MinimumTrainingDriverMajor {
			return fail("TRAINING_DRIVER_UNSUPPORTED")
		}
		if gpu.ComputeCapability < MinimumTrainingComputeCapability || gpu.MIGMode == "Enabled" || gpu.MemoryTotalMiB < MinimumTrainingGPUMemoryMiB || gpu.MemoryFreeMiB < MinimumTrainingGPUMemoryMiB {
			return fail("TRAINING_GPU_UNSUPPORTED")
		}
	}
	if !found {
		return fail("TRAINING_GPU_UNAVAILABLE")
	}
	return nil
}

func ValidateAgentPolicy(config Config, policy settings.Policy) error {
	p := config.Policy()
	if config.Validate() != nil || policy.Validate() != nil || policy.ControlConnections < 2 || p.StopSeconds >= policy.DockerHeaderSeconds || p.StopSeconds >= policy.DockerSeconds || policy.RemoveSeconds <= p.StopSeconds {
		return fail("TRAINING_AGENT_CONFIGURATION_INVALID")
	}
	return nil
}
func validateHostMemory(body string, required int64) error {
	seen := false
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "MemAvailable:" {
			continue
		}
		if seen || len(fields) != 3 || fields[2] != "kB" {
			return fail("TRAINING_HOST_MEMORY_LOW")
		}
		seen = true
		available, e := strconv.ParseInt(fields[1], 10, 54)
		if e != nil || available < 0 || available < ((required+1023)/1024) {
			return fail("TRAINING_HOST_MEMORY_LOW")
		}
	}
	if !seen {
		return fail("TRAINING_HOST_MEMORY_LOW")
	}
	return nil
}
func (m *Manager) Begin(node string, lease agentwire.Lease) (Journal, error) {
	if lease.Operation != wire.RunOperation || lease.Training == nil || lease.Component != nil || lease.Inference != nil || m.Config.Bind(*lease.Training) != nil {
		return Journal{}, fail("AGENT_TASK_UNSUPPORTED")
	}
	return m.Lifecycle.Store.Begin(node, lease.TaskID, lease.LeaseToken, *lease.Training)
}
func (m *Manager) prepare(ctx context.Context, j Journal, transport Transport) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if m.preflight == nil || m.prepareModel == nil {
		return fail("TRAINING_AGENT_CONFIGURATION_INVALID")
	}
	if e := m.preflight(ctx, j.Command); e != nil {
		return e
	}
	var required int64 = j.Command.Recipe.Resources.MaximumOutputBytes + j.Command.Inputs.Train.SizeBytes + j.Command.Inputs.Validation.SizeBytes + m.Config.Policy().MinimumFreeBytes + agentwire.ModelBytes()
	if j.Command.Resume != nil {
		for _, asset := range j.Command.Resume.Files {
			required += asset.SizeBytes
		}
	}
	available, e := availableSpace(m.Config.RootDirectory)
	if e != nil {
		return e
	}
	if available < uint64(required) {
		return fail("TRAINING_DISK_SPACE_LOW")
	}
	base := "/agent/v1/nodes/" + j.NodeID + "/tasks/" + j.TaskID
	download := func(ctx context.Context, kind string, asset datawire.Asset, destination string) error {
		return transport.TrainingFile(ctx, base, j.LeaseToken, kind, asset, destination)
	}
	if e = m.prepareModel(ctx, m.Config, j, download); e != nil {
		return e
	}
	return prepareAttempt(ctx, m.Config, j, download)
}

// Execute always reconciles the exact owned container with an independent bounded cleanup context.
// A nonzero receipt can be submitted through the restricted stop endpoint even after execution authority expires.
func (m *Manager) Execute(ctx context.Context, node string, lease agentwire.Lease, transport Transport) (receipt wire.StopReceipt, outcome []byte, executionError error) {
	j, e := m.Begin(node, lease)
	if e != nil {
		return receipt, nil, e
	}
	if j.Phase != "preparing" {
		return receipt, nil, fail("TRAINING_AGENT_PREVIOUS_ATTEMPT_REQUIRES_RECONCILIATION")
	}
	defer func() {
		if executionError != nil {
			code := executionError.Error()
			if errors.Is(executionError, context.DeadlineExceeded) {
				code = "TRAINING_PROCESSOR_TIMEOUT"
			} else if errors.Is(executionError, context.Canceled) {
				code = "TRAINING_AGENT_INTERRUPTED"
			} else if !wire.ValidAgentError(code) || code == "" {
				code = "TRAINING_AGENT_EXECUTION_FAILED"
			}
			j.AgentErrorCode = code
			// Physical cleanup remains mandatory even if this diagnostic write fails.
			_ = m.Lifecycle.Store.Save(j)
		}
		cleanup, stop := context.WithTimeout(context.Background(), settings.Seconds(m.AgentPolicy.RemoveSeconds))
		defer stop()
		proof, e := m.Lifecycle.Stop(cleanup, &j)
		if e != nil {
			executionError = e
			receipt = wire.StopReceipt{}
			return
		}
		receipt = proof
	}()
	if e = m.prepare(ctx, j, transport); e != nil {
		return receipt, nil, e
	}
	if e = m.Lifecycle.Start(ctx, &j); e != nil {
		return receipt, nil, e
	}
	for {
		if e = ctx.Err(); e != nil {
			return receipt, nil, e
		}
		container, e := m.Lifecycle.owned(ctx, j)
		if e != nil {
			return receipt, nil, e
		}
		if !container.State.Running && container.State.OOMKilled {
			return receipt, nil, fail("TRAINING_PROCESSOR_MEMORY_EXCEEDED")
		}
		if e = outputBudget(ctx, filepath.Join(attemptPath(m.Config, j), "output"), j.Command.Recipe.Resources, m.Config.Policy().UserID); e != nil {
			return receipt, nil, e
		}
		if e = m.relayEvents(ctx, &j, transport, !container.State.Running); e != nil {
			return receipt, nil, e
		}
		if !container.State.Running {
			if container.State.OOMKilled {
				return receipt, nil, fail("TRAINING_PROCESSOR_MEMORY_EXCEEDED")
			}
			request, e := j.Command.ContainerRequest()
			if e != nil {
				return receipt, nil, e
			}
			raw, e := workerBytes(filepath.Join(attemptPath(m.Config, j), "output/result.json"), min(j.Command.Recipe.Resources.MaximumResultBytes, int64(wire.MaximumCheckpointMetadataBytes)), m.Config.Policy().UserID)
			if e != nil {
				return receipt, nil, fail("TRAINING_RESULT_INVALID")
			}
			result, e := wire.DecodeResult(raw, request, agentwire.ApprovedModel().Repository)
			if e != nil {
				return receipt, nil, e
			}
			if result.Status == "trained" {
				if container.State.ExitCode != 0 || result.EventCount != j.LastEventSequence {
					return receipt, nil, fail("TRAINING_RESULT_INVALID")
				}
				assets := []datawire.Asset{}
				for _, asset := range result.Files {
					if strings.HasPrefix(asset.Path, "adapter/") {
						asset.Path = strings.TrimPrefix(asset.Path, "adapter/")
						assets = append(assets, asset)
					}
				}
				if e = m.upload(ctx, j, transport, "artifact", raw, filepath.Join(attemptPath(m.Config, j), "output/adapter"), assets, result.GlobalStep); e != nil {
					return receipt, nil, fail("TRAINING_OUTPUT_TRANSFER_FAILED")
				}
			}
			if e = m.saveOutcome(&j, raw); e != nil {
				return receipt, nil, e
			}
			return receipt, raw, nil
		}
		select {
		case <-ctx.Done():
			return receipt, nil, ctx.Err()
		case <-time.After(time.Duration(m.Config.Policy().PollMilliseconds) * time.Millisecond):
		}
	}
}
func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func (m *Manager) relayEvents(ctx context.Context, j *Journal, transport Transport, finished bool) error {
	raw, e := workerBytes(filepath.Join(attemptPath(m.Config, *j), "events.jsonl"), j.Command.Recipe.Resources.MaximumEventBytes, m.Config.Policy().UserID)
	if os.IsNotExist(e) && j.LastEventSequence == 0 && !finished {
		return nil
	}
	if e != nil {
		return fail("TRAINING_EVENT_INVALID")
	}
	lines := bytes.Split(raw, []byte{'\n'})
	if len(lines[len(lines)-1]) != 0 && finished {
		return fail("TRAINING_EVENT_INVALID")
	}
	lines = lines[:len(lines)-1]
	if int64(len(lines)) > wire.MaximumEventSequence {
		return fail("TRAINING_EVENT_LIMIT")
	}
	for i, line := range lines {
		event, e := wire.DecodeEvent(line, j.Command.Scope.JobID, j.Command.AttemptID, j.Command.Configuration.MaximumSteps)
		if e != nil || event.Sequence != int64(i+1) {
			return fail("TRAINING_EVENT_INVALID")
		}
		if event.Sequence <= j.LastEventSequence {
			continue
		}
		base := "/agent/v1/nodes/" + j.NodeID + "/tasks/" + j.TaskID
		if _, e = transport.TrainingEvents(ctx, base, j.LeaseToken, j.Command, [][]byte{line}); e != nil {
			return fail("TRAINING_EVENT_TRANSFER_FAILED")
		}
		j.LastEventSequence = event.Sequence
		if e = m.Lifecycle.Store.Save(*j); e != nil {
			return e
		}
		if event.Kind == "checkpoint" {
			request, e := j.Command.ContainerRequest()
			if e != nil {
				return e
			}
			directory := filepath.Join(attemptPath(m.Config, *j), "output/checkpoints", *event.Path)
			metadata, e := workerBytes(filepath.Join(directory, "projectone-checkpoint.json"), wire.MaximumCheckpointMetadataBytes, m.Config.Policy().UserID)
			if e != nil || digest(metadata) != *event.ReceiptSHA256 {
				return fail("TRAINING_CHECKPOINT_INVALID")
			}
			checkpoint, e := wire.DecodeCheckpoint(metadata, request, agentwire.ApprovedModel().Repository)
			if e != nil {
				return e
			}
			assets := append(append([]datawire.Asset{}, checkpoint.Files...), datawire.Asset{Path: "projectone-checkpoint.json", SizeBytes: int64(len(metadata)), SHA256: digest(metadata)})
			if e = m.upload(ctx, *j, transport, "checkpoint", metadata, directory, assets, checkpoint.GlobalStep); e != nil {
				return e
			}
		}
	}
	return nil
}
func sameAssets(a, b []datawire.Asset) bool {
	if len(a) != len(b) {
		return false
	}
	values := map[string]datawire.Asset{}
	for _, asset := range a {
		if _, ok := values[asset.Path]; ok {
			return false
		}
		values[asset.Path] = asset
	}
	for _, asset := range b {
		expected, ok := values[asset.Path]
		if !ok || asset != expected {
			return false
		}
		delete(values, asset.Path)
	}
	return len(values) == 0
}
func (m *Manager) upload(ctx context.Context, j Journal, transport Transport, kind string, metadata []byte, directory string, assets []datawire.Asset, step int64) error {
	base := "/agent/v1/nodes/" + j.NodeID + "/tasks/" + j.TaskID
	upload, e := transport.BeginTrainingUpload(ctx, base, j.LeaseToken, kind, metadata)
	if e != nil {
		return e
	}
	if upload.JobID != j.Command.Scope.JobID || upload.AttemptID != j.Command.AttemptID || upload.GlobalStep != step || !sameAssets(assets, upload.Files) {
		return fail("TRAINING_UPLOAD_INVALID")
	}
	for _, asset := range assets {
		if kind == "artifact" && !wire.AdapterAssetAllowed(asset.Path) || kind == "checkpoint" && !wire.CheckpointAssetAllowed(asset.Path) {
			return fail("TRAINING_UPLOAD_INVALID")
		}
		file, e := verifiedAsset(ctx, filepath.Join(directory, asset.Path), asset, m.Config.Policy().UserID)
		if e != nil {
			return e
		}
		for offset := int64(0); offset < asset.SizeBytes; {
			length := min(wire.TransferChunkBytes, asset.SizeBytes-offset)
			body := make([]byte, length)
			_, e = io.ReadFull(file, body)
			if e != nil {
				file.Close()
				return fail("TRAINING_OUTPUT_CHANGED")
			}
			if _, e = transport.TrainingChunk(ctx, base, j.LeaseToken, upload.ID, wire.ChunkRequest{Asset: asset.Path, Offset: offset, Length: length, SHA256: digest(body)}, body); e != nil {
				file.Close()
				return e
			}
			offset += length
		}
		file.Close()
	}
	_, e = transport.FinishTrainingUpload(ctx, base, j.LeaseToken, upload)
	return e
}
func (m *Manager) outcomePath(task string) string {
	return filepath.Join(m.Lifecycle.Store.Directory, "training-outcomes", task+".json")
}
func (m *Manager) saveOutcome(j *Journal, raw []byte) error {
	parent := filepath.Dir(m.outcomePath(j.TaskID))
	if e := rootDirectory(parent); e != nil {
		return e
	}
	if e := newPrivateFile(m.outcomePath(j.TaskID), raw, os.Geteuid(), os.Getegid()); e != nil {
		return e
	}
	sha := digest(raw)
	j.OutcomeSHA256 = &sha
	return m.Lifecycle.Store.Save(*j)
}
func (m *Manager) ReadOutcome(j Journal) ([]byte, error) {
	if j.OutcomeSHA256 == nil {
		return nil, nil
	}
	f, e := privateRegular(m.outcomePath(j.TaskID), wire.MaximumCheckpointMetadataBytes)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, wire.MaximumCheckpointMetadataBytes+1))
	if e != nil || digest(raw) != *j.OutcomeSHA256 {
		return nil, fail("TRAINING_OUTPUT_CHANGED")
	}
	return raw, nil
}
func (m *Manager) Acknowledge(j *Journal) error {
	if j.StopReceipt == nil {
		return fail("TRAINING_AGENT_JOURNAL_INVALID")
	}
	j.Phase = "acknowledged"
	if e := m.Lifecycle.Store.Save(*j); e != nil {
		return e
	}
	parent := filepath.Join(m.Lifecycle.Store.Directory, "training-history")
	if e := rootDirectory(parent); e != nil {
		return e
	}
	destination := filepath.Join(parent, j.TaskID+".json")
	raw, _ := json.Marshal(j)
	if e := newPrivateFile(destination, raw, os.Geteuid(), os.Getegid()); e != nil {
		f, readError := privateRegular(destination, MaximumJournalBytes)
		if readError != nil {
			return e
		}
		previous, readError := io.ReadAll(io.LimitReader(f, MaximumJournalBytes+1))
		f.Close()
		if readError != nil || !bytes.Equal(previous, raw) {
			return fail("TRAINING_AGENT_JOURNAL_INVALID")
		}
	}
	if os.Remove(m.Lifecycle.Store.Path()) != nil {
		return fail("TRAINING_AGENT_IO_FAILED")
	}
	return syncDirectory(m.Lifecycle.Store.Directory)
}

func (m *Manager) AcceptStop(receipt wire.StopReceipt) error {
	j, e := m.Lifecycle.Store.Read()
	if e != nil {
		return e
	}
	expected, _ := json.Marshal(j.StopReceipt)
	given, _ := json.Marshal(receipt)
	if j.StopReceipt == nil || !bytes.Equal(expected, given) || receipt.ValidateFor(j.Command, j.NodeID, j.TaskID) != nil {
		return fail("TRAINING_AGENT_JOURNAL_INVALID")
	}
	return m.Acknowledge(&j)
}
func (m *Manager) Recover(ctx context.Context, node string, transport Transport) error {
	j, e := m.Lifecycle.Store.Read()
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if j.NodeID != node {
		return fail("TRAINING_AGENT_JOURNAL_INVALID")
	}
	if j.StopReceipt == nil && j.AgentErrorCode == "" {
		j.AgentErrorCode = "TRAINING_AGENT_INTERRUPTED"
		if e = m.Lifecycle.Store.Save(j); e != nil {
			return e
		}
	}
	receipt, e := m.Lifecycle.Stop(ctx, &j)
	if e != nil {
		return e
	}
	if j.Phase == "acknowledged" {
		return m.Acknowledge(&j)
	}
	outcome, e := m.ReadOutcome(j)
	if e != nil {
		return e
	}
	base := "/agent/v1/nodes/" + j.NodeID + "/tasks/" + j.TaskID
	if _, e = transport.TrainingStop(ctx, base, j.LeaseToken, j.Command, receipt, outcome); e != nil {
		return e
	}
	return m.Acknowledge(&j)
}
