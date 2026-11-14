package modelpreparations

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type HoldoutCapabilities struct {
	Enabled         bool  `json:"enabled"`
	CanReserve      bool  `json:"can_reserve"`
	MaximumHoldouts int   `json:"maximum_holdouts"`
	MaximumRows     int64 `json:"maximum_rows"`
	MinimumRows     int64 `json:"minimum_rows"`
}

func (s *Service) HoldoutCapabilities(ctx context.Context, a identity.Auth, workspace string) (HoldoutCapabilities, error) {
	var value HoldoutCapabilities
	e := s.preparationActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		value.Enabled = s.Datasets != nil && s.Datasets.PublishedSourcesAvailable()
		value.CanReserve = value.Enabled && owner(a, w)
		value.MaximumHoldouts = s.policy().MaxHoldoutsPerWorkspace
		value.MaximumRows, value.MinimumRows = wire.MaximumRows, wire.MinimumRows
		return nil
	})
	if e != nil {
		return HoldoutCapabilities{}, e
	}
	return value, e
}
