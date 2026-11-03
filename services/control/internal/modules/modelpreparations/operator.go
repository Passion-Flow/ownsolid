package modelpreparations

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func ValidOperatorReason(reason string) bool { return text(reason, 500) }

// InitializeConfiguredStore requires the caller's installation lock and preserves every retained namespace.
func (s *Service) InitializeConfiguredStore(ctx context.Context, path string, uid, gid int, reason string) error {
	if !ValidOperatorReason(reason) {
		return fail("VALIDATION_FAILED")
	}
	if !s.Identity.Config.ModelPreparation.Enabled() || path == "" || path != s.Identity.Config.ModelPreparation.ArtifactRoot {
		return fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	tenant, required, e := s.storageRequired(ctx)
	if e != nil {
		return e
	}
	if required {
		return fail("MODEL_PREPARATION_STORE_RESTORE_REQUIRED")
	}
	if e = InitializeStore(path, tenant, uid, gid); e != nil {
		return e
	}
	return s.AuditInitializedStore(ctx, path, uid, gid, reason)
}

// AuditInitializedStore verifies the explicitly requested runtime owner without impersonating it.
// The caller owns the exclusive installation lock. Failed audits retain the selected private namespace.
func (s *Service) AuditInitializedStore(ctx context.Context, path string, uid, gid int, reason string) error {
	if !ValidOperatorReason(reason) {
		return fail("VALIDATION_FAILED")
	}
	tenant, required, e := s.storageRequired(ctx)
	if e != nil {
		return e
	}
	if required {
		return fail("MODEL_PREPARATION_STORE_RESTORE_REQUIRED")
	}
	id, e := VerifyInitializedStore(path, tenant, uid, gid)
	if e != nil {
		return e
	}
	metadata, _ := json.Marshal(map[string]any{"store_id": id, "accepted_artifacts": 0, "source": "protected_local_operator", "referenced_files_verified": true, "runtime_uid": uid, "runtime_gid": gid})
	return s.storeAudit(ctx, tenant, id, "initialize", reason, metadata)
}
func (s *Service) storeAudit(ctx context.Context, tenant, id, operation, reason string, metadata []byte) error {
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().TransactionSeconds))
	defer cancel()
	store, repositoryError := s.runtimeRepository()
	if repositoryError != nil {
		return repositoryError
	}
	e := store.Within(ctx, false, func(ctx context.Context, store transaction.Executor) error {
		_, e := store.Exec(ctx, preparationSelectedSQL(store, preparationDM(`INSERT INTO "AUDIT_EVENTS"("ID","OCCURRED_AT","TENANT_ID","AUDIENCE","ACTION","OUTCOME","TARGET_TYPE","TARGET_ID","REASON","REQUEST_ID","METADATA") VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,'operator',?,'success','model_preparation_store',?,?,?,CAST(? AS CLOB))`, 6), "INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES($1,clock_timestamp(),$2,'operator',$3,'success','model_preparation_store',$4,$5,$6,$7)", `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,'operator',?,'success','model_preparation_store',?,?,?,CAST(? AS JSON))`, uuid.NewString(), tenant, "model.preparation_store_"+operation+"_verified", id, reason, uuid.NewString(), metadata))
		return e
	})
	return preparationRepositoryError(e)
}
func (s *Service) AuditStoreOperation(ctx context.Context, operation, reason string) error {
	store, repositoryError := s.runtimeRepository()
	if repositoryError != nil {
		return repositoryError
	}
	if operation != "verify" && operation != "export" && operation != "import" || !ValidOperatorReason(reason) {
		return fail("VALIDATION_FAILED")
	}
	if s.Store == nil {
		return fail("MODEL_PREPARATION_STORAGE_REQUIRED")
	}
	if e := s.VerifyStored(ctx); e != nil {
		return e
	}
	var artifacts int64
	if e := preparationQueryRow(ctx, store, preparationSelectedSQL(store, preparationDM(`SELECT COUNT(*) FROM "MODEL_PREPARATION_ARTIFACTS" WHERE "TENANT_ID"=?`), "SELECT count(*) FROM model_preparation_artifacts WHERE tenant_id=$1", `SELECT count(*) FROM model_preparation_artifacts WHERE tenant_id=?`, s.Store.namespace.TenantID)).Scan(&artifacts); e != nil {
		return e
	}
	metadata, _ := json.Marshal(map[string]any{"store_id": s.Store.Namespace(), "accepted_artifacts": artifacts, "source": "protected_local_operator", "referenced_files_verified": true})
	return s.storeAudit(ctx, s.Store.namespace.TenantID, s.Store.Namespace(), operation, reason, metadata)
}

// Referenced preserves both base provenance and immutable approved derivations, including retired history.
func (s *Service) Referenced(ctx context.Context, q transaction.Executor, tenant, workspace, model string) (bool, error) {
	if s == nil || ctx == nil || q == nil {
		return false, fail("STORAGE_UNAVAILABLE")
	}
	if q.Dialect() == transaction.Dameng {
		store, e := s.runtimeRepository()
		if e != nil {
			return false, e
		}
		if e = preparationBorrowedExecutor(store, q); e != nil {
			return false, e
		}
	}
	statement := `SELECT EXISTS(SELECT 1 FROM model_preparations WHERE tenant_id=$1 AND workspace_id=$2 AND model_id=$3) OR EXISTS(SELECT 1 FROM model_derivations WHERE tenant_id=$1 AND workspace_id=$2 AND (model_id=$3 OR base_model_id=$3))`
	args := []any{tenant, workspace, model}
	if q.Dialect() == transaction.MySQL {
		statement = `SELECT EXISTS(SELECT 1 FROM model_preparations WHERE tenant_id=? AND workspace_id=? AND model_id=?) OR EXISTS(SELECT 1 FROM model_derivations WHERE tenant_id=? AND workspace_id=? AND (model_id=? OR base_model_id=?))`
		args = []any{tenant, workspace, model, tenant, workspace, model, model}
	}
	if q.Dialect() == transaction.Dameng {
		statement = `SELECT CASE WHEN EXISTS(SELECT 1 FROM "MODEL_PREPARATIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "MODEL_ID"=?) OR EXISTS(SELECT 1 FROM "MODEL_DERIVATIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND ("MODEL_ID"=? OR "BASE_MODEL_ID"=?)) THEN 1 ELSE 0 END FROM DUAL`
		args = []any{tenant, workspace, model, tenant, workspace, model, model}
	}
	var found bool
	e := preparationQueryRow(ctx, q, transaction.SQL(q.Dialect(), statement, args...)).Scan(&found)
	return found, e
}
