package modelpreparations

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// Actor operations retain current session and Workspace fences until the owning
// commit. Read callbacks may lock rows; they never grant mutation authority.
func (s *Service) preparationActorTransaction(ctx context.Context, a identity.Auth, workspace string, write bool, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	if ctx == nil || !datawire.ValidID(workspace) || a.Me.Audience != "portal" && !admin(a) {
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
		if w.Status != "active" || write && !owner(a, w) {
			return fail("ACCESS_DENIED")
		}
		return fn(ctx, q, w)
	})
	return preparationRepositoryError(e)
}
func (s *Service) replayPreparationWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, key string, wanted []byte) (Preparation, bool, error) {
	var id string
	var previous []byte
	e := preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationDM(`SELECT "ID","CREATION_FINGERPRINT" FROM "MODEL_PREPARATIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "CREATED_BY"=? AND "CREATION_AUDIENCE"=? AND "CREATION_KEY"=? FOR UPDATE`), `SELECT id::text,creation_fingerprint FROM model_preparations WHERE tenant_id=$1 AND workspace_id=$2 AND created_by=$3 AND creation_audience=$4 AND creation_key=$5 FOR SHARE`, `SELECT id,creation_fingerprint FROM model_preparations WHERE tenant_id=? AND workspace_id=? AND created_by=? AND creation_audience=? AND creation_key=? FOR SHARE`, w.TenantID, w.ID, a.Me.User.ID, a.Me.Audience, key)).Scan(&id, &previous)
	if errors.Is(e, sql.ErrNoRows) {
		return Preparation{}, false, nil
	}
	if e != nil {
		return Preparation{}, false, e
	}
	if !same(previous, wanted) {
		return Preparation{}, false, fail("IDEMPOTENCY_CONFLICT")
	}
	p, e := lockedPreparationWithin(ctx, q, w, id)
	if e != nil {
		return Preparation{}, false, e
	}
	return p, true, nil
}
func (s *Service) preparationSourcesWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, input CreateRequest) (preparationInputs, datasets.Version, error) {
	if s == nil || s.Nodes == nil {
		return preparationInputs{}, datasets.Version{}, fail("MODEL_PREPARATION_UNAVAILABLE")
	}
	if _, e := s.Nodes.ModelPreparationTargetWithin(ctx, q, w.TenantID, w.ID, input.NodeID); e != nil {
		return preparationInputs{}, datasets.Version{}, e
	}
	return s.retainedPreparationSourcesWithin(ctx, q, w, input.TrainingJobID, input.HeldoutID)
}
func preparationTimestampsUTC(p *Preparation) {
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	for _, stamp := range []**time.Time{&p.CancelRequestedAt, &p.FinishedAt} {
		if *stamp != nil {
			value := (*stamp).UTC()
			*stamp = &value
		}
	}
}
