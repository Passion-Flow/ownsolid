package recovery

import (
	"context"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/artifactstores"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Fence only actual installed owner tables. A partial schema is never skipped.
// This borrows the restore transaction and never starts or commits another one.
func fenceRestoredArtifactsWithin(ctx context.Context, q transaction.Executor, c config.Config) error {
	var tables int
	pg := `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('artifact_objects','artifact_object_parts','artifact_object_grants','artifact_object_ingestions')`
	my := `SELECT count(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('artifact_objects','artifact_object_parts','artifact_object_grants','artifact_object_ingestions')`
	query := pg
	if q.Dialect() == transaction.MySQL {
		query = my
	}
	if e := q.QueryRow(ctx, transaction.SQL(q.Dialect(), query)).Scan(&tables); e != nil {
		return e
	}
	if tables == 0 && (c.ObjectStorage == nil || !c.ObjectStorage.Enabled) {
		return nil
	}
	if tables != 4 {
		return errors.New("OBJECT_SCHEMA_NOT_ACTIVE")
	}
	var tenant string
	if e := q.QueryRow(ctx, transaction.SQL(q.Dialect(), `SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete`)).Scan(&tenant); e != nil {
		return e
	}
	return artifactstores.FenceAfterRestoreWithin(ctx, q, tenant)
}
