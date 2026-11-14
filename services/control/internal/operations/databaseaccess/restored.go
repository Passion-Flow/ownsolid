package databaseaccess

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const maximumRestoreContextBytes = 16 * 1024

func LoadRestoredContext(name string) (release.RestoredRuntimeContext, error) {
	var value release.RestoredRuntimeContext
	raw, e := readPrivate(name, maximumRestoreContextBytes)
	if e != nil || release.StrictJSON(raw, &value) != nil || !value.Valid() {
		return value, errors.New("RUNTIME_ACCESS_RESTORE_CONTEXT_INVALID")
	}
	return value, nil
}

func verifyRestoredTarget(ctx context.Context, lease *pgxpool.Conn, t target, r release.RestoredRuntimeContext) error {
	if !r.Valid() || r.TargetDatabase != t.Database {
		return errors.New("RUNTIME_ACCESS_RESTORE_TARGET_MISMATCH")
	}
	var operator, tenant string
	var version, users, admins, tenants, workspaces int
	var complete bool
	e := lease.QueryRow(ctx, `SELECT CURRENT_USER,tenant_id::text,bootstrap_complete,(SELECT max(version) FROM schema_migrations),(SELECT count(*) FROM users),(SELECT count(*) FROM users WHERE status='active' AND is_platform_admin),(SELECT count(*) FROM tenants),(SELECT count(*) FROM workspaces) FROM installation_state WHERE id=1`).Scan(&operator, &tenant, &complete, &version, &users, &admins, &tenants, &workspaces)
	if e != nil || operator != r.TargetOperator || tenant != r.TenantID || version != r.SchemaVersion || !complete || users < 1 || admins < 1 || tenants != 1 || workspaces < 1 {
		return errors.New("RUNTIME_ACCESS_RESTORE_STATE_MISMATCH")
	}
	return nil
}

func PrepareRestored(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput, r release.RestoredRuntimeContext) (string, error) {
	return prepare(ctx, p, lease, input, &r)
}

func restoredAuditMetadata(r release.RestoredRuntimeContext, role string) []byte {
	raw, _ := json.Marshal(map[string]any{"restore_id": r.RestoreID, "source_scope_id": r.SourceScopeID, "target_scope_id": r.TargetScopeID, "backup_manifest_sha256": r.BackupManifestSHA256, "sql_archive_sha256": r.SQLArchiveSHA256, "runtime_role": role, "schema_version": r.SchemaVersion})
	return raw
}
