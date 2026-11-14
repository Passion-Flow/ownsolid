package recovery

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/privateclusters"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func fenceRestoredPrivateClustersWithin(ctx context.Context, q transaction.Executor, accounts *identity.Service) error {
	query := "SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('private_cluster_connections','private_cluster_runs','private_cluster_intents')"
	if q.Dialect() == transaction.MySQL {
		query = "SELECT count(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('private_cluster_connections','private_cluster_runs','private_cluster_intents')"
	}
	var tables int
	if e := q.QueryRow(ctx, transaction.SQL(q.Dialect(), query)).Scan(&tables); e != nil {
		return e
	}
	// MySQL has no active M28 catalog yet. An explicitly disabled, absent
	// variant is allowed; a partial or enabled schema always fails closed.
	if tables == 0 && accounts.Config.PrivateClusterConfigFile == "" && q.Dialect() == transaction.MySQL {
		return nil
	}
	if tables != 3 {
		return errors.New("PRIVATE_CLUSTER_STORED_INVALID")
	}
	return privateclusters.InvalidateAuthoritiesWithin(ctx, q)
}
