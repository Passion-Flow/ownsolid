package modelpreparations

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type Engine interface {
	InspectImage(context.Context, Config) (Image, error)
	CheckEngine(context.Context) error
	InspectContainer(context.Context, string) (Container, error)
	Create(context.Context, string, ContainerConfig) (string, error)
	Start(context.Context, string) error
	Stop(context.Context, string, time.Duration) error
	Kill(context.Context, string) error
	Remove(context.Context, string) error
}
type Lifecycle struct {
	Config Config
	Engine Engine
	Store  JournalStore
}

func (m *Lifecycle) expected(ctx context.Context, j Journal) (ContainerConfig, Image, error) {
	var expected ContainerConfig
	var image Image
	if j.Validate() != nil || m.Config.Bind(j.Command) != nil || m.Engine == nil {
		return expected, image, fail("MODEL_PREPARATION_AGENT_RECIPE_MISMATCH")
	}
	expected, e := ContainerConfiguration(m.Config, j.NodeID, j.TaskID, j.JournalID, j.Command, filepath.Join(m.Config.RootDirectory, "models", j.Command.ModelCatalogSHA256), filepath.Join(m.Config.RootDirectory, "preparations", j.Command.Scope.TenantID, j.Command.Scope.WorkspaceID, j.Command.Scope.PreparationID, j.Command.AttemptID))
	if e != nil {
		return expected, image, e
	}
	image, e = m.Engine.InspectImage(ctx, m.Config)
	return expected, image, e
}
func (m *Lifecycle) owned(ctx context.Context, j Journal) (Container, error) {
	reference := j.ContainerID
	if reference == "" {
		reference = wire.ContainerName(j.Command.AttemptID)
	}
	value, e := m.Engine.InspectContainer(ctx, reference)
	if e != nil {
		return value, e
	}
	expected, image, e := m.expected(ctx, j)
	if e != nil {
		return Container{}, e
	}
	if e = VerifyContainer(value, expected, image, wire.ContainerName(j.Command.AttemptID), j.ContainerID); e != nil {
		return value, e
	}
	// Name and immutable ID must still identify the same owned object.
	named, e := m.Engine.InspectContainer(ctx, wire.ContainerName(j.Command.AttemptID))
	if e != nil || named.ID != value.ID {
		return value, fail("MODEL_PREPARATION_CONTAINER_OWNERSHIP_CONFLICT")
	}
	if e = VerifyContainer(named, expected, image, wire.ContainerName(j.Command.AttemptID), value.ID); e != nil {
		return value, e
	}
	return value, nil
}
func (m *Lifecycle) Start(ctx context.Context, j *Journal) error {
	if j.Phase != "preparing" || j.ContainerID != "" || j.LastEventSequence != 0 {
		return fail("MODEL_PREPARATION_AGENT_ATTEMPT_ALREADY_STARTED")
	}
	if e := m.Engine.CheckEngine(ctx); e != nil {
		return e
	}
	expected, _, e := m.expected(ctx, *j)
	if e != nil {
		return e
	}
	if _, e = m.Engine.InspectContainer(ctx, wire.ContainerName(j.Command.AttemptID)); e == nil {
		return fail("MODEL_PREPARATION_CONTAINER_OWNERSHIP_CONFLICT")
	} else if e.Error() != "MODEL_PREPARATION_DOCKER_NOT_FOUND" {
		return e
	}
	j.Phase = "creating"
	if e = m.Store.Save(*j); e != nil {
		return e
	}
	id, createError := m.Engine.Create(ctx, wire.ContainerName(j.Command.AttemptID), expected)
	if createError == nil {
		j.ContainerID = id
		if e = m.Store.Save(*j); e != nil {
			return e
		}
	}
	value, e := m.owned(ctx, *j)
	if e != nil {
		if createError != nil {
			return createError
		}
		return e
	}
	if value.State.Running || value.State.Status != "created" {
		return fail("MODEL_PREPARATION_CONTAINER_OWNERSHIP_CONFLICT")
	}
	j.ContainerID = value.ID
	j.ContainerVerified = true
	if e = m.Store.Save(*j); e != nil {
		return e
	}
	if e = m.Engine.Start(ctx, j.ContainerID); e != nil {
		return e
	}
	j.Phase = "running"
	return m.Store.Save(*j)
}
func notFound(e error) bool { return e != nil && e.Error() == "MODEL_PREPARATION_DOCKER_NOT_FOUND" }
func (m *Lifecycle) absent(ctx context.Context, j Journal) error {
	if _, e := m.Engine.InspectContainer(ctx, wire.ContainerName(j.Command.AttemptID)); !notFound(e) {
		return fail("MODEL_PREPARATION_STOP_UNCERTAIN")
	}
	if j.ContainerID != "" {
		if _, e := m.Engine.InspectContainer(ctx, j.ContainerID); !notFound(e) {
			return fail("MODEL_PREPARATION_STOP_UNCERTAIN")
		}
	}
	return nil
}

