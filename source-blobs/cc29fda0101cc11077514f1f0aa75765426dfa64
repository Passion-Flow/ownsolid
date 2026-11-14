package models

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"strconv"
)

const nativeImportFields = "ID,WORKSPACE_ID,NAME,CATALOG_DIGEST,EXPECTED_BYTES,UPLOADED_BYTES,STATE,ERROR_CODE,MODEL_ID,ATTEMPTS,CREATED_AT,UPDATED_AT"
const nativeVersionFields = "ID,WORKSPACE_ID,NAME,MODEL_KEY,CATALOG_DIGEST,MANIFEST,STATE,CREATED_AT,KIND"

// Native selectors take independently authored statements. They do not translate SQL.
func modelNativeSQL(q transaction.Executor, native, postgres, mysql string, args ...any) transaction.Statement {
	if q.Dialect() == transaction.Dameng {
		return transaction.SQL(transaction.Dameng, native, args...)
	}
	return modelSQL(q, postgres, mysql, args...)
}

// The configuration/lookup contract permits 1..100 rows and one overflow sentinel.
// LIMIT binding has not been target-qualified; only a checked decimal literal is emitted.
func modelPageSQL(q transaction.Executor, native, postgres, mysql string, args ...any) transaction.Statement {
	if q.Dialect() != transaction.Dameng {
		return modelSQL(q, postgres, mysql, args...)
	}
	if len(args) == 0 {
		return transaction.SQL(transaction.Dameng, "")
	}
	limit, ok := args[len(args)-1].(int)
	if !ok || limit < 1 || limit > 101 {
		return transaction.SQL(transaction.Dameng, "")
	}
	return transaction.SQL(transaction.Dameng, native+" LIMIT "+strconv.Itoa(limit), args[:len(args)-1]...)
}

// A native mutation must affect its one intended row. A statement failure exits
// immediately: no SELECT or retry may use the now-poisoned physical connection.
func modelExecOne(ctx context.Context, q transaction.Executor, statement transaction.Statement) (int64, error) {
	count, e := q.Exec(ctx, statement)
	if e != nil {
		return count, e
	}
	if q.Dialect() == transaction.Dameng && count != 1 {
		return count, fail("STORAGE_UNAVAILABLE")
	}
	return count, nil
}
