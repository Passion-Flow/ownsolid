package engine

import (
	"context"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"slices"
	"strconv"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const DatabaseMigrate = "migrate"
const DatabaseSchemaCheck = "check-schema"
const DatabaseRuntimePrepare = "prepare-runtime-access"
const DatabaseRuntimeRestorePrepare = "prepare-restored-runtime-access"
const DatabaseRuntimeCheck = "check-runtime-access"
const DatabaseCumulativeReleases = "upgrade-releases"
const DatabaseCumulativeMVP = "upgrade-mvp-postgresql-cpu-v1"
const DatabaseBootstrap = "bootstrap-admin-file"
const DatabaseBootstrapReconcile = "reconcile-bootstrap-file"
const DatabaseBootstrapResume = "resume-bootstrap-file"
const BootstrapStateRole = "bootstrap-state"
const BootstrapTaskDestination = "/var/lib/projectone/bootstrap"
const BootstrapCredentialFile = "credential.json"
const DatabaseTaskDestination = "/run/projectone/operator"

// DatabaseTask permits fixed control operator commands, never a caller-supplied job.
// The scope caller must durably record every effect before invoking a primitive.
type DatabaseTask struct {
	NativePostgreSQLRestoreRootsInspection *NativePostgreSQLRestoreRootsInspectionProfile `json:"native_postgresql_restore_roots_inspection,omitempty"`
	NativePostgreSQLTargetInitialization   *NativePostgreSQLTargetInitializationProfile   `json:"native_postgresql_target_initialization,omitempty"`
	NativePostgreSQLRecovery               *NativePostgreSQLRecoveryProfile               `json:"native_postgresql_recovery,omitempty"`
	NativePostgreSQLBackupVerifier         *NativePostgreSQLBackupVerifierProfile         `json:"native_postgresql_backup_verifier,omitempty"`
	LocalizationOwner                      *Ownership                                     `json:"localization_owner,omitempty"`
	StoreRestore                           *StoreRestoreProfile                           `json:"store_restore,omitempty"`
	Reason                                 string                                         `json:"reason,omitempty"`
	OutputOwner                            *Ownership                                     `json:"output_owner,omitempty"`
	SourceOwner                            *Ownership                                     `json:"source_owner,omitempty"`
	Owner                                  Ownership                                      `json:"owner"`
	VolumeOwner                            Ownership                                      `json:"volume_owner"`
	Kind                                   string                                         `json:"kind"`
	Image                                  string                                         `json:"image"`
	UID                                    int                                            `json:"uid"`
	GID                                    int                                            `json:"gid"`
	NetworkID                              string                                         `json:"network_id"`
	Peers                                  map[string]string                              `json:"peers"`
}

// Only the two original fixed upgrade commands share cumulative input custody.
func (t DatabaseTask) cumulativeDatabaseTask() bool {
	return t.Kind == DatabaseCumulativeReleases || t.Kind == DatabaseCumulativeMVP
}

func (t DatabaseTask) name() string {
	return "projectone-" + t.Owner.ScopeID + "-database-" + t.Kind + "-" + t.Owner.OperationID
}
func (t DatabaseTask) command() []string {
	if t.NativePostgreSQLRestoreRootsInspection != nil {
		return t.nativeRestoreRootsCommand()
	}
	if t.NativePostgreSQLTargetInitialization != nil {
		return t.nativeTargetInitializationCommand()
	}
	if t.NativePostgreSQLRecovery != nil {
		return t.nativeRecoveryCommand()
	}
	if t.NativePostgreSQLBackupVerifier != nil {
		return t.nativeVerifierCommand()
	}
	args := []string{t.Kind, "--config", DatabaseTaskDestination + "/private/control.yaml"}
	if t.Kind == DatabaseRuntimePrepare || t.Kind == DatabaseRuntimeCheck || t.Kind == DatabaseRuntimeRestorePrepare || t.cumulativeDatabaseTask() {
		args = append(args, "--runtime-config", DatabaseTaskDestination+"/private/runtime.yaml")
	}
	if t.Kind == DatabaseRuntimeRestorePrepare {
		args = append(args, "--restore-context", DatabaseTaskDestination+"/private/restored-runtime.json")
	}
	if t.bootstrap() {
		args = append(args, "--bootstrap-input", DatabaseTaskDestination+"/private/bootstrap-request.json", "--credential-output", BootstrapTaskDestination+"/private/"+BootstrapCredentialFile)
	}
	args = append(args, t.storeArguments()...)
	args = append(args, t.storeBackupArguments()...)
	args = append(args, t.storeRestoreArguments()...)
	args = append(args, t.restoredAuthorityArguments()...)
	return args
}
func (t DatabaseTask) bootstrap() bool {
	return t.Kind == DatabaseBootstrap || t.Kind == DatabaseBootstrapReconcile || t.Kind == DatabaseBootstrapResume
}
func (t DatabaseTask) valid() bool {
	if t.NativePostgreSQLRestoreRootsInspection != nil {
		return t.validNativePostgreSQLRestoreRootsInspection()
	}
	if t.NativePostgreSQLTargetInitialization != nil {
		return t.validNativePostgreSQLTargetInitialization()
	}
	if t.NativePostgreSQLRecovery != nil {
		return t.validNativePostgreSQLRecovery()
	}
	if t.NativePostgreSQLBackupVerifier != nil {
		return t.validNativePostgreSQLVerifier()
	}
	if t.LocalizationOwner != nil && (!t.LocalizationOwner.valid() || t.LocalizationOwner.ScopeID != t.Owner.ScopeID || t.LocalizationOwner.Role != LocalizationConfigurationRole || t.Peers["control"] == "") {
		return false
	}
	store, storeKind := storeTask(t.Kind)
	backup, backupKind := storeBackupTask(t.Kind)
	restore, restoreKind := storeRestoreTask(t.Kind)
	inputRole := "operator-config"
	if t.cumulativeDatabaseTask() {
		inputRole = CumulativeOperatorRole + "-" + t.Owner.OperationID
	}
	if backupKind {
		inputRole = StoreBackupInputRole + "-" + t.Owner.OperationID
	}
	if restoreKind || t.restoredAuthority() {
		inputRole = StoreRestoreConfigRole + "-" + t.Owner.OperationID
	}
	generatedQualification := t.restoredQualificationInput()
	if generatedQualification {
		inputRole = StoreRestoreConfigRole + "-" + t.Owner.OperationID
	}
	if (t.Kind != DatabaseMigrate && t.Kind != DatabaseSchemaCheck && t.Kind != DatabaseRuntimePrepare && t.Kind != DatabaseRuntimeCheck && t.Kind != DatabaseRuntimeRestorePrepare && !t.cumulativeDatabaseTask() && !t.bootstrap() && !storeKind && !backupKind && !restoreKind && !t.restoredAuthority()) || !utilityIdentity(t.Owner, t.Image, t.UID, t.GID) || t.Owner.Role != "database-"+t.Kind || !t.VolumeOwner.valid() || t.VolumeOwner.Role != inputRole || t.VolumeOwner.ScopeID != t.Owner.ScopeID || !containerIdentity.MatchString(t.NetworkID) || len(t.Peers) < 1 || len(t.Peers) > 3 || t.Peers["postgres"] == "" || !restoreKind && t.StoreRestore != nil {
		return false
	}
	if (generatedQualification || t.cumulativeDatabaseTask()) && (t.VolumeOwner.OperationID != t.Owner.OperationID || t.Peers["control"] == "") {
		return false
	}
	if t.bootstrap() {
		if t.OutputOwner == nil || !t.OutputOwner.valid() || t.OutputOwner.ScopeID != t.Owner.ScopeID || t.OutputOwner.Role != BootstrapStateRole || *t.OutputOwner == t.VolumeOwner {
			return false
		}
	} else if storeKind {
		if t.OutputOwner == nil || !t.OutputOwner.valid() || t.OutputOwner.ScopeID != t.Owner.ScopeID || t.OutputOwner.Role != store.role || t.Peers["control"] == "" || (store.mode == "check" && t.Reason != "") || (store.mode != "check" && !validStoreReason(t.Reason)) {
			return false
		}
	} else if t.restoredAuthority() {
		if t.VolumeOwner.OperationID != t.Owner.OperationID || !validStoreReason(t.Reason) || t.OutputOwner != nil || t.Peers["control"] == "" {
			return false
		}
	} else if restoreKind {
		if !t.validStoreRestoreTask(restore) {
			return false
		}
	} else if backupKind {
		if t.OutputOwner == nil || !t.OutputOwner.valid() || t.OutputOwner.ScopeID != t.Owner.ScopeID || t.OutputOwner.OperationID != t.Owner.OperationID || t.OutputOwner.Role != "backup-"+backup.role+"-"+t.Owner.OperationID || t.VolumeOwner.OperationID != t.Owner.OperationID || t.SourceOwner == nil || !t.SourceOwner.valid() || t.SourceOwner.ScopeID != t.Owner.ScopeID || t.SourceOwner.Role != backup.role || t.Peers["control"] == "" || !validStoreReason(t.Reason) {
			return false
		}
	} else if t.OutputOwner != nil {
		return false
	}
	if !backupKind && !restoreKind && t.SourceOwner != nil {
		return false
	}
	if !storeKind && !backupKind && !restoreKind && !t.restoredAuthority() && t.Reason != "" {
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
func databaseNetworkOptions(options map[string]string) bool {
	for key, value := range options {
		if key == "com.docker.network.enable_ipv4" && value == "true" || key == "com.docker.network.enable_ipv6" && value == "false" {
			continue
		}
		return false
	}
	return true
}
func (c *Client) databaseTaskNetwork(ctx context.Context, t DatabaseTask, taskID string, starting bool) error {
	if t.NativePostgreSQLBackupVerifier != nil {
		return c.nativeVerifierConsumers(ctx, t, taskID)
	}
	if t.NativePostgreSQLRecovery != nil {
		if e := c.nativeRecoveryConsumers(ctx, t, taskID); e != nil {
			return e
		}
	}
	if t.NativePostgreSQLTargetInitialization != nil {
		if e := c.nativeTargetInitializationConsumers(ctx, t, taskID); e != nil {
			return e
		}
	}
	if t.NativePostgreSQLRestoreRootsInspection != nil {
		if e := c.nativeRestoreRootsConsumers(ctx, t, taskID); e != nil {
			return e
		}
	}
	if e := c.databaseLocalizationConsumers(ctx, t, taskID); e != nil {
		return e
	}
	n, found, e := c.InspectRuntimeNetwork(ctx, t.Owner.ScopeID)
	if e != nil {
		return e
	}
	if !found || n.ID != t.NetworkID || n.Driver != "bridge" || n.Scope != "local" || !n.Internal || n.Attachable || n.Ingress || !databaseNetworkOptions(n.Options) || n.Labels["io.projectone.isolated-scope"] != t.Owner.ScopeID || n.Labels["io.projectone.purpose"] != "isolated-validation" || n.Labels["com.docker.compose.project"] != RuntimeProject(t.Owner.ScopeID) || n.Labels["com.docker.compose.network"] != "isolated" {
		return failure("OFFLINE_ENGINE_DATABASE_TASK_NETWORK_MISMATCH")
	}
	wanted := map[string]string{}
	for role, id := range t.Peers {
		peer, found, e := c.InspectRuntime(ctx, t.Owner.ScopeID, role)
		if e != nil {
			return e
		}
		if !found || peer.ID != id || peer.Config.Labels["io.projectone.isolated-scope"] != t.Owner.ScopeID || peer.Config.Labels["io.projectone.purpose"] != "isolated-validation" || peer.Config.Labels["com.docker.compose.project"] != RuntimeProject(t.Owner.ScopeID) || peer.Config.Labels["com.docker.compose.service"] != role {
			return failure("OFFLINE_ENGINE_DATABASE_TASK_PEER_MISMATCH")
		}
		if starting && (role == "postgres" && (!peer.State.Running || peer.State.Status != "running" || peer.State.PID < 1 || peer.State.OOMKilled || peer.State.Paused || peer.State.Restarting || peer.State.Dead) || role != "postgres" && (peer.State.Running || peer.State.PID != 0 || peer.State.Status != "created" && peer.State.Status != "exited")) {
			return failure("OFFLINE_ENGINE_DATABASE_TASK_PEER_PROCESS_INVALID")
		}
		wanted[id] = RuntimeContainerName(t.Owner.ScopeID, role)
	}
	if taskID != "" && t.NativePostgreSQLRestoreRootsInspection == nil {
		wanted[taskID] = t.name()
	}
	for id, endpoint := range n.Containers {
		if wanted[id] == "" || wanted[id] != endpoint.Name {
			return failure("OFFLINE_ENGINE_DATABASE_TASK_FOREIGN_ENDPOINT")
		}
	}
	return nil
}
func (c *Client) databaseTaskContext(ctx context.Context, t DatabaseTask) (Image, error) {
	if c.utilityPolicy() != nil || !t.valid() {
		return Image{}, failure("OFFLINE_ENGINE_DATABASE_TASK_CONTEXT_INVALID")
	}
	image, found, e := c.Inspect(ctx, t.Image)
	if e != nil {
		return image, e
	}
	if !found || image.ID != t.Image || image.OS != "linux" || image.Architecture != "amd64" || image.Config == nil || len(image.Config.Volumes) > 0 || !slices.Equal(image.Config.Entrypoint, []string{modelTaskEntrypoint}) {
		return Image{}, failure("OFFLINE_ENGINE_DATABASE_TASK_IMAGE_MISMATCH")
	}
	if _, found, e = c.InspectVolume(ctx, t.VolumeOwner); e != nil {
		return image, e
	} else if !found {
		return Image{}, failure("OFFLINE_ENGINE_DATABASE_TASK_VOLUME_REQUIRED")
	}
	if t.OutputOwner != nil {
		if _, found, e := c.InspectVolume(ctx, *t.OutputOwner); e != nil {
			return image, e
		} else if !found {
			return image, failure("OFFLINE_ENGINE_BOOTSTRAP_STATE_VOLUME_REQUIRED")
		}
	}
	if t.SourceOwner != nil {
		if _, found, e := c.InspectVolume(ctx, *t.SourceOwner); e != nil {
			return image, e
		} else if !found {
			return image, failure("OFFLINE_ENGINE_BACKUP_SOURCE_VOLUME_REQUIRED")
		}
	}
	if t.LocalizationOwner != nil {
		if _, found, e := c.InspectVolume(ctx, *t.LocalizationOwner); e != nil {
			return image, e
		} else if !found {
			return image, failure("OFFLINE_ENGINE_DATABASE_LOCALIZATION_VOLUME_REQUIRED")
		}
	}
	if t.NativePostgreSQLBackupVerifier != nil {
		if e := c.nativeVerifierVolumes(ctx, t); e != nil {
			return image, e
		}
	}
	if t.NativePostgreSQLRecovery != nil {
		if e := c.nativeRecoveryVolumes(ctx, t); e != nil {
			return image, e
		}
	}
	if t.NativePostgreSQLTargetInitialization != nil {
		if e := c.nativeTargetInitializationVolumes(ctx, t); e != nil {
			return image, e
		}
	}
	if t.NativePostgreSQLRestoreRootsInspection != nil {
		if e := c.nativeRestoreRootsVolumes(ctx, t); e != nil {
			return image, e
		}
	}
	return image, nil
}
func (c *Client) InspectDatabaseTask(ctx context.Context, t DatabaseTask) (RuntimeContainer, bool, error) {
	image, e := c.databaseTaskContext(ctx, t)
	if e != nil {
		return RuntimeContainer{}, false, e
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+url.PathEscape(t.name())+"/json", nil, nil, c.config.RequestSeconds)
	if e != nil {
		return RuntimeContainer{}, false, e
	}
	if status == http.StatusNotFound {
		if e = c.databaseTaskNetwork(ctx, t, "", false); e != nil {
			return RuntimeContainer{}, false, e
		}
		return RuntimeContainer{}, false, nil
	}
	var result RuntimeContainer
	if status != http.StatusOK || wire.UniqueJSON(raw, &result) != nil {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_DATABASE_TASK_RESPONSE_INVALID")
	}
	h := result.HostConfig
	u := c.config.Utility
	mounts := len(t.databaseMountProfiles())
	expectedEnv, ie := t.databaseTaskEnvironment(image)
	actualEnv, ae := imageEnvironment(result.Config.Env)
	if !containerIdentity.MatchString(result.ID) || result.Name != "/"+t.name() || result.Image != t.Image || result.Platform != "linux" || result.Config.Image != t.Image || result.Config.User != strconv.Itoa(t.UID)+":"+strconv.Itoa(t.GID) || !slices.Equal(result.Config.Entrypoint, []string{modelTaskEntrypoint}) || !slices.Equal(result.Config.Cmd, t.command()) || result.Config.WorkingDir != "/" || len(result.Config.Volumes) > 0 || !t.Owner.matches(result.Config.Labels) || !ie || !ae || !reflect.DeepEqual(expectedEnv, actualEnv) || h.NetworkMode != t.databaseTaskNetworkMode() || h.IpcMode != "private" || h.PidMode != "" || h.UTSMode != "" || h.UsernsMode != "" || h.CgroupnsMode != "private" || h.CgroupParent != "" || h.Runtime != "runc" || !h.ReadonlyRootfs || h.Privileged || h.AutoRemove || h.PublishAllPorts || h.Memory != u.MemoryBytes || h.MemorySwap != u.MemoryBytes || h.NanoCPUs != u.MilliCPU*nanosecondsPerMillicore || h.PIDs != u.PIDs || !slices.Equal(h.CapDrop, []string{"ALL"}) || len(h.CapAdd) > 0 || !slices.Equal(h.SecurityOpt, []string{"no-new-privileges:true"}) || len(h.Binds) > 0 || len(h.VolumesFrom) > 0 || len(h.Devices) > 0 || len(h.DeviceRequests) > 0 || len(h.PortBindings) > 0 || len(h.DNS) > 0 || len(h.ExtraHosts) > 0 || len(h.Links) > 0 || len(h.Tmpfs) > 0 || h.RestartPolicy.Name != "no" || h.RestartPolicy.MaximumRetryCount != 0 || h.LogConfig.Type != "none" || len(h.LogConfig.Config) > 0 || len(h.Mounts) != mounts || len(result.Mounts) != mounts || len(result.NetworkSettings.Networks) != 1 || len(result.NetworkSettings.Ports) > 0 {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_DATABASE_TASK_SECURITY_MISMATCH")
	}
	networkName := RuntimeNetworkName(t.Owner.ScopeID)
	if t.databaseTaskNetworkMode() == "none" {
		networkName = "none"
	}
	endpoint, ok := result.NetworkSettings.Networks[networkName]
	if !ok || t.databaseTaskNetworkMode() != "none" && (endpoint.NetworkID != "" && endpoint.NetworkID != t.NetworkID || result.State.Running && endpoint.NetworkID != t.NetworkID) {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_DATABASE_TASK_ENDPOINT_MISMATCH")
	}
	if !t.validDatabaseMounts(result) {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_DATABASE_TASK_MOUNT_MISMATCH")
	}
	if t.OutputOwner != nil {
		if e = c.databaseOutputConsumers(ctx, t, result.ID); e != nil {
			return RuntimeContainer{}, false, e
		}
	}
	s := result.State
	if s.Paused || s.Restarting || s.Dead || (s.Status != "created" && s.Status != "running" && s.Status != "exited") || s.Running != (s.Status == "running") || s.Running != (s.PID > 0) || s.Status == "created" && (s.StartedAt != neverStarted || s.ExitCode != 0 || s.OOMKilled) || s.Status != "created" && (!validTaskTime(s.StartedAt) || s.StartedAt == neverStarted) {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_DATABASE_TASK_STATE_INVALID")
	}
	if e = c.databaseTaskNetwork(ctx, t, result.ID, false); e != nil {
		return RuntimeContainer{}, false, e
	}
	if e = c.noForeignConsumers(ctx, t.VolumeOwner, result.ID); e != nil {
		return RuntimeContainer{}, false, e
	}
	return result, true, nil
}
func (c *Client) CreateDatabaseTask(ctx context.Context, t DatabaseTask) (RuntimeContainer, error) {
	if _, found, e := c.InspectDatabaseTask(ctx, t); e != nil {
		return RuntimeContainer{}, e
	} else if found {
		return RuntimeContainer{}, failure("OFFLINE_ENGINE_EXISTING_DATABASE_TASK_PRESERVED")
	}
	if e := c.noForeignConsumers(ctx, t.VolumeOwner, ""); e != nil {
		return RuntimeContainer{}, e
	}
	if t.OutputOwner != nil {
		if e := c.databaseOutputConsumers(ctx, t, ""); e != nil {
			return RuntimeContainer{}, e
		}
	}
	u := c.config.Utility
	mounts := []any{}
	for _, m := range t.databaseMountProfiles() {
		options := map[string]any{"NoCopy": true}
		if m.private {
			options["Subpath"] = UtilityPrivateDirectory
		}
		mounts = append(mounts, map[string]any{"Type": "volume", "Source": m.owner.VolumeName(), "Target": m.target, "ReadOnly": m.readOnly, "VolumeOptions": options})
	}
	body := map[string]any{"Image": t.Image, "User": strconv.Itoa(t.UID) + ":" + strconv.Itoa(t.GID), "Entrypoint": []string{modelTaskEntrypoint}, "Cmd": t.command(), "WorkingDir": "/", "Labels": t.Owner.labels(), "HostConfig": map[string]any{"NetworkMode": t.databaseTaskNetworkMode(), "IpcMode": "private", "CgroupnsMode": "private", "Runtime": "runc", "ReadonlyRootfs": true, "Privileged": false, "AutoRemove": false, "RestartPolicy": map[string]string{"Name": "no"}, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "Memory": u.MemoryBytes, "MemorySwap": u.MemoryBytes, "NanoCpus": u.MilliCPU * nanosecondsPerMillicore, "PidsLimit": u.PIDs, "LogConfig": map[string]any{"Type": "none", "Config": map[string]string{}}, "Mounts": mounts}}
	if t.NativePostgreSQLBackupVerifier != nil || t.NativePostgreSQLRecovery != nil || t.NativePostgreSQLTargetInitialization != nil || t.NativePostgreSQLRestoreRootsInspection != nil {
		image, e := c.databaseTaskContext(ctx, t)
		if e != nil {
			return RuntimeContainer{}, e
		}
		env, ok := t.databaseTaskEnvironment(image)
		if !ok {
			return RuntimeContainer{}, failure("OFFLINE_ENGINE_NATIVE_VERIFIER_ENVIRONMENT_INVALID")
		}
		body["Env"] = nativeVerifierEnvironmentValues(env)
	}
	raw, status, e := c.jsonRequest(ctx, http.MethodPost, "/v"+c.policy.APIVersion+"/containers/create", url.Values{"name": {t.name()}, "platform": {"linux/amd64"}}, body)
	var reply struct {
		ID       string `json:"Id"`
		Warnings []string
	}
	if e != nil || status != http.StatusCreated || wire.UniqueJSON(raw, &reply) != nil || !containerIdentity.MatchString(reply.ID) || len(reply.Warnings) > 0 {
		return RuntimeContainer{}, failure("OFFLINE_ENGINE_DATABASE_TASK_CREATE_UNCERTAIN")
	}
	result, found, e := c.InspectDatabaseTask(ctx, t)
	if e != nil || !found || result.ID != reply.ID || result.State.Status != "created" {
		return RuntimeContainer{ID: reply.ID}, failure("OFFLINE_ENGINE_DATABASE_TASK_CREATE_UNCERTAIN")
	}
	return result, nil
}
func (c *Client) exactDatabaseTask(ctx context.Context, t DatabaseTask, id string) (RuntimeContainer, error) {
	if !containerIdentity.MatchString(id) {
		return RuntimeContainer{}, failure("OFFLINE_ENGINE_DATABASE_TASK_ID_INVALID")
	}
	r, found, e := c.InspectDatabaseTask(ctx, t)
	if e != nil {
		return r, e
	}
	if !found || r.ID != id {
		return RuntimeContainer{}, failure("OFFLINE_ENGINE_DATABASE_TASK_ID_CHANGED")
	}
	return r, nil
}
func (c *Client) InspectDatabaseTaskInput(ctx context.Context, t DatabaseTask, id string, files []ConfigurationFile) (ConfigurationObservation, error) {
	if e := c.ValidateConfigurationFiles(files, t.UID, t.GID); e != nil {
		return ConfigurationObservation{}, e
	}
	configFound, runtimeFound, bootstrapFound, restoredFound := false, false, false, false
	for _, file := range files {
		if file.Name == "control.yaml" {
			configFound = true
		}
		if file.Name == "bootstrap-request.json" {
			bootstrapFound = true
		}
		if file.Name == "runtime.yaml" {
			runtimeFound = true
		}
		if file.Name == "restored-runtime.json" {
			restoredFound = true
		}
	}
	if t.NativePostgreSQLBackupVerifier != nil {
		if !t.nativeVerifierFiles(files) {
			return ConfigurationObservation{}, failure("OFFLINE_ENGINE_NATIVE_VERIFIER_REQUEST_CHANGED")
		}
		configFound = true
	}
	if t.NativePostgreSQLRecovery != nil && !t.nativeRecoveryFiles(files) {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_NATIVE_RECOVERY_REQUEST_CHANGED")
	}
	if t.NativePostgreSQLTargetInitialization != nil && !t.nativeTargetInitializationFiles(files) {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_NATIVE_TARGET_REQUEST_CHANGED")
	}
	if t.NativePostgreSQLRestoreRootsInspection != nil {
		if !t.nativeRestoreRootsFiles(files) {
			return ConfigurationObservation{}, failure("OFFLINE_ENGINE_NATIVE_ROOTS_REQUEST_CHANGED")
		}
		configFound = true
	}
	if !configFound || (t.Kind == DatabaseRuntimePrepare || t.Kind == DatabaseRuntimeCheck || t.Kind == DatabaseRuntimeRestorePrepare || t.cumulativeDatabaseTask()) && !runtimeFound || t.bootstrap() && !bootstrapFound || t.Kind == DatabaseRuntimeRestorePrepare && !restoredFound {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_DATABASE_TASK_CONFIG_REQUIRED")
	}
	before, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	if before.State.Running {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_NONRUNNING_DATABASE_TASK_REQUIRED")
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+id+"/archive", url.Values{"path": {DatabaseTaskDestination}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusOK {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_DATABASE_TASK_INPUT_READBACK_FAILED")
	}
	result, e := inspectPrivateArchive(raw, files, t.UID, t.GID, path.Base(DatabaseTaskDestination))
	if e != nil {
		return result, e
	}
	if result.Status != "verified_private_volume_payload" {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_DATABASE_TASK_PRIVATE_INPUT_REQUIRED")
	}
	after, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil || before.State != after.State {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_DATABASE_TASK_CHANGED_DURING_READBACK")
	}
	result.Qualification = "verified_nonrunning_task_private_input_only_not_schema_outcome_or_target"
	return result, nil
}
func (c *Client) StartDatabaseTask(ctx context.Context, t DatabaseTask, id string, files []ConfigurationFile, reservePercent int) (ResourceAdmission, error) {
	return c.startDatabaseTask(ctx, t, id, files, reservePercent, c.AdmitRuntimeResources)
}
func (c *Client) startDatabaseTask(ctx context.Context, t DatabaseTask, id string, files []ConfigurationFile, reservePercent int, admit func(context.Context, ResourceRequirement, int) (ResourceAdmission, error)) (ResourceAdmission, error) {
	r, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil {
		return ResourceAdmission{}, e
	}
	if r.State.Status != "created" {
		return ResourceAdmission{}, failure("OFFLINE_ENGINE_DATABASE_TASK_EXECUTION_PRESERVED")
	}
	if _, e = c.InspectDatabaseTaskInput(ctx, t, id, files); e != nil {
		return ResourceAdmission{}, e
	}
	if _, ok := storeBackupTask(t.Kind); ok {
		if e = c.inspectEmptyStoreBackupOutput(ctx, t, id); e != nil {
			return ResourceAdmission{}, e
		}
	}
	if t.StoreRestore != nil {
		if e = c.inspectStoreRestoreTaskSource(ctx, t, id); e != nil {
			return ResourceAdmission{}, e
		}
		if e = c.inspectEmptyStoreRestoreTarget(ctx, t, id); e != nil {
			return ResourceAdmission{}, e
		}
	}
	u := c.config.Utility
	admission, e := admit(ctx, ResourceRequirement{u.MemoryBytes, u.MilliCPU}, reservePercent)
	if e != nil {
		return admission, e
	}
	current, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil || current.State != r.State {
		return admission, failure("OFFLINE_ENGINE_DATABASE_TASK_CHANGED_BEFORE_START")
	}
	if e = c.databaseTaskNetwork(ctx, t, id, true); e != nil {
		return admission, e
	}
	_, status, e := c.request(ctx, http.MethodPost, "/v"+c.policy.APIVersion+"/containers/"+id+"/start", nil, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusNoContent {
		return admission, failure("OFFLINE_ENGINE_DATABASE_TASK_START_UNCERTAIN")
	}
	return admission, nil
}
func (c *Client) StopDatabaseTask(ctx context.Context, t DatabaseTask, id string, graceSeconds int) error {
	if graceSeconds < 1 || graceSeconds > 120 {
		return failure("OFFLINE_ENGINE_DATABASE_TASK_STOP_BOUND_INVALID")
	}
	r, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil {
		return e
	}
	if !r.State.Running {
		return failure("OFFLINE_ENGINE_DATABASE_TASK_NOT_RUNNING")
	}
	_, status, e := c.request(ctx, http.MethodPost, "/v"+c.policy.APIVersion+"/containers/"+id+"/stop", url.Values{"t": {strconv.Itoa(graceSeconds)}}, nil, graceSeconds+c.config.RequestSeconds)
	if e != nil || status != http.StatusNoContent {
		return failure("OFFLINE_ENGINE_DATABASE_TASK_STOP_UNCERTAIN")
	}
	return nil
}
func (c *Client) RemoveDatabaseTask(ctx context.Context, t DatabaseTask, id string) error {
	r, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil {
		return e
	}
	if r.State.Running {
		return failure("OFFLINE_ENGINE_RUNNING_DATABASE_TASK_PRESERVED")
	}
	_, status, e := c.request(ctx, http.MethodDelete, "/v"+c.policy.APIVersion+"/containers/"+id, url.Values{"force": {"0"}, "v": {"0"}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusNoContent {
		return failure("OFFLINE_ENGINE_DATABASE_TASK_REMOVE_UNCERTAIN")
	}
	if _, found, e := c.InspectDatabaseTask(ctx, t); e != nil || found {
		return failure("OFFLINE_ENGINE_DATABASE_TASK_REMOVE_UNCERTAIN")
	}
	return nil
}
