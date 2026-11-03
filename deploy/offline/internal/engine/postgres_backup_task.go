package engine

import (
	"context"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const PostgresBackupKind = "logical-backup"
const PostgresBackupInputRole = "backup-postgres-config"
const PostgresBackupOutputRole = "backup-postgres"
const PostgresBackupInputDestination = "/run/projectone/postgres-backup"
const PostgresBackupOutputDestination = "/var/lib/postgresql"
const PostgresBackupPasswordName = "pgpass"
const postgresPeerAlias = "postgres"

// Fixed parameters are positional arguments, never shell source. No live
// PostgreSQL data directory is mounted into this client-only container.
const postgresBackupProgram = `umask 077
root=/var/lib/postgresql/private
test -d "$root"
for name in database.dump contents.list source.json tools.txt; do
  test ! -e "$root/$name"
  test ! -L "$root/$name"
done
pg_dump --version > "$root/tools.txt"
pg_restore --version >> "$root/tools.txt"
pg_dump --no-password --host="$1" --port="$2" --dbname="$3" --username="$4" --format=custom --file="$root/database.dump"
pg_restore --list --file="$root/contents.list" "$root/database.dump"
psql -X -A -t --set=ON_ERROR_STOP=1 --no-password --host="$1" --port="$2" --dbname="$3" --username="$4" --command="SELECT pg_catalog.json_build_object('database',pg_catalog.current_database(),'operator',CURRENT_USER,'server_version_number',pg_catalog.current_setting('server_version_num')::integer,'tenant_id',(SELECT tenant_id::text FROM public.installation_state WHERE id=1 AND bootstrap_complete),'schema_version',(SELECT max(version) FROM public.schema_migrations),'vector_version',(SELECT extversion FROM pg_catalog.pg_extension WHERE extname='vector'))" > "$root/source.json"
`

// PostgresBackupTask is a fixed private client operation. A scoped caller must
// durably record every effect; this primitive never retries an unknown effect.
type PostgresBackupTask struct {
	Restore     *PostgresRestoreProfile `json:"restore,omitempty"`
	Owner       Ownership               `json:"owner"`
	InputOwner  Ownership               `json:"input_owner"`
	OutputOwner Ownership               `json:"output_owner"`
	Image       string                  `json:"image"`
	UID         int                     `json:"uid"`
	GID         int                     `json:"gid"`
	NetworkID   string                  `json:"network_id"`
	Peers       map[string]string       `json:"peers"`
	Host        string                  `json:"host"`
	Port        int                     `json:"port"`
	Database    string                  `json:"database"`
	User        string                  `json:"user"`
}

func (t PostgresBackupTask) networkTask() DatabaseTask {
	return DatabaseTask{Owner: t.Owner, Kind: t.postgresKind(), NetworkID: t.NetworkID, Peers: t.Peers}
}
func (t PostgresBackupTask) name() string { return t.networkTask().name() }
func (t PostgresBackupTask) command() []string {
	if t.Restore != nil {
		return []string{"-eu", "-c", postgresRestoreProgram, "projectone-postgres-restore", t.Host, strconv.Itoa(t.Port), t.Database, t.User}
	}
	return []string{"-eu", "-c", postgresBackupProgram, "projectone-postgres-backup", t.Host, strconv.Itoa(t.Port), t.Database, t.User}
}
func backupConnectionPart(v string) bool {
	return len(v) > 0 && len(v) <= 63 && utf8.ValidString(v) && !strings.ContainsAny(v, "\x00\r\n")
}
func (t PostgresBackupTask) valid() bool {
	inputRole, outputRole := PostgresBackupInputRole, PostgresBackupOutputRole
	if t.Restore != nil {
		inputRole, outputRole = PostgresRestoreInputRole, PostgresRestoreOutputRole
		if !t.validRestoreProfile() {
			return false
		}
	}
	if !utilityIdentity(t.Owner, t.Image, t.UID, t.GID) || t.Owner.Role != "database-"+t.postgresKind() || !t.InputOwner.valid() || !t.OutputOwner.valid() || t.InputOwner.Role != inputRole+"-"+t.Owner.OperationID || t.OutputOwner.Role != outputRole+"-"+t.Owner.OperationID || t.InputOwner.OperationID != t.Owner.OperationID || t.OutputOwner.OperationID != t.Owner.OperationID || t.InputOwner.ScopeID != t.Owner.ScopeID || t.OutputOwner.ScopeID != t.Owner.ScopeID || t.InputOwner == t.OutputOwner || !containerIdentity.MatchString(t.NetworkID) || t.Host != postgresPeerAlias || t.Port < 1 || t.Port > 65535 || !backupConnectionPart(t.Database) || strings.ContainsAny(t.Database, "=") || strings.Contains(t.Database, "://") || !backupConnectionPart(t.User) || t.Database == "*" || t.User == "*" || t.Peers["postgres"] == "" || t.Peers["control"] == "" || len(t.Peers) < 2 || len(t.Peers) > 3 {
		return false
	}
	seen := map[string]bool{}
	for role, id := range t.Peers {
		if !runtimeService(role) || !containerIdentity.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func pgpassPart(v string) string { return strings.NewReplacer("\\", "\\\\", ":", "\\:").Replace(v) }

// PostgresBackupPasswordFile keeps the actual credential out of task metadata.
func PostgresBackupPasswordFile(t PostgresBackupTask, password string) (ConfigurationFile, error) {
	if !t.valid() || len(password) < 1 || len(password) > 4096 || !utf8.ValidString(password) || strings.ContainsAny(password, "\x00\r\n") {
		return ConfigurationFile{}, failure("OFFLINE_ENGINE_BACKUP_PASSWORD_INPUT_INVALID")
	}
	line := pgpassPart(t.Host) + ":" + strconv.Itoa(t.Port) + ":" + pgpassPart(t.Database) + ":" + pgpassPart(t.User) + ":" + pgpassPart(password) + "\n"
	return ConfigurationFile{Name: PostgresBackupPasswordName, Contents: []byte(line)}, nil
}
func (t PostgresBackupTask) environment(image Image) (map[string]string, bool) {
	env, ok := imageEnvironment(image.Config.Env)
	if !ok {
		return nil, false
	}
	for _, key := range []string{"PGPASSWORD", "PGSERVICE", "PGSERVICEFILE", "PGHOST", "PGHOSTADDR", "PGPORT", "PGDATABASE", "PGUSER", "PGOPTIONS", "PGSSLCERT", "PGSSLKEY", "PGSSLROOTCERT"} {
		if _, found := env[key]; found {
			return nil, false
		}
	}
	env["PGPASSFILE"] = PostgresBackupInputDestination + "/private/" + PostgresBackupPasswordName
	env["PGSSLMODE"] = "disable"
	env["PGAPPNAME"] = "projectone-offline-backup"
	if t.Restore != nil {
		env["PGAPPNAME"] = "projectone-offline-restore"
	}
	return env, true
}
func (c *Client) postgresBackupContext(ctx context.Context, t PostgresBackupTask) (Image, error) {
	if c.utilityPolicy() != nil || !t.valid() {
		return Image{}, failure("OFFLINE_ENGINE_BACKUP_CONTEXT_INVALID")
	}
	image, found, e := c.Inspect(ctx, t.Image)
	if e != nil {
		return image, e
	}
	if !found || image.OS != "linux" || image.Architecture != "amd64" || image.Config == nil || len(image.Config.Volumes) != 1 || image.Config.Volumes[PostgresBackupOutputDestination] == nil {
		return Image{}, failure("OFFLINE_ENGINE_BACKUP_DECLARED_IMAGE_VOLUME_REQUIRED")
	}
	if _, ok := t.environment(image); !ok {
		return Image{}, failure("OFFLINE_ENGINE_BACKUP_IMAGE_ENVIRONMENT_INVALID")
	}
	peer, found, e := c.InspectRuntime(ctx, t.Owner.ScopeID, "postgres")
	if e != nil {
		return image, e
	}
	peerEnvironment, valid := imageEnvironment(peer.Config.Env)
	if !found || peer.ID != t.Peers["postgres"] || peer.Image != t.Image || peer.Config.Image != t.Image || peer.Config.User != strconv.Itoa(t.UID)+":"+strconv.Itoa(t.GID) || !valid || peerEnvironment["POSTGRES_DB"] != t.Database || peerEnvironment["POSTGRES_USER"] != t.User {
		return image, failure("OFFLINE_ENGINE_BACKUP_DATABASE_PEER_MISMATCH")
	}
	for _, owner := range t.postgresOwners() {
		if _, found, e := c.InspectVolume(ctx, owner); e != nil {
			return image, e
		} else if !found {
			return image, failure("OFFLINE_ENGINE_BACKUP_OWNED_VOLUME_REQUIRED")
		}
	}
	return image, nil
}
func (c *Client) backupConsumers(ctx context.Context, t PostgresBackupTask, id string) error {
	for _, owner := range t.postgresOwners() {
		if e := c.noForeignConsumers(ctx, owner, id); e != nil {
			return e
		}
	}
	return nil
}
func (c *Client) InspectPostgresBackupTask(ctx context.Context, t PostgresBackupTask) (RuntimeContainer, bool, error) {
	image, e := c.postgresBackupContext(ctx, t)
	if e != nil {
		return RuntimeContainer{}, false, e
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+url.PathEscape(t.name())+"/json", nil, nil, c.config.RequestSeconds)
	if e != nil {
		return RuntimeContainer{}, false, e
	}
	if status == http.StatusNotFound {
		if e = c.databaseTaskNetwork(ctx, t.networkTask(), "", false); e != nil {
			return RuntimeContainer{}, false, e
		}
		return RuntimeContainer{}, false, nil
	}
	var result RuntimeContainer
	if status != http.StatusOK || wire.UniqueJSON(raw, &result) != nil {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_BACKUP_TASK_RESPONSE_INVALID")
	}
	h := result.HostConfig
	u := c.config.Utility
	expected, ok := t.environment(image)
	actual, ae := imageEnvironment(result.Config.Env)
	wanted := t.postgresMounts()
	if !containerIdentity.MatchString(result.ID) || result.Name != "/"+t.name() || result.Image != t.Image || result.Platform != "linux" || result.Config.Image != t.Image || result.Config.User != strconv.Itoa(t.UID)+":"+strconv.Itoa(t.GID) || !slices.Equal(result.Config.Entrypoint, []string{"/bin/sh"}) || !slices.Equal(result.Config.Cmd, t.command()) || result.Config.WorkingDir != "/" || !reflect.DeepEqual(result.Config.Volumes, image.Config.Volumes) || !t.Owner.matches(result.Config.Labels) || !ok || !ae || !reflect.DeepEqual(expected, actual) || h.NetworkMode != t.NetworkID || h.IpcMode != "private" || h.PidMode != "" || h.UTSMode != "" || h.UsernsMode != "" || h.CgroupnsMode != "private" || h.CgroupParent != "" || h.Runtime != "runc" || !h.ReadonlyRootfs || h.Privileged || h.AutoRemove || h.PublishAllPorts || h.Memory != u.MemoryBytes || h.MemorySwap != u.MemoryBytes || h.NanoCPUs != u.MilliCPU*nanosecondsPerMillicore || h.PIDs != u.PIDs || !slices.Equal(h.CapDrop, []string{"ALL"}) || len(h.CapAdd) > 0 || !slices.Equal(h.SecurityOpt, []string{"no-new-privileges:true"}) || len(h.Binds) > 0 || len(h.VolumesFrom) > 0 || len(h.Devices) > 0 || len(h.DeviceRequests) > 0 || len(h.PortBindings) > 0 || len(h.DNS) > 0 || len(h.ExtraHosts) > 0 || len(h.Links) > 0 || len(h.Tmpfs) > 0 || h.RestartPolicy.Name != "no" || h.RestartPolicy.MaximumRetryCount != 0 || h.LogConfig.Type != "none" || len(h.LogConfig.Config) > 0 || len(h.Mounts) != len(wanted) || len(result.Mounts) != len(wanted) || len(result.NetworkSettings.Networks) != 1 {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_BACKUP_TASK_SECURITY_MISMATCH")
	}
	for _, bindings := range result.NetworkSettings.Ports {
		if len(bindings) > 0 {
			return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_BACKUP_TASK_PUBLISHED_PORT")
		}
	}
	actualMounts := map[string]bool{}
	for _, m := range result.Mounts {
		profile, found := wanted[m.Destination]
		if !found || actualMounts[m.Destination] || m.Type != "volume" || m.Name != profile.owner.VolumeName() || m.Driver != "local" || m.RW == profile.readOnly {
			return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_BACKUP_TASK_MOUNT_MISMATCH")
		}
		actualMounts[m.Destination] = true
	}
	seen := map[string]bool{}
	for _, m := range h.Mounts {
		profile, found := wanted[m.Target]
		if !found || seen[m.Target] || m.Type != "volume" || m.Source != profile.owner.VolumeName() || m.ReadOnly != profile.readOnly || m.VolumeOptions == nil || !m.VolumeOptions.NoCopy || m.VolumeOptions.Subpath != profile.subpath {
			return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_BACKUP_TASK_MOUNT_MISMATCH")
		}
		seen[m.Target] = true
	}
	endpoint, found := result.NetworkSettings.Networks[RuntimeNetworkName(t.Owner.ScopeID)]
	if !found || endpoint.NetworkID != "" && endpoint.NetworkID != t.NetworkID || result.State.Running && endpoint.NetworkID != t.NetworkID {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_BACKUP_TASK_ENDPOINT_MISMATCH")
	}
	s := result.State
	if s.Paused || s.Restarting || s.Dead || (s.Status != "created" && s.Status != "running" && s.Status != "exited") || s.Running != (s.Status == "running") || s.Running != (s.PID > 0) || s.Status == "created" && (s.StartedAt != neverStarted || s.ExitCode != 0 || s.OOMKilled) || s.Status != "created" && (!validTaskTime(s.StartedAt) || s.StartedAt == neverStarted) {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_BACKUP_TASK_STATE_INVALID")
	}
	if e = c.databaseTaskNetwork(ctx, t.networkTask(), result.ID, false); e != nil {
		return RuntimeContainer{}, false, e
	}
	if e = c.backupConsumers(ctx, t, result.ID); e != nil {
		return RuntimeContainer{}, false, e
	}
	return result, true, nil
}
func (c *Client) CreatePostgresBackupTask(ctx context.Context, t PostgresBackupTask) (RuntimeContainer, error) {
	image, e := c.postgresBackupContext(ctx, t)
	if e != nil {
		return RuntimeContainer{}, e
	}
	if _, found, e := c.InspectPostgresBackupTask(ctx, t); e != nil {
		return RuntimeContainer{}, e
	} else if found {
		return RuntimeContainer{}, failure("OFFLINE_ENGINE_EXISTING_BACKUP_TASK_PRESERVED")
	}
	if e = c.backupConsumers(ctx, t, ""); e != nil {
		return RuntimeContainer{}, e
	}
	environment, _ := t.environment(image)
	env := []string{}
	for key, value := range environment {
		env = append(env, key+"="+value)
	}
	slices.Sort(env)
	u := c.config.Utility
	mounts := []any{}
	for _, target := range t.postgresMountOrder() {
		m := t.postgresMounts()[target]
		options := map[string]any{"NoCopy": true}
		if m.subpath != "" {
			options["Subpath"] = m.subpath
		}
		mounts = append(mounts, map[string]any{"Type": "volume", "Source": m.owner.VolumeName(), "Target": target, "ReadOnly": m.readOnly, "VolumeOptions": options})
	}
	body := map[string]any{"Image": t.Image, "User": strconv.Itoa(t.UID) + ":" + strconv.Itoa(t.GID), "Entrypoint": []string{"/bin/sh"}, "Cmd": t.command(), "Env": env, "WorkingDir": "/", "Labels": t.Owner.labels(), "HostConfig": map[string]any{"NetworkMode": t.NetworkID, "IpcMode": "private", "CgroupnsMode": "private", "Runtime": "runc", "ReadonlyRootfs": true, "Privileged": false, "AutoRemove": false, "RestartPolicy": map[string]string{"Name": "no"}, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "Memory": u.MemoryBytes, "MemorySwap": u.MemoryBytes, "NanoCpus": u.MilliCPU * nanosecondsPerMillicore, "PidsLimit": u.PIDs, "LogConfig": map[string]any{"Type": "none", "Config": map[string]string{}}, "Mounts": mounts}}
	raw, status, e := c.jsonRequest(ctx, http.MethodPost, "/v"+c.policy.APIVersion+"/containers/create", url.Values{"name": {t.name()}, "platform": {"linux/amd64"}}, body)
	var reply struct {
		ID       string `json:"Id"`
		Warnings []string
	}
	if e != nil || status != http.StatusCreated || wire.UniqueJSON(raw, &reply) != nil || !containerIdentity.MatchString(reply.ID) || len(reply.Warnings) > 0 {
		return RuntimeContainer{}, failure("OFFLINE_ENGINE_BACKUP_TASK_CREATE_UNCERTAIN")
	}
	result, found, e := c.InspectPostgresBackupTask(ctx, t)
	if e != nil || !found || result.ID != reply.ID || result.State.Status != "created" {
		return RuntimeContainer{ID: reply.ID}, failure("OFFLINE_ENGINE_BACKUP_TASK_CREATE_UNCERTAIN")
	}
	return result, nil
}
func (c *Client) exactPostgresBackupTask(ctx context.Context, t PostgresBackupTask, id string) (RuntimeContainer, error) {
	if !containerIdentity.MatchString(id) {
		return RuntimeContainer{}, failure("OFFLINE_ENGINE_BACKUP_TASK_ID_INVALID")
	}
	r, found, e := c.InspectPostgresBackupTask(ctx, t)
	if e != nil {
		return r, e
	}
	if !found || r.ID != id {
		return RuntimeContainer{}, failure("OFFLINE_ENGINE_BACKUP_TASK_ID_CHANGED")
	}
	return r, nil
}
func (c *Client) InspectPostgresBackupInput(ctx context.Context, t PostgresBackupTask, id string, files []ConfigurationFile) (ConfigurationObservation, error) {
	if len(files) != 1 || files[0].Name != PostgresBackupPasswordName || c.ValidateConfigurationFiles(files, t.UID, t.GID) != nil {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_BACKUP_PASSWORD_FILE_REQUIRED")
	}
	prefix := pgpassPart(t.Host) + ":" + strconv.Itoa(t.Port) + ":" + pgpassPart(t.Database) + ":" + pgpassPart(t.User) + ":"
	line := string(files[0].Contents)
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, "\n") || strings.ContainsAny(strings.TrimSuffix(line, "\n"), "\x00\r\n") || len(line) <= len(prefix)+1 {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_BACKUP_PASSWORD_FILE_INVALID")
	}
	password := line[len(prefix) : len(line)-1]
	for i := 0; i < len(password); i++ {
		if password[i] == ':' {
			return ConfigurationObservation{}, failure("OFFLINE_ENGINE_BACKUP_PASSWORD_FILE_INVALID")
		}
		if password[i] == '\\' {
			i++
			if i == len(password) || password[i] != '\\' && password[i] != ':' {
				return ConfigurationObservation{}, failure("OFFLINE_ENGINE_BACKUP_PASSWORD_FILE_INVALID")
			}
		}
	}
	before, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	if before.State.Running {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_NONRUNNING_BACKUP_TASK_REQUIRED")
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+id+"/archive", url.Values{"path": {PostgresBackupInputDestination}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusOK {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_BACKUP_INPUT_READBACK_FAILED")
	}
	result, e := inspectPrivateArchive(raw, files, t.UID, t.GID, "postgres-backup")
	if e != nil {
		return result, e
	}
	if result.Status != "verified_private_volume_payload" {
		return result, failure("OFFLINE_ENGINE_BACKUP_PRIVATE_INPUT_REQUIRED")
	}
	after, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil || before.State != after.State {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_BACKUP_TASK_CHANGED_DURING_READBACK")
	}
	return result, nil
}
func (c *Client) StartPostgresBackupTask(ctx context.Context, t PostgresBackupTask, id string, files []ConfigurationFile, reservePercent int) (ResourceAdmission, error) {
	r, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil {
		return ResourceAdmission{}, e
	}
	if r.State.Status != "created" {
		return ResourceAdmission{}, failure("OFFLINE_ENGINE_BACKUP_EXECUTION_PRESERVED")
	}
	if _, e = c.InspectPostgresBackupInput(ctx, t, id, files); e != nil {
		return ResourceAdmission{}, e
	}
	if e = c.inspectEmptyPostgresBackupOutput(ctx, t, id); e != nil {
		return ResourceAdmission{}, e
	}
	if t.Restore != nil {
		if _, e = c.InspectPostgresRestoreSource(ctx, t, id); e != nil {
			return ResourceAdmission{}, e
		}
	}
	u := c.config.Utility
	admission, e := c.AdmitRuntimeResources(ctx, ResourceRequirement{u.MemoryBytes, u.MilliCPU}, reservePercent)
	if e != nil {
		return admission, e
	}
	current, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil || current.State != r.State {
		return admission, failure("OFFLINE_ENGINE_BACKUP_TASK_CHANGED_BEFORE_START")
	}
	if e = c.databaseTaskNetwork(ctx, t.networkTask(), id, true); e != nil {
		return admission, e
	}
	_, status, e := c.request(ctx, http.MethodPost, "/v"+c.policy.APIVersion+"/containers/"+id+"/start", nil, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusNoContent {
		return admission, failure("OFFLINE_ENGINE_BACKUP_TASK_START_UNCERTAIN")
	}
	return admission, nil
}

func (c *Client) inspectEmptyPostgresBackupOutput(ctx context.Context, t PostgresBackupTask, id string) error {
	before, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil {
		return e
	}
	if before.State.Status != "created" {
		return failure("OFFLINE_ENGINE_BACKUP_EXECUTION_PRESERVED")
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+id+"/archive", url.Values{"path": {t.postgresOutputDestination()}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusOK {
		return failure("OFFLINE_ENGINE_BACKUP_EMPTY_OUTPUT_READBACK_FAILED")
	}
	value, e := inspectPrivateArchive(raw, nil, t.UID, t.GID, path.Base(t.postgresOutputDestination()))
	if e != nil || value.Status != "verified_private_volume_payload" {
		return failure("OFFLINE_ENGINE_NONEMPTY_BACKUP_OUTPUT_PRESERVED")
	}
	after, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil || before.State != after.State {
		return failure("OFFLINE_ENGINE_BACKUP_TASK_CHANGED_DURING_READBACK")
	}
	return nil
}
func (c *Client) StopPostgresBackupTask(ctx context.Context, t PostgresBackupTask, id string, graceSeconds int) error {
	if graceSeconds < 1 || graceSeconds > 120 {
		return failure("OFFLINE_ENGINE_BACKUP_STOP_BOUND_INVALID")
	}
	r, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil {
		return e
	}
	if !r.State.Running {
		return failure("OFFLINE_ENGINE_BACKUP_TASK_NOT_RUNNING")
	}
	_, status, e := c.request(ctx, http.MethodPost, "/v"+c.policy.APIVersion+"/containers/"+id+"/stop", url.Values{"t": {strconv.Itoa(graceSeconds)}}, nil, graceSeconds+c.config.RequestSeconds)
	if e != nil || status != http.StatusNoContent {
		return failure("OFFLINE_ENGINE_BACKUP_TASK_STOP_UNCERTAIN")
	}
	return nil
}
func (c *Client) RemovePostgresBackupTask(ctx context.Context, t PostgresBackupTask, id string) error {
	r, e := c.exactPostgresBackupTask(ctx, t, id)
	if e != nil {
		return e
	}
	if r.State.Running {
		return failure("OFFLINE_ENGINE_RUNNING_BACKUP_TASK_PRESERVED")
	}
	_, status, e := c.request(ctx, http.MethodDelete, "/v"+c.policy.APIVersion+"/containers/"+id, url.Values{"force": {"0"}, "v": {"0"}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusNoContent {
		return failure("OFFLINE_ENGINE_BACKUP_TASK_REMOVE_UNCERTAIN")
	}
	if _, found, e := c.InspectPostgresBackupTask(ctx, t); e != nil || found {
		return failure("OFFLINE_ENGINE_BACKUP_TASK_REMOVE_UNCERTAIN")
	}
	return nil
}
