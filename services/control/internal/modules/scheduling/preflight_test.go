package scheduling

import (
	"errors"
	"reflect"
	"testing"
)

func TestPreflightExplainsWholeJobShortfalls(t *testing.T) {
	tree, err := BuildQuotaTree(quotaFixture())
	if err != nil {
		t.Fatal(err)
	}
	intent := JobIntent{Version: IntentVersion, Kind: Training, SourceID: "distributed-job", Groups: []ReplicaGroup{
		{Name: "driver", Replicas: 1, Requests: Resources{CPU: 1000, NVIDIAGPU: 1}},
		{Name: "worker", Replicas: 2, Requests: Resources{CPU: 1000, NVIDIAGPU: 1}},
	}}
	reserved := map[string]Resources{
		"platform": {NVIDIAGPU: 6}, "tenant": {NVIDIAGPU: 6}, "department": {NVIDIAGPU: 6},
		"workspace-a": {NVIDIAGPU: 2},
	}
	cell := CellCapacity{Total: Resources{NVIDIAGPU: 2, CPU: 4000}, Reserved: Resources{CPU: 2000}}
	got, err := tree.Preflight("tenant", "workspace-a", intent, reserved, cell)
	if err != nil {
		t.Fatal(err)
	}
	want := []Limit{
		{QuotaInUse, WorkspaceQuota, NVIDIAGPU, 3, 2},
		{QuotaInUse, DepartmentQuota, NVIDIAGPU, 3, 2},
		{QuotaInUse, TenantQuota, NVIDIAGPU, 3, 2},
		{QuotaInUse, PlatformQuota, NVIDIAGPU, 3, 2},
		{CellInUse, "", CPU, 3000, 2000},
		{CellTooSmall, "", NVIDIAGPU, 3, 2},
	}
	if !reflect.DeepEqual(got.Limits, want) || got.Demand[NVIDIAGPU] != 3 {
		t.Fatalf("preflight = %+v, want limits %v", got, want)
	}
	if reserved["workspace-a"][NVIDIAGPU] != 2 || cell.Reserved[CPU] != 2000 {
		t.Fatal("preflight mutated the reservation snapshot")
	}
	for id := range reserved {
		reserved[id] = nil
	}
	cell = CellCapacity{Total: Resources{CPU: 3000, NVIDIAGPU: 3}}
	got, err = tree.Preflight("tenant", "workspace-a", intent, reserved, cell)
	if err != nil || len(got.Limits) != 0 {
		t.Fatalf("exact fit: %+v, %v", got, err)
	}
	delete(reserved, "workspace-a")
	if _, err := tree.Preflight("tenant", "workspace-a", intent, reserved, cell); !errors.Is(err, ErrInvalidResources) {
		t.Fatalf("missing usage snapshot: %v", err)
	}
	if _, err := tree.Preflight("foreign", "workspace-a", intent, reserved, cell); !errors.Is(err, ErrQuotaScopeNotFound) {
		t.Fatalf("foreign preflight: %v", err)
	}
}
