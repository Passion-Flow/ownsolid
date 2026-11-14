package inference

import (
	"context"
	"net"
	"os"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"strconv"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// Execute runs under the caller's exclusive agent state/identity lock and fenced task context.
func (m *Manager) Execute(ctx context.Context, node, task string, command wire.InferenceCommand, download ModelDownload) (wire.InferenceObservation, error) {
	failure := func(e error) (wire.InferenceObservation, error) {
		e = safeError(e)
		out := m.Observe(ctx, node)
		out.ErrorCode = e.Error()
		if out.Deployment != nil {
			out.Deployment.Ready = false
			out.Deployment.ErrorCode = e.Error()
		}
		return out, e
	}
	if !deploymentID.MatchString(node) || !deploymentID.MatchString(task) {
		return failure(fail("INFERENCE_CONFIGURATION_INVALID"))
	}
	if e := command.Validate(command.Operation()); e != nil {
		return failure(e)
	}
	if ctx.Err() != nil {
		return failure(fail("INFERENCE_CANCELLED"))
	}
	if command.DeploymentID == "" {
		out := m.Observe(ctx, node)
		if out.ProbeErrorCode != "" {
			out.ErrorCode = out.ProbeErrorCode
		}
		if out.ErrorCode != "" {
			return out, fail(out.ErrorCode)
		}
		return out, nil
	}
	var j journal
	if _, e := os.Lstat(m.journalPath()); e == nil {
		j, e = m.read()
		if e != nil {
			return failure(e)
		}
		if j.NodeID != node {
			return failure(fail("INFERENCE_OWNERSHIP_CONFLICT"))
		}
		if j.Command.DeploymentID == command.DeploymentID {
			if !sameCommandIdentity(j.Command, command) || command.Revision < j.Command.Revision {
				return failure(fail("INFERENCE_REVISION_CONFLICT"))
			}
			if j.TaskID == task {
				if j.CommandDigest != commandDigest(command) {
					return failure(fail("INFERENCE_REVISION_CONFLICT"))
				}
				if j.CommittedTaskID == task {
					out := m.Observe(ctx, node)
					if e = inferenceOutcome(command, out); e != nil {
						return failure(e)
					}
					return out, nil
				}
			} else if command.Revision <= j.Command.Revision {
				return failure(fail("INFERENCE_REVISION_CONFLICT"))
			}
		} else if j.Phase != "removed" {
			return failure(fail("INFERENCE_OWNERSHIP_CONFLICT"))
		} else {
			j = journal{}
		}
	} else if !os.IsNotExist(e) {
		return failure(fail("INFERENCE_STATE_INVALID"))
	}
	if j.SchemaVersion == 0 {
		if command.Action != "deploy" && command.Action != "remove" && command.Action != "stop" {
			return failure(fail("INFERENCE_STATE_INVALID"))
		}
		if _, e := os.Lstat(m.deploymentPath(command.DeploymentID)); !os.IsNotExist(e) {
			return failure(fail("INFERENCE_OWNERSHIP_CONFLICT"))
		}
		j = journal{SchemaVersion: 1, NodeID: node, Phase: "preparing", Stages: []string{}}
	}
	j.TaskID = task
	j.Command = command
	j.CommandDigest = commandDigest(command)
	j.CommittedTaskID = ""
	j.ErrorCode = ""
	if e := m.save(j); e != nil {
		return failure(e)
	}
	var e error
	switch command.Action {
	case "deploy":
		e = m.deploy(ctx, &j, download)
	case "stop":
		e = m.stop(ctx, &j)
	case "remove":
		e = m.remove(ctx, &j)
	case "observe":
	case "verify":
		var container Container
		container, e = m.ownedContainer(ctx, j)
		if e == nil && !container.State.Running {
			e = fail("INFERENCE_NOT_READY")
		}
		if e == nil {
			var value wire.GenerationVerification
			value, e = m.probes.Generate(ctx)
			if e == nil {
				j.Verification = &value
				j.Phase = "installed"
				e = m.save(j)
			}
		}
	}
	if e != nil {
		if ctx.Err() != nil {
			e = fail("INFERENCE_CANCELLED")
		}
		j.ErrorCode = safeError(e).Error()
		m.save(j)
		return failure(e)
	}
	if ctx.Err() != nil {
		return failure(fail("INFERENCE_CANCELLED"))
	}
	out := m.Observe(ctx, node)
	if e = inferenceOutcome(command, out); e != nil {
		j.ErrorCode = safeError(e).Error()
		m.save(j)
		return failure(e)
	}
	j.CommittedTaskID = task
	if e = m.save(j); e != nil {
		return failure(e)
	}
	return out, nil
}
func inferenceOutcome(command wire.InferenceCommand, out wire.InferenceObservation) error {
	if e := out.Validate(); e != nil {
		return fail("INFERENCE_STATE_INVALID")
	}
	if out.ErrorCode != "" {
		return fail(out.ErrorCode)
	}
	d := out.Deployment
	if command.DeploymentID == "" {
		return nil
	}
	if d == nil || d.DeploymentID != command.DeploymentID || d.ModelID != command.ModelID || d.GPUUUID != command.GPUUUID || d.Revision != command.Revision || !command.MatchesServingIdentity(*d) {
		return fail("INFERENCE_STATE_INVALID")
	}
	if d.ErrorCode != "" {
		return fail(d.ErrorCode)
	}
	switch command.Action {
	case "deploy", "verify":
		if !d.Managed || d.State != "running" || !d.Ready || d.Verification == nil {
			return fail("INFERENCE_NOT_READY")
		}
	case "stop":
		if d.Ready || d.State != "stopped" && d.State != "not_installed" {
			return fail("INFERENCE_STATE_INVALID")
		}
	case "remove":
		if d.Managed || d.State != "not_installed" || d.ContainerID != "" {
			return fail("INFERENCE_STATE_INVALID")
		}
	}
	return nil
}
func (m *Manager) allocationPreflight(ctx context.Context, command wire.InferenceCommand) error {
	if ctx.Err() != nil {
		return fail("INFERENCE_CANCELLED")
	}
	if m.hostAvailable == nil {
		return fail("INFERENCE_STATE_INVALID")
	}
	if e := m.hostAvailable(); e != nil {
		return e
	}
	if e := m.docker.CheckEngine(ctx); e != nil {
		return e
	}
	gpus, busy, e := m.gpu(ctx)
	if e != nil {
		return e
	}
	if e = ValidateCommandGPU(gpus, busy, command); e != nil {
		return e
	}
	listener, e := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(wire.ApprovedInferenceRecipe().HostPort))
	if e != nil {
		return fail("INFERENCE_PORT_CONFLICT")
	}
	listener.Close()
	return nil
}
func (m *Manager) findContainer(ctx context.Context, j journal) (Container, bool, error) {
	if j.ImageID == "" || j.NetworkID == "" {
		value, e := m.docker.InspectContainer(ctx, "projectone-vllm-"+j.Command.DeploymentID)
		if e == nil || value.ID != "" {
			return value, false, fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		if e.Error() == "INFERENCE_DOCKER_NOT_FOUND" {
			return Container{}, false, nil
		}
		return value, false, e
	}
	value, e := m.ownedContainer(ctx, j)
	if e == nil {
		return value, true, nil
	}
	if e.Error() != "INFERENCE_DOCKER_NOT_FOUND" {
		return value, false, e
	}
	if j.ContainerID != "" {
		other, lookup := m.docker.InspectContainer(ctx, "projectone-vllm-"+j.Command.DeploymentID)
		if lookup == nil || other.ID != "" {
			return value, false, fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		if lookup.Error() != "INFERENCE_DOCKER_NOT_FOUND" {
			return value, false, lookup
		}
	}
	return Container{}, false, nil
}
func (m *Manager) deploy(ctx context.Context, j *journal, download ModelDownload) error {
	container, exists, e := m.findContainer(ctx, *j)
	if e != nil {
		return e
	}
	if !exists || !container.State.Running {
		if e = m.allocationPreflight(ctx, j.Command); e != nil {
			return e
		}
	}
	image, e := m.docker.InspectApprovedImage(ctx)
	if e != nil {
		return e
	}
	if e = m.ensureModel(ctx, j, download); e != nil {
		return e
	}
	if e = m.prepareRuntimeCache(*j); e != nil {
		return e
	}
	if !exists {
		j.ImageID = image.ID
		j.NetworkID, e = m.docker.EnsureNetwork(ctx, j.NodeID)
		if e != nil {
			return e
		}
		j.Phase = "creating"
		if e = m.save(*j); e != nil {
			return e
		}
		if ctx.Err() != nil {
			return fail("INFERENCE_CANCELLED")
		}
		if j.Command.Derived == nil {
			j.ContainerID, e = m.docker.CreateContainer(ctx, j.Command.DeploymentID, j.NodeID, j.Command.GPUUUID, m.modelPath(), m.cachePath(j.Command.DeploymentID), j.NetworkID)
		} else {
			engine, ok := m.docker.(interface {
				CreateDerivedContainer(context.Context, wire.InferenceCommand, string, string, string, string) (string, error)
			})
			if !ok {
				return fail("INFERENCE_CONFIGURATION_INVALID")
			}
			j.ContainerID, e = engine.CreateDerivedContainer(ctx, j.Command, j.NodeID, m.commandModelPath(j.Command), m.cachePath(j.Command.DeploymentID), j.NetworkID)
		}
		if e != nil {
			return e
		}
		if e = m.save(*j); e != nil {
			return e
		}
		container, e = m.ownedContainer(ctx, *j)
		if e != nil {
			return e
		}
	} else if j.ContainerID == "" {
		j.ContainerID = container.ID
		if e = m.save(*j); e != nil {
			return e
		}
	}
	if !container.State.Running {
		if e = m.allocationPreflight(ctx, j.Command); e != nil {
			return e
		}
		j.Phase = "loading"
		if e = m.save(*j); e != nil {
			return e
		}
		if ctx.Err() != nil {
			return fail("INFERENCE_CANCELLED")
		}
		if e = m.docker.StartContainer(ctx, j.ContainerID); e != nil {
			return e
		}
		j.Verification = nil
	}
	deadline := time.NewTimer(settings.Seconds(settings.Resolve(m.runtime).ModelLoadSeconds))
	defer deadline.Stop()
	for {
		if ctx.Err() != nil {
			return fail("INFERENCE_CANCELLED")
		}
		container, e = m.ownedContainer(ctx, *j)
		if e != nil {
			return e
		}
		if !container.State.Running {
			if container.State.OOMKilled {
				return fail("INFERENCE_OOM")
			}
			return m.docker.ContainerFailure(ctx, container.ID)
		}
		if e = m.probes.Health(ctx); e == nil {
			verification, e := m.probes.Generate(ctx)
			if e != nil {
				return e
			}
			j.Verification = &verification
			j.Phase = "installed"
			j.ErrorCode = ""
			return m.save(*j)
		}
		timer := time.NewTimer(time.Duration(settings.Resolve(m.runtime).ModelPollMilliseconds) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fail("INFERENCE_CANCELLED")
		case <-deadline.C:
			timer.Stop()
			return fail("INFERENCE_LOAD_TIMEOUT")
		case <-timer.C:
		}
	}
}
