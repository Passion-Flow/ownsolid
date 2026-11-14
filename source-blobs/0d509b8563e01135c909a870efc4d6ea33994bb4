package postgresbackup

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/imageregistry"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const restoreIntentName = "intent.json"
const restoreFilesName = "files.json"
const restoreCompleteName = "complete.json"

type RestoreTarget struct {
	SystemIdentifier string `json:"system_identifier"`
	DatabaseOID      uint32 `json:"database_oid"`
	Database         string `json:"database"`
	Operator         string `json:"operator"`
	BackendPID       int    `json:"backend_pid"`
	ServerVersion    int    `json:"server_version"`
	Marker           string `json:"marker"`
}
type RestoreIntent struct {
	Format              string                           `json:"format"`
	Request             release.PostgreSQLRestoreRequest `json:"request"`
	RequestSHA256       string                           `json:"request_sha256"`
	BackupID            string                           `json:"backup_id"`
	SourceScopeID       string                           `json:"source_scope_id"`
	ArchiveSHA256       string                           `json:"archive_sha256"`
	TenantID            string                           `json:"tenant_id"`
	SourceDataSHA256    string                           `json:"source_data_sha256"`
	Target              RestoreTarget                    `json:"target"`
	Roots               []RootIdentity                   `json:"roots"`
	RecordRoot          RootIdentity                     `json:"record_root"`
	CreatedAt           string                           `json:"created_at"`
	ConfigurationSHA256 string                           `json:"configuration_sha256"`
	RegistryRoot        *RootIdentity                    `json:"registry_root,omitempty"`
}
type RestoreFiles struct {
	Format       string                                       `json:"format"`
	RestoreID    string                                       `json:"restore_id"`
	IntentSHA256 string                                       `json:"intent_sha256"`
	Roots        []RootIdentity                               `json:"roots"`
	TargetTrees  []TreeReadback                               `json:"target_trees"`
	Roundtrip    []StoreReadback                              `json:"roundtrip"`
	Registry     *imageregistry.OperatorSnapshotImportReceipt `json:"registry,omitempty"`
}
type RestoreCompletion struct {
	Format              string                         `json:"format"`
	RestoreID           string                         `json:"restore_id"`
	IntentSHA256        string                         `json:"intent_sha256"`
	FilesSHA256         string                         `json:"files_sha256"`
	ManifestSHA256      string                         `json:"manifest_sha256"`
	ArchiveSHA256       string                         `json:"archive_sha256"`
	TargetBindingSHA256 string                         `json:"target_binding_sha256"`
	PostDataSHA256      string                         `json:"post_data_sha256"`
	RuntimeContext      release.RestoredRuntimeContext `json:"runtime_context"`
	CompletedAt         string                         `json:"completed_at"`
	CurrentLockHeld     bool                           `json:"current_lock_held"`
	Qualification       string                         `json:"qualification"`
}

// Same exact empty-target predicate as the retained native PostgreSQL restore
// owner. It admits initial plpgsql only; partial vector/schema objects refuse.
const emptyPostgresTarget = `NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname NOT IN ('public','information_schema') AND nspname !~ '^pg_') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_extension WHERE extname<>'plpgsql') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_event_trigger) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_publication) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_subscription)`

func restoreRequestDigest(r release.PostgreSQLRestoreRequest) string {
	raw, e := canonical(r)
	if e != nil {
		return ""
	}
	return digest(raw)
}
func targetBinding(t RestoreTarget) string {
	t.BackendPID = 0
	raw, e := canonical(t)
	if e != nil {
		return ""
	}
	return digest(raw)
}

func readRestoreTarget(ctx context.Context, lease *pgxpool.Conn, r release.PostgreSQLRestoreRequest, empty bool) (RestoreTarget, error) {
	bad := errors.New("POSTGRES_RESTORE_EXACT_LOCKED_TARGET_REQUIRED")
	var t RestoreTarget
	var held, super, ordinary bool
	if ctx == nil || lease == nil || !r.Valid() {
		return t, bad
	}
	q := `SELECT (SELECT system_identifier::text FROM pg_catalog.pg_control_system()),d.oid,d.datname,CURRENT_USER,pg_catalog.pg_backend_pid(),pg_catalog.current_setting('server_version_num')::integer,COALESCE(pg_catalog.shobj_description(d.oid,'pg_database'),''),NOT d.datistemplate AND d.datallowconn,(SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname=CURRENT_USER),EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE locktype='advisory' AND pid=pg_catalog.pg_backend_pid() AND classid=0::oid AND objid=$1::bigint::oid AND objsubid=1 AND mode='ExclusiveLock' AND granted AND database=d.oid) FROM pg_catalog.pg_database d WHERE d.datname=pg_catalog.current_database()`
	if lease.QueryRow(ctx, q, database.InstallationLockID).Scan(&t.SystemIdentifier, &t.DatabaseOID, &t.Database, &t.Operator, &t.BackendPID, &t.ServerVersion, &t.Marker, &ordinary, &super, &held) != nil || !held || !super || !ordinary || t.DatabaseOID == 0 || t.BackendPID < 1 || t.SystemIdentifier == "" || t.ServerVersion != 180006 || t.Database != r.TargetDatabase || t.Operator != r.TargetOperator || t.Marker != "projectone:kubernetes-restore:"+r.TargetScopeID {
		return RestoreTarget{}, bad
	}
	if empty {
		var clean bool
		if lease.QueryRow(ctx, "SELECT "+emptyPostgresTarget).Scan(&clean) != nil || !clean {
			return RestoreTarget{}, errors.New("POSTGRES_RESTORE_FRESH_DATABASE_REQUIRED")
		}
	}
	return t, nil
}

func newRestoreIntent(r release.PostgreSQLRestoreRequest, m Manifest, t RestoreTarget, roots []RootIdentity, configuration string, record RootIdentity) RestoreIntent {
	return RestoreIntent{Format: "projectone.postgres.restore-intent.v1", Request: r, RequestSHA256: restoreRequestDigest(r), BackupID: m.Request.BackupID, SourceScopeID: m.Request.SourceScopeID, ArchiveSHA256: m.Archive.SHA256, TenantID: m.Database.TenantID, SourceDataSHA256: m.Database.ContentSHA256, Target: t, Roots: roots, RecordRoot: record, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), ConfigurationSHA256: configuration}
}
