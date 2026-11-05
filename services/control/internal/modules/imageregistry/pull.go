package imageregistry

import (
	"context"
	"encoding/json"
	"io"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type pullGrant struct {
	id, tenant, workspace, pin, node, task, deployment, actor string
	session                                                   string
	tokenHash                                                 []byte
	revision, epoch                                           int64
	expires                                                   time.Time
}

func (s *Service) pull(ctx context.Context, node, credential, task, lease string, operation func(context.Context, transaction.Executor, Pin, pullGrant, wire.InferenceCommand) error) error {
	if ctx == nil || !validID(node) || !validID(task) || operation == nil {
		return failure("AGENT_UNAUTHORIZED")
	}
	store, e := s.store()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.OperationTimeout())
	defer cancel()
	var g pullGrant
	// Discovery is not authorization and returns no projection or remote URL.
	e = store.QueryRow(ctx, statement(store, `SELECT id::text,tenant_id::text,workspace_id::text,pin_id::text,node_id::text,task_id::text,deployment_id::text,created_by::text,revision,actor_epoch,expires_at,created_session_id::text,created_token_hash FROM image_registry_grants WHERE node_id=$1 AND task_id=$2 AND revoked_at IS NULL AND expires_at>clock_timestamp()`, `SELECT id,tenant_id,workspace_id,pin_id,node_id,task_id,deployment_id,created_by,revision,actor_epoch,expires_at,created_session_id,created_token_hash FROM image_registry_grants WHERE node_id=? AND task_id=? AND revoked_at IS NULL AND expires_at>UTC_TIMESTAMP(6)`, node, task)).Scan(&g.id, &g.tenant, &g.workspace, &g.pin, &g.node, &g.task, &g.deployment, &g.actor, &g.revision, &g.epoch, &g.expires, &g.session, &g.tokenHash)
	if e != nil {
		return failure("AGENT_UNAUTHORIZED")
	}
	return safeError(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		// Rebuild only from immutable persisted server facts. The original exact
		// session/source/epoch must still be live; no client can supply this Auth.
		actor := identity.Auth{SessionID: g.session, TokenHash: g.tokenHash, Epoch: g.epoch,
			Me: identity.Me{Audience: "admin", User: identity.User{ID: g.actor, IsPlatformAdmin: true}}}
		if e := s.identity.CurrentWithin(ctx, q, actor); e != nil {
			return e
		}
		// Every node pulling for one person shares these rows, so on PostgreSQL the node
		// lock fences the read instead of a Workspace lock (ADR 0020).
		checkScope := func() error {
			scope, e := s.workspaces.CurrentScopeWithin(ctx, q, actor, g.workspace)
			if e != nil {
				return e
			}
			if scope.ID != g.workspace || scope.TenantID != g.tenant || scope.Status != "active" {
				return failure("ACCESS_DENIED")
			}
			return nil
		}
		if q.Dialect() != transaction.PostgreSQL {
			if e := checkScope(); e != nil {
				return e
			}
		}
		if e := s.license.CheckWithin(ctx, q); e != nil {
			return e
		}
		n, t, c, e := s.nodes.InferenceRegistryWithin(ctx, q, g.tenant, g.workspace, node, credential, task, lease)
		if e != nil {
			return e
		}
		if q.Dialect() == transaction.PostgreSQL {
			if e := checkScope(); e != nil {
				return e
			}
		}
		var requester string
		if e = q.QueryRow(ctx, statement(q, `SELECT requested_by::text FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4`, `SELECT requested_by FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, g.tenant, g.workspace, node, task)).Scan(&requester); e != nil {
			return e
		}
		if requester != g.actor {
			return failure("ACCESS_DENIED")
		}
		_, e = s.deployments.RegistryReferenceWithin(ctx, q, n, t, c, func(ctx context.Context, q transaction.Executor, d deployments.RegistryDeployment) error {
			if d.DeploymentID != g.deployment || d.Revision != g.revision {
				return failure("REGISTRY_GRANT_STALE")
			}
			p, e := pinWithin(ctx, q, g.tenant, g.workspace, g.pin)
			if e != nil {
				return e
			}
			if p.State != "ready" || p.ManifestDigest == nil || *p.ManifestDigest != d.ImageManifestDigest || p.ConfigDigest == nil || *p.ConfigDigest != d.ImageConfigDigest {
				return failure("REGISTRY_PIN_UNAVAILABLE")
			}
			var live bool
			if e = q.QueryRow(ctx, statement(q, `SELECT g.revoked_at IS NULL AND g.expires_at>clock_timestamp() AND r.released_at IS NULL AND g.created_by=$1 AND g.actor_epoch=$2 FROM image_registry_grants g JOIN image_registry_deployment_refs r ON r.tenant_id=g.tenant_id AND r.workspace_id=g.workspace_id AND r.deployment_id=g.deployment_id AND r.pin_id=g.pin_id WHERE g.id=$3 AND g.tenant_id=$4 AND g.workspace_id=$5 AND g.node_id=$6 AND g.task_id=$7 FOR UPDATE OF g`, `SELECT g.revoked_at IS NULL AND g.expires_at>UTC_TIMESTAMP(6) AND r.released_at IS NULL AND g.created_by=? AND g.actor_epoch=? FROM image_registry_grants g JOIN image_registry_deployment_refs r ON r.tenant_id=g.tenant_id AND r.workspace_id=g.workspace_id AND r.deployment_id=g.deployment_id AND r.pin_id=g.pin_id WHERE g.id=? AND g.tenant_id=? AND g.workspace_id=? AND g.node_id=? AND g.task_id=? FOR UPDATE`, g.actor, g.epoch, g.id, g.tenant, g.workspace, node, task)).Scan(&live); e != nil {
				return e
			}
			if !live {
				return failure("REGISTRY_GRANT_REVOKED")
			}
			if e = operation(ctx, q, p, g, c); e != nil {
				return e
			}
			if _, _, _, e = s.nodes.InferenceRegistryWithin(ctx, q, g.tenant, g.workspace, node, credential, task, lease); e != nil {
				return e
			}
			if e = s.identity.CurrentWithin(ctx, q, actor); e != nil {
				return e
			}
			if e = s.license.CheckWithin(ctx, q); e != nil {
				return e
			}
			if e = q.QueryRow(ctx, statement(q, `SELECT expires_at>clock_timestamp() AND revoked_at IS NULL FROM image_registry_grants WHERE id=$1`, `SELECT expires_at>UTC_TIMESTAMP(6) AND revoked_at IS NULL FROM image_registry_grants WHERE id=?`, g.id)).Scan(&live); e != nil {
				return e
			}
			if !live {
				return failure("REGISTRY_GRANT_REVOKED")
			}
			return nil
		})
		return e
	}))
}
func (s *Service) PullManifest(ctx context.Context, node, credential, task, lease string) (wire.RegistryManifest, error) {
	var out wire.RegistryManifest
	e := s.pull(ctx, node, credential, task, lease, func(ctx context.Context, q transaction.Executor, p Pin, g pullGrant, c wire.InferenceCommand) error {
		var image cachedImage
		if json.Unmarshal(p.inventory, &image) != nil || cachedMatches(p, image) != nil {
			return failure("REGISTRY_CONTENT_INVALID")
		}
		proof, _ := json.Marshal(image.Proof)
		out = wire.RegistryManifest{ReleaseProof: proof, PinID: p.ID, GrantID: g.id, DeploymentID: g.deployment, Revision: g.revision, ImageManifestDigest: image.Inventory.ManifestDigest, ImageConfigDigest: image.Inventory.ConfigDigest, ArchiveSHA256: image.Archive.TransportSHA256, ArchiveBytes: image.Archive.TransportBytes, ExpandedBytes: image.Inventory.ExpandedBytes, SourceArchiveSHA256: image.ReleaseArchiveSHA256, SourceManifestSHA256: image.ReleaseManifestSHA256, OS: image.Inventory.OS, Architecture: image.Inventory.Architecture, ExpiresAt: g.expires.UTC()}
		return out.Validate(c)
	})
	if e != nil {
		return wire.RegistryManifest{}, e
	}
	return out, nil
}
func readArchiveRange(ctx context.Context, c Config, p Pin, offset, length int64) ([]byte, error) {
	if p.ArchiveBytes == nil || offset < 0 || length < 1 || length > wire.RegistryMaximumRangeBytes || offset > *p.ArchiveBytes-length {
		return nil, failure("REGISTRY_RANGE_INVALID")
	}
	root, e := privateRoot(c.StoreRoot)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	f, e := root.Open(p.ID + "/archive.tar")
	if e != nil {
		return nil, failure("REGISTRY_CONTENT_MISSING")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != *p.ArchiveBytes {
		return nil, failure("REGISTRY_CONTENT_INVALID")
	}
	raw := make([]byte, length)
	if _, e = io.ReadFull(io.NewSectionReader(f, offset, length), raw); e != nil {
		return nil, failure("REGISTRY_CONTENT_INVALID")
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	return raw, nil
}
func (s *Service) PullRange(ctx context.Context, node, credential, task, lease string, r wire.RegistryRange) ([]byte, string, error) {
	if r.Validate() != nil {
		return nil, "", failure("REGISTRY_RANGE_INVALID")
	}
	var raw []byte
	e := s.pull(ctx, node, credential, task, lease, func(ctx context.Context, q transaction.Executor, p Pin, g pullGrant, c wire.InferenceCommand) error {
		if r.GrantID != g.id {
			return failure("REGISTRY_GRANT_STALE")
		}
		var e error
		raw, e = readArchiveRange(ctx, s.config, p, r.Offset, r.Length)
		return e
	})
	if e != nil {
		return nil, "", e
	}
	return raw, digestBytes(raw), nil
}
