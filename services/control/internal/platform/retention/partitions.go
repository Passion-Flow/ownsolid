package retention

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
)

// partitionMonthsAhead is how far ahead partitions exist. Control can stand still this long before
// a write finds no partition, and a pass runs every minute (ADR 0029).
const partitionMonthsAhead = 3

// partitionedTable is an append table this package keeps partitions for.
type partitionedTable struct {
	name string
	// keepDays is how long rows stay. Zero keeps every partition.
	keepDays func(config.RetentionPolicy) int
}

var partitionedTables = []partitionedTable{
	{"audit_events", func(p config.RetentionPolicy) int { return p.AuditDays }},
	{"training_events", func(p config.RetentionPolicy) int { return p.EventDays }},
	{"model_preparation_events", func(p config.RetentionPolicy) int { return p.EventDays }},
	{"node_task_history", func(p config.RetentionPolicy) int { return p.TaskHistoryDays }},
	{"node_task_progress", func(p config.RetentionPolicy) int { return p.TaskHistoryDays }},
}

// partitionReach is the last result of each table, for the metrics store.
var partitionReach sync.Map

// maintainPartitions creates partitions ahead of time and drops expired ones, each table in its
// own transaction so that a table waiting for a lock delays no other. It needs no bootstrap,
// because migrations leave partitions that an idle installation would outlive. A lock that could
// not be taken is not a failure: the next pass tries again.
func maintainPartitions(ctx context.Context, store transaction.Store, policy config.RetentionPolicy) error {
	if store.Dialect() != transaction.PostgreSQL {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(policy.TimeoutSeconds))
	defer cancel()
	for _, table := range partitionedTables {
		var created, dropped, blocked int
		var reaches time.Time
		e := store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			return tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT created,dropped,blocked,reaches FROM manage_time_partitions(current_schema()::text,$1,$2,$3)`, table.name, partitionMonthsAhead, table.keepDays(policy))).Scan(&created, &dropped, &blocked, &reaches)
		})
		if e != nil {
			return e
		}
		partitionReach.Store(table.name, reaches)
		if created > 0 || dropped > 0 || blocked > 0 {
			slog.Info("retention_partitions_changed", "table", table.name, "created", created, "dropped", dropped, "blocked", blocked, "reaches", reaches)
		}
	}
	return nil
}

// PartitionSamples reports how far ahead each table can take writes.
func PartitionSamples() []telemetrystore.Sample {
	out := []telemetrystore.Sample{}
	partitionReach.Range(func(name, reaches any) bool {
		out = append(out, telemetrystore.Sample{Name: "ownsolid_partition_reach_days", Value: time.Until(reaches.(time.Time)).Hours() / 24, Labels: []telemetrystore.Label{{Name: "table", Value: name.(string)}}})
		return true
	})
	return out
}
