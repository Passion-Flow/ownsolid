package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/monitoringmetrics"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
)

// runtimeSampleNames maps the Go runtime values monitoringmetrics reads to series names.
var runtimeSampleNames = map[string]string{
	"/sched/goroutines:goroutines":        "ownsolid_go_goroutines",
	"/memory/classes/total:bytes":         "ownsolid_go_memory_total_bytes",
	"/memory/classes/heap/released:bytes": "ownsolid_go_memory_heap_released_bytes",
	"/memory/classes/heap/objects:bytes":  "ownsolid_go_memory_heap_objects_bytes",
	"/gc/cycles/total:gc-cycles":          "ownsolid_go_gc_cycles_total",
}

// runtimeSamples reports the values the Admin monitoring snapshot also reads, so both
// views agree. A value the runtime cannot represent is left out rather than guessed.
func runtimeSamples() []telemetrystore.Sample {
	values := monitoringmetrics.Runtime().Values
	samples := make([]telemetrystore.Sample, 0, len(values))
	for _, v := range values {
		if name, known := runtimeSampleNames[v.Name]; known && v.Available && v.Value != nil {
			samples = append(samples, telemetrystore.Sample{Name: name, Value: float64(*v.Value)})
		}
	}
	return samples
}

// databasePoolSamples reports connection pool saturation for the metrics store.
func databasePoolSamples(pool *pgxpool.Pool) telemetrystore.SampleSource {
	return func() []telemetrystore.Sample {
		s := pool.Stat()
		return []telemetrystore.Sample{
			{Name: "ownsolid_database_pool_acquired_connections", Value: float64(s.AcquiredConns())},
			{Name: "ownsolid_database_pool_idle_connections", Value: float64(s.IdleConns())},
			{Name: "ownsolid_database_pool_total_connections", Value: float64(s.TotalConns())},
			{Name: "ownsolid_database_pool_maximum_connections", Value: float64(s.MaxConns())},
			{Name: "ownsolid_database_pool_empty_acquires_total", Value: float64(s.EmptyAcquireCount())},
			{Name: "ownsolid_database_pool_canceled_acquires_total", Value: float64(s.CanceledAcquireCount())},
		}
	}
}
