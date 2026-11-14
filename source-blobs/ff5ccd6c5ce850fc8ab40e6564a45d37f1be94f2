package artifactstores

import (
	"context"
	"strconv"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Borrow the original restore transaction. Every pending operation receives a
// distinct canonical UUID, preventing an old worker from acknowledging restored
// work. Never continue after a failed native statement on a poisoned connection.
func fenceDamengAfterRestore(ctx context.Context, q transaction.Executor, tenant string) error {
	after := ""
	for {
		rows, e := q.Query(ctx, transaction.SQL(transaction.Dameng,
			`SELECT "ID","OPERATION_ID" FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=? AND "OPERATION_ID" IS NOT NULL AND (? IS NULL OR "ID">?) ORDER BY "ID" LIMIT `+strconv.Itoa(MaximumObjectPageSize)+` FOR UPDATE`, tenant, damengCursor(after), damengCursor(after)))
		if e != nil {
			return e
		}
		type pending struct{ id, operation string }
		batch := []pending{}
		last := after
		for rows.Next() {
			var v pending
			if e = rows.Scan(&v.id, &v.operation); e != nil {
				rows.Close()
				return e
			}
			if !id(v.id) || !id(v.operation) || v.id <= last || len(batch) >= MaximumObjectPageSize {
				rows.Close()
				return fail("OBJECT_STORED_PROVENANCE_INVALID")
			}
			batch = append(batch, v)
			last = v.id
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(batch) == 0 {
			return nil
		}
		for _, v := range batch {
			n, e := q.Exec(ctx, transaction.SQL(transaction.Dameng,
				`UPDATE "ARTIFACT_OBJECTS" SET "OPERATION_ID"=?,"OPERATION_PHASE"='sent',"ERROR_CODE"='OBJECT_RESTORE_RECONCILE_REQUIRED',"OPERATION_EXPIRES_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)),"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)),"REVISION"="REVISION"+1 WHERE "TENANT_ID"=? AND "ID"=? AND "OPERATION_ID"=?`, uuid.NewString(), tenant, v.id, v.operation))
			if e != nil {
				return e
			}
			if n != 1 {
				return fail("OBJECT_OPERATION_STALE")
			}
			after = v.id
		}
	}
}
