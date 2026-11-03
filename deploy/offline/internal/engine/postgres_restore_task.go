package engine

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const PostgresRestoreKind = "logical-restore"
const PostgresRestoreInputRole = "restore-postgres-config"
const PostgresRestoreOutputRole = "restore-postgres-result"
const PostgresRestoreSourceRole = "restore-postgres"
const PostgresRestoreOutputDestination = "/var/lib/projectone/postgres-restore"
const MinimumPostgresRestoreArchiveBytes = archiveReadbackReserveBytes

type PostgresRestoreProfile struct {
	SourceOwner    Ownership            `json:"source_owner"`
	SourceUID      int                  `json:"source_uid"`
	SourceGID      int                  `json:"source_gid"`
	ArchiveSHA256  string               `json:"archive_sha256"`
	ArchiveBytes   int64                `json:"archive_bytes"`
	ExpectedSource PostgresBackupSource `json:"expected_source"`
	ExpectedFiles  []BackupArchiveFile  `json:"expected_files"`
}

// Existing backup records omit Restore. The optional profile selects only this
// fixed SQL client operation, never caller-supplied commands or restore flags.
const postgresEmptyTargetPredicate = `NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname NOT IN ('public','information_schema') AND nspname !~ '^pg_') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_extension WHERE extname<>'plpgsql') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_event_trigger) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_publication) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_subscription)`

const postgresRestoreProgram = `umask 077
root=/var/lib/projectone/postgres-restore/private
dump=/var/lib/postgresql/private
test -d "$root"
test -f "$dump/database.dump"
test ! -L "$dump/database.dump"
for name in before.json source.json tools.txt; do
  test ! -e "$root/$name"
  test ! -L "$root/$name"
done
pg_restore --version > "$root/tools.txt"
psql -X -A -t --set=ON_ERROR_STOP=1 --no-password --host="$1" --port="$2" --dbname="$3" --username="$4" --command="SELECT pg_catalog.json_build_object('database',pg_catalog.current_database(),'operator',CURRENT_USER,'server_version_number',pg_catalog.current_setting('server_version_num')::integer,'empty',(` + postgresEmptyTargetPredicate + `))" > "$root/before.json"
psql -X -A -t --set=ON_ERROR_STOP=1 --no-password --host="$1" --port="$2" --dbname="$3" --username="$4" --command="SELECT 1 / CASE WHEN (` + postgresEmptyTargetPredicate + `) THEN 1 ELSE 0 END" > /dev/null
pg_restore --exit-on-error --single-transaction --no-owner --no-privileges --no-password --host="$1" --port="$2" --dbname="$3" --username="$4" "$dump/database.dump"
psql -X -A -t --set=ON_ERROR_STOP=1 --no-password --host="$1" --port="$2" --dbname="$3" --username="$4" --command="SELECT pg_catalog.json_build_object('database',pg_catalog.current_database(),'operator',CURRENT_USER,'server_version_number',pg_catalog.current_setting('server_version_num')::integer,'tenant_id',(SELECT tenant_id::text FROM public.installation_state WHERE id=1 AND bootstrap_complete),'schema_version',(SELECT max(version) FROM public.schema_migrations),'vector_version',(SELECT extversion FROM pg_catalog.pg_extension WHERE extname='vector'))" > "$root/source.json"
`

func (t PostgresBackupTask) postgresKind() string {
	if t.Restore != nil {
		return PostgresRestoreKind
	}
	return PostgresBackupKind
}
func (t PostgresBackupTask) postgresOutputDestination() string {
	if t.Restore != nil {
		return PostgresRestoreOutputDestination
	}
	return PostgresBackupOutputDestination
}
func (t PostgresBackupTask) postgresOwners() []Ownership {
	owners := []Ownership{t.InputOwner, t.OutputOwner}
	if t.Restore != nil {
		owners = append(owners, t.Restore.SourceOwner)
	}
	return owners
}

type postgresMount struct {
	owner    Ownership
	readOnly bool
	subpath  string
}

func (t PostgresBackupTask) postgresMounts() map[string]postgresMount {
	m := map[string]postgresMount{PostgresBackupInputDestination: {owner: t.InputOwner, readOnly: true}, t.postgresOutputDestination(): {owner: t.OutputOwner, readOnly: false}}
	if t.Restore != nil {
		m[PostgresBackupOutputDestination] = postgresMount{owner: t.Restore.SourceOwner, readOnly: true, subpath: postgresBackupRootName}
	}
	return m
}
func (t PostgresBackupTask) postgresMountOrder() []string {
	m := []string{PostgresBackupInputDestination, t.postgresOutputDestination()}
	if t.Restore != nil {
		m = append(m, PostgresBackupOutputDestination)
	}
	return m
}

