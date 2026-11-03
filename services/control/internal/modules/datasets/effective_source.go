package datasets

import (
	"context"
	"errors"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// PublishedSourcesAvailable reports the initialized dataset dependency, independently of GPU execution configuration.
func (s *Service) PublishedSourcesAvailable() bool { return s.Store != nil }

// PublishedSourceInTransaction resolves an immutable effective source under the caller's current scoped authority locks.
func (s *Service) PublishedSourceInTransaction(ctx context.Context, tx pgx.Tx, tenant, workspace, dataset, version string, retained bool) (Version, error) {
	return s.PublishedSourceWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, dataset, version, retained)
}
func (s *Service) PublishedSourceSnapshot(ctx context.Context, a identity.Auth, workspace, dataset, version string, retained bool) (Version, error) {
	if s == nil || ctx == nil || s.Identity == nil || s.Workspaces == nil || s.Workspaces.Identity != s.Identity {
		return Version{}, failure("DATASET_IDENTITY_REQUIRED")
	}
	if !wire.ValidID(workspace) || a.Me.Audience != "portal" && !isAdmin(a) {
		return Version{}, failure("ACCESS_DENIED")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return Version{}, failure("DATASET_STORAGE_MISMATCH")
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return Version{}, publishedSourceError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	var out Version
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if w.Status != "active" {
			return failure("ACCESS_DENIED")
		}
		out, e = s.PublishedSourceWithin(ctx, q, w.TenantID, w.ID, dataset, version, retained)
		return e
	})
	if e != nil {
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
			return Version{}, publishedSourceError(e)
		}
		return Version{}, e
	}
	return out, nil
}

// OpenPublishedSource must follow scoped snapshot authorization. Hashing/streaming runs outside business locks.
func (s *Service) OpenPublishedSource(ctx context.Context, value Version, maximum int64) (*os.File, int64, error) {
	if s.Store == nil {
		return nil, 0, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	return s.Store.OpenEffective(ctx, value.ID, value.AttemptID, value.EffectiveSourceSHA256, maximum)
}

// PublishedSourceSize is internal metadata for a previously authorized immutable source snapshot.
func (s *Service) PublishedSourceSize(value Version) (int64, error) {
	if s.Store == nil {
		return 0, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	return s.Store.EffectiveSize(value.ID, value.AttemptID)
}
