package artifactstores

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type Capabilities struct {
	MaximumObjectBytes  int64 `json:"maximum_object_bytes"`
	PartBytes           int64 `json:"part_bytes"`
	MaximumGrantSeconds int   `json:"maximum_grant_seconds"`
	CanCreate           bool  `json:"can_create"`
	CanImportModel      bool  `json:"can_import_model"`
	CanIngestDataset    bool  `json:"can_ingest_dataset"`
}

func (s *Service) Capabilities(ctx context.Context, a identity.Auth, workspace string) (Capabilities, error) {
	ctx, done, e := s.begin(ctx)
	if e != nil {
		return Capabilities{}, e
	}
	defer done()
	var out Capabilities
	e = s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		out = Capabilities{s.Configuration.MaximumObjectBytes, s.Configuration.PartBytes, s.Configuration.MaximumGrantSeconds, writeRole(a, w), admin(a), writeRole(a, w)}
		return nil
	})
	if e != nil {
		return Capabilities{}, e
	}
	return out, nil
}
