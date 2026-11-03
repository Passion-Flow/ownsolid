package modelpreparations

import (
	"database/sql"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func (s *Service) recoveryPreflight(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, id string, input RecoveryRequest, key string, intent []byte) (Preparation, datasets.Version, bool, error) {
	var empty Preparation
	var version datasets.Version
	var previous []byte
	e := preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "CREATION_FINGERPRINT" FROM "MODEL_PREPARATION_RECOVERIES" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "PREPARATION_ID"=? AND "REQUESTED_BY"=? AND "REQUESTED_AUDIENCE"=? AND "CREATION_KEY"=?`), `SELECT creation_fingerprint FROM model_preparation_recoveries WHERE tenant_id=$1 AND workspace_id=$2 AND preparation_id=$3 AND requested_by=$4 AND requested_audience=$5 AND creation_key=$6`, `SELECT creation_fingerprint FROM model_preparation_recoveries WHERE tenant_id=? AND workspace_id=? AND preparation_id=? AND requested_by=? AND requested_audience=? AND creation_key=?`, w.TenantID, w.ID, id, a.Me.User.ID, a.Me.Audience, key)).Scan(&previous)
	if e == nil {
		if !same(previous, intent) {
			return empty, version, false, fail("IDEMPOTENCY_CONFLICT")
		}
		p, e := lockedPreparationWithin(ctx, tx, w, id)
		return p, version, true, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return empty, version, false, e
	}
	n, e := s.Nodes.ModelPreparationTargetWithin(ctx, tx, w.TenantID, w.ID, input.NodeID)
	if e != nil {
		return empty, version, false, e
	}
	p, e := lockedPreparationWithin(ctx, tx, w, id)
	if e != nil {
		return p, version, false, e
	}
	if p.Revision != input.ExpectedRevision {
		return p, version, false, fail("MODEL_PREPARATION_REVISION_CONFLICT")
	}
	if p.State != "failed" && p.State != "cancelled" && p.State != "recovery_required" || p.CurrentAttemptID == nil {
		return p, version, false, fail("MODEL_PREPARATION_RECOVERY_NOT_ALLOWED")
	}
	if p.AttemptNumber >= s.policy().MaxAttempts {
		return p, version, false, fail("MODEL_PREPARATION_ATTEMPTS_EXHAUSTED")
	}
	if e = clearedAttempt(ctx, tx, p); e != nil {
		return p, version, false, e
	}
	var active int
	if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT COUNT(*) FROM "MODEL_PREPARATIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"<>? AND "STATE" IN ('queued','preparing','running','cancelling','recovery_required')`), `SELECT count(*) FROM model_preparations WHERE tenant_id=$1 AND workspace_id=$2 AND id<>$3 AND state IN ('queued','preparing','running','cancelling','recovery_required')`, `SELECT count(*) FROM model_preparations WHERE tenant_id=? AND workspace_id=? AND id<>? AND state IN ('queued','preparing','running','cancelling','recovery_required')`, w.TenantID, w.ID, p.ID)).Scan(&active); e != nil {
		return p, version, false, e
	}
	if active >= s.policy().MaxActivePerWorkspace {
		return p, version, false, fail("MODEL_PREPARATION_LIMIT_REACHED")
	}
	installed, e := s.executionRecipe()
	if e != nil {
		return p, version, false, e
	}
	frozen, _ := json.Marshal(p.Recipe)
	current, _ := json.Marshal(installed)
	if !same(frozen, current) {
		return p, version, false, fail("MODEL_PREPARATION_RECIPE_UNAVAILABLE")
	}
	if e = s.resourcePreflight(ctx, tx, n, p.GPUUUID); e != nil {
		return p, version, false, e
	}
	sources, version, e := s.preparationSourcesWithin(ctx, tx, w, CreateRequest{NodeID: n.ID, TrainingJobID: p.TrainingJobID, HeldoutID: p.HeldoutID})
	if e != nil {
		return p, version, false, e
	}
	if sources.digest() != p.InputSHA256 {
		return p, version, false, fail("MODEL_PREPARATION_INPUT_CHANGED")
	}
	return p, version, false, nil
}

