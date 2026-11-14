package scheduling

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
)

func TestJobIntentNormalize(t *testing.T) {
	for _, kind := range []Kind{Training, Inference, Notebook, Batch} {
		t.Run(string(kind), func(t *testing.T) {
			intent := JobIntent{Version: IntentVersion, Kind: kind, SourceID: "source-1", Groups: []ReplicaGroup{
				{Name: "worker", Replicas: 4, Requests: Resources{CPU: 2000, NVIDIAGPU: 2}},
				{Name: "driver", Replicas: 1, Requests: Resources{CPU: 1000, Memory: 1 << 30}},
			}}
			got, demand, err := intent.Normalize()
			if err != nil {
				t.Fatal(err)
			}
			want := Resources{CPU: 9000, Memory: 1 << 30, NVIDIAGPU: 8}
			if got.Groups[0].Name != "driver" || !reflect.DeepEqual(demand, want) {
				t.Fatalf("unexpected normalized demand: %+v, %v", got, demand)
			}
			slices.Reverse(intent.Groups)
			again, _, err := intent.Normalize()
			if err != nil || !reflect.DeepEqual(got, again) {
				t.Fatalf("permutation changed intent: %+v, %v", again, err)
			}
			got.Groups[0].Requests[CPU] = 0
			if intent.Groups[0].Requests[CPU] != 1000 {
				t.Fatal("normalized resources alias input")
			}
		})
	}
}

func TestJobIntentRejectsInvalidDemand(t *testing.T) {
	cases := []struct {
		name   string
		groups []ReplicaGroup
		want   error
	}{
		{"missing group", nil, ErrInvalidJob},
		{"duplicate group", []ReplicaGroup{{"x", 1, Resources{CPU: 1}}, {"x", 1, Resources{CPU: 1}}}, ErrInvalidJob},
		{"zero replicas", []ReplicaGroup{{"x", 0, Resources{CPU: 1}}}, ErrInvalidJob},
		{"invalid quantity", []ReplicaGroup{{"x", 1, Resources{CPU: -1}}}, ErrInvalidResources},
		{"zero demand", []ReplicaGroup{{"x", 1, Resources{CPU: 0}}}, ErrInvalidResources},
		{"invalid resource", []ReplicaGroup{{"x", 1, Resources{"invalid/gpu": 1}}}, ErrInvalidResources},
		{"replica overflow", []ReplicaGroup{{"x", 2, Resources{CPU: math.MaxInt64}}}, ErrResourceOverflow},
		{"group overflow", []ReplicaGroup{{"x", 1, Resources{CPU: math.MaxInt64}}, {"y", 1, Resources{CPU: 1}}}, ErrResourceOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := (JobIntent{Version: IntentVersion, Kind: Batch, SourceID: "source-1", Groups: tc.groups}).Normalize()
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestJobTransitionRequiresRuntimeRemoval(t *testing.T) {
	for _, edge := range [][2]JobState{{Queued, Reserved}, {Reserved, Running}, {Running, Stopping}, {Stopping, Cancelled}, {Reserved, Succeeded}} {
		if err := edge[0].CheckTransition(edge[1]); err != nil {
			t.Errorf("%v: %v", edge, err)
		}
	}
	for _, edge := range [][2]JobState{{Queued, Running}, {Reserved, Cancelled}, {Running, Queued}, {Succeeded, Running}, {Stopping, Reserved}} {
		if err := edge[0].CheckTransition(edge[1]); !errors.Is(err, ErrJobStateConflict) {
			t.Errorf("%v: %v", edge, err)
		}
	}
}
