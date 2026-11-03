package training

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func (s *Service) OpenConfiguredStore(ctx context.Context) error {
	c := s.Identity.Config.Training
	if !c.Enabled() {
		return s.VerifyStored(ctx)
	}
	if s.Identity.Config.Models.ArtifactRoot == "" || s.Datasets == nil || s.Datasets.Store == nil {
		return fail("TRAINING_INPUT_STORES_REQUIRED")
	}
	var tenant string
	if e := s.trainingStorageTransaction(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		return q.QueryRow(ctx, acceptedAdapterSQL(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1")).Scan(&tenant)
	}); e != nil {
		return fail("TRAINING_INSTALLATION_REQUIRED")
	}
	// Physical retained-file validation uses versioned wire ceilings. New-job policy cannot retroactively invalidate accepted history.
	// Each live upload remains bounded separately by its job's frozen recipe resources.
	store, e := OpenStore(c.ArtifactRoot, tenant, s.policy().MinimumFreeBytes, wire.MaximumCheckpointBytes, wire.MaximumCheckpointFiles)
	if e != nil {
		return e
	}
	s.Store = store
	if e = s.VerifyStored(ctx); e != nil {
		store.Close()
		s.Store = nil
		return e
	}
	return nil
}

// VerifyStored checks accepted references, including non-successful historical attempts, without adopting partial output stages.
func (s *Service) VerifyStored(ctx context.Context) error {
	if s.Store == nil {
		var required bool
		e := s.trainingStorageTransaction(ctx, true, func(ctx context.Context, q transaction.Executor) error {
			return q.QueryRow(ctx, acceptedAdapterSQL(q, `SELECT EXISTS(SELECT 1 FROM training_uploads u JOIN installation_state i ON i.tenant_id=u.tenant_id WHERE i.id=1) OR EXISTS(SELECT 1 FROM training_jobs j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND j.state NOT IN ('succeeded','failed','cancelled'))`, `SELECT EXISTS(SELECT 1 FROM training_uploads u JOIN installation_state i ON i.tenant_id=u.tenant_id WHERE i.id=1) OR EXISTS(SELECT 1 FROM training_jobs j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND j.state NOT IN ('succeeded','failed','cancelled'))`)).Scan(&required)
		})
		if e != nil {
			return e
		}
		if required {
			return fail("TRAINING_STORAGE_REQUIRED")
		}
		return nil
	}
	s.Store.mu.Lock()
	e := s.Store.checkBase()
	s.Store.mu.Unlock()
	if e != nil {
		return e
	}
	references, e := s.storedReferences(ctx, false)
	if e != nil {
		return e
	}
	if references.TenantID != s.Store.namespace.TenantID {
		return fail("TRAINING_STORE_NAMESPACE_INVALID")
	}
	for _, descriptor := range references.Outputs {
		if e = s.Store.VerifyPublished(ctx, descriptor); e != nil {
			return e
		}
	}
	return nil
}