func (t PostgresBackupTask) validRestoreProfile() bool {
	r := t.Restore
	if r == nil {
		return false
	}
	s := r.ExpectedSource
	if !r.SourceOwner.valid() || r.SourceOwner.ScopeID != t.Owner.ScopeID || r.SourceOwner.OperationID != t.Owner.OperationID || r.SourceOwner.Role != PostgresRestoreSourceRole+"-"+t.Owner.OperationID || r.SourceUID != t.UID || r.SourceGID != t.GID || !wire.ValidSHA256(r.ArchiveSHA256) || r.ArchiveBytes < MinimumPostgresRestoreArchiveBytes || r.ArchiveBytes > maximumBackupArchiveBytes || !s.Valid() || len(r.ExpectedFiles) != 4 {
		return false
	}
	seen := map[string]bool{}
	wanted := map[string]bool{"database.dump": true, "contents.list": true, "source.json": true, "tools.txt": true}
	var total int64
	for _, f := range r.ExpectedFiles {
		if !wanted[f.Name] || seen[f.Name] || f.Bytes < 1 || f.Bytes > r.ArchiveBytes || !wire.ValidSHA256(f.SHA256) || total > r.ArchiveBytes-f.Bytes {
			return false
		}
		seen[f.Name] = true
		total += f.Bytes
	}
	return true
}

func (t PostgresBackupTask) restoreSourceArchiveTask(uid, gid int) PostgresBackupTask {
	s := t.Restore.ExpectedSource
	return PostgresBackupTask{Owner: Ownership{t.Owner.ScopeID, t.Owner.OperationID, "database-" + PostgresBackupKind}, InputOwner: Ownership{t.Owner.ScopeID, t.Owner.OperationID, PostgresBackupInputRole + "-" + t.Owner.OperationID}, OutputOwner: Ownership{t.Owner.ScopeID, t.Owner.OperationID, PostgresBackupOutputRole + "-" + t.Owner.OperationID}, Image: t.Image, UID: uid, GID: gid, NetworkID: t.NetworkID, Peers: t.Peers, Host: t.Host, Port: t.Port, Database: s.Database, User: s.Operator}
}
func restoreFilesMatch(a, b []BackupArchiveFile) bool {
	if len(a) != 4 || len(b) != 4 {
		return false
	}
	values := map[string]BackupArchiveFile{}
	for _, f := range a {
		if _, ok := values[f.Name]; ok {
			return false
		}
		values[f.Name] = f
	}
	for _, f := range b {
		if values[f.Name] != f {
			return false
		}
		delete(values, f.Name)
	}
	return len(values) == 0
}

func (c *Client) InspectPostgresRestoreSource(ctx context.Context, t PostgresBackupTask, id string) (PostgresBackupArchive, error) {
	if !t.valid() || t.Restore == nil {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_PROFILE_REQUIRED")
	}
	before, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil {
		return PostgresBackupArchive{}, e
	}
	if before.State.Running {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_NONRUNNING_RESTORE_TASK_REQUIRED")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.config.LoadSeconds)*time.Second)
	defer cancel()
	address := unixHTTPAuthority + "/v" + c.policy.APIVersion + "/containers/" + id + "/archive?" + (url.Values{"path": {PostgresBackupOutputDestination}}).Encode()
	req, e := http.NewRequestWithContext(bounded, http.MethodGet, address, nil)
	if e != nil {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_SOURCE_REQUEST_INVALID")
	}
	response, e := c.http.Do(req)
	if e != nil {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_SOURCE_READBACK_FAILED")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_SOURCE_RESPONSE_INVALID")
	}
	maximum := t.Restore.ArchiveBytes + archiveReadbackReserveBytes
	if maximum > maximumBackupArchiveBytes {
		maximum = maximumBackupArchiveBytes
	}
	result, e := readPostgresArchiveWithRoot(response.Body, io.Discard, t.restoreSourceArchiveTask(t.UID, t.GID), maximum, c.config.ResponseBytes, t.UID, t.GID)
	if e != nil || !reflect.DeepEqual(result.Source, t.Restore.ExpectedSource) || !restoreFilesMatch(result.Files, t.Restore.ExpectedFiles) {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_SOURCE_BYTES_OR_METADATA_CHANGED")
	}
	after, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil || after.State != before.State {
		return PostgresBackupArchive{}, failure("OFFLINE_ENGINE_RESTORE_TASK_CHANGED_DURING_READBACK")
	}
	return result, nil
}
