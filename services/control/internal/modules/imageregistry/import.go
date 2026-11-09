package imageregistry

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/google/uuid"
)

// importSelected does not repeat external work on an idempotent replay. An interrupted
// pin is retained and only an explicit new Reconcile intent may inspect/retry it.
func (s *Service) importSelected(ctx context.Context, a identity.Auth, m Mutation, asset string, acknowledgeLicenses bool) (Pin, error) {
	if !acknowledgeLicenses {
		return Pin{}, failure("REGISTRY_LICENSE_ACK_REQUIRED")
	}
	if m.validate() != nil || asset == "" || len(asset) > 160 {
		return Pin{}, failure("VALIDATION_FAILED")
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.OperationTimeout())
	defer cancel()
	select {
	case s.imports <- struct{}{}:
		defer func() { <-s.imports }()
	default:
		return Pin{}, failure("REGISTRY_IMPORT_BUSY")
	}
	intent := struct{ Asset, Reason, Archive, Manifest string }{
		asset, m.Reason, s.config.ReleaseArchiveSHA256, s.config.ReleaseManifestSHA256,
	}
	fingerprint, e := mutationFingerprint(intent)
	if e != nil {
		return Pin{}, e
	}
	var out Pin
	replayed := false
	e = s.actor(ctx, a, m.WorkspaceID, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		target, replay, e := lookupMutationWithin(ctx, q, a, w, "import", m.Key, fingerprint)
		replayed = replay
		if e != nil || !replay {
			return e
		}
		out, e = pinWithin(ctx, q, w.TenantID, w.ID, target)
		return e
	})
	if e != nil {
		return Pin{}, e
	}
	if replayed {
		return out, nil
	}

	// Unapproved input must not consume a durable slot. Verify outside SQL,
	// then recheck the actor and idempotency key when reserving the intent.
	signed, proof, e := bundle.PreparedImageProof(ctx, s.config.Verification, s.config.PreparedReleaseRoot, intent.Archive, intent.Manifest, asset)
	if e != nil {
		return Pin{}, failure("REGISTRY_RELEASE_INVALID")
	}
	pin, token := uuid.NewString(), uuid.NewString()
	fresh := false
	e = s.actor(ctx, a, m.WorkspaceID, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		target, replay, e := mutationWithin(ctx, q, a, w, "import", pin, m, intent)
		if e != nil {
			return e
		}
		pin = target
		if replay {
			out, e = pinWithin(ctx, q, w.TenantID, w.ID, pin)
			return e
		}
		var count int
		var retained int64
		if e = q.QueryRow(ctx, statement(q, `SELECT COUNT(*),COALESCE(SUM(COALESCE(archive_bytes,$1)),0)*2 FROM image_registry_pins WHERE tenant_id=$2 AND state<>'deleted'`, `SELECT COUNT(*),COALESCE(SUM(COALESCE(archive_bytes,?)),0)*2 FROM image_registry_pins WHERE tenant_id=? AND state<>'deleted'`, s.config.MaximumArchiveBytes, w.TenantID)).Scan(&count, &retained); e != nil {
			return e
		}
		if count >= s.config.MaximumPins || retained > s.config.MaximumRetainedBytes-s.config.MaximumArchiveBytes*2 {
			return failure("REGISTRY_CAPACITY_REQUIRED")
		}
		_, e = q.Exec(ctx, statement(q, `INSERT INTO image_registry_pins(id,tenant_id,workspace_id,asset_id,source_archive_sha256,source_manifest_sha256,backend_identity,state,created_by,actor_epoch) VALUES($1,$2,$3,$4,$5,$6,$7,'importing',$8,$9)`, `INSERT INTO image_registry_pins(id,tenant_id,workspace_id,asset_id,source_archive_sha256,source_manifest_sha256,backend_identity,state,created_by,actor_epoch) VALUES(?,?,?,?,?,?,?,'importing',?,?)`, pin, w.TenantID, w.ID, asset, s.config.ReleaseArchiveSHA256, s.config.ReleaseManifestSHA256, s.remote.identity, a.Me.User.ID, a.Epoch))
		if e != nil {
			return e
		}
		_, e = q.Exec(ctx, statement(q, `INSERT INTO image_registry_imports(tenant_id,workspace_id,pin_id,owner_token,phase) VALUES($1,$2,$3,$4,'prepared')`, `INSERT INTO image_registry_imports(tenant_id,workspace_id,pin_id,owner_token,phase) VALUES(?,?,?,?,'prepared')`, w.TenantID, w.ID, pin, token))
		if e != nil {
			return e
		}
		fresh = true
		return registryAudit(ctx, q, a, w, "import.started", pin, m)
	})
	if e != nil {
		return Pin{}, e
	}
	if !fresh {
		return out, nil
	}
	image, e := prepareImage(ctx, s.config, pin, signed, proof)
	if e != nil {
		return Pin{}, e
	}
	e = s.actor(ctx, a, m.WorkspaceID, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		p, e := pinWithin(ctx, q, w.TenantID, w.ID, pin)
		if e != nil {
			return e
		}
		if p.actor != a.Me.User.ID || p.epoch != a.Epoch || p.State != "importing" || p.backend != s.remote.identity {
			return failure("REGISTRY_INTENT_STALE")
		}
		if e = ownerWithin(ctx, q, p, token); e != nil {
			return e
		}
		return bindPreparedWithin(ctx, q, p, token, image)
	})
	if e != nil {
		return Pin{}, e
	}
	p, e := s.Get(ctx, a, m.WorkspaceID, pin)
	if e != nil {
		return Pin{}, e
	}
	if e = s.push(ctx, p, image); e != nil {
		s.markUnknown(ctx, a, m, pin, token)
		return Pin{}, e
	}
	return s.finish(ctx, a, m, pin, token, image)
}
func ownerWithin(ctx context.Context, q transaction.Executor, p Pin, token string) error {
	var actual string
	e := q.QueryRow(ctx, statement(q, `SELECT owner_token::text FROM image_registry_imports WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 FOR UPDATE`, `SELECT owner_token FROM image_registry_imports WHERE tenant_id=? AND workspace_id=? AND pin_id=? FOR UPDATE`, p.tenant, p.WorkspaceID, p.ID)).Scan(&actual)
	if e != nil {
		return e
	}
	if actual != token {
		return failure("REGISTRY_INTENT_STALE")
	}
	return nil
}
func phaseWithin(ctx context.Context, q transaction.Executor, p Pin, token, phase string) error {
	changed, e := q.Exec(ctx, statement(q, `UPDATE image_registry_imports SET phase=$1 WHERE tenant_id=$2 AND workspace_id=$3 AND pin_id=$4 AND owner_token=$5`, `UPDATE image_registry_imports SET phase=? WHERE tenant_id=? AND workspace_id=? AND pin_id=? AND owner_token=?`, phase, p.tenant, p.WorkspaceID, p.ID, token))
	if e != nil {
		return e
	}
	if changed != 1 {
		return failure("REGISTRY_INTENT_STALE")
	}
	return nil
}
func stateWithin(ctx context.Context, q transaction.Executor, p Pin, state string) error {
	changed, e := q.Exec(ctx, statement(q, `UPDATE image_registry_pins SET state=$1,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND id=$4 AND revision=$5`, `UPDATE image_registry_pins SET state=?,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=? AND revision=?`, state, p.tenant, p.WorkspaceID, p.ID, p.Revision))
	if e != nil {
		return e
	}
	if changed != 1 {
		return failure("REGISTRY_INTENT_STALE")
	}
	return nil
}
func (s *Service) push(ctx context.Context, p Pin, image cachedImage) error {
	repo := repository(p.tenant, p.WorkspaceID, p.ID)
	for _, d := range image.Descriptors {
		if d.Kind != "manifest" {
			if e := s.remote.PutBlob(ctx, repo, d, filepath.Join(s.config.StoreRoot, p.ID, "blobs", d.Digest[7:])); e != nil {
				return e
			}
		}
	}
	root, e := privateRoot(s.config.StoreRoot)
	if e != nil {
		return e
	}
	defer root.Close()
	raw, e := root.ReadFile(p.ID + "/manifest.json")
	if e != nil {
		return failure("REGISTRY_CONTENT_MISSING")
	}
	return s.remote.PutManifest(ctx, repo, image.Descriptors[0], raw)
}
func (s *Service) markUnknown(ctx context.Context, a identity.Auth, m Mutation, pin, token string) {
	// Best effort only; any failed/cancelled acknowledgement remains unknown. No
	// independent background retry or successful response is manufactured.
	_ = s.actor(ctx, a, m.WorkspaceID, true, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		p, e := pinWithin(ctx, q, w.TenantID, w.ID, pin)
		if e != nil {
			return e
		}
		if e = ownerWithin(ctx, q, p, token); e != nil {
			return e
		}
		if e = stateWithin(ctx, q, p, "unknown"); e != nil {
			return e
		}
		if e = phaseWithin(ctx, q, p, token, "unknown"); e != nil {
			return e
		}
		return registryAudit(ctx, q, a, w, "import.unknown", pin, m)
	})
}
func (s *Service) finish(ctx context.Context, a identity.Auth, m Mutation, pin, token string, image cachedImage) (Pin, error) {
	var out Pin
	e := s.actor(ctx, a, m.WorkspaceID, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		p, e := pinWithin(ctx, q, w.TenantID, w.ID, pin)
		if e != nil {
			return e
		}
		if p.backend != s.remote.identity || p.State != "importing" && p.State != "unknown" {
			return failure("REGISTRY_INTENT_STALE")
		}
		if e = ownerWithin(ctx, q, p, token); e != nil {
			return e
		}
		if e = cachedMatches(p, image); e != nil {
			return e
		}
		if e = stateWithin(ctx, q, p, "ready"); e != nil {
			return e
		}
		if e = phaseWithin(ctx, q, p, token, "completed"); e != nil {
			return e
		}
		if e = registryAudit(ctx, q, a, w, "import.ready", pin, m); e != nil {
			return e
		}
		out, e = pinWithin(ctx, q, w.TenantID, w.ID, pin)
		return e
	})
	if e != nil {
		return Pin{}, e
	}
	return out, nil
}

