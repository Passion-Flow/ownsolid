package imageregistry

import (
	"context"
	"encoding/json"
	"os"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func eligibleWithin(ctx context.Context, q transaction.Executor, p Pin) error {
	// Pin lock serializes every late reference insertion. Do not acquire a reverse
	// deployment lock here; a concurrent creator waits and observes deleting.
	var held bool
	var query transaction.Statement
	if q.Dialect() == transaction.MySQL {
		query = transaction.SQL(transaction.MySQL, `SELECT EXISTS(SELECT 1 FROM image_registry_deployment_refs WHERE tenant_id=? AND workspace_id=? AND pin_id=? AND released_at IS NULL) OR EXISTS(SELECT 1 FROM image_registry_offline_refs WHERE tenant_id=? AND workspace_id=? AND pin_id=? AND released_at IS NULL)`, p.tenant, p.WorkspaceID, p.ID, p.tenant, p.WorkspaceID, p.ID)
	} else {
		query = transaction.SQL(transaction.PostgreSQL, `SELECT EXISTS(SELECT 1 FROM image_registry_deployment_refs WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 AND released_at IS NULL) OR EXISTS(SELECT 1 FROM image_registry_offline_refs WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 AND released_at IS NULL)`, p.tenant, p.WorkspaceID, p.ID)
	}
	e := q.QueryRow(ctx, query).Scan(&held)
	if e != nil {
		return e
	}
	if held {
		return failure("REGISTRY_REFERENCE_HELD")
	}
	return nil
}
func (s *Service) Remove(ctx context.Context, a identity.Auth, m Mutation, pin string) (Pin, error) {
	out, e := s.remove(ctx, a, m, pin, false)
	if e != nil {
		return Pin{}, safeError(e)
	}
	return out, nil
}

// ReconcileRemoval is an explicit new observed-delete intent; normal key replay
// never repeats an unacknowledged registry mutation.
func (s *Service) ReconcileRemoval(ctx context.Context, a identity.Auth, m Mutation, pin string) (Pin, error) {
	out, e := s.remove(ctx, a, m, pin, true)
	if e != nil {
		return Pin{}, safeError(e)
	}
	return out, nil
}
func (s *Service) remove(ctx context.Context, a identity.Auth, m Mutation, pin string, reconcile bool) (Pin, error) {
	if m.validate() != nil || !validID(pin) {
		return Pin{}, failure("VALIDATION_FAILED")
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.OperationTimeout())
	defer cancel()
	var p Pin
	fresh := false
	e := s.actor(ctx, a, m.WorkspaceID, true, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		operation := "remove"
		if reconcile {
			operation = "reconcile"
		}
		_, repeat, e := mutationWithin(ctx, q, a, w, operation, pin, m, struct {
			Pin, Reason string
			Removal     bool
		}{pin, m.Reason, true})
		if e != nil {
			return e
		}
		p, e = pinWithin(ctx, q, w.TenantID, w.ID, pin)
		if e != nil {
			return e
		}
		if repeat || p.State == "deleted" {
			return nil
		}
		if p.backend != s.remote.identity || p.ManifestDigest == nil || p.State != "ready" && !(reconcile && p.State == "deleting") {
			return failure("REGISTRY_RECOVERY_REQUIRED")
		}
		if e = eligibleWithin(ctx, q, p); e != nil {
			return e
		}
		if p.State != "deleting" {
			if e = stateWithin(ctx, q, p, "deleting"); e != nil {
				return e
			}
		}
		fresh = true
		return registryAudit(ctx, q, a, w, "remove.started", pin, m)
	})
	if e != nil {
		return Pin{}, e
	}
	if !fresh {
		if reconcile && p.State == "deleted" {
			if e = s.cleanup(ctx, p); e != nil {
				return Pin{}, e
			}
		}
		return p, nil
	}
	if e = s.remote.Remove(ctx, repository(p.tenant, p.WorkspaceID, p.ID), *p.ManifestDigest); e != nil {
		return Pin{}, e
	}
	e = s.actor(ctx, a, m.WorkspaceID, true, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := pinWithin(ctx, q, w.TenantID, w.ID, pin)
		if e != nil {
			return e
		}
		if current.State != "deleting" || current.backend != s.remote.identity {
			return failure("REGISTRY_INTENT_STALE")
		}
		if e = eligibleWithin(ctx, q, current); e != nil {
			return e
		}
		if e = stateWithin(ctx, q, current, "deleted"); e != nil {
			return e
		}
		if e = registryAudit(ctx, q, a, w, "remove.completed", pin, m); e != nil {
			return e
		}
		p, e = pinWithin(ctx, q, w.TenantID, w.ID, pin)
		return e
	})
	if e != nil {
		return Pin{}, e
	}
	// Tombstone commits before local cleanup, preventing a late reference race.
	if e = s.cleanup(ctx, p); e != nil {
		return Pin{}, e
	}
	return p, nil
}
func (s *Service) cleanup(ctx context.Context, p Pin) error {
	if p.State != "deleted" || !validID(p.ID) {
		return failure("REGISTRY_RECOVERY_REQUIRED")
	}
	var image cachedImage
	if json.Unmarshal(p.inventory, &image) != nil {
		return failure("REGISTRY_CONTENT_INVALID")
	}
	if e := cachedMatches(p, image); e != nil {
		return e
	}
	root, e := privateRoot(s.config.StoreRoot)
	if e != nil {
		return e
	}
	defer root.Close()
	// Exact metadata-derived paths only. Unexpected files prevent directory removal
	// and are retained as evidence; no recursive/wildcard deletion is used.
	seen := map[string]bool{}
	for _, d := range image.Descriptors {
		if seen[d.Digest] {
			continue
		}
		seen[d.Digest] = true
		if e = root.Remove(p.ID + "/blobs/" + d.Digest[7:]); e != nil && !os.IsNotExist(e) {
			return failure("REGISTRY_CLEANUP_REQUIRED")
		}
	}
	for _, name := range []string{"blobs", "archive.tar", "manifest.json", "inventory.json"} {
		if e = root.Remove(p.ID + "/" + name); e != nil && !os.IsNotExist(e) {
			return failure("REGISTRY_CLEANUP_REQUIRED")
		}
	}
	if e = root.Remove(p.ID); e != nil {
		return failure("REGISTRY_CLEANUP_REQUIRED")
	}
	if e = syncDirectory(root, "."); e != nil {
		return failure("REGISTRY_CLEANUP_REQUIRED")
	}
	return nil
}