// Stop requires a separate bounded cleanup context after the execution/lease context is cancelled.
// It never releases control ownership; only an accepted persisted receipt can do that.
func (m *Lifecycle) Stop(ctx context.Context, j *Journal) (wire.StopReceipt, error) {
	var out wire.StopReceipt
	if j.Validate() != nil {
		return out, fail("MODEL_PREPARATION_AGENT_JOURNAL_INVALID")
	}
	if j.StopReceipt != nil {
		if e := m.absent(ctx, *j); e != nil {
			return out, e
		}
		return *j.StopReceipt, nil
	}
	previousPhase := j.Phase
	value, e := m.owned(ctx, *j)
	if notFound(e) {
		if j.ContainerID == "" && (previousPhase != "preparing" || j.LastEventSequence != 0 || j.OutcomeSHA256 != nil) {
			return out, fail("MODEL_PREPARATION_STOP_UNCERTAIN")
		}
		if j.ContainerID != "" && !j.ContainerVerified {
			return out, fail("MODEL_PREPARATION_STOP_UNCERTAIN")
		}
	} else if e != nil {
		return out, e
	} else {
		j.ContainerID = value.ID
		j.ContainerVerified = true
		j.Phase = "stopping"
		if e = m.Store.Save(*j); e != nil {
			return out, e
		}
		if value.State.Running {
			stopError := m.Engine.Stop(ctx, value.ID, time.Duration(m.Config.Policy().StopSeconds)*time.Second)
			value, e = m.owned(ctx, *j)
			if e != nil {
				return out, e
			}
			if value.State.Running {
				// Re-inspection above is mandatory even when graceful stop failed.
				if e = m.Engine.Kill(ctx, value.ID); e != nil {
					return out, e
				}
				for value.State.Running {
					select {
					case <-ctx.Done():
						return out, fail("MODEL_PREPARATION_STOP_UNCERTAIN")
					case <-time.After(time.Duration(m.Config.Policy().PollMilliseconds) * time.Millisecond):
					}
					value, e = m.owned(ctx, *j)
					if e != nil {
						return out, e
					}
				}
			} else if stopError != nil && ctx.Err() != nil {
				return out, fail("MODEL_PREPARATION_STOP_UNCERTAIN")
			}
		}
		// Recheck ownership immediately before non-forced removal.
		value, e = m.owned(ctx, *j)
		if e != nil || value.State.Running {
			return out, fail("MODEL_PREPARATION_STOP_UNCERTAIN")
		}
		if e = m.Engine.Remove(ctx, value.ID); e != nil {
			return out, e
		}
	}
	if e = m.absent(ctx, *j); e != nil {
		return out, e
	}
	id, e := newUUID()
	if e != nil {
		return out, e
	}
	out = wire.StopReceipt{SchemaVersion: 1, ReceiptID: id, JournalID: j.JournalID, NodeID: j.NodeID, TaskID: j.TaskID, Scope: j.Command.Scope, AttemptID: j.Command.AttemptID, AttemptNumber: j.Command.AttemptNumber, CommandSHA256: j.CommandSHA256, ImageID: *j.Command.Recipe.Identity.ImageID, GPUUUID: j.Command.GPUUUID, ContainerName: wire.ContainerName(j.Command.AttemptID), Proof: "never_created", OwnedContainerAbsent: true, OutcomeSHA256: j.OutcomeSHA256, CompletedAt: time.Now().UTC()}
	if j.ContainerID != "" {
		value := j.ContainerID
		out.ContainerID = &value
		out.Proof = "owned_container_removed"
	}
	out.AgentErrorCode = j.AgentErrorCode
	if e = out.ValidateFor(j.Command, j.NodeID, j.TaskID); e != nil {
		return wire.StopReceipt{}, e
	}
	j.Phase = "removed"
	j.StopReceipt = &out
	if e = m.Store.Save(*j); e != nil {
		return wire.StopReceipt{}, e
	}
	return out, nil
}

func IsUncertain(e error) bool {
	return e != nil && (errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) || e.Error() == "MODEL_PREPARATION_STOP_UNCERTAIN" || e.Error() == "MODEL_PREPARATION_CONTAINER_OWNERSHIP_CONFLICT")
}