func clearedAttempt(ctx context.Context, tx transaction.Executor, p Preparation) error {
	if p.CurrentAttemptID == nil {
		return fail("MODEL_PREPARATION_PHYSICAL_STOP_REQUIRED")
	}
	var cleared bool
	e := preparationQueryRow(ctx, tx, preparationSelectedRuntimeSQL(tx, preparationDM(`SELECT CASE WHEN EXISTS(SELECT 1 FROM "MODEL_PREPARATION_ATTEMPTS" "A" JOIN "GPU_LEASES" "G" ON "G"."MODEL_PREPARATION_ID"="A"."PREPARATION_ID" AND "G"."NODE_ID"="A"."NODE_ID" AND "G"."REVISION"="A"."ATTEMPT_NUMBER" WHERE "A"."ID"=? AND "A"."PREPARATION_ID"=? AND "A"."PHYSICAL_STOPPED_AT" IS NOT NULL AND "G"."GPU_UUID"=? AND "G"."STATE"='released') AND NOT EXISTS(SELECT 1 FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "PREPARATION_ID"=? AND "PHYSICAL_STOPPED_AT" IS NULL) THEN 1 ELSE 0 END FROM DUAL`), `SELECT EXISTS(SELECT 1 FROM model_preparation_attempts a JOIN gpu_leases g ON g.model_preparation_id=a.preparation_id AND g.node_id=a.node_id AND g.revision=a.attempt_number WHERE a.id=$1 AND a.preparation_id=$2 AND a.physical_stopped_at IS NOT NULL AND g.gpu_uuid=$3 AND g.state='released') AND NOT EXISTS(SELECT 1 FROM model_preparation_attempts WHERE preparation_id=$2 AND physical_stopped_at IS NULL)`, `SELECT EXISTS(SELECT 1 FROM model_preparation_attempts a JOIN gpu_leases g ON g.model_preparation_id=a.preparation_id AND g.node_id=a.node_id AND g.revision=a.attempt_number WHERE a.id=? AND a.preparation_id=? AND a.physical_stopped_at IS NOT NULL AND g.gpu_uuid=? AND g.state='released') AND NOT EXISTS(SELECT 1 FROM model_preparation_attempts WHERE preparation_id=? AND physical_stopped_at IS NULL)`, []any{*p.CurrentAttemptID, p.ID, p.GPUUUID}, []any{*p.CurrentAttemptID, p.ID, p.GPUUUID, p.ID})).Scan(&cleared)
	if e != nil {
		return e
	}
	if !cleared {
		return fail("MODEL_PREPARATION_PHYSICAL_STOP_REQUIRED")
	}
	return nil
}

