package plugins

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

func (s *Service) Import(ctx context.Context, a identity.Auth, input ImportRequest, key, request string) (Version, error) {
	var out Version
	if !admin(a) {
		return out, failure("ACCESS_DENIED")
	}
	if _, e := s.repository(); e != nil {
		return out, pluginError(e)
	}
	if !s.Identity.Config.Plugins.Enabled {
		return out, failure("PLUGIN_UNAVAILABLE")
	}
	if !validReason(input.Reason) || len(input.Capsule) > base64.StdEncoding.EncodedLen(s.policy().CapsuleBytes) {
		return out, failure("VALIDATION_FAILED")
	}
	capsule, e := base64.StdEncoding.Strict().DecodeString(input.Capsule)
	if e != nil || base64.StdEncoding.EncodeToString(capsule) != input.Capsule {
		return out, failure("PLUGIN_PACKAGE_INVALID")
	}
	keys, e := s.Identity.Config.Plugins.PublicKeys()
	if e != nil {
		return out, failure("PLUGIN_TRUST_KEY_INVALID")
	}
	verified, e := wire.Verify(capsule, input.SHA256, keys, s.policy().PackageLimits())
	if e != nil {
		return out, e
	}
	release, _ := wire.ParseVersion(verified.Manifest.Version)
	e = s.adminTransaction(ctx, a, true, func(ctx context.Context, tx transaction.Executor, tenant string) error {
		previous, digest, e := s.intent(ctx, tx, a, tenant, "import", key, input)
		if e != nil {
			return e
		}
		if previous != "" {
			v, e := scanVersion(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+versionFields+" FROM plugin_versions WHERE tenant_id=$1 AND id=$2", "SELECT "+mysqlVersionFields+" FROM plugin_versions WHERE tenant_id=? AND id=?", nativePluginVersionByID, tenant, previous)))
			if e != nil {
				return e
			}
			out = s.present(v)
			return nil
		}
		existing, e := scanVersion(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+versionFields+" FROM plugin_versions WHERE tenant_id=$1 AND plugin_id=$2 AND release_version=$3", "SELECT "+mysqlVersionFields+" FROM plugin_versions WHERE tenant_id=? AND plugin_id=? AND release_version=?", nativePluginVersionRelease, tenant, verified.Manifest.PluginID, verified.Manifest.Version)))
		if e == nil {
			if existing.CapsuleSHA256 != verified.CapsuleSHA256 || existing.KeyID != verified.KeyID || existing.ManifestSHA256 != verified.ManifestSHA256 {
				return failure("PLUGIN_VERSION_CONFLICT")
			}
			if e = saveIntent(ctx, tx, a, tenant, "import", key, existing.ID, digest); e != nil {
				return e
			}
			out = s.present(existing)
			return nil
		}
		if e.Error() != "PLUGIN_VERSION_NOT_FOUND" {
			return e
		}
		var count int
		if e = tx.QueryRow(ctx, pluginSQL(tx, "SELECT count(*) FROM plugin_versions WHERE tenant_id=$1", "SELECT count(*) FROM plugin_versions WHERE tenant_id=?", nativePluginVersionCount, tenant)).Scan(&count); e != nil {
			return e
		}
		if count >= s.policy().MaximumVersions {
			return failure("PLUGIN_LIMIT_EXCEEDED")
		}
		id := uuid.NewString()
		manifest, _ := json.Marshal(verified.Manifest)
		if e = pluginExecOne(ctx, tx, pluginSQL(tx, `INSERT INTO plugin_versions(id,tenant_id,plugin_id,release_version,version_major,version_minor,version_patch,abi,executor,capsule,capsule_sha256,manifest,manifest_sha256,key_id,state,imported_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'imported',$15)`, `INSERT INTO plugin_versions(id,tenant_id,plugin_id,release_version,version_major,version_minor,version_patch,abi,executor,capsule,capsule_sha256,manifest,manifest_sha256,key_id,state,imported_by) VALUES(?,?,?,?,?,?,?,?,?,?,?,CAST(? AS JSON),?,?,'imported',?)`, nativePluginVersionCreate, id, tenant, verified.Manifest.PluginID, verified.Manifest.Version, release[0], release[1], release[2], verified.Manifest.ABI, verified.Manifest.Executor, capsule, verified.CapsuleSHA256, manifest, verified.ManifestSHA256, verified.KeyID, a.Me.User.ID)); e != nil {
			return e
		}
		if e = saveIntent(ctx, tx, a, tenant, "import", key, id, digest); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, tenant, "", "plugin.imported", "plugin_version", id, input.Reason, request); e != nil {
			return e
		}
		v, e := scanVersion(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+versionFields+" FROM plugin_versions WHERE tenant_id=$1 AND id=$2", "SELECT "+mysqlVersionFields+" FROM plugin_versions WHERE tenant_id=? AND id=?", nativePluginVersionByID, tenant, id)))
		if e != nil {
			return e
		}
		out = s.present(v)
		return nil
	})
	if e != nil {
		return Version{}, e
	}
	return out, nil
}
func (s *Service) Get(ctx context.Context, a identity.Auth, id string) (Version, error) {
	var out Version
	if !validID(id) {
		return out, failure("PLUGIN_VERSION_NOT_FOUND")
	}
	e := s.adminTransaction(ctx, a, false, func(ctx context.Context, tx transaction.Executor, tenant string) error {
		v, e := scanVersion(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+versionFields+" FROM plugin_versions WHERE tenant_id=$1 AND id=$2", "SELECT "+mysqlVersionFields+" FROM plugin_versions WHERE tenant_id=? AND id=?", nativePluginVersionByID, tenant, id)))
		if e != nil {
			return e
		}
		out = s.present(v)
		return nil
	})
	if e != nil {
		return Version{}, e
	}
	return out, nil
}
func (s *Service) Install(ctx context.Context, a identity.Auth, id string, input RevisionReason, key, request string) (Version, error) {
	var out Version
	if !validID(id) || !validReason(input.Reason) || input.ExpectedRevision < 1 || input.ExpectedRevision >= MaximumRevision {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.adminTransaction(ctx, a, true, func(ctx context.Context, tx transaction.Executor, tenant string) error {
		previous, digest, e := s.intent(ctx, tx, a, tenant, "install:"+id, key, input)
		if e != nil {
			return e
		}
		v, e := scanVersion(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+versionFields+" FROM plugin_versions WHERE tenant_id=$1 AND id=$2 FOR UPDATE", "SELECT "+mysqlVersionFields+" FROM plugin_versions WHERE tenant_id=? AND id=? FOR UPDATE", nativePluginVersionLocked, tenant, id)))
		if e != nil {
			return e
		}
		if previous != "" {
			if previous != id {
				return failure("IDEMPOTENCY_CONFLICT")
			}
			out = s.present(v)
			return nil
		}
		if v.Revision != input.ExpectedRevision {
			return failure("PLUGIN_REVISION_CONFLICT")
		}
		if _, e = s.verified(v); e != nil {
			return e
		}
		if v.State != InstalledState {
			if e = pluginExecOne(ctx, tx, pluginSQL(tx, "UPDATE plugin_versions SET state='installed',revision=revision+1,installed_at=clock_timestamp(),uninstalled_at=NULL WHERE tenant_id=$1 AND id=$2", "UPDATE plugin_versions SET state='installed',revision=revision+1,installed_at=UTC_TIMESTAMP(6),uninstalled_at=NULL WHERE tenant_id=? AND id=?", nativePluginVersionInstall, tenant, id)); e != nil {
				return e
			}
			if e = s.audit(ctx, tx, a, tenant, "", "plugin.installed", "plugin_version", id, input.Reason, request); e != nil {
				return e
			}
		}
		if e = saveIntent(ctx, tx, a, tenant, "install:"+id, key, id, digest); e != nil {
			return e
		}
		v, e = scanVersion(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+versionFields+" FROM plugin_versions WHERE tenant_id=$1 AND id=$2", "SELECT "+mysqlVersionFields+" FROM plugin_versions WHERE tenant_id=? AND id=?", nativePluginVersionByID, tenant, id)))
		if e != nil {
			return e
		}
		out = s.present(v)
		return nil
	})
	if e != nil {
		return Version{}, e
	}
	return out, nil
}