// Reconcile observes exact persisted bytes; retryAbsent is a new explicit human
// intent to upload a proved-missing closure, never an automatic uncertain retry.
func (s *Service) reconcileSelected(ctx context.Context, a identity.Auth, m Mutation, pin string, retryAbsent bool) (Pin, error) {
	if m.validate() != nil || !validID(pin) {
		return Pin{}, failure("VALIDATION_FAILED")
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.OperationTimeout())
	defer cancel()
	select {
	case s.imports <- struct{}{}:
		defer func() { <-s.imports }()
	default:
		return Pin{}, failure("REGISTRY_IMPORT_BUSY")
	}
	token := uuid.NewString()
	var p Pin
	replay := false
	e := s.actor(ctx, a, m.WorkspaceID, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		_, repeat, e := mutationWithin(ctx, q, a, w, "reconcile", pin, m, struct {
			Pin, Reason string
			Retry       bool
		}{pin, m.Reason, retryAbsent})
		if e != nil {
			return e
		}
		replay = repeat
		p, e = pinWithin(ctx, q, w.TenantID, w.ID, pin)
		if e != nil {
			return e
		}
		if repeat {
			return nil
		}
		if p.backend != s.remote.identity || p.State != "unknown" && p.State != "importing" && p.State != "ready" {
			return failure("REGISTRY_RECOVERY_REQUIRED")
		}
		if p.State == "ready" {
			if e = stateWithin(ctx, q, p, "unknown"); e != nil {
				return e
			}
			p.State = "unknown"
		}
		phase := "remote"
		if p.ManifestDigest == nil {
			phase = "prepared"
		}
		_, e = q.Exec(ctx, statement(q, `UPDATE image_registry_imports SET owner_token=$1,phase=$2 WHERE tenant_id=$3 AND workspace_id=$4 AND pin_id=$5`, `UPDATE image_registry_imports SET owner_token=?,phase=? WHERE tenant_id=? AND workspace_id=? AND pin_id=?`, token, phase, p.tenant, p.WorkspaceID, p.ID))
		if e != nil {
			return e
		}
		return registryAudit(ctx, q, a, w, "reconcile.started", pin, m)
	})
	if e != nil {
		return Pin{}, e
	}
	if replay {
		return p, nil
	}
	var image cachedImage
	if p.ManifestDigest == nil {
		image, e = recoverPrepared(ctx, s.config, pin)
		if e != nil {
			return Pin{}, e
		}
		e = s.actor(ctx, a, m.WorkspaceID, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
			current, e := pinWithin(ctx, q, w.TenantID, w.ID, pin)
			if e != nil {
				return e
			}
			return bindPreparedWithin(ctx, q, current, token, image)
		})
		if e != nil {
			return Pin{}, e
		}
		p, e = s.Get(ctx, a, m.WorkspaceID, pin)
		if e != nil {
			return Pin{}, e
		}
	} else {
		image, e = readCached(ctx, s.config, pin)
		if e != nil {
			return Pin{}, e
		}
	}
	if e = cachedMatches(p, image); e != nil {
		return Pin{}, e
	}
	missing := false
	for _, d := range image.Descriptors {
		e = s.remote.Verify(ctx, repository(p.tenant, p.WorkspaceID, p.ID), d)
		if e != nil {
			if e.Error() != "REGISTRY_CONTENT_MISSING" {
				return Pin{}, e
			}
			missing = true
		}
	}
	if missing {
		if !retryAbsent {
			return Pin{}, failure("REGISTRY_RETRY_REQUIRED")
		}
		if e = s.push(ctx, p, image); e != nil {
			s.markUnknown(ctx, a, m, pin, token)
			return Pin{}, e
		}
	}
	return s.finish(ctx, a, m, pin, token, image)
}

