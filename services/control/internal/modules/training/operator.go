package training

import (
	"context"
	"encoding/json"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"github.com/google/uuid"
)

func ValidOperatorReason(reason string) bool { return validReason(reason) }

// AuditStoreOperation requires the caller's exclusive installation/process lock and open private store.
// Audit failure fails the operator command while retaining physical files for explicit verification/recovery.
func (s *Service) AuditStoreOperation(ctx context.Context, operation, reason string) error {
	if operation != "initialize" && operation != "verify" && operation != "export" && operation != "import" || !validReason(reason) {
		return fail("VALIDATION_FAILED")
	}
	if s.Store == nil {
		return fail("TRAINING_STORAGE_REQUIRED")
	}
	if e := s.VerifyStored(ctx); e != nil {
		return e
	}
	return s.trainingStorageTransaction(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var checkpoints, artifacts int64
		var tenant string
		if e := q.QueryRow(ctx, acceptedAdapterSQL(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete FOR SHARE", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1 FOR SHARE")).Scan(&tenant); e != nil {
			return fail("TRAINING_INSTALLATION_REQUIRED")
		}
		if tenant != s.Store.namespace.TenantID {
			return fail("TRAINING_STORE_NAMESPACE_INVALID")
		}
		if e := q.QueryRow(ctx, transferStatement(q, "SELECT (SELECT count(*) FROM training_checkpoints WHERE tenant_id=$1),(SELECT count(*) FROM training_artifacts WHERE tenant_id=$1)", "SELECT (SELECT count(*) FROM training_checkpoints WHERE tenant_id=?),(SELECT count(*) FROM training_artifacts WHERE tenant_id=?)", []any{tenant}, []any{tenant, tenant})).Scan(&checkpoints, &artifacts); e != nil {
			return e
		}
		metadata, _ := json.Marshal(struct {
			StoreID                 string `json:"store_id"`
			Checkpoints             int64  `json:"accepted_checkpoints"`
			Artifacts               int64  `json:"accepted_artifacts"`
			Source                  string `json:"source"`
			ReferencedFilesVerified bool   `json:"referenced_files_verified"`
		}{s.Store.Namespace(), checkpoints, artifacts, "protected_local_operator", true})
		n, e := q.Exec(ctx, acceptedAdapterSQL(q, "INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES($1,clock_timestamp(),$2,'operator',$3,'success','training_store',$4,$5,$6,$7)", "INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,'operator',?,'success','training_store',?,?,?,CAST(? AS JSON))", uuid.NewString(), tenant, "training.store_"+operation+"_verified", s.Store.Namespace(), reason, uuid.NewString(), metadata))
		if e == nil && n != 1 {
			return fail("STORAGE_UNAVAILABLE")
		}
		return e
	})
}