// Recover freezes one explicit fresh delegation; physical dispatch remains a separate transaction.
func (s *Service) Recover(ctx context.Context, a identity.Auth, workspace, id string, input RecoveryRequest, key, request string) (Preparation, error) {
	var out Preparation
	if !datawire.ValidID(id) || !datawire.ValidID(key) || !datawire.ValidID(input.NodeID) || input.ExpectedRevision < 1 || !text(input.Reason, 500) {
		return out, fail("VALIDATION_FAILED")
	}
	intent := fingerprint(a, struct {
		WorkspaceID   string
		PreparationID string
		Input         RecoveryRequest
	}{workspace, id, input})
	var version datasets.Version
	replay := false
	e := s.preparationActorTransaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, version, replay, e = s.recoveryPreflight(ctx, tx, a, w, id, input, key, intent)
		return e
	})
	if e != nil {
		return Preparation{}, e
	}
	if replay {
		return out, nil
	}
	if e = s.verifyPreparationSources(ctx, out.inputs, version, out.Recipe); e != nil {
		return Preparation{}, e
	}
	e = s.preparationActorTransaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		p, _, replayed, e := s.recoveryPreflight(ctx, tx, a, w, id, input, key, intent)
		if e != nil {
			return e
		}
		if replayed {
			out = p
			return nil
		}
		if p.InputSHA256 != out.InputSHA256 || p.CurrentAttemptID == nil || out.CurrentAttemptID == nil || *p.CurrentAttemptID != *out.CurrentAttemptID {
			return fail("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		var membership *string
		if !admin(a) {
			authority, e := s.Workspaces.MembershipAuthorityWithin(ctx, tx, w.TenantID, w.ID, a.Me.User.ID)
			if e != nil {
				return e
			}
			membership = &authority
		}
		var previousNode string
		if e = preparationIDRow(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "NODE_ID" FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "ID"=? AND "PREPARATION_ID"=?`), "SELECT node_id::text FROM model_preparation_attempts WHERE id=$1 AND preparation_id=$2", `SELECT node_id FROM model_preparation_attempts WHERE id=? AND preparation_id=?`, *p.CurrentAttemptID, p.ID)), 0).Scan(&previousNode); e != nil {
			return e
		}
		recovery := uuid.NewString()
		_, e = tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`INSERT INTO "MODEL_PREPARATION_RECOVERIES"("ID","TENANT_ID","WORKSPACE_ID","PREPARATION_ID","PREVIOUS_ATTEMPT_ID","PREVIOUS_NODE_ID","NEXT_NODE_ID","NEXT_ATTEMPT_NUMBER","REQUESTED_BY","REQUESTED_AUDIENCE","REQUESTED_EPOCH","MEMBERSHIP_AUTHORITY","REASON","CREATION_KEY","CREATION_FINGERPRINT") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`), `INSERT INTO model_preparation_recoveries(id,tenant_id,workspace_id,preparation_id,previous_attempt_id,previous_node_id,next_node_id,next_attempt_number,requested_by,requested_audience,requested_epoch,membership_authority,reason,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, `INSERT INTO model_preparation_recoveries(id,tenant_id,workspace_id,preparation_id,previous_attempt_id,previous_node_id,next_node_id,next_attempt_number,requested_by,requested_audience,requested_epoch,membership_authority,reason,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, recovery, w.TenantID, w.ID, p.ID, *p.CurrentAttemptID, previousNode, input.NodeID, p.AttemptNumber+1, a.Me.User.ID, a.Me.Audience, a.Epoch, membership, input.Reason, key, intent))
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATIONS" SET "STATE"='queued',"CURRENT_NODE_ID"=?,"PENDING_RECOVERY_ID"=?,"REQUESTED_BY"=?,"REQUESTED_AUDIENCE"=?,"REQUESTED_EPOCH"=?,"MEMBERSHIP_AUTHORITY"=?,"CANCEL_REQUESTED_AT"=NULL,"CANCEL_REQUESTED_BY"=NULL,"ERROR_CODE"=NULL,"WAIT_CODE"=NULL,"FINISHED_AT"=NULL,"NEXT_DISPATCH_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"REVISION"="REVISION"+1,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), `UPDATE model_preparations SET state='queued',current_node_id=$2,pending_recovery_id=$3,requested_by=$4,requested_audience=$5,requested_epoch=$6,membership_authority=$7,cancel_requested_at=NULL,cancel_requested_by=NULL,error_code=NULL,wait_code=NULL,finished_at=NULL,next_dispatch_at=clock_timestamp(),revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE model_preparations SET state='queued',current_node_id=?,pending_recovery_id=?,requested_by=?,requested_audience=?,requested_epoch=?,membership_authority=?,cancel_requested_at=NULL,cancel_requested_by=NULL,error_code=NULL,wait_code=NULL,finished_at=NULL,next_dispatch_at=UTC_TIMESTAMP(6),revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{p.ID, input.NodeID, recovery, a.Me.User.ID, a.Me.Audience, a.Epoch, membership}, []any{input.NodeID, recovery, a.Me.User.ID, a.Me.Audience, a.Epoch, membership, p.ID}))
		if e != nil {
			return e
		}
		if e = preparationAuditWithin(ctx, tx, a, w, "model.preparation_recovery_requested", p.ID, input.Reason, request); e != nil {
			return e
		}
		out, e = lockedPreparationWithin(ctx, tx, w, p.ID)
		return e
	})
	if e != nil {
		return Preparation{}, e
	}
	return out, e
}

