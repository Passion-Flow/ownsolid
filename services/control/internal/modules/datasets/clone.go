package datasets

import (
	"context"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"strconv"
)

func (s *Service) CloneVersion(ctx context.Context, a identity.Auth, workspace, dataset, versionID, reason, key, requestID string) (Draft, error) {
	if !validReason(reason) || !wire.ValidID(key) {
		return Draft{}, failure("VALIDATION_FAILED")
	}
	if s.Store == nil {
		return Draft{}, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	var version Version
	var source *os.File
	var size int64
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		parent, e := lockedDataset(ctx, tx, w, dataset)
		if e != nil {
			return e
		}
		if parent.RetiredAt != nil {
			return failure("DATASET_RETIRED")
		}
		version, e = lockedVersion(ctx, tx, w, dataset, versionID)
		if e != nil {
			return e
		}
		if version.RetiredAt != nil {
			return failure("DATASET_VERSION_RETIRED")
		}
		// Preserve all pre-dedup effective rows, including duplicate bridges between source groups.
		source, size, e = s.Store.OpenEffective(ctx, version.ID, version.AttemptID, version.EffectiveSourceSHA256, s.policy().Limits.MaximumBytes)
		return e
	})
	if e != nil {
		if source != nil {
			source.Close()
		}
		return Draft{}, e
	}
	defer source.Close()
	draft, e := s.createDraft(ctx, a, workspace, dataset, DraftRequest{SourceName: "version-" + strconv.FormatInt(version.Ordinal, 10) + ".jsonl", ExpectedBytes: size, SourceSHA256: version.EffectiveSourceSHA256, Reason: reason}, key, requestID, versionID)
	if e != nil {
		return Draft{}, e
	}
	if draft.SourceReceivedAt != nil {
		return draft, nil
	}
	return s.Upload(ctx, a, workspace, dataset, draft.ID, version.EffectiveSourceSHA256, size, source, requestID)
}
