package report

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestQuantileIsWithinOneBucket(t *testing.T) {
	var h Histogram
	for i := 1; i <= 100; i++ {
		h.Add(time.Duration(i) * time.Millisecond)
	}
	for _, c := range []struct {
		q    float64
		want time.Duration
	}{{0.5, 50 * time.Millisecond}, {0.9, 90 * time.Millisecond}, {0.99, 99 * time.Millisecond}} {
		got := h.Quantile(c.q)
		if got < c.want || float64(got) > float64(c.want)*growth {
			t.Errorf("q%.2f: %s, want %s within one bucket", c.q, got, c.want)
		}
	}
	if h.Quantile(1) != 100*time.Millisecond {
		t.Fatalf("maximum %s", h.Quantile(1))
	}
	var empty Histogram
	if empty.Quantile(0.5) != 0 {
		t.Fatal("an empty histogram has a median")
	}
}

func TestQuantileOfAnOverflowIsTheMaximum(t *testing.T) {
	var h Histogram
	h.Add(time.Hour)
	if h.Quantile(0.5) != time.Hour {
		t.Fatalf("overflow %s", h.Quantile(0.5))
	}
}

func TestRecoveryIsFullOnlyWhenEveryHitAgentCameBack(t *testing.T) {
	r := New(4, time.Now())
	index := r.AddFault(Fault{Kind: "disconnect", Agents: 3, Measured: true})
	r.Recovered(index, time.Second)
	r.Recovered(index, 3*time.Second)
	if f := r.Snapshot().Faults[0]; f.FullRecovery != nil || *f.P50Recovery != 1 || *f.P99Recovery != 3 {
		t.Fatalf("partial recovery %+v", f)
	}
	r.Recovered(index, 2*time.Second)
	if f := r.Snapshot().Faults[0]; f.FullRecovery == nil || *f.FullRecovery != 3 || *f.P50Recovery != 2 {
		t.Fatalf("full recovery %+v", f)
	}
}

func TestRecorderCountsFromManyGoroutines(t *testing.T) {
	r := New(100, time.Now())
	var group sync.WaitGroup
	for range 100 {
		group.Go(func() {
			r.Succeeded(CallHeartbeat, time.Millisecond)
			r.Failed(CallHeartbeat, "RATE_LIMITED")
			r.Task(func(t *Tasks) { t.Succeeded++ })
		})
	}
	group.Wait()
	out := r.Finish(time.Now(), 90, 80, 1)
	if c := out.Calls[CallHeartbeat]; c.Succeeded != 100 || c.Failed["RATE_LIMITED"] != 100 || out.Tasks.Succeeded != 100 || out.Connected != 90 {
		t.Fatalf("report %+v", out)
	}
	raw, e := Encode(out)
	var decoded map[string]any
	if e != nil || json.Unmarshal(raw, &decoded) != nil || decoded["connected_at_end"] != float64(90) {
		t.Fatalf("encoded %s %v", raw, e)
	}
}
