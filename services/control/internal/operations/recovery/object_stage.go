package recovery

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

// InitializeObjectStage records durable intent before the filesystem effect.
// An uncertain intent commit prevents that effect. An uncertain final audit
// retains the namespace for explicit readback; it never deletes or adopts it.
func InitializeObjectStage(ctx context.Context, accounts *identity.Service, reason string) error {
	if accounts == nil || !operatorReason(reason) {
		return errors.New("OPERATOR_REASON_REQUIRED")
	}
	c := accounts.Config.ObjectStorage
	if c == nil || !c.Enabled || c.Validate() != nil {
		return errors.New("OBJECT_NOT_ADMITTED")
	}
	operation := uuid.NewString()
	var tenant, workspace string
	record := func(ctx context.Context, phase string) error {
		return restoreTransaction(ctx, accounts, func(ctx context.Context, q transaction.Executor) error {
			if q.Dialect() != transaction.PostgreSQL {
				return errors.New("OBJECT_SCHEMA_NOT_ACTIVE")
			}
			var currentTenant, currentWorkspace string
			if e := q.QueryRow(ctx, transaction.SQL(q.Dialect(), `SELECT tenant_id,default_workspace_id FROM installation_state WHERE id=1 AND bootstrap_complete FOR UPDATE`)).Scan(&currentTenant, &currentWorkspace); e != nil {
				return e
			}
			if tenant != "" && (tenant != currentTenant || workspace != currentWorkspace) {
				return errors.New("OBJECT_STORE_CHANGED")
			}
			tenant, workspace = currentTenant, currentWorkspace
			return operatorAuditWithin(ctx, q, tenant, workspace, "object.stage_initialization_"+phase, "artifact_stage", operation, reason, map[string]any{"phase": phase, "backend_sha256": objectstore.ConfigurationIdentity(*c)})
		})
	}
	if e := record(ctx, "requested"); e != nil {
		return e
	}
	e := objectstore.InitializeStage(*c, tenant)
	phase := "completed"
	if e != nil {
		phase = "failed"
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(accounts.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	return errors.Join(e, record(cleanup, phase))
}
