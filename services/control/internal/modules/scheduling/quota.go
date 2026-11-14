package scheduling

import (
	"errors"
	"fmt"
	"maps"
)

var (
	// ErrInvalidQuotaTree indicates a missing, cyclic or wrongly scoped node.
	ErrInvalidQuotaTree = errors.New("SCHEDULING_INVALID_QUOTA_TREE")
	// ErrQuotaExceedsParent indicates that configured child allocations exceed a parent.
	ErrQuotaExceedsParent = errors.New("SCHEDULING_QUOTA_EXCEEDS_PARENT")
	// ErrQuotaScopeNotFound indicates no matching workspace in the tenant.
	ErrQuotaScopeNotFound = errors.New("SCHEDULING_QUOTA_SCOPE_NOT_FOUND")
)

// QuotaLevel identifies which organizational scope a quota node governs.
type QuotaLevel string

const (
	// PlatformQuota is the root of the quota tree.
	PlatformQuota QuotaLevel = "platform"
	// TenantQuota belongs directly to the platform.
	TenantQuota QuotaLevel = "tenant"
	// DepartmentQuota belongs to a tenant or another department of that tenant.
	DepartmentQuota QuotaLevel = "department"
	// WorkspaceQuota is the leaf at which requests are admitted.
	WorkspaceQuota QuotaLevel = "workspace"
)

// Quota contains the guaranteed allocation and inclusive hard burst ceiling.
type Quota struct {
	Guaranteed Resources
	Burst      Resources
}

// QuotaNode describes an allocation assigned to a platform, tenant,
// department or workspace. Tenant and workspace IDs match their owner IDs.
type QuotaNode struct {
	ID       string
	ParentID string
	TenantID string
	Level    QuotaLevel
	// Borrowing enables loans among the department's direct workspaces.
	Borrowing bool `json:"Borrowing,omitempty"`
	Limits    Quota
}

// QuotaTree is an immutable, validated allocation snapshot.
type QuotaTree struct {
	nodes map[string]QuotaNode
}

func (n QuotaNode) accepts(child QuotaNode) bool {
	switch n.Level {
	case PlatformQuota:
		return child.Level == TenantQuota && child.ID == child.TenantID
	case TenantQuota:
		return child.Level == DepartmentQuota && n.TenantID == child.TenantID
	case DepartmentQuota:
		return (child.Level == DepartmentQuota || child.Level == WorkspaceQuota) && n.TenantID == child.TenantID
	default:
		return false
	}
}

// BuildQuotaTree validates a complete proposed allocation. Each resource's
// immediate child guarantees and burst ceilings must independently fit their
// parent's respective limits. The caller owns persistence of the snapshot.
func BuildQuotaTree(nodes []QuotaNode) (*QuotaTree, error) {
	byID := make(map[string]QuotaNode, len(nodes))
	children := make(map[string][]QuotaNode)
	var root string
	for _, node := range nodes {
		if node.ID == "" || byID[node.ID].ID != "" {
			return nil, fmt.Errorf("%w: missing or repeated node identity", ErrInvalidQuotaTree)
		}
		if node.Level == PlatformQuota {
			if root != "" || node.ParentID != "" || node.TenantID != "" {
				return nil, fmt.Errorf("%w: expected one unscoped platform root", ErrInvalidQuotaTree)
			}
			root = node.ID
		} else if node.ParentID == "" || node.TenantID == "" {
			return nil, fmt.Errorf("%w: node %q needs a parent and tenant", ErrInvalidQuotaTree, node.ID)
		}
		if node.Borrowing && (node.Level != DepartmentQuota || !canonicalReservationID(node.TenantID)) {
			return nil, fmt.Errorf("%w: borrowing requires a department with a tenant ID", ErrInvalidQuotaTree)
		}
		guaranteed, err := normalizeResources(node.Limits.Guaranteed)
		if err != nil {
			return nil, fmt.Errorf("quota %q guarantee: %w", node.ID, err)
		}
		burst, err := normalizeResources(node.Limits.Burst)
		if err != nil {
			return nil, fmt.Errorf("quota %q burst: %w", node.ID, err)
		}
		if !guaranteed.fits(burst) {
			return nil, fmt.Errorf("%w: node %q guarantee exceeds its burst ceiling", ErrInvalidQuotaTree, node.ID)
		}
		node.Limits = Quota{Guaranteed: guaranteed, Burst: burst}
		byID[node.ID] = node
		children[node.ParentID] = append(children[node.ParentID], node)
	}
	if root == "" {
		return nil, fmt.Errorf("%w: platform root is missing", ErrInvalidQuotaTree)
	}

	reachable := []QuotaNode{byID[root]}
	for i := 0; i < len(reachable); i++ {
		parent := reachable[i]
		total := Quota{Guaranteed: make(Resources), Burst: make(Resources)}
		for _, child := range children[parent.ID] {
			if !parent.accepts(child) {
				return nil, fmt.Errorf("%w: node %q cannot be a child of %q", ErrInvalidQuotaTree, child.ID, parent.ID)
			}
			if parent.Borrowing && child.Level == WorkspaceQuota && !canonicalReservationID(child.ID) {
				return nil, fmt.Errorf("%w: borrowing requires an owner workspace ID", ErrInvalidQuotaTree)
			}
			if total.Guaranteed.addScaled(child.Limits.Guaranteed, 1) != nil || !total.Guaranteed.fits(parent.Limits.Guaranteed) {
				return nil, fmt.Errorf("%w: guarantees under %q exceed the parent allocation", ErrQuotaExceedsParent, parent.ID)
			}
			if total.Burst.addScaled(child.Limits.Burst, 1) != nil || !total.Burst.fits(parent.Limits.Burst) {
				return nil, fmt.Errorf("%w: burst ceilings under %q exceed the parent allocation", ErrQuotaExceedsParent, parent.ID)
			}
			reachable = append(reachable, child)
		}
	}
	if len(reachable) != len(nodes) {
		return nil, fmt.Errorf("%w: disconnected node or cycle", ErrInvalidQuotaTree)
	}
	return &QuotaTree{nodes: byID}, nil
}

// WorkspacePath returns independently owned limits from the workspace up to
// the platform. A workspace outside the requested tenant is treated as absent.
func (t *QuotaTree) WorkspacePath(tenantID, workspaceID string) ([]QuotaNode, error) {
	node, err := t.workspace(tenantID, workspaceID)
	if err != nil {
		return nil, err
	}
	var path []QuotaNode
	for node.ID != "" {
		node.Limits.Guaranteed = maps.Clone(node.Limits.Guaranteed)
		node.Limits.Burst = maps.Clone(node.Limits.Burst)
		path = append(path, node)
		node = t.nodes[node.ParentID]
	}
	return path, nil
}

func (t *QuotaTree) workspace(tenantID, workspaceID string) (QuotaNode, error) {
	node := t.nodes[workspaceID]
	if node.Level != WorkspaceQuota || node.TenantID != tenantID {
		return QuotaNode{}, ErrQuotaScopeNotFound
	}
	return node, nil
}
