package monitoringmetrics

import (
	"runtime"
	"runtime/metrics"
	"time"
)

type RuntimeValue struct {
	Name      string  `json:"name"`
	Unit      string  `json:"unit"`
	Available bool    `json:"available"`
	Value     *uint64 `json:"value"`
}
type RuntimeSnapshot struct {
	ObservedAt time.Time      `json:"observed_at"`
	GOOS       string         `json:"goos"`
	GOARCH     string         `json:"goarch"`
	GoVersion  string         `json:"go_version"`
	Values     []RuntimeValue `json:"values"`
}

// Runtime reads only fixed standard metrics. These are Go process accounting,
// never host RSS, hardware free memory, GPU capacity or target qualification.
func Runtime() RuntimeSnapshot {
	names := []string{"/sched/goroutines:goroutines", "/memory/classes/total:bytes", "/memory/classes/heap/released:bytes", "/memory/classes/heap/objects:bytes", "/gc/cycles/total:gc-cycles"}
	samples := make([]metrics.Sample, len(names))
	for i, n := range names {
		samples[i].Name = n
	}
	metrics.Read(samples)
	out := RuntimeSnapshot{time.Now().UTC(), runtime.GOOS, runtime.GOARCH, runtime.Version(), make([]RuntimeValue, 0, len(names))}
	units := []string{"goroutines", "bytes", "bytes", "bytes", "gc-cycles"}
	for i, s := range samples {
		v := RuntimeValue{Name: s.Name, Unit: units[i]}
		if s.Value.Kind() == metrics.KindUint64 {
			n := s.Value.Uint64()
			if n <= MaximumExactCounter {
				v.Available = true
				v.Value = &n
			}
		}
		out.Values = append(out.Values, v)
	}
	return out
}
