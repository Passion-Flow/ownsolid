package imageregistry

import (
	"context"
	"encoding/json"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type GCProtection struct {
	PinID           string `json:"pin_id"`
	ManifestDigest  string `json:"manifest_digest"`
	ArchiveSHA256   string `json:"archive_sha256"`
	InventorySHA256 string `json:"inventory_sha256"`
}
type GCReceipt struct {
	Pins   []GCProtection `json:"pins"`
	SHA256 string         `json:"sha256"`
}

// CheckGCProtection is used both before and after the actual centrally owned
// stopped/read-only Distribution GC. It does not claim to toggle remote GC mode.
// All retained ready pins, not only current running refs, require their owned tag.
func (s *Service) CheckGCProtection(ctx context.Context, a identity.Auth, workspace string) (GCReceipt, error) {
	out := GCReceipt{Pins: []GCProtection{}}
	e := s.actor(ctx, a, workspace, true, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		rows, e := q.Query(ctx, statement(q, "SELECT "+pinPG+" FROM image_registry_pins WHERE tenant_id=$1 AND workspace_id=$2 AND state<>'deleted' ORDER BY id FOR UPDATE", "SELECT "+pinMY+" FROM image_registry_pins WHERE tenant_id=? AND workspace_id=? AND state<>'deleted' ORDER BY id FOR UPDATE", w.TenantID, w.ID))
		if e != nil {
			return e
		}
		pins := []Pin{}
		for rows.Next() {
			p, e := scanPin(rows)
			if e != nil {
				rows.Close()
				return e
			}
			pins = append(pins, p)
			if len(pins) > s.config.MaximumPins {
				rows.Close()
				return failure("REGISTRY_CAPACITY_REQUIRED")
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, p := range pins {
			if p.State != "ready" || p.backend != s.remote.identity {
				return failure("REGISTRY_RECOVERY_REQUIRED")
			}
			image, e := readCached(ctx, s.config, p.ID)
			if e != nil {
				return e
			}
			if e = cachedMatches(p, image); e != nil {
				return e
			}
			repo := repository(p.tenant, p.WorkspaceID, p.ID)
			if e = s.remote.VerifyTag(ctx, repo, image.Descriptors[0]); e != nil {
				return failure("REGISTRY_GC_TAG_DRIFT")
			}
			for _, d := range image.Descriptors {
				if e = s.remote.Verify(ctx, repo, d); e != nil {
					return e
				}
			}
			out.Pins = append(out.Pins, GCProtection{p.ID, *p.ManifestDigest, *p.ArchiveSHA256, *p.inventorySHA})
		}
		raw, _ := json.Marshal(out.Pins)
		out.SHA256 = digestBytes(raw)
		return nil
	})
	if e != nil {
		return GCReceipt{}, safeError(e)
	}
	return out, nil
}
