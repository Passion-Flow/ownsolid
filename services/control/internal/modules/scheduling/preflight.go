package scheduling

import (
	"fmt"
	"slices"
)

// LimitReason distinguishes policy changes from waiting for a release.
type LimitReason string

const (
	// QuotaTooSmall means the full demand exceeds a configured quota.
	QuotaTooSmall LimitReason = "SCHEDULING_QUOTA_TOO_SMALL"
	// QuotaInUse means earlier reservations use quota needed by this job.
	QuotaInUse LimitReason = "SCHEDULING_QUOTA_IN_USE"
	// CellTooSmall means the job exceeds the cell's total known capacity.
	CellTooSmall LimitReason = "SCHEDULING_CELL_TOO_SMALL"
	// CellInUse means earlier reservations use cell capacity needed by this job.
	CellInUse LimitReason = "SCHEDULING_CELL_IN_USE"
)

// Limit describes a resource shortfall with a stable machine-readable reason.
type Limit struct {
	Reason    LimitReason
	Level     QuotaLevel
	Resource  Resource
	Requested int64
	Available int64
}

// CellCapacity is the total and already-reserved capacity of a cell.
type CellCapacity struct {
	Total    Resources
	Reserved Resources
}

// PreflightResult estimates whole-job demand and describes every resource
// shortfall. An empty Limits list indicates a quota fit in this snapshot.
type PreflightResult struct {
	Demand Resources
	Limits []Limit
}

// WaitReasons projects this estimate into actionable guidance. A fit has no
// waiting reason. Shared ancestor and cell quantities stay in the estimate.
func (r PreflightResult) WaitReasons() []WaitReason {
	return explainLimits(r.Limits)
}

// Preflight evaluates a workspace's whole demand without mutating allocations.
// Callers must supply aggregate reserved usage for every node in the path,
// using an empty vector for an unused node. A fit is not a reservation and does
// not guarantee that the cell can place the requested replica groups.
func (t *QuotaTree) Preflight(tenantID, workspaceID string, intent JobIntent, reserved map[string]Resources, cell CellCapacity) (PreflightResult, error) {
	path, err := t.WorkspacePath(tenantID, workspaceID)
	if err != nil {
		return PreflightResult{}, err
	}
	_, demand, err := intent.Normalize()
	if err != nil {
		return PreflightResult{}, err
	}
	resources := make([]Resource, 0, len(demand))
	for resource := range demand {
		resources = append(resources, resource)
	}
	slices.Sort(resources)
	out := PreflightResult{Demand: demand}
	for _, node := range path {
		used, ok := reserved[node.ID]
		if !ok {
			return PreflightResult{}, fmt.Errorf("%w: reservation snapshot is incomplete", ErrInvalidResources)
		}
		if _, err := normalizeResources(used); err != nil {
			return PreflightResult{}, err
		}
		out.check(resources, node.Limits.Burst, used, node.Level, QuotaTooSmall, QuotaInUse)
	}
	if _, err := normalizeResources(cell.Total); err != nil {
		return PreflightResult{}, err
	}
	if _, err := normalizeResources(cell.Reserved); err != nil {
		return PreflightResult{}, err
	}
	out.check(resources, cell.Total, cell.Reserved, "", CellTooSmall, CellInUse)
	return out, nil
}

func (r *PreflightResult) check(resources []Resource, total, used Resources, level QuotaLevel, tooSmall, inUse LimitReason) {
	for _, resource := range resources {
		if limit, blocked := capacityLimit(resource, r.Demand[resource], total[resource], used[resource], level, tooSmall, inUse); blocked {
			r.Limits = append(r.Limits, limit)
		}
	}
}

func capacityLimit(resource Resource, requested, total, used int64, level QuotaLevel, tooSmall, inUse LimitReason) (Limit, bool) {
	if requested > total {
		return Limit{tooSmall, level, resource, requested, total}, true
	}
	if available := max(0, total-used); requested > available {
		return Limit{inUse, level, resource, requested, available}, true
	}
	return Limit{}, false
}
