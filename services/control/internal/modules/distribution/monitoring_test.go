package distribution

import "testing"

func TestMonitoringNearestRankUsesActualBoundedSample(t *testing.T) {
	if sampleQuantile(nil, 95) != nil {
		t.Fatal("empty fabricated quantile")
	}
	values := make([]uint64, 100)
	for i := range values {
		values[i] = uint64(i + 1)
	}
	for p, want := range map[int]uint64{50: 50, 95: 95, 99: 99} {
		if got := sampleQuantile(values, p); got == nil || *got != want {
			t.Fatal(p, got, want)
		}
	}
	if v := sampleQuantile([]uint64{17}, 95); v == nil || *v != 17 {
		t.Fatal(v)
	}
}
