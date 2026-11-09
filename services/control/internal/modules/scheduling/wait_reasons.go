package scheduling

import (
	"context"
	"errors"
	"fmt"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/scheduling/celladapter"
)

// WaitAction describes how the caller can resolve or recheck a blocked decision.
type WaitAction string

const (
	// ChangeRequestOrQuota applies when the request exceeds its quota ceiling.
	ChangeRequestOrQuota WaitAction = "change_request_or_quota"
	// ChangeRequestOrCell applies when the cell cannot meet the whole request.
	ChangeRequestOrCell WaitAction = "change_request_or_cell"
	// WaitForCapacity applies while held work occupies capacity.
	WaitForCapacity WaitAction = "wait_for_capacity"
	// ReviewBorrowableQuota asks the owner to recheck lendable guarantees or reduce the request.
	ReviewBorrowableQuota WaitAction = "review_borrowable_quota"
	// RequestQuotaReclaim asks the workload owner to initiate an eligible reclaim.
	RequestQuotaReclaim WaitAction = "request_quota_reclaim"
	// WaitForReclaimRelease applies while the exact claim lacks release evidence.
	WaitForReclaimRelease WaitAction = "wait_for_reclaim_release"
	// WaitForWorkspaceClaim applies while another attempt holds the workspace's claim.
	WaitForWorkspaceClaim WaitAction = "wait_for_workspace_claim"
	// VerifyWorkspaceAccess asks the caller to check authorization and workspace selection.
	VerifyWorkspaceAccess WaitAction = "verify_workspace_access"
	// WaitForQueueTurn applies when the dispatcher defers the entry by its current rank.
	WaitForQueueTurn WaitAction = "wait_for_queue_turn"
	// WaitForCellQuota applies when the cell scheduler is waiting for unused quota.
	WaitForCellQuota WaitAction = "wait_for_cell_quota"
	// ReviewCellTopology asks the operator to review the current placement constraints.
	ReviewCellTopology WaitAction = "review_cell_topology"
	// ReviewCellQueue asks the operator to verify the cell queue configuration.
	ReviewCellQueue WaitAction = "review_cell_queue"
	// WaitForCellPods applies when an admitted workload's pods are not ready.
	WaitForCellPods WaitAction = "wait_for_cell_pods"
)

// WaitReason is guidance from one decision. Available is present only for a
// subject-workspace limit. No reason contains another job or workspace identity.
type WaitReason struct {
	Code      string
	Action    WaitAction
	Level     QuotaLevel
	Resource  Resource
	Requested int64
	Available *int64
}

type reservationQuotaError struct {
	limits []Limit
}

func (e *reservationQuotaError) Error() string {
	first := e.limits[0]
	if first.Reason == QuotaTooSmall {
		return fmt.Sprintf("%s: %s requires %d, quota ceiling is %d", ErrReservationQuota, first.Resource, first.Requested, first.Available)
	}
	return fmt.Sprintf("%s: %s requires %d, %d available", ErrReservationQuota, first.Resource, first.Requested, first.Available)
}

func (e *reservationQuotaError) Unwrap() error { return ErrReservationQuota }

var errReclaimReleasePending = fmt.Errorf("%w: waiting for selected releases", ErrReclaimPending)

// WaitReasons projects known scheduling errors from an already authorized
// operation. A nil or unrelated error has no scheduling explanation. The
// caller must project a fresh decision after changes to quota, holds or claims.
func WaitReasons(err error) []WaitReason {
	var quota *reservationQuotaError
	switch {
	case errors.Is(err, ErrQuotaScopeNotFound):
		return []WaitReason{{Code: ErrQuotaScopeNotFound.Error(), Action: VerifyWorkspaceAccess}}
	case errors.As(err, &quota):
		return explainLimits(quota.limits)
	case errors.Is(err, ErrReclaimRequired):
		return []WaitReason{{Code: ErrReclaimRequired.Error(), Action: RequestQuotaReclaim}}
	case errors.Is(err, errReclaimReleasePending):
		return []WaitReason{{Code: "SCHEDULING_RECLAIM_RELEASE_PENDING", Action: WaitForReclaimRelease}}
	case errors.Is(err, ErrReclaimPending):
		return []WaitReason{{Code: ErrReclaimPending.Error(), Action: WaitForWorkspaceClaim}}
	case errors.Is(err, ErrBorrowingUnavailable):
		return []WaitReason{{Code: ErrBorrowingUnavailable.Error(), Action: ReviewBorrowableQuota}}
	default:
		return nil
	}
}

// ObserveCellWaitReasons reads fresh guidance for an owner-authorized workload.
// The caller supplies the reference from the durable attempt binding and the
// client for that bound cell. Failed identity checks return no guidance.
func ObserveCellWaitReasons(ctx context.Context, client *celladapter.Client, reference celladapter.Reference) ([]WaitReason, error) {
	if client == nil {
		return nil, celladapter.ErrConfiguration
	}
	observation, err := client.Observe(ctx, reference)
	if err != nil {
		return nil, err
	}
	return cellWaitReasons(observation.Blocker), nil
}

func cellWaitReasons(blocker celladapter.Blocker) []WaitReason {
	var action WaitAction
	switch blocker {
	case celladapter.WaitingForQuota:
		action = WaitForCellQuota
	case celladapter.ExceedsMaxQuota:
		action = ChangeRequestOrQuota
	case celladapter.TopologyPlacementFailed:
		action = ReviewCellTopology
	case celladapter.QueueInadmissible:
		action = ReviewCellQueue
	case celladapter.PodsNotReady:
		action = WaitForCellPods
	default:
		return nil
	}
	return []WaitReason{{Code: string(blocker), Action: action}}
}

func explainLimits(limits []Limit) []WaitReason {
	var reasons []WaitReason
	for _, limit := range limits {
		reason := WaitReason{Code: string(limit.Reason), Level: limit.Level, Resource: limit.Resource, Requested: limit.Requested}
		switch limit.Reason {
		case QuotaTooSmall:
			reason.Action = ChangeRequestOrQuota
		case CellTooSmall:
			reason.Action = ChangeRequestOrCell
		case QuotaInUse, CellInUse:
			reason.Action = WaitForCapacity
		default:
			continue
		}
		if limit.Level == WorkspaceQuota {
			available := limit.Available
			reason.Available = &available
		}
		reasons = append(reasons, reason)
	}
	return reasons
}
