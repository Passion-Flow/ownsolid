package deployments

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"time"
)

// Observation JSON is decoded/validated by scan. Its revision is compared in Go
// without native JSON numeric coercion, then the ordinary ready-target checks run.
func nativeGatewayDeployment(tenant, workspace, node, deployment string, age int) transaction.Statement {
	return transaction.SQL(transaction.Dameng, `SELECT D.ID,D.WORKSPACE_ID,D.NODE_ID,D.MODEL_ID,D.GPU_UUID,D.DESIRED_STATE,D.OBSERVED_STATE,D.REVISION,D.OBSERVATION,D.OBSERVED_AT,D.LAST_TASK_ID,T.STATE,T.ERROR_CODE,T.DEADLINE_AT,G.STATE,CASE WHEN N.STATUS<>'active' OR N.LAST_HEARTBEAT_AT IS NULL OR N.LAST_HEARTBEAT_AT<TIMESTAMPADD(SECOND,-?,CURRENT_TIMESTAMP(6)) OR D.OBSERVED_AT IS NULL OR D.OBSERVED_AT<TIMESTAMPADD(SECOND,-?,CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END,D.CREATED_AT,D.REMOVED_AT,D.MODEL_KIND,D.CATALOG_DIGEST,D.SERVING_RECIPE_SHA256 FROM DEPLOYMENTS D JOIN GPU_LEASES G ON G.DEPLOYMENT_ID=D.ID AND G.TENANT_ID=D.TENANT_ID AND G.WORKSPACE_ID=D.WORKSPACE_ID AND G.NODE_ID=D.NODE_ID AND G.GPU_UUID=D.GPU_UUID AND G.REVISION=D.REVISION JOIN MANAGED_NODES N ON N.TENANT_ID=D.TENANT_ID AND N.WORKSPACE_ID=D.WORKSPACE_ID AND N.ID=D.NODE_ID LEFT JOIN NODE_TASKS T ON T.TENANT_ID=D.TENANT_ID AND T.WORKSPACE_ID=D.WORKSPACE_ID AND T.NODE_ID=D.NODE_ID AND T.ID=D.LAST_TASK_ID WHERE D.TENANT_ID=? AND D.WORKSPACE_ID=? AND D.NODE_ID=? AND D.ID=?`, age, age, tenant, workspace, node, deployment)
}
func gatewayNativeSQL(q transaction.Executor, native, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() != transaction.Dameng {
		return gatewaySQL(q, pg, my, args...)
	}
	for _, arg := range args {
		if value, ok := arg.(time.Time); ok && !gatewayNativeTime(value) {
			return transaction.SQL(transaction.Dameng, "")
		}
	}
	return transaction.SQL(transaction.Dameng, native, args...)
}
func gatewayNativeTime(value time.Time) bool {
	return value.Location() == time.UTC && value.Year() >= 1970 && value.Year() <= 9999 && value.Nanosecond()%1000 == 0
}

// Lock exact owning rows before the projection. This preserves d -> g order
// without requiring native FOR UPDATE to lock a nullable outer-join task row.
func lockNativeGatewayIdentity(ctx context.Context, q transaction.Executor, tenant, workspace, node, deployment string) error {
	var locked string
	e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT ID FROM DEPLOYMENTS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND NODE_ID=? AND ID=? FOR UPDATE`, tenant, workspace, node, deployment)).Scan(&locked)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return e
	}
	e = q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT DEPLOYMENT_ID FROM GPU_LEASES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND NODE_ID=? AND DEPLOYMENT_ID=? FOR UPDATE`, tenant, workspace, node, deployment)).Scan(&locked)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("RESOURCE_NOT_FOUND")
	}
	return e
}
