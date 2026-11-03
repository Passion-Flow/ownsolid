package deployments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	trainwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// Native statements are owned here or at their concrete call site. This helper
// selects explicit SQL; it never translates another engine's statement. Scalar
// nullable text must reach the native driver as string/nil, not a Go pointer.
func deploymentNativeSQL(q transaction.Executor, native, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() != transaction.Dameng {
		return deploymentSQL(q, pg, my, args...)
	}
	bound := append([]any(nil), args...)
	for i, arg := range bound {
		if text, ok := arg.(*string); ok {
			bound[i] = nil
			if text != nil {
				bound[i] = *text
			}
		}
	}
	return transaction.SQL(transaction.Dameng, native, bound...)
}

// Native inserts must prove exactly one owned row; failures suppress even a
// driver's partial count, and a poisoned/uncertain transaction is never retried.
func deploymentExecOne(ctx context.Context, q transaction.Executor, statement transaction.Statement) (int64, error) {
	changed, e := q.Exec(ctx, statement)
	if e != nil {
		return 0, e
	}
	if q.Dialect() == transaction.Dameng && changed != 1 {
		return 0, fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	return changed, nil
}

func deploymentJSON(q transaction.Executor, raw []byte) any {
	if q.Dialect() == transaction.Dameng {
		return string(raw)
	}
	return raw
}

// Freshness's JSON revision component is validated in Go after scan: no native
// JSON number conversion may round an exact immutable deployment revision.
const nativeRuntimeFields = `D."ID",D."WORKSPACE_ID",D."NODE_ID",D."MODEL_ID",D."GPU_UUID",D."DESIRED_STATE",D."OBSERVED_STATE",D."REVISION",D."OBSERVATION",D."OBSERVED_AT",D."LAST_TASK_ID",T."STATE",T."ERROR_CODE",T."DEADLINE_AT",G."STATE",CASE WHEN N."STATUS"<>'active' OR N."LAST_HEARTBEAT_AT" IS NULL OR N."LAST_HEARTBEAT_AT"<TIMESTAMPADD(SECOND,-?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) OR D."OBSERVED_AT" IS NULL OR D."OBSERVED_AT"<TIMESTAMPADD(SECOND,-?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) THEN 1 ELSE 0 END,D."CREATED_AT",D."REMOVED_AT",D."MODEL_KIND",D."CATALOG_DIGEST",D."SERVING_RECIPE_SHA256"`
const nativeRuntimeJoins = ` FROM "DEPLOYMENTS" D JOIN "GPU_LEASES" G ON G."DEPLOYMENT_ID"=D."ID" AND G."TENANT_ID"=D."TENANT_ID" AND G."WORKSPACE_ID"=D."WORKSPACE_ID" AND G."NODE_ID"=D."NODE_ID" AND G."GPU_UUID"=D."GPU_UUID" AND G."REVISION"=D."REVISION" JOIN "MANAGED_NODES" N ON N."TENANT_ID"=D."TENANT_ID" AND N."WORKSPACE_ID"=D."WORKSPACE_ID" AND N."ID"=D."NODE_ID" LEFT JOIN "NODE_TASKS" T ON T."TENANT_ID"=D."TENANT_ID" AND T."WORKSPACE_ID"=D."WORKSPACE_ID" AND T."NODE_ID"=D."NODE_ID" AND T."ID"=D."LAST_TASK_ID"`

type nativeRuntimeValidatedRow struct{ transaction.Row }

func (row nativeRuntimeValidatedRow) Scan(dest ...any) error {
	if e := row.Row.Scan(dest...); e != nil {
		return e
	}
	raw := *dest[8].(*[]byte)
	if raw != nil {
		var observation wire.DeploymentObservation
		if trainwire.DecodeStrict(raw, &observation) != nil {
			return fail("STORAGE_UNAVAILABLE")
		}
	}
	if !gatewayNativeTime(*dest[16].(*time.Time)) {
		return fail("STORAGE_UNAVAILABLE")
	}
	for _, index := range []int{9, 13, 17} {
		stamp := *dest[index].(**time.Time)
		if stamp != nil && !gatewayNativeTime(*stamp) {
			return fail("STORAGE_UNAVAILABLE")
		}
	}
	return nil
}

func runtimeScan(q transaction.Executor, row transaction.Row) (Deployment, error) {
	if q.Dialect() == transaction.Dameng {
		row = nativeRuntimeValidatedRow{row}
	}
	d, e := scan(row)
	if e != nil || q.Dialect() != transaction.Dameng {
		return d, e
	}
	if !gatewayNativeTime(d.CreatedAt) {
		return Deployment{}, fail("STORAGE_UNAVAILABLE")
	}
	for _, stamp := range []*time.Time{d.ObservedAt, d.TaskDeadlineAt, d.RemovedAt} {
		if stamp != nil && !gatewayNativeTime(*stamp) {
			return Deployment{}, fail("STORAGE_UNAVAILABLE")
		}
	}
	if d.Observation == nil || d.Observation.Revision != d.Revision {
		d.Stale = true
	}
	return d, nil
}

// Lock deployment before its exact GPU owner. FOR UPDATE on the nullable outer
// task join is deliberately avoided. The caller retains node/Workspace locks.
func (s *Service) nativeDeploymentWithin(ctx context.Context, q transaction.Executor, tenant, workspace, deployment string, lock bool) (Deployment, error) {
	if lock {
		var node, lease string
		e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT "NODE_ID" FROM "DEPLOYMENTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=? FOR UPDATE`, tenant, workspace, deployment)).Scan(&node)
		if errors.Is(e, sql.ErrNoRows) {
			return Deployment{}, fail("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return Deployment{}, e
		}
		e = q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT "DEPLOYMENT_ID" FROM "GPU_LEASES" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "DEPLOYMENT_ID"=? FOR UPDATE`, tenant, workspace, node, deployment)).Scan(&lease)
		if errors.Is(e, sql.ErrNoRows) {
			return Deployment{}, fail("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return Deployment{}, e
		}
	}
	age := s.Nodes.Identity.Config.Policy().Nodes.OnlineSeconds
	return runtimeScan(q, q.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT "+nativeRuntimeFields+nativeRuntimeJoins+` WHERE D."TENANT_ID"=? AND D."WORKSPACE_ID"=? AND D."ID"=?`, age, age, tenant, workspace, deployment)))
}

func nativeRuntimeList(tenant, workspace, model string, retained bool, age, limit int) transaction.Statement {
	// Match the existing typed UI lookup and online-window contract. Only this
	// validated integer becomes structural native LIMIT text.
	if age < 20 || age > 300 || limit < 1 || limit > 100 {
		return transaction.SQL(transaction.Dameng, "")
	}
	text := "SELECT " + nativeRuntimeFields + nativeRuntimeJoins + ` WHERE D."TENANT_ID"=? AND D."WORKSPACE_ID"=?`
	args := []any{age, age, tenant, workspace}
	if model != "" {
		text += ` AND D."MODEL_ID"=?`
		args = append(args, model)
	}
	if retained {
		text += ` AND D."REMOVED_AT" IS NULL`
	}
	text += fmt.Sprintf(` ORDER BY D."CREATED_AT" DESC,D."ID" DESC LIMIT %d`, limit)
	return transaction.SQL(transaction.Dameng, text, args...)
}

func nativeRuntimeHardware(tenant, workspace, node string, age int, hardware bool) transaction.Statement {
	text := `SELECT "OBSERVATION","RECEIVED_AT",CASE WHEN "RECEIVED_AT">TIMESTAMPADD(SECOND,-?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) THEN 1 ELSE 0 END FROM "INFERENCE_NODES" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=?`
	if hardware {
		text = `SELECT "OBSERVATION",CASE WHEN "RECEIVED_AT">TIMESTAMPADD(SECOND,-?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) THEN 1 ELSE 0 END FROM "INFERENCE_NODES" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND EXISTS(SELECT 1 FROM "INSTALLATION_STATE" WHERE "ID"=1 AND "BOOTSTRAP_COMPLETE"=1 AND "TENANT_ID"=?)`
		return transaction.SQL(transaction.Dameng, text, age, tenant, workspace, node, tenant)
	}
	return transaction.SQL(transaction.Dameng, text, age, tenant, workspace, node)
}

// Node's current owner lock serializes updates and first insertion. A conflicting
// first insert aborts the transaction; no retry of an uncertain mutation is made.
func nativeRecordHardware(ctx context.Context, q transaction.Executor, n nodes.Node, raw []byte) error {
	changed, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE "INFERENCE_NODES" SET "OBSERVATION"=?,"RECEIVED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=?`, string(raw), n.TenantID, n.WorkspaceID, n.ID))
	if e != nil {
		return e
	}
	if changed == 1 {
		return nil
	}
	if changed != 0 {
		return fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	changed, e = q.Exec(ctx, transaction.SQL(transaction.Dameng, `INSERT INTO "INFERENCE_NODES"("TENANT_ID","WORKSPACE_ID","NODE_ID","OBSERVATION","RECEIVED_AT") VALUES(?,?,?,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)))`, n.TenantID, n.WorkspaceID, n.ID, string(raw)))
	if e == nil && changed != 1 {
		return fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	return e
}

func (s *Service) nativeDeploymentByTaskWithin(ctx context.Context, q transaction.Executor, t nodes.Task) (Deployment, error) {
	var raw []byte
	if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT "PAYLOAD" FROM "NODE_TASKS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "ID"=?`, t.TenantID, t.WorkspaceID, t.NodeID, t.ID)).Scan(&raw); e != nil {
		return Deployment{}, e
	}
	var command wire.InferenceCommand
	if trainwire.DecodeStrict(raw, &command) != nil || command.Validate(t.Operation) != nil || !id(command.DeploymentID) {
		return Deployment{}, fail("STORAGE_UNAVAILABLE")
	}
	d, e := s.deploymentWithin(ctx, q, t.TenantID, t.WorkspaceID, command.DeploymentID, false)
	if e != nil {
		return Deployment{}, e
	}
	if d.NodeID != t.NodeID || d.ModelID != command.ModelID || d.GPUUUID != command.GPUUUID || d.CatalogDigest != command.CatalogDigest || command.Revision > d.Revision {
		return Deployment{}, fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	return d, nil
}

func runtimeDecodeObservation(q transaction.Executor, raw []byte, out *wire.InferenceObservation) error {
	if q.Dialect() != transaction.Dameng {
		return decodeObservation(raw, out)
	}
	if trainwire.DecodeStrict(raw, out) != nil || out.Validate() != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return nil
}

// The selected native owner must attest its own exact in-flight executor. A
// dialect string alone cannot establish same-store provenance. Missing shared
// capability fails closed until the coordinator integrates its concrete proof.
func nativeRuntimeOwnsExecutor(store transaction.Store, q transaction.Executor) bool {
	if _, pool := q.(transaction.Store); pool {
		return false
	}
	owner, ok := store.(interface {
		OwnsDamengExecutor(transaction.Executor) bool
	})
	return ok && owner.OwnsDamengExecutor(q)
}
