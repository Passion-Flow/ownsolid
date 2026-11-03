// Package recovery coordinates module-owned authority invalidation in a single operator transaction.
package recovery

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/imageregistry"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func InvalidateRestored(ctx context.Context, accounts *identity.Service, reason string) error {
	if !operatorReason(reason) {
		return errors.New("OPERATOR_REASON_REQUIRED")
	}
	return restoreTransaction(ctx, accounts, func(ctx context.Context, q transaction.Executor) error {
		return InvalidateRestoredWithin(ctx, accounts, q, reason)
	})
}

// InvalidateRestoredWithin borrows the actual caller transaction so native
// restore can commit all original owners and its exact receipt together. It does
// not open a pool, commit independently or claim physical workload removal.
func InvalidateRestoredWithin(ctx context.Context, accounts *identity.Service, q transaction.Executor, reason string) error {
	if !operatorReason(reason) {
		return errors.New("OPERATOR_REASON_REQUIRED")
	}
	if ctx == nil || accounts == nil || q == nil {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	store, e := accounts.Repository()
	if e != nil || store.Dialect() != q.Dialect() {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	if e := accounts.Config.ValidateApplicationProfile(); e != nil {
		return e
	}
	if accounts.Config.AccountRuntime() {
		if e := database.CheckAccountProfileWithin(ctx, q, accounts.Config.Server.Profile()); e != nil {
			return e
		}
		if e := accounts.InvalidateAccountAuthoritiesWithin(ctx, q, reason); e != nil {
			return e
		}
		if accounts.Config.NodeRuntime() {
			if e := nodes.InvalidateAuthoritiesWithin(ctx, q); e != nil {
				return e
			}
		}
		if accounts.Config.ModelsRuntime() {
			if e := models.InvalidateAuthoritiesWithin(ctx, q); e != nil {
				return e
			}
			if e := deployments.InvalidateAuthoritiesWithin(ctx, q); e != nil {
				return e
			}
			if accounts.Config.GatewayRuntime() {
				if e := distribution.InvalidateAuthoritiesWithin(ctx, q); e != nil {
					return e
				}
			}
			if accounts.Config.DatasetRuntime() {
				if e := datasets.InvalidateAuthoritiesWithin(ctx, q); e != nil {
					return e
				}
			}
			if accounts.Config.TrainingRuntime() {
				if e := training.InvalidateAuthoritiesWithin(ctx, q); e != nil {
					return e
				}
			}
			if accounts.Config.LifecycleRuntime() {
				if e := modelpreparations.InvalidateAuthoritiesWithin(ctx, q); e != nil {
					return e
				}
			}
			if accounts.Config.SSORuntime() {
				if e := sso.InvalidateAuthoritiesWithin(ctx, q); e != nil {
					return e
				}
			}
			if accounts.Config.KnowledgeRuntime() {
				if e := knowledge.InvalidateAuthoritiesWithin(ctx, q); e != nil {
					return e
				}
			}
			if accounts.Config.PluginsRuntime() {
				if e := plugins.InvalidateAuthoritiesWithin(ctx, q); e != nil {
					return e
				}
				return workflows.InvalidateAuthoritiesWithin(ctx, q)
			}
			if accounts.Config.WorkflowsOnly() {
				return workflows.InvalidateKnowledgeAuthoritiesWithin(ctx, q)
			}
			return nil
		}
		return nil
	}

	if e := accounts.InvalidateAccountAuthoritiesWithin(ctx, q, reason); e != nil {
		return e
	}
	owners := []func(context.Context, transaction.Executor) error{
		sso.InvalidateAuthoritiesWithin, nodes.InvalidateAuthoritiesWithin,
		models.InvalidateAuthoritiesWithin, deployments.InvalidateAuthoritiesWithin,
		datasets.InvalidateAuthoritiesWithin, knowledge.InvalidateAuthoritiesWithin,
		plugins.InvalidateAuthoritiesWithin, workflows.InvalidateAuthoritiesWithin,
		training.InvalidateAuthoritiesWithin, modelpreparations.InvalidateAuthoritiesWithin,
		distribution.InvalidateAuthoritiesWithin, imageregistry.InvalidateAuthoritiesWithin,
	}
	for _, invalidate := range owners {
		if e := invalidate(ctx, q); e != nil {
			return e
		}
	}
	if e := fenceRestoredArtifactsWithin(ctx, q, accounts.Config); e != nil {
		return e
	}
	if e := fenceRestoredMiddlewareWithin(ctx, q, accounts, reason); e != nil {
		return e
	}
	if e := fenceRestoredPrivateClustersWithin(ctx, q, accounts); e != nil {
		return e
	}
	return fenceRestoredTaskEventsWithin(ctx, q, accounts)
}
