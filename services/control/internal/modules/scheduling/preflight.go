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
		request := r.Demand[resource]
		free := max(0, total[resource]-used[resource])
		switch {
		case request > total[resource]:
			r.Limits = append(r.Limits, Limit{tooSmall, level, resource, request, total[resource]})
		case request > free:
			r.Limits = append(r.Limits, Limit{inUse, level, resource, request, free})
		}
	}
}
