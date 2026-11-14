package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// AuditFileOperation records the canonical scoped inventory after a local operator file operation.
// If audit fails, the command fails and retains its new filesystem scope for explicit review.
func (s *Service) AuditFileOperation(ctx context.Context, operation, reason string) error {
	if operation != "export" && operation != "import" || !reasonValid(reason) {
		return fail("VALIDATION_FAILED")
	}
	id, request := uuid.NewString(), uuid.NewString()
	return s.fileWithin(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		inventory, e := fileInventoryWithin(ctx, q, false)
		if e != nil {
			return e
		}
		raw, _ := json.Marshal(inventory)
		sum := sha256.Sum256(raw)
		metadata, _ := json.Marshal(map[string]any{"inventory_sha256": hex.EncodeToString(sum[:]), "model_versions": len(inventory.Models), "source": "protected_local_operator", "immutable_files_retained": true})
		_, e = modelExecOne(ctx, q, modelNativeSQL(q, `INSERT INTO AUDIT_EVENTS(ID,OCCURRED_AT,TENANT_ID,AUDIENCE,ACTION,OUTCOME,TARGET_TYPE,TARGET_ID,REASON,REQUEST_ID,METADATA) VALUES(?,CURRENT_TIMESTAMP(6),?,'operator',?,'success','model_store',?,?,?,?)`,
			`INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES($1,clock_timestamp(),$2,'operator',$3,'success','model_store',$4,$5,$6,$7::jsonb)`,
			`INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,'operator',?,'success','model_store',?,?,?,CAST(? AS JSON))`,
			id, inventory.TenantID, "model.files_"+operation+"ed", inventory.TenantID, reason, request, string(metadata)))
		return e
	})
}
