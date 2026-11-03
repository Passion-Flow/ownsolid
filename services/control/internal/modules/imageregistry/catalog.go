package imageregistry

import (
	"context"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

type ReleaseImage struct {
	ID             string   `json:"id"`
	ManifestDigest string   `json:"manifest_digest"`
	ConfigDigest   string   `json:"config_digest"`
	ArchiveBytes   int64    `json:"archive_bytes"`
	Licenses       []string `json:"licenses"`
}

func (s *Service) ReleaseImages(ctx context.Context, a identity.Auth, workspace string) ([]ReleaseImage, error) {
	out := []ReleaseImage{}
	e := s.actor(ctx, a, workspace, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		proof, e := bundle.VerifyPrepared(ctx, s.config.Verification, s.config.PreparedReleaseRoot, s.config.ReleaseArchiveSHA256, s.config.ReleaseManifestSHA256)
		if e != nil {
			return failure("REGISTRY_RELEASE_INVALID")
		}
		for _, asset := range proof.Manifest.Assets {
			if asset.Kind == "image_archive" {
				out = append(out, ReleaseImage{asset.ID, asset.ImageID, asset.ImageConfigDigest, asset.Size, append([]string(nil), asset.Licenses...)})
			}
		}
		return s.license.CheckWithin(ctx, q)
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

type DeploymentReference struct {
	DeploymentID    string     `json:"deployment_id"`
	NodeID          string     `json:"node_id"`
	CreatedRevision int64      `json:"created_revision"`
	ReleasedAt      *time.Time `json:"released_at"`
}
type OfflineReference struct {
	ID         string     `json:"id"`
	ReleasedAt *time.Time `json:"released_at"`
}
type References struct {
	Deployments []DeploymentReference `json:"deployments"`
	Offline     []OfflineReference    `json:"offline"`
}

func (s *Service) References(ctx context.Context, a identity.Auth, workspace, pin string) (References, error) {
	out := References{Deployments: []DeploymentReference{}, Offline: []OfflineReference{}}
	e := s.actor(ctx, a, workspace, true, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if _, e := pinWithin(ctx, q, w.TenantID, w.ID, pin); e != nil {
			return e
		}
		rows, e := q.Query(ctx, statement(q, `SELECT deployment_id::text,node_id::text,created_revision,released_at FROM image_registry_deployment_refs WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 ORDER BY deployment_id LIMIT $4`, `SELECT deployment_id,node_id,created_revision,released_at FROM image_registry_deployment_refs WHERE tenant_id=? AND workspace_id=? AND pin_id=? ORDER BY deployment_id LIMIT ?`, w.TenantID, w.ID, pin, s.config.MaximumReferences+1))
		if e != nil {
			return e
		}
		for rows.Next() {
			var r DeploymentReference
			if e = rows.Scan(&r.DeploymentID, &r.NodeID, &r.CreatedRevision, &r.ReleasedAt); e != nil {
				rows.Close()
				return e
			}
			out.Deployments = append(out.Deployments, r)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(out.Deployments) > s.config.MaximumReferences {
			return failure("REGISTRY_CAPACITY_REQUIRED")
		}
		rows, e = q.Query(ctx, statement(q, `SELECT id::text,released_at FROM image_registry_offline_refs WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 ORDER BY id LIMIT $4`, `SELECT id,released_at FROM image_registry_offline_refs WHERE tenant_id=? AND workspace_id=? AND pin_id=? ORDER BY id LIMIT ?`, w.TenantID, w.ID, pin, s.config.MaximumReferences+1))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var r OfflineReference
			if e = rows.Scan(&r.ID, &r.ReleasedAt); e != nil {
				return e
			}
			out.Offline = append(out.Offline, r)
		}
		if len(out.Offline) > s.config.MaximumReferences {
			return failure("REGISTRY_CAPACITY_REQUIRED")
		}
		return rows.Err()
	})
	if e != nil {
		return References{}, e
	}
	return out, nil
}
