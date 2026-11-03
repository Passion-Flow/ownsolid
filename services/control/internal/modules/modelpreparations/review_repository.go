package modelpreparations

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const mysqlPreparationFields = `id,tenant_id,workspace_id,node_id,current_node_id,model_id,training_job_id,training_attempt_id,training_artifact_id,heldout_id,gpu_uuid,input_snapshot,input_sha256,configuration,configuration_sha256,execution_recipe,created_by,reason,state,revision,wait_code,error_code,cancel_requested_at,created_at,updated_at,finished_at,attempt_number,current_attempt_id,pending_recovery_id,review_id,derived_model_id`
const mysqlReviewFields = `id,workspace_id,preparation_id,attempt_id,artifact_id,report_sha256,expected_revision,decision,model_id,model_name,reviewed_by,reason,created_at`
const mysqlHoldoutFields = `id,workspace_id,dataset_id,dataset_version_id,name,manifest_sha256,effective_source_sha256,row_mapping_sha256,source_bytes,row_count,label_counts,recipe_id,recipe_sha256,instruction_sha256,fingerprint_algorithm,created_by,audience,reason,created_at`

func preparationSQL(q transaction.Executor, pg, my string, args ...any) transaction.Statement {
	switch q.Dialect() {
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	}
	return transaction.SQL(q.Dialect(), "")
}
func preparationRepositoryError(e error) error {
	if e == nil {
		return nil
	}
	if outcome, ok := transaction.CommitOutcome(e); ok {
		return fail(string(outcome))
	}
	if errors.Is(e, context.Canceled) {
		return fail(string(transaction.Cancelled))
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return fail(string(transaction.Timeout))
	}
	// Vendor errors are normalized by Executor. Only stable component codes escape.
	code := e.Error()
	if len(code) > 120 {
		return fail("STORAGE_UNAVAILABLE")
	}
	for _, c := range code {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return fail("STORAGE_UNAVAILABLE")
		}
	}
	for _, prefix := range []string{"MODEL_", "DATASET_", "TRAINING_", "NODE_", "AGENT_", "STORAGE_", "AUTH_"} {
		if strings.HasPrefix(code, prefix) {
			return fail(code)
		}
	}
	switch code {
	case "ACCESS_DENIED", "VALIDATION_FAILED", "INVALID_CURSOR", "IDEMPOTENCY_CONFLICT", "RESOURCE_NOT_FOUND", string(transaction.Cancelled), string(transaction.Timeout), string(transaction.LockTimeout), string(transaction.Deadlock), string(transaction.CommitUnknown), string(transaction.CommitRolledBack):
		return fail(code)
	}
	return fail("STORAGE_UNAVAILABLE")
}
func (s *Service) reviewRepository() (transaction.Store, error) {
	if s == nil || s.Identity == nil || s.Workspaces == nil || s.Workspaces.Identity != s.Identity {
		return nil, fail("MODEL_PREPARATION_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return nil, fail("MODEL_PREPARATION_STORAGE_MISMATCH")
	}
	return s.Identity.Repository()
}
func (s *Service) reviewTransaction(ctx context.Context, a identity.Auth, workspace string, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	if ctx == nil || !admin(a) || !datawire.ValidID(workspace) {
		return fail("ACCESS_DENIED")
	}
	store, e := s.reviewRepository()
	if e != nil {
		return preparationRepositoryError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().TransactionSeconds))
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := preparationBorrowedExecutor(store, q); e != nil {
			return e
		}
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if w.Status != "active" {
			return fail("ACCESS_DENIED")
		}
		return fn(ctx, q, w)
	})
	return preparationRepositoryError(e)
}
func lockedPreparationWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, id string) (Preparation, error) {
	if !datawire.ValidID(id) {
		return Preparation{}, fail("MODEL_PREPARATION_NOT_FOUND")
	}
	p, e := scanPreparation(preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationDM("SELECT "+nativePreparationFields+` FROM "MODEL_PREPARATIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=? FOR UPDATE`), "SELECT "+preparationFields+" FROM model_preparations WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", "SELECT "+mysqlPreparationFields+" FROM model_preparations WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE", w.TenantID, w.ID, id)))
	if e != nil {
		return Preparation{}, preparationRepositoryError(e)
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	for _, stamp := range []**time.Time{&p.CancelRequestedAt, &p.FinishedAt} {
		if *stamp != nil {
			value := (*stamp).UTC()
			*stamp = &value
		}
	}
	return p, nil
}
func (s *Service) replayReviewWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, key string, intent []byte) (ModelReview, bool, error) {
	var stored []byte
	e := preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationDM(`SELECT "FINGERPRINT" FROM "MODEL_PREPARATION_REVIEWS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "REVIEWED_BY"=? AND "IDEMPOTENCY_KEY"=? FOR UPDATE`), "SELECT fingerprint FROM model_preparation_reviews WHERE tenant_id=$1 AND workspace_id=$2 AND reviewed_by=$3 AND idempotency_key=$4 FOR SHARE", "SELECT fingerprint FROM model_preparation_reviews WHERE tenant_id=? AND workspace_id=? AND reviewed_by=? AND idempotency_key=? FOR SHARE", w.TenantID, w.ID, a.Me.User.ID, key)).Scan(&stored)
	if errors.Is(e, sql.ErrNoRows) {
		return ModelReview{}, false, nil
	}
	if e != nil {
		return ModelReview{}, false, e
	}
	if !same(stored, intent) {
		return ModelReview{}, false, fail("IDEMPOTENCY_CONFLICT")
	}
	out, e := scanReview(preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationDM("SELECT "+nativeReviewFields+` FROM "MODEL_PREPARATION_REVIEWS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "REVIEWED_BY"=? AND "IDEMPOTENCY_KEY"=?`), "SELECT "+reviewFields+" FROM model_preparation_reviews WHERE tenant_id=$1 AND workspace_id=$2 AND reviewed_by=$3 AND idempotency_key=$4", "SELECT "+mysqlReviewFields+" FROM model_preparation_reviews WHERE tenant_id=? AND workspace_id=? AND reviewed_by=? AND idempotency_key=?", w.TenantID, w.ID, a.Me.User.ID, key)))
	if e != nil {
		return ModelReview{}, false, e
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return out, true, nil
}
func preparationAuditWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, action, id, reason, request string) error {
	n, e := q.Exec(ctx, preparationSelectedSQL(q, preparationDM(`INSERT INTO "AUDIT_EVENTS"("ID","OCCURRED_AT","ACTOR_USER_ID","TENANT_ID","WORKSPACE_ID","AUDIENCE","ACTION","OUTCOME","TARGET_TYPE","TARGET_ID","REASON","REQUEST_ID") VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,?,?,?,?,'success','model_preparation',?,?,?)`), `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6,'success','model_preparation',$7,$8,$9)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,?,?,'success','model_preparation',?,?,?)`, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, id, reason, request))
	if e != nil {
		return e
	}
	if n != 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	return nil
}
