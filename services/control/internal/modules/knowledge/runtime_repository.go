package knowledge

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Vendor text and positional bindings belong to each operation. No SQL rewriting
// or PostgreSQL fallback occurs at runtime.
func knowledgeSQL(q transaction.Executor, pg, my string, pgArgs, myArgs []any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, myArgs...)
	}
	return transaction.SQL(transaction.PostgreSQL, pg, pgArgs...)
}
func knowledgeSQLNative(q transaction.Executor, pg, my string, pgArgs, myArgs []any, native transaction.Statement) transaction.Statement {
	if q.Dialect() == transaction.Dameng {
		return native
	}
	if q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL {
		return transaction.Statement{}
	}
	return knowledgeSQL(q, pg, my, pgArgs, myArgs)
}
func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil || s.Workspaces == nil || s.Workspaces.Identity != s.Identity {
		return nil, failure("KNOWLEDGE_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return nil, failure("KNOWLEDGE_STORAGE_MISMATCH")
	}
	repo, e := s.Identity.Repository()
	if e != nil {
		return nil, knowledgeError(e)
	}
	if repo.Dialect() != transaction.MySQL && repo.Dialect() != transaction.PostgreSQL {
		return nil, failure("KNOWLEDGE_STORAGE_MISMATCH")
	}
	return repo, nil
}
func knowledgeError(e error) error {
	if e == nil {
		return nil
	}
	var rate *identity.RateError
	if errors.As(e, &rate) {
		return e // Preserve the ordinary429 code and measured Retry-After capability.
	}
	var storage *transaction.Error
	if errors.As(e, &storage) {
		return failure(string(storage.Kind))
	}
	if errors.Is(e, context.Canceled) {
		return failure(string(transaction.Cancelled))
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return failure(string(transaction.Timeout))
	}
	code := e.Error()
	switch code {
	case string(transaction.Unavailable), string(transaction.NoRows), string(transaction.UniqueConflict), string(transaction.ForeignKeyConflict), string(transaction.CheckConflict), string(transaction.Deadlock), string(transaction.LockTimeout), string(transaction.Cancelled), string(transaction.Timeout), string(transaction.CommitUnknown), string(transaction.CommitRolledBack), "ACCESS_DENIED", "RESOURCE_NOT_FOUND", "VALIDATION_FAILED", "IDEMPOTENCY_CONFLICT", "INVALID_CURSOR", "INVALID_QUERY":
		return failure(code)
	}
	for _, prefix := range []string{"KNOWLEDGE_", "AUTH_", "WORKSPACE_", "MODEL_", "GATEWAY_", "LICENSE_", "KEY_"} {
		if strings.HasPrefix(code, prefix) {
			return failure(code)
		}
	}
	return failure("STORAGE_UNAVAILABLE")
}
func knowledgeCommitted[T any](value T, e error) (T, error) {
	if e != nil {
		var zero T
		return zero, knowledgeError(e)
	}
	return value, nil
}
func knowledgeUnique(e error, code string) error {
	var storage *transaction.Error
	if errors.As(e, &storage) && storage.Kind == transaction.UniqueConflict {
		return failure(code)
	}
	return e
}

const mysqlBaseFields = `id,workspace_id,name,enabled,revision,active_generation_id,building_generation_id,created_by,created_at,updated_at`
const mysqlDocumentFields = `id,base_id,workspace_id,name,revision,state,visible_source_id,pending_source_id,current_attempt_id,created_by,created_at,updated_at,deleted_at`

func (s *Service) borrowed(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil {
		return failure("KNOWLEDGE_STORAGE_MISMATCH")
	}
	repo, e := s.repository()
	if e != nil {
		return e
	}
	if repo.Dialect() != q.Dialect() {
		return failure("KNOWLEDGE_STORAGE_MISMATCH")
	}
	return nil
}
func utcKnowledge(stamp *time.Time) *time.Time {
	if stamp == nil {
		return nil
	}
	value := stamp.UTC()
	return &value
}
