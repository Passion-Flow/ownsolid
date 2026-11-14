package imageregistry

import (
	"context"
	"encoding/json"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func holdWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, pin, reference string) error {
	var live bool
	e := q.QueryRow(ctx, statement(q, `SELECT released_at IS NULL FROM image_registry_offline_refs WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 AND id=$4 FOR UPDATE`, `SELECT released_at IS NULL FROM image_registry_offline_refs WHERE tenant_id=? AND workspace_id=? AND pin_id=? AND id=? FOR UPDATE`, w.TenantID, w.ID, pin, reference)).Scan(&live)
	if e != nil {
		return e
	}
	if !live {
		return failure("REGISTRY_REFERENCE_RELEASED")
	}
	return nil
}
func (s *Service) OfflineManifest(ctx context.Context, a identity.Auth, workspace, pin, reference string) (wire.RegistryOfflineManifest, error) {
	if !validID(pin) || !validID(reference) {
		return wire.RegistryOfflineManifest{}, failure("VALIDATION_FAILED")
	}
	var out wire.RegistryOfflineManifest
	e := s.actor(ctx, a, workspace, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		p, e := pinWithin(ctx, q, w.TenantID, w.ID, pin)
		if e != nil {
			return e
		}
		if p.State != "ready" {
			return failure("REGISTRY_PIN_UNAVAILABLE")
		}
		if e = holdWithin(ctx, q, w, pin, reference); e != nil {
			return e
		}
		var image cachedImage
		if json.Unmarshal(p.inventory, &image) != nil || cachedMatches(p, image) != nil {
			return failure("REGISTRY_CONTENT_INVALID")
		}
		proof, _ := json.Marshal(image.Proof)
		out = wire.RegistryOfflineManifest{ReferenceID: reference, Image: wire.RegistryImageIdentity{ReleaseProof: proof, PinID: p.ID, ImageManifestDigest: image.Inventory.ManifestDigest, ImageConfigDigest: image.Inventory.ConfigDigest, ArchiveSHA256: image.Archive.TransportSHA256, ArchiveBytes: image.Archive.TransportBytes, ExpandedBytes: image.Inventory.ExpandedBytes, SourceArchiveSHA256: image.ReleaseArchiveSHA256, SourceManifestSHA256: image.ReleaseManifestSHA256, OS: image.Inventory.OS, Architecture: image.Inventory.Architecture}}
		return out.Validate()
	})
	if e != nil {
		return wire.RegistryOfflineManifest{}, e
	}
	return out, nil
}
func (s *Service) OfflineRange(ctx context.Context, a identity.Auth, workspace, pin, reference string, offset, length int64) ([]byte, string, error) {
	if !validID(pin) || !validID(reference) {
		return nil, "", failure("VALIDATION_FAILED")
	}
	var raw []byte
	e := s.actor(ctx, a, workspace, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		p, e := pinWithin(ctx, q, w.TenantID, w.ID, pin)
		if e != nil {
			return e
		}
		if p.State != "ready" {
			return failure("REGISTRY_PIN_UNAVAILABLE")
		}
		if e = holdWithin(ctx, q, w, pin, reference); e != nil {
			return e
		}
		raw, e = readArchiveRange(ctx, s.config, p, offset, length)
		if e != nil {
			return e
		}
		return s.license.CheckWithin(ctx, q)
	})
	if e != nil {
		return nil, "", e
	}
	return raw, digestBytes(raw), nil
}
