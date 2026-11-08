package training

import (
	"context"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/scheduling"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func schedulingAttempt(w workspaces.Workspace, j Job, command wire.Command) scheduling.Attempt {
	return scheduling.Attempt{
		TenantID: w.TenantID, WorkspaceID: w.ID, Kind: scheduling.Training, SourceID: j.ID,
		ID: command.AttemptID, Number: command.AttemptNumber,
	}
}

// reserveSchedulingWithin returns a wait code only for a recoverable quota decision.
func (s *Service) reserveSchedulingWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, j Job, command wire.Command) (string, error) {
	if !s.schedulingReservations {
		return "", nil
	}
	attempt := schedulingAttempt(w, j, command)
	intent := scheduling.JobIntent{
		Version: scheduling.IntentVersion, Kind: attempt.Kind, SourceID: attempt.SourceID,
		Groups: []scheduling.ReplicaGroup{{
			Name: "worker", Replicas: 1,
			Requests: scheduling.Resources{
				scheduling.NVIDIAGPU: 1,
				scheduling.Memory:    command.Recipe.Resources.MemoryBytes,
			},
		}},
	}
	created, err := scheduling.ReserveWithin(ctx, q, attempt, intent)
	switch {
	case errors.Is(err, scheduling.ErrReservationQuota):
		return "TRAINING_SCHEDULING_QUOTA_EXCEEDED", nil
	case errors.Is(err, scheduling.ErrQuotaUnconfigured):
		return "TRAINING_SCHEDULING_QUOTA_UNCONFIGURED", nil
	case errors.Is(err, scheduling.ErrQuotaScopeNotFound):
		return "TRAINING_SCHEDULING_QUOTA_SCOPE_MISSING", nil
	case errors.Is(err, scheduling.ErrReservationConflict), errors.Is(err, scheduling.ErrReservationReleased), err == nil && !created:
		// A queued job cannot own an already held or released attempt.
		return "", fail("TRAINING_ATTEMPT_STALE")
	default:
		return "", err
	}
}

func (s *Service) releaseSchedulingWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, j Job, command wire.Command) error {
	if !s.schedulingReservations {
		return nil
	}
	released, err := scheduling.ReleaseWithin(ctx, q, schedulingAttempt(w, j, command))
	if errors.Is(err, scheduling.ErrReservationNotFound) || err == nil && !released {
		// Owner-level replays return before this point. A new stop must release a live hold.
		return fail("TRAINING_SCHEDULING_RESERVATION_INVALID")
	}
	return err
}
