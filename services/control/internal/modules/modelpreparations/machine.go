package modelpreparations

import (
	"database/sql"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"context"
	"crypto/sha256"
	"errors"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func (s *Service) machineCandidate(ctx context.Context, node, task string) (candidate, string, error) {
	var c candidate
	store, repositoryError := s.runtimeRepository()
	if repositoryError != nil {
		return c, "", repositoryError
	}
	var taskID string
	if !datawire.ValidID(node) || task != "" && !datawire.ValidID(task) {
		return c, taskID, fail("AGENT_UNAUTHORIZED")
	}
	query := `SELECT p.id::text,p.tenant_id::text,p.workspace_id::text,p.current_node_id::text,p.requested_by::text,p.requested_audience,p.requested_epoch,p.membership_authority::text,t.id::text FROM model_preparations p JOIN model_preparation_attempts a ON a.id=p.current_attempt_id AND a.preparation_id=p.id JOIN node_tasks t ON t.id=a.task_id AND t.node_id=p.current_node_id JOIN installation_state i ON i.tenant_id=p.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND p.current_node_id=$1 AND t.operation=$2`
	mysqlQuery := `SELECT p.id,p.tenant_id,p.workspace_id,p.current_node_id,p.requested_by,p.requested_audience,p.requested_epoch,p.membership_authority,t.id FROM model_preparations p JOIN model_preparation_attempts a ON a.id=p.current_attempt_id AND a.preparation_id=p.id JOIN node_tasks t ON t.id=a.task_id AND t.node_id=p.current_node_id JOIN installation_state i ON i.tenant_id=p.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND p.current_node_id=? AND t.operation=?`
	nativeQuery := `SELECT P."ID",P."TENANT_ID",P."WORKSPACE_ID",P."CURRENT_NODE_ID",P."REQUESTED_BY",P."REQUESTED_AUDIENCE",P."REQUESTED_EPOCH",P."MEMBERSHIP_AUTHORITY",T."ID" FROM "MODEL_PREPARATIONS" P JOIN "MODEL_PREPARATION_ATTEMPTS" A ON A."ID"=P."CURRENT_ATTEMPT_ID" AND A."PREPARATION_ID"=P."ID" JOIN "NODE_TASKS" T ON T."ID"=A."TASK_ID" AND T."NODE_ID"=P."CURRENT_NODE_ID" JOIN "INSTALLATION_STATE" I ON I."TENANT_ID"=P."TENANT_ID" WHERE I."ID"=1 AND I."BOOTSTRAP_COMPLETE"=1 AND P."CURRENT_NODE_ID"=? AND T."OPERATION"=?`
	args := []any{node, wire.RunOperation}
	if task != "" {
		query += " AND t.id=$3"
		mysqlQuery += " AND t.id=?"
		nativeQuery += ` AND T."ID"=?`
		args = append(args, task)
	} else {
		query += " AND t.state IN ('pending','running')"
		mysqlQuery += " AND t.state IN ('pending','running')"
		nativeQuery += ` AND T."STATE" IN ('pending','running')`
	}
	query += " ORDER BY t.created_at,t.id LIMIT 1"
	mysqlQuery += " ORDER BY t.created_at,t.id LIMIT 1"
	nativeQuery += ` ORDER BY T."CREATED_AT",T."ID" LIMIT 1`
	e := preparationIDRow(preparationQueryRow(ctx, store, preparationSelectedSQL(store, preparationDM(nativeQuery), query, mysqlQuery, args...)), 0, 1, 2, 3, 4, 7, 8).Scan(&c.id, &c.tenant, &c.workspace, &c.node, &c.actor, &c.audience, &c.epoch, &c.membership, &taskID)
	return c, taskID, e
}

func bindFrozenCommand(p Preparation, command wire.Command) error {
	if p.CurrentAttemptID == nil || command.AttemptID != *p.CurrentAttemptID || command.AttemptNumber != p.AttemptNumber {
		return fail("MODEL_PREPARATION_ATTEMPT_STALE")
	}
	expected := commandForPreparation(p, workspaces.Workspace{ID: p.WorkspaceID, TenantID: p.tenant}, *p.CurrentAttemptID, p.AttemptNumber)
	got, e := command.Digest()
	want, expectedError := expected.Digest()
	if e != nil || expectedError != nil || got != want {
		return fail("MODEL_PREPARATION_ATTEMPT_STALE")
	}
	return nil
}

func bindAttempt(ctx context.Context, tx transaction.Executor, p Preparation, command wire.Command, task string) error {
	if e := bindFrozenCommand(p, command); e != nil {
		return e
	}
	sha, e := command.Digest()
	if e != nil {
		return e
	}
	var bound bool
	e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT CASE WHEN EXISTS(SELECT 1 FROM "MODEL_PREPARATION_ATTEMPTS" "A" JOIN "MODEL_PREPARATIONS" "P" ON "P"."ID"="A"."PREPARATION_ID" AND "P"."CURRENT_ATTEMPT_ID"="A"."ID" JOIN "GPU_LEASES" "G" ON "G"."MODEL_PREPARATION_ID"="A"."PREPARATION_ID" AND "G"."NODE_ID"="A"."NODE_ID" AND "G"."REVISION"="A"."ATTEMPT_NUMBER" WHERE "A"."ID"=? AND "A"."PREPARATION_ID"=? AND "A"."TASK_ID"=? AND "A"."NODE_ID"=? AND "A"."COMMAND_SHA256"=? AND "A"."PHYSICAL_STOPPED_AT" IS NULL AND "A"."STATE" IN ('pending','preparing','running') AND "G"."GPU_UUID"=? AND "G"."STATE"='held' AND "A"."REQUESTED_BY"="P"."REQUESTED_BY" AND "A"."REQUESTED_AUDIENCE"="P"."REQUESTED_AUDIENCE" AND "A"."REQUESTED_EPOCH"="P"."REQUESTED_EPOCH" AND ("A"."MEMBERSHIP_AUTHORITY"="P"."MEMBERSHIP_AUTHORITY" OR "A"."MEMBERSHIP_AUTHORITY" IS NULL AND "P"."MEMBERSHIP_AUTHORITY" IS NULL)) THEN 1 ELSE 0 END FROM DUAL`), `SELECT EXISTS(SELECT 1 FROM model_preparation_attempts a JOIN model_preparations p ON p.id=a.preparation_id AND p.current_attempt_id=a.id JOIN gpu_leases g ON g.model_preparation_id=a.preparation_id AND g.node_id=a.node_id AND g.revision=a.attempt_number WHERE a.id=$1 AND a.preparation_id=$2 AND a.task_id=$3 AND a.node_id=$4 AND a.command_sha256=$5 AND a.physical_stopped_at IS NULL AND a.state IN ('pending','preparing','running') AND g.gpu_uuid=$6 AND g.state='held' AND a.requested_by=p.requested_by AND a.requested_audience=p.requested_audience AND a.requested_epoch=p.requested_epoch AND a.membership_authority IS NOT DISTINCT FROM p.membership_authority)`, `SELECT EXISTS(SELECT 1 FROM model_preparation_attempts a JOIN model_preparations p ON p.id=a.preparation_id AND p.current_attempt_id=a.id JOIN gpu_leases g ON g.model_preparation_id=a.preparation_id AND g.node_id=a.node_id AND g.revision=a.attempt_number WHERE a.id=? AND a.preparation_id=? AND a.task_id=? AND a.node_id=? AND a.command_sha256=? AND a.physical_stopped_at IS NULL AND a.state IN ('pending','preparing','running') AND g.gpu_uuid=? AND g.state='held' AND a.requested_by=p.requested_by AND a.requested_audience=p.requested_audience AND a.requested_epoch=p.requested_epoch AND a.membership_authority <=> p.membership_authority)`, command.AttemptID, p.ID, task, p.CurrentNodeID, sha, p.GPUUUID)).Scan(&bound)
	if e != nil {
		return e
	}
	if !bound {
		return fail("MODEL_PREPARATION_ATTEMPT_STALE")
	}
	return nil
}

func stopTokenHash(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte("projectone-model-preparation-stop-token:" + token))
}

func (s *Service) ClaimPreparation(ctx context.Context, node, credential, request string) (agentwire.Claim, bool, error) {
	var out agentwire.Claim
	c, task, e := s.machineCandidate(ctx, node, "")
	if errors.Is(e, sql.ErrNoRows) {
		return out, false, nil
	}
	if e != nil {
		return out, true, e
	}
	e = s.delegatedTransaction(ctx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, p Preparation, valid bool) error {
		current, e := s.Nodes.CredentialWithin(ctx, tx, c.tenant, c.workspace, node, credential)
		if e != nil {
			return e
		}
		if !valid || current.Status != "active" || p.State != "preparing" || p.CancelRequestedAt != nil {
			return fail("ACCESS_DENIED")
		}
		input := CreateRequest{WorkspaceID: w.ID, TrainingJobID: p.TrainingJobID, HeldoutID: p.HeldoutID, NodeID: current.ID}
		sources, _, e := s.preparationSourcesWithin(ctx, tx, w, input)
		if e != nil {
			return e
		}
		if sources.digest() != p.InputSHA256 {
			return fail("MODEL_PREPARATION_INPUT_CHANGED")
		}
		out, e = s.Nodes.ClaimPreparationWithin(ctx, tx, current, task, request)
		if e != nil || out.Lease == nil {
			return e
		}
		if e = bindAttempt(ctx, tx, p, *out.Lease.Preparation, task); e != nil {
			return e
		}
		hash := stopTokenHash(out.Lease.LeaseToken)
		tag, e := tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_ATTEMPTS" SET "STATE"='preparing',"STOP_TOKEN_HASH"=?,"STARTED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=? AND "STOP_TOKEN_HASH" IS NULL`), "UPDATE model_preparation_attempts SET state='preparing',stop_token_hash=$2,started_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND stop_token_hash IS NULL", `UPDATE model_preparation_attempts SET state='preparing',stop_token_hash=?,started_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=? AND stop_token_hash IS NULL`, []any{*p.CurrentAttemptID, hash[:]}, []any{hash[:], *p.CurrentAttemptID}))
		if e == nil && tag != 1 {
			return fail("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		return e
	})
	if e != nil {
		return agentwire.Claim{}, true, e
	}
	return out, true, nil
}

func (s *Service) RenewPreparation(ctx context.Context, node, credential, task, token string) (agentwire.RenewedLease, bool, error) {
	var out agentwire.RenewedLease
	c, _, e := s.machineCandidate(ctx, node, task)
	if errors.Is(e, sql.ErrNoRows) {
		return out, false, nil
	}
	if e != nil {
		return out, true, e
	}
	e = s.delegatedTransaction(ctx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, p Preparation, valid bool) error {
		current, e := s.Nodes.CredentialWithin(ctx, tx, c.tenant, c.workspace, node, credential)
		if e != nil {
			return e
		}
		if !valid || current.Status != "active" || p.State != "preparing" && p.State != "running" || p.CancelRequestedAt != nil {
			return fail("ACCESS_DENIED")
		}
		var command wire.Command
		out, command, e = s.Nodes.RenewPreparationWithin(ctx, tx, current, task, token)
		if e != nil {
			return e
		}
		return bindAttempt(ctx, tx, p, command, task)
	})
	if e != nil {
		return agentwire.RenewedLease{}, true, e
	}
	return out, true, nil
}
