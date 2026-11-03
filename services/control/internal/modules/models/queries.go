package models

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

func (s *Service) Imports(ctx context.Context, a identity.Auth, workspace string) ([]Import, error) {
	return s.listImports(ctx, a, workspace)
}
func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, id string) (Version, error) {
	return s.getVersion(ctx, a, workspace, id)
}