func (s *Service) pendingRecovery(ctx context.Context, tx transaction.Executor, p Preparation, c candidate) error {
	if p.AttemptNumber == 0 {
		if p.PendingRecoveryID != nil {
			return fail("MODEL_PREPARATION_DATA_INVALID")
		}
		return nil
	}
	if p.PendingRecoveryID == nil || p.CurrentAttemptID == nil {
		return fail("MODEL_PREPARATION_RECOVERY_NOT_ALLOWED")
	}
	var bound bool
	e := preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT CASE WHEN EXISTS(SELECT 1 FROM "MODEL_PREPARATION_RECOVERIES" "R" JOIN "MODEL_PREPARATION_ATTEMPTS" "A" ON "A"."ID"="R"."PREVIOUS_ATTEMPT_ID" AND "A"."PREPARATION_ID"="R"."PREPARATION_ID" AND "A"."NODE_ID"="R"."PREVIOUS_NODE_ID" AND "A"."ATTEMPT_NUMBER"+1="R"."NEXT_ATTEMPT_NUMBER" WHERE "R"."ID"=? AND "R"."TENANT_ID"=? AND "R"."WORKSPACE_ID"=? AND "R"."PREPARATION_ID"=? AND "R"."PREVIOUS_ATTEMPT_ID"=? AND "R"."NEXT_NODE_ID"=? AND "R"."NEXT_ATTEMPT_NUMBER"=? AND "R"."REQUESTED_BY"=? AND "R"."REQUESTED_AUDIENCE"=? AND "R"."REQUESTED_EPOCH"=? AND ("R"."MEMBERSHIP_AUTHORITY"=? OR "R"."MEMBERSHIP_AUTHORITY" IS NULL AND ? IS NULL) AND "R"."APPLIED_AT" IS NULL) THEN 1 ELSE 0 END FROM DUAL`).order(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 10), `SELECT EXISTS(SELECT 1 FROM model_preparation_recoveries r JOIN model_preparation_attempts a ON a.id=r.previous_attempt_id AND a.preparation_id=r.preparation_id AND a.node_id=r.previous_node_id AND a.attempt_number+1=r.next_attempt_number WHERE r.id=$1 AND r.tenant_id=$2 AND r.workspace_id=$3 AND r.preparation_id=$4 AND r.previous_attempt_id=$5 AND r.next_node_id=$6 AND r.next_attempt_number=$7 AND r.requested_by=$8 AND r.requested_audience=$9 AND r.requested_epoch=$10 AND r.membership_authority IS NOT DISTINCT FROM $11::uuid AND r.applied_at IS NULL)`, `SELECT EXISTS(SELECT 1 FROM model_preparation_recoveries r JOIN model_preparation_attempts a ON a.id=r.previous_attempt_id AND a.preparation_id=r.preparation_id AND a.node_id=r.previous_node_id AND a.attempt_number+1=r.next_attempt_number WHERE r.id=? AND r.tenant_id=? AND r.workspace_id=? AND r.preparation_id=? AND r.previous_attempt_id=? AND r.next_node_id=? AND r.next_attempt_number=? AND r.requested_by=? AND r.requested_audience=? AND r.requested_epoch=? AND r.membership_authority <=> ? AND r.applied_at IS NULL)`, *p.PendingRecoveryID, p.tenant, p.WorkspaceID, p.ID, *p.CurrentAttemptID, p.CurrentNodeID, p.AttemptNumber+1, c.actor, c.audience, c.epoch, c.membership)).Scan(&bound)
	if e != nil {
		return e
	}
	if !bound {
		return fail("MODEL_PREPARATION_ATTEMPT_STALE")
	}
	return clearedAttempt(ctx, tx, p)
}
