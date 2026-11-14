package deployments

import (
	"reflect"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Only validated integers become statement text; anything else leaves an empty statement,
// which every executor refuses.
func TestPlacementStatementRefusesUnboundedText(t *testing.T) {
	empty := func(d transaction.Dialect) transaction.Statement { return transaction.SQL(d, "") }
	for _, d := range []transaction.Dialect{transaction.PostgreSQL, transaction.MySQL, transaction.Dameng} {
		for _, bounds := range [][2]int{{0, 10}, {30, 0}, {30, 101}} {
			if got := placementStatement(d, "t", "w", bounds[0], bounds[1]); !reflect.DeepEqual(got, empty(d)) {
				t.Fatalf("%s accepted bounds %v", d, bounds)
			}
		}
		if got := placementStatement(d, "t", "w", 30, 100); reflect.DeepEqual(got, empty(d)) {
			t.Fatalf("%s refused valid bounds", d)
		}
	}
	if got := placementStatement("sqlite", "t", "w", 30, 100); !reflect.DeepEqual(got, empty("sqlite")) {
		t.Fatal("an unknown dialect got a statement")
	}
}

func TestRecommendPrefersFreeMemoryThenTheNewestObservation(t *testing.T) {
	refused := "INFERENCE_GPU_UNSUPPORTED"
	busy := "INFERENCE_RESOURCE_BUSY"
	candidates := []PlacementNode{
		{NodeID: "newest", GPUs: []PlacementGPU{{UUID: "a", MemoryFreeMiB: 8000}, {UUID: "b", MemoryFreeMiB: 90000, Refusal: &refused}}},
		{NodeID: "busy", Refusal: &busy, GPUs: []PlacementGPU{{UUID: "c", MemoryFreeMiB: 90000}}},
		{NodeID: "older", GPUs: []PlacementGPU{{UUID: "d", MemoryFreeMiB: 8000}, {UUID: "e", MemoryFreeMiB: 12000}}},
		{NodeID: "oldest", GPUs: []PlacementGPU{{UUID: "f", MemoryFreeMiB: 12000}}},
	}
	if got := recommend(candidates); got == nil || *got != (PlacementChoice{NodeID: "older", GPUUUID: "e"}) {
		t.Fatalf("recommended %+v", got)
	}
	candidates[2].GPUs = nil
	candidates[3].GPUs = nil
	if got := recommend(candidates); got == nil || *got != (PlacementChoice{NodeID: "newest", GPUUUID: "a"}) {
		t.Fatalf("recommended %+v", got)
	}
	if got := recommend(candidates[1:2]); got != nil {
		t.Fatalf("a refused node was recommended: %+v", got)
	}
}
