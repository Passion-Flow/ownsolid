package datasets

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Each call owns all vendor statements and their positional bindings. This
// selector does not inspect/rewrite SQL or infer argument ordering.
func datasetSQL(q transaction.Executor, pg, my string, pgArgs, myArgs []any, dm transaction.Statement) transaction.Statement {
	switch q.Dialect() {
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, myArgs...)
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, pgArgs...)
	case transaction.Dameng:
		return dm
	default:
		return transaction.Statement{}
	}
}
func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil {
		return nil, failure("DATASET_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces != nil && (s.Workspaces.Identity != s.Identity || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB) {
		return nil, failure("DATASET_STORAGE_MISMATCH")
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return nil, datasetRuntimeError(e)
	}
	if store.Dialect() != transaction.PostgreSQL && store.Dialect() != transaction.MySQL && store.Dialect() != transaction.Dameng {
		return nil, failure("DATASET_STORAGE_MISMATCH")
	}
	return store, nil
}
func datasetRuntimeError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, context.Canceled) {
		return failure(string(transaction.Cancelled))
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return failure(string(transaction.Timeout))
	}
	var storage *transaction.Error
	if errors.As(e, &storage) {
		return failure(string(storage.Kind))
	}
	code := e.Error()
	switch code {
	case string(transaction.Unavailable), string(transaction.NoRows), string(transaction.UniqueConflict), string(transaction.ForeignKeyConflict), string(transaction.CheckConflict), string(transaction.Deadlock), string(transaction.LockTimeout), string(transaction.Cancelled), string(transaction.Timeout), string(transaction.CommitUnknown), string(transaction.CommitRolledBack):
		return failure(code)
	}
	if strings.HasPrefix(code, "MONGODB_") || strings.HasPrefix(code, "DATASET_") || strings.HasPrefix(code, "AUTH_") || code == "ACCESS_DENIED" || code == "RESOURCE_NOT_FOUND" || code == "VALIDATION_FAILED" || code == "IDEMPOTENCY_CONFLICT" || code == "INVALID_CURSOR" || code == "INVALID_QUERY" {
		return failure(code)
	}
	return failure("STORAGE_UNAVAILABLE")
}

// A fixed structural batch size keeps parameter count and SQL packet size below
// vendor ceilings. The product row/byte limits remain the owning typed policy.
const inspectionInsertBatch = 64

func insertInspectedRows(ctx context.Context, q transaction.Executor, records, history [][]any) error {
	for _, data := range []struct {
		table   string
		columns []string
		rows    [][]any
	}{
		{"dataset_rows", []string{"id", "tenant_id", "workspace_id", "dataset_id", "draft_id", "line_number", "source_id", "prompt", "completion", "error_code", "raw_preview", "preview_replaced"}, records},
		{"dataset_row_history", []string{"tenant_id", "workspace_id", "dataset_id", "draft_id", "row_id", "revision", "actor_user_id", "action", "content_sha256", "reason"}, history},
	} {
		if q.Dialect() == transaction.Dameng {
			// Single-row native inserts keep bounded parameters without relying
			// on another vendor's multi-row VALUES extension. The caller owns
			// one atomic rows/history/publication transaction.
			statement := dmInsertInspectedRow
			if data.table == "dataset_row_history" {
				statement = dmInsertInspectedHistory
			}
			for _, row := range data.rows {
				if len(row) != len(data.columns) {
					return failure("DATASET_PROCESSOR_RESULT_INVALID")
				}
				if _, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, statement, row...)); e != nil {
					return e
				}
			}
			continue
		}
		for offset := 0; offset < len(data.rows); offset += inspectionInsertBatch {
			end := min(offset+inspectionInsertBatch, len(data.rows))
			args := []any{}
			pgRows, myRows := []string{}, []string{}
			for _, row := range data.rows[offset:end] {
				if len(row) != len(data.columns) {
					return failure("DATASET_PROCESSOR_RESULT_INVALID")
				}
				pg, my := []string{}, []string{}
				for _, value := range row {
					args = append(args, value)
					pg = append(pg, "$"+strconv.Itoa(len(args)))
					my = append(my, "?")
				}
				pgRows = append(pgRows, "("+strings.Join(pg, ",")+")")
				myRows = append(myRows, "("+strings.Join(my, ",")+")")
			}
			prefix := "INSERT INTO " + data.table + "(" + strings.Join(data.columns, ",") + ") VALUES"
			if _, e := q.Exec(ctx, datasetSQL(q, prefix+strings.Join(pgRows, ","), prefix+strings.Join(myRows, ","), args, args, transaction.Statement{})); e != nil {
				return e
			}
		}
	}
	return nil
}

func committed[T any](value T, e error) (T, error) {
	if e != nil {
		var zero T
		return zero, datasetRuntimeError(e)
	}
	return value, nil
}

const mysqlDraftFields = `error_line_number,id,dataset_id,workspace_id,created_by,parent_version_id,source_name,source_sha256,expected_bytes,source_received_at,state,revision,CASE WHEN validation_completed_at IS NOT NULL THEN total_rows END,CASE WHEN validation_completed_at IS NOT NULL THEN invalid_rows END,validation_completed_at,fatal_source_error,validation_errors,errors_truncated,error_code,current_job_id,published_version_id,created_at,updated_at`

const mysqlRowFields = `id,line_number,revision,source_id,prompt,completion,error_code,raw_preview,preview_replaced`

const mysqlJobFields = `error_line_number,reason,id,dataset_id,draft_id,workspace_id,kind,requested_by,expected_revision,state,version_id,input_sha256,split_seed,validation_basis_points,attempt_count,attempt_id,lease_expires_at,cancel_requested_at,error_code,retryable,created_at,updated_at,completed_at`

func datasetMySQLFields(fields string) string {
	switch fields {
	case datasetFields:
		return mysqlPublishedDatasetFields
	case draftFields:
		return mysqlDraftFields
	case versionFields:
		return mysqlPublishedVersionFields
	default:
		return ""
	}
}