func bindPreparedWithin(ctx context.Context, q transaction.Executor, p Pin, token string, image cachedImage) error {
	if e := ownerWithin(ctx, q, p, token); e != nil {
		return e
	}
	var archive, manifest string
	if e := q.QueryRow(ctx, statement(q, `SELECT source_archive_sha256,source_manifest_sha256 FROM image_registry_pins WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3`, `SELECT source_archive_sha256,source_manifest_sha256 FROM image_registry_pins WHERE tenant_id=? AND workspace_id=? AND id=?`, p.tenant, p.WorkspaceID, p.ID)).Scan(&archive, &manifest); e != nil {
		return e
	}
	if p.ManifestDigest != nil || p.AssetID != image.Asset.ID || archive != image.ReleaseArchiveSHA256 || manifest != image.ReleaseManifestSHA256 {
		return failure("REGISTRY_INTENT_STALE")
	}
	raw, e := json.Marshal(image)
	if e != nil {
		return e
	}
	_, e = q.Exec(ctx, statement(q, `UPDATE image_registry_pins SET manifest_digest=$1,config_digest=$2,archive_sha256=$3,archive_bytes=$4,inventory=$5,inventory_sha256=$6,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$7 AND workspace_id=$8 AND id=$9`, `UPDATE image_registry_pins SET manifest_digest=?,config_digest=?,archive_sha256=?,archive_bytes=?,inventory=CAST(? AS JSON),inventory_sha256=?,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`, image.Inventory.ManifestDigest, image.Inventory.ConfigDigest, image.Archive.TransportSHA256, image.Archive.TransportBytes, raw, strings.TrimPrefix(digestBytes(raw), "sha256:"), p.tenant, p.WorkspaceID, p.ID))
	if e != nil {
		return e
	}
	for ordinal, d := range image.Descriptors {
		_, e = q.Exec(ctx, statement(q, `INSERT INTO image_registry_blobs(tenant_id,workspace_id,pin_id,ordinal,digest,size,media_type,kind) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, `INSERT INTO image_registry_blobs(tenant_id,workspace_id,pin_id,ordinal,digest,size,media_type,kind) VALUES(?,?,?,?,?,?,?,?)`, p.tenant, p.WorkspaceID, p.ID, ordinal, d.Digest, d.Size, d.MediaType, d.Kind))
		if e != nil {
			return e
		}
	}
	return phaseWithin(ctx, q, p, token, "remote")
}

func (s *Service) Import(ctx context.Context, a identity.Auth, m Mutation, asset string, acknowledgeLicenses bool) (Pin, error) {
	out, e := s.importSelected(ctx, a, m, asset, acknowledgeLicenses)
	if e != nil {
		return Pin{}, safeError(e)
	}
	return out, nil
}
func (s *Service) Reconcile(ctx context.Context, a identity.Auth, m Mutation, pin string, retryAbsent bool) (Pin, error) {
	out, e := s.reconcileSelected(ctx, a, m, pin, retryAbsent)
	if e != nil {
		return Pin{}, safeError(e)
	}
	return out, nil
}
