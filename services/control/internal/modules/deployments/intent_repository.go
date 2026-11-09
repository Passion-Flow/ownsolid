package deployments

import (
	"context"
	"database/sql"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func resourceConflict(e error) error {
	var value *transaction.Error
	if errors.As(e, &value) && value.Kind == transaction.UniqueConflict {
		return fail("INFERENCE_RESOURCE_BUSY")
	}
	return e
}
func (s *Service) recordTaskWithin(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, c wire.InferenceCommand) error {
	if c.DeploymentID == "" {
		return nil
	}
	changed, e := q.Exec(ctx, deploymentNativeSQL(q, `UPDATE "DEPLOYMENTS" SET "LAST_TASK_ID"=?,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "ID"=?`, `UPDATE deployments SET last_task_id=$1,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND node_id=$4 AND id=$5`, `UPDATE deployments SET last_task_id=?,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, t.ID, n.TenantID, n.WorkspaceID, n.ID, c.DeploymentID))
	if e != nil {
		return e
	}
	if changed != 1 {
		return fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	return nil
}
func (s *Service) deploymentByTaskWithin(ctx context.Context, q transaction.Executor, t nodes.Task) (Deployment, error) {
	if q.Dialect() == transaction.Dameng {
		return s.nativeDeploymentByTaskWithin(ctx, q, t)
	}
	var deployment string
	e := q.QueryRow(ctx, deploymentSQL(q, `SELECT payload->>'deployment_id' FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4`, `SELECT JSON_UNQUOTE(JSON_EXTRACT(payload,'$.deployment_id')) FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, t.TenantID, t.WorkspaceID, t.NodeID, t.ID)).Scan(&deployment)
	if e != nil {
		return Deployment{}, e
	}
	if !id(deployment) {
		return Deployment{}, fail("STORAGE_UNAVAILABLE")
	}
	return s.deploymentWithin(ctx, q, t.TenantID, t.WorkspaceID, deployment, false)
}

// Replay executes its original owner transaction before file IO; a concurrently
// deleted immutable task cannot be recreated by this path.
func (s *Service) replayCreation(ctx context.Context, a identity.Auth, n nodes.Node, input CreateRequest, key, request, binding string, admit func(context.Context, transaction.Executor) error, bound func(context.Context, transaction.Executor, nodes.Node, nodes.Task, wire.InferenceCommand, bool) error) (Deployment, bool, error) {
	var out Deployment
	found := false
	e := s.runtimeActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		if admit != nil {
			if e := admit(ctx, q); e != nil {
				return e
			}
		}
		var operation string
		e := q.QueryRow(ctx, deploymentNativeSQL(q, `SELECT "OPERATION" FROM "NODE_TASKS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "IDEMPOTENCY_KEY"=?`, `SELECT operation FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND idempotency_key=$4`, `SELECT operation FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND idempotency_key=?`, n.TenantID, n.WorkspaceID, n.ID, key)).Scan(&operation)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		found = true
		if !wire.IsInferenceOperation(operation) {
			return fail("IDEMPOTENCY_CONFLICT")
		}
		t, e := s.Nodes.CreateInferenceTaskBoundWithin(ctx, q, a, n.ID, operation, key, input.Reason, request, registryFingerprint(a.Me.User.ID, input, binding), func(context.Context, transaction.Executor, nodes.Node) (wire.InferenceCommand, error) {
			return wire.InferenceCommand{}, fail("IDEMPOTENCY_CONFLICT")
		}, s.recordTaskWithin)
		if e != nil {
			return e
		}
		if bound != nil {
			command, readErr := s.Nodes.ReadInferenceCommandWithin(ctx, q, n, t)
			if readErr != nil {
				return readErr
			}
			if e = bound(ctx, q, n, t, command, true); e != nil {
				return e
			}
		}
		out, e = s.deploymentByTaskWithin(ctx, q, t)
		return e
	})
	if e != nil {
		return Deployment{}, found, e
	}
	return out, found, nil
}
func (s *Service) createSelected(ctx context.Context, a identity.Auth, input CreateRequest, key, request string) (Deployment, error) {
	return s.createBoundSelected(ctx, a, input, key, request, "", nil, nil)
}
func (s *Service) createBoundSelected(ctx context.Context, a identity.Auth, input CreateRequest, key, request, binding string, admit func(context.Context, transaction.Executor) error, bound func(context.Context, transaction.Executor, nodes.Node, nodes.Task, wire.InferenceCommand, bool) error) (Deployment, error) {
	if !id(input.NodeID) || !id(input.ModelID) || !id(key) || !id(request) || !validReason(input.Reason) {
		return Deployment{}, fail("VALIDATION_FAILED")
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return Deployment{}, fail("ACCESS_DENIED")
	}
	if _, e := s.runtimeRepository(); e != nil {
		return Deployment{}, e
	}
	n, e := s.Nodes.Get(ctx, a, "", input.NodeID)
	if e != nil {
		return Deployment{}, e
	}
	if previous, found, e := s.replayCreation(ctx, a, n, input, key, request, binding, admit, bound); found || e != nil {
		return previous, e
	}
	version, e := s.Models.Get(ctx, a, n.WorkspaceID, input.ModelID)
	if e != nil {
		return Deployment{}, e
	}
	if e = s.admitModelUse(ctx, n.WorkspaceID, version.Kind, version.CatalogDigest); e != nil {
		return Deployment{}, e
	}
	command, e := s.initialCommand(ctx, version, n, input.GPUUUID)
	if e != nil {
		return Deployment{}, e
	}
	command.RegistryPinID = binding
	if e = command.Validate(command.Operation()); e != nil {
		return Deployment{}, e
	}
	var out Deployment
	e = s.runtimeActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		if admit != nil {
			if e := admit(ctx, q); e != nil {
				return e
			}
		}
		recorded := false
		t, e := s.Nodes.CreateInferenceTaskBoundWithin(ctx, q, a, n.ID, command.Operation(), key, input.Reason, request, registryFingerprint(a.Me.User.ID, input, binding), func(ctx context.Context, q transaction.Executor, current nodes.Node) (wire.InferenceCommand, error) {
			if e := s.preflightCommand(ctx, q, current, command, true); e != nil {
				return wire.InferenceCommand{}, e
			}
			if e := s.readyCommand(ctx, q, current, command); e != nil {
				return wire.InferenceCommand{}, e
			}
			var retained bool
			if e := q.QueryRow(ctx, deploymentNativeSQL(q, `SELECT CASE WHEN EXISTS(SELECT 1 FROM "DEPLOYMENTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "REMOVED_AT" IS NULL) THEN 1 ELSE 0 END FROM DUAL`, `SELECT EXISTS(SELECT 1 FROM deployments WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND removed_at IS NULL)`, `SELECT EXISTS(SELECT 1 FROM deployments WHERE tenant_id=? AND workspace_id=? AND node_id=? AND removed_at IS NULL)`, current.TenantID, current.WorkspaceID, current.ID)).Scan(&retained); e != nil {
				return wire.InferenceCommand{}, e
			}
			if retained {
				return wire.InferenceCommand{}, fail("INFERENCE_RESOURCE_BUSY")
			}
			_, e := deploymentExecOne(ctx, q, deploymentNativeSQL(q, `INSERT INTO "DEPLOYMENTS"("ID","TENANT_ID","WORKSPACE_ID","NODE_ID","MODEL_ID","GPU_UUID","DESIRED_STATE","REVISION","CREATED_BY","MODEL_KIND","CATALOG_DIGEST","SERVING_RECIPE_SHA256","CREATED_AT","UPDATED_AT") VALUES(?,?,?,?,?,?,'running',1,?,?,?,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)))`, `INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,revision,created_by,model_kind,catalog_digest,serving_recipe_sha256) VALUES($1,$2,$3,$4,$5,$6,'running',1,$7,$8,$9,$10)`, `INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,revision,created_by,model_kind,catalog_digest,serving_recipe_sha256) VALUES(?,?,?,?,?,?,'running',1,?,?,?,?)`, command.DeploymentID, current.TenantID, current.WorkspaceID, current.ID, command.ModelID, command.GPUUUID, a.Me.User.ID, version.Kind, command.CatalogDigest, servingSHA(command)))
			if e != nil {
				return wire.InferenceCommand{}, resourceConflict(e)
			}
			_, e = deploymentExecOne(ctx, q, deploymentNativeSQL(q, `INSERT INTO "GPU_LEASES"("TENANT_ID","WORKSPACE_ID","NODE_ID","GPU_UUID","DEPLOYMENT_ID","REVISION","STATE","UPDATED_AT") VALUES(?,?,?,?,?,1,'held',SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)))`, `INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state) VALUES($5,$1,$2,$3,$4,$5,1,'held')`, `INSERT INTO gpu_leases(tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state) VALUES(?,?,?,?,?,1,'held')`, current.TenantID, current.WorkspaceID, current.ID, command.GPUUUID, command.DeploymentID))
			if e != nil {
				return wire.InferenceCommand{}, resourceConflict(e)
			}
			return command, nil
		}, func(ctx context.Context, q transaction.Executor, current nodes.Node, t nodes.Task, c wire.InferenceCommand) error {
			if e := s.recordTaskWithin(ctx, q, current, t, c); e != nil {
				return e
			}
			if bound != nil {
				if e := bound(ctx, q, current, t, c, false); e != nil {
					return e
				}
				recorded = true
			}
			return nil
		})
		if e != nil {
			return e
		}
		if bound != nil && !recorded {
			command, readErr := s.Nodes.ReadInferenceCommandWithin(ctx, q, n, t)
			if readErr != nil {
				return readErr
			}
			if e = bound(ctx, q, n, t, command, true); e != nil {
				return e
			}
		}
		out, e = s.deploymentByTaskWithin(ctx, q, t)
		return e
	})
	if e != nil {
		return Deployment{}, e
	}
	return out, nil
}
func (s *Service) actionSelected(ctx context.Context, a identity.Auth, deployment string, input ActionRequest, key, request string) (Deployment, error) {
	if input.Action != "deploy" && input.Action != "stop" && input.Action != "remove" && input.Action != "observe" && input.Action != "verify" || input.Revision < 1 || input.Revision >= 9007199254740991 || !validReason(input.Reason) || !id(key) || !id(request) {
		return Deployment{}, fail("VALIDATION_FAILED")
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return Deployment{}, fail("ACCESS_DENIED")
	}
	current, e := s.Get(ctx, a, "", deployment)
	if e != nil {
		return Deployment{}, e
	}
	intent := struct {
		ID      string
		Request ActionRequest
	}{deployment, input}
	operation := wire.InferenceOperation(input.Action)
	if current.ModelKind == wire.DerivedModelKind {
		operation = wire.DerivedInferenceOperation
	}
	var out Deployment
	var ownerError error
	e = s.runtimeActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		recorded := false
		t, e := s.Nodes.CreateInferenceTaskBoundWithin(ctx, q, a, current.NodeID, operation, key, input.Reason, request, fingerprint(a.Me.User.ID, intent), func(ctx context.Context, q transaction.Executor, n nodes.Node) (wire.InferenceCommand, error) {
			d, e := s.deploymentWithin(ctx, q, n.TenantID, n.WorkspaceID, deployment, true)
			if e != nil {
				return wire.InferenceCommand{}, e
			}
			if d.NodeID != n.ID {
				return wire.InferenceCommand{}, fail("INFERENCE_OWNERSHIP_CONFLICT")
			}
			if d.RemovedAt != nil || d.Revision != input.Revision {
				return wire.InferenceCommand{}, fail("VERSION_CONFLICT")
			}
			c := wire.InferenceCommand{Action: input.Action, DeploymentID: d.ID, ModelID: d.ModelID, GPUUUID: d.GPUUUID, CatalogDigest: d.CatalogDigest, Revision: d.Revision + 1}
			c, e = s.retainedCommand(ctx, q, n, d, c)
			if e != nil {
				return wire.InferenceCommand{}, e
			}
			if s.RegistryActions == nil {
				witnessed, readErr := s.Nodes.RegistryIntentRetainedWithin(ctx, q, n, d.ID)
				if readErr != nil {
					return wire.InferenceCommand{}, readErr
				}
				if witnessed {
					ownerError = fail("REGISTRY_OWNER_REQUIRED")
					return wire.InferenceCommand{}, ownerError
				}
			}
			if s.RegistryActions != nil {
				c, ownerError = s.RegistryActions.PrepareActionWithin(ctx, q, a, n, d, c)
				if ownerError != nil {
					return wire.InferenceCommand{}, ownerError
				}
			}
			desired := d.DesiredState
			state := d.ResourceState
			switch input.Action {
			case "deploy":
				if ownerError = s.admitModelUse(ctx, d.WorkspaceID, d.ModelKind, d.CatalogDigest); ownerError != nil {
					return wire.InferenceCommand{}, ownerError
				}
				if state == "recovery_required" {
					return wire.InferenceCommand{}, fail("INFERENCE_RECOVERY_REQUIRED")
				}
				if e = s.preflightCommand(ctx, q, n, c, d.ObservedState != "running"); e != nil {
					return wire.InferenceCommand{}, e
				}
				if e = s.readyCommand(ctx, q, n, c); e != nil {
					return wire.InferenceCommand{}, e
				}
				desired = "running"
				state = "held"
			case "stop":
				desired = "stopped"
			case "remove":
				desired = "removed"
			}
			changed, e := q.Exec(ctx, deploymentNativeSQL(q, `UPDATE "DEPLOYMENTS" SET "DESIRED_STATE"=?,"REVISION"=?,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "ID"=? AND "REVISION"=?`, `UPDATE deployments SET desired_state=$1,revision=$2,updated_at=clock_timestamp() WHERE tenant_id=$3 AND workspace_id=$4 AND node_id=$5 AND id=$6 AND revision=$7`, `UPDATE deployments SET desired_state=?,revision=?,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND revision=?`, desired, c.Revision, n.TenantID, n.WorkspaceID, n.ID, d.ID, d.Revision))
			if e != nil {
				return wire.InferenceCommand{}, e
			}
			if changed != 1 {
				return wire.InferenceCommand{}, fail("VERSION_CONFLICT")
			}
			changed, e = q.Exec(ctx, deploymentNativeSQL(q, `UPDATE "GPU_LEASES" SET "REVISION"=?,"STATE"=?,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "DEPLOYMENT_ID"=? AND "GPU_UUID"=? AND "REVISION"=?`, `UPDATE gpu_leases SET revision=$1,state=$2,updated_at=clock_timestamp() WHERE tenant_id=$3 AND workspace_id=$4 AND node_id=$5 AND deployment_id=$6 AND gpu_uuid=$7 AND revision=$8`, `UPDATE gpu_leases SET revision=?,state=?,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND deployment_id=? AND gpu_uuid=? AND revision=?`, c.Revision, state, n.TenantID, n.WorkspaceID, n.ID, d.ID, d.GPUUUID, d.Revision))
			if e != nil {
				return wire.InferenceCommand{}, resourceConflict(e)
			}
			if changed != 1 {
				return wire.InferenceCommand{}, fail("INFERENCE_OWNERSHIP_CONFLICT")
			}
			return c, nil
		}, func(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, c wire.InferenceCommand) error {
			if e := s.recordTaskWithin(ctx, q, n, t, c); e != nil {
				return e
			}
			if s.RegistryActions != nil {
				ownerError = s.RegistryActions.BindActionWithin(ctx, q, a, n, t, c, false, input.Reason, request)
				if ownerError != nil {
					return ownerError
				}
			}
			recorded = true
			return nil
		})
		if e != nil {
			return e
		}
		if s.RegistryActions != nil && !recorded {
			n, original, c, readErr := s.Nodes.RetainedInferenceTaskForActorWithin(ctx, q, a, t.NodeID, t.ID)
			if readErr != nil {
				return readErr
			}
			ownerError = s.RegistryActions.BindActionWithin(ctx, q, a, n, original, c, true, input.Reason, request)
			if ownerError != nil {
				return ownerError
			}
		}
		out, e = s.deploymentByTaskWithin(ctx, q, t)
		return e
	})
	if e != nil {
		if kind, ok := transaction.CommitOutcome(e); ok {
			return Deployment{}, fail(string(kind))
		}
		if ownerError != nil {
			return Deployment{}, ownerError
		}
		return Deployment{}, e
	}
	return out, nil
}
func (s *Service) discoverSelected(ctx context.Context, a identity.Auth, node, reason, key, request string) (nodes.Task, error) {
	if !validReason(reason) {
		return nodes.Task{}, fail("VALIDATION_FAILED")
	}
	intent := struct{ Node, Reason string }{node, reason}
	var out nodes.Task
	e := s.runtimeActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		var e error
		out, e = s.Nodes.CreateInferenceTaskBoundWithin(ctx, q, a, node, wire.InferenceOperation("observe"), key, reason, request, fingerprint(a.Me.User.ID, intent), func(context.Context, transaction.Executor, nodes.Node) (wire.InferenceCommand, error) {
			return wire.InferenceCommand{Action: "observe"}, nil
		}, s.recordTaskWithin)
		return e
	})
	if e != nil {
		return nodes.Task{}, e
	}
	return out, nil
}
