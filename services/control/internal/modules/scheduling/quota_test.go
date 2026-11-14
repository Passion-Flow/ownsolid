package scheduling

import (
	"errors"
	"math"
	"slices"
	"testing"
)

func quotaFixture() []QuotaNode {
	limits := func(guarantee, burst int64) Quota {
		return Quota{
			Guaranteed: Resources{CPU: 8000, NVIDIAGPU: guarantee},
			Burst:      Resources{CPU: 16000, NVIDIAGPU: burst},
		}
	}
	return []QuotaNode{
		{ID: "workspace-a", ParentID: "department", TenantID: "tenant", Level: WorkspaceQuota,
			Limits: Quota{Guaranteed: Resources{CPU: 4000, NVIDIAGPU: 2}, Burst: Resources{CPU: 8000, NVIDIAGPU: 4}}},
		{ID: "platform", Level: PlatformQuota, Limits: limits(4, 8)},
		{ID: "department", ParentID: "tenant", TenantID: "tenant", Level: DepartmentQuota, Limits: limits(4, 8)},
		{ID: "tenant", ParentID: "platform", TenantID: "tenant", Level: TenantQuota, Limits: limits(4, 8)},
		{ID: "workspace-b", ParentID: "department", TenantID: "tenant", Level: WorkspaceQuota,
			Limits: Quota{Guaranteed: Resources{CPU: 4000, NVIDIAGPU: 2}, Burst: Resources{CPU: 8000, NVIDIAGPU: 4}}},
	}
}

func TestQuotaTreeExactFitAndTenantIsolation(t *testing.T) {
	nodes := quotaFixture()
	tree, err := BuildQuotaTree(nodes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.WorkspacePath("different-tenant", "workspace-a"); !errors.Is(err, ErrQuotaScopeNotFound) {
		t.Fatalf("foreign workspace lookup: %v", err)
	}
	path, err := tree.WorkspacePath("tenant", "workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, n := range path {
		ids = append(ids, n.ID)
	}
	if !slices.Equal(ids, []string{"workspace-a", "department", "tenant", "platform"}) {
		t.Fatalf("wrong path: %v", ids)
	}
	nodes[0].Limits.Burst[NVIDIAGPU] = 999
	path[0].Limits.Guaranteed[NVIDIAGPU] = 999
	again, err := tree.WorkspacePath("tenant", "workspace-a")
	if err != nil || again[0].Limits.Guaranteed[NVIDIAGPU] != 2 || again[0].Limits.Burst[NVIDIAGPU] != 4 {
		t.Fatalf("quota snapshot can be changed through an alias: %+v, %v", again, err)
	}
}

func TestQuotaTreeRejectsChildOverAllocation(t *testing.T) {
	tests := []struct {
		name       string
		node       int
		guaranteed bool
		resource   Resource
		value      int64
	}{
		{"workspace guarantees", 4, true, NVIDIAGPU, 3},
		{"workspace bursts", 4, false, NVIDIAGPU, 5},
		{"tenant above platform", 3, false, NVIDIAGPU, 9},
		{"department above tenant", 2, false, CPU, 16001},
		{"parent reduction", 2, true, CPU, 7999},
		{"missing parent dimension", 2, true, CPU, 0},
		{"overflowing sibling sum", 4, false, NVIDIAGPU, math.MaxInt64},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodes := quotaFixture()
			limits := nodes[tc.node].Limits
			amounts := limits.Burst
			if tc.guaranteed {
				amounts = limits.Guaranteed
			}
			amounts[tc.resource] = tc.value
			if tc.value == math.MaxInt64 {
				for _, i := range []int{1, 2, 3} {
					nodes[i].Limits.Burst[tc.resource] = math.MaxInt64
				}
			}
			if _, err := BuildQuotaTree(nodes); !errors.Is(err, ErrQuotaExceedsParent) {
				t.Fatalf("wanted parent allocation error, got %v", err)
			}
		})
	}
}

func TestQuotaTreeRejectsInvalidHierarchy(t *testing.T) {
	for _, parent := range []string{"tenant", "workspace-b", "missing"} {
		nodes := quotaFixture()
		nodes[0].ParentID = parent
		if _, err := BuildQuotaTree(nodes); !errors.Is(err, ErrInvalidQuotaTree) {
			t.Errorf("parent %s: %v", parent, err)
		}
	}
	nodes := quotaFixture()
	nodes[4].TenantID = "different-tenant"
	if _, err := BuildQuotaTree(nodes); !errors.Is(err, ErrInvalidQuotaTree) {
		t.Errorf("foreign workspace parent: %v", err)
	}
	nodes = quotaFixture()
	nodes[2].ParentID = "child"
	nodes = append(nodes, QuotaNode{ID: "child", ParentID: "department", TenantID: "tenant", Level: DepartmentQuota})
	if _, err := BuildQuotaTree(nodes); !errors.Is(err, ErrInvalidQuotaTree) {
		t.Errorf("department cycle: %v", err)
	}
}
