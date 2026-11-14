package main

import (
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/monitoringmetrics"
)

// A renamed runtime value would silently stop reaching the metrics store.
func TestRuntimeSamplesCoverEveryValueMonitoringReads(t *testing.T) {
	values := monitoringmetrics.Runtime().Values
	for _, v := range values {
		if _, known := runtimeSampleNames[v.Name]; !known {
			t.Errorf("runtime value %s has no series name", v.Name)
		}
	}
	samples := runtimeSamples()
	if len(samples) != len(values) || len(samples) != len(runtimeSampleNames) {
		t.Fatalf("%d samples from %d values and %d names", len(samples), len(values), len(runtimeSampleNames))
	}
	for _, s := range samples {
		if s.Name == "ownsolid_go_goroutines" && s.Value < 1 {
			t.Fatalf("%+v", s)
		}
	}
}
