package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
)

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
