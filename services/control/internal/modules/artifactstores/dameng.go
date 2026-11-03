package artifactstores

import "github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

// These repositories bind native positional parameters in their textual order.
// SQL remains an explicit third literal at each owning operation; no rewriting
// or PostgreSQL/MySQL execution fallback is permitted.
func positionalArguments(q transaction.Executor) bool {
	return q.Dialect() == transaction.MySQL || q.Dialect() == transaction.Dameng
}

func jsonParameter(q transaction.Executor, raw []byte) any {
	if q.Dialect() == transaction.Dameng {
		// Native binary values are VARBINARY; JSON columns are CLOB text. Keep
		// fingerprint/token hashes as []byte at their original binding sites.
		return string(raw)
	}
	return raw
}

func damengCursor(after string) any {
	if after == "" {
		return nil
	}
	return after
}

func expiryArgs(q transaction.Executor, tenant, workspace, store string, limit int) []any {
	if q.Dialect() == transaction.Dameng {
		return []any{tenant, workspace, store}
	}
	return []any{tenant, workspace, store, limit}
}

// All callers choose only these protocol-owned column names. No request text
// can supply an identifier. An unknown internal name produces invalid SQL.
func damengColumn(column string) string {
	switch column {
	case "expires_at":
		return `"EXPIRES_AT"`
	case "upload_expires_at":
		return `"UPLOAD_EXPIRES_AT"`
	case "model_import_id":
		return `"MODEL_IMPORT_ID"`
	case "dataset_draft_id":
		return `"DATASET_DRAFT_ID"`
	default:
		return `""`
	}
}
