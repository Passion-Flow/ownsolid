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
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const ModelTaskRole = "model-store-initialize"
const ModelTaskDestination = "/var/lib/projectone/model-init"
const modelTaskEntrypoint = "/usr/local/bin/control"

// ModelStoreTask is a fixed local operator command, never a caller-supplied shell.
// The scope caller must durably record each intent before invoking a mutation.
type ModelStoreTask struct {
	Owner       Ownership `json:"owner"`
	VolumeOwner Ownership `json:"volume_owner"`
	Image       string    `json:"image"`
	UID         int       `json:"uid"`
	GID         int       `json:"gid"`
}

func (t ModelStoreTask) name() string {
	return "projectone-" + t.Owner.ScopeID + "-" + ModelTaskRole + "-" + t.Owner.OperationID
}
func (t ModelStoreTask) valid() bool {
	return utilityIdentity(t.Owner, t.Image, t.UID, t.GID) && t.Owner.Role == ModelTaskRole && t.VolumeOwner.valid() && t.VolumeOwner.Role == "models" && t.VolumeOwner.ScopeID == t.Owner.ScopeID
}
func (t ModelStoreTask) command() []string {
	return []string{"initialize-model-store", "--model-store-root", ModelTaskDestination + "/" + UtilityPrivateDirectory, "--owner-uid", strconv.Itoa(t.UID), "--owner-gid", strconv.Itoa(t.GID), "--confirm-empty-model-store"}
}
func imageEnvironment(values []string) (map[string]string, bool) {
	result := map[string]string{}
	for _, value := range values {
		key, v, ok := strings.Cut(value, "=")
		if !ok || key == "" {
			return nil, false
		}
		if _, exists := result[key]; exists {
			return nil, false
		}
		result[key] = v
	}
	return result, true
}
func (c *Client) modelTaskContext(ctx context.Context, t ModelStoreTask) (Image, error) {
	if c.utilityPolicy() != nil || !t.valid() {
		return Image{}, failure("OFFLINE_ENGINE_MODEL_TASK_CONTEXT_INVALID")
	}
	image, found, e := c.Inspect(ctx, t.Image)
	if e != nil {
		return image, e
	}
	if !found || image.ID != t.Image || image.OS != "linux" || image.Architecture != "amd64" || image.Config == nil || len(image.Config.Volumes) > 0 || !slices.Equal(image.Config.Entrypoint, []string{modelTaskEntrypoint}) {
		return Image{}, failure("OFFLINE_ENGINE_MODEL_TASK_IMAGE_MISMATCH")
	}
	if _, found, e = c.InspectVolume(ctx, t.VolumeOwner); e != nil {
		return Image{}, e
	} else if !found {
		return Image{}, failure("OFFLINE_ENGINE_MODEL_TASK_VOLUME_REQUIRED")
	}
	return image, nil
}
func (c *Client) InspectModelTask(ctx context.Context, t ModelStoreTask) (RuntimeContainer, bool, error) {
	image, e := c.modelTaskContext(ctx, t)
	if e != nil {
		return RuntimeContainer{}, false, e
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+url.PathEscape(t.name())+"/json", nil, nil, c.config.RequestSeconds)
	if e != nil {
		return RuntimeContainer{}, false, e
	}
	if status == http.StatusNotFound {
		return RuntimeContainer{}, false, nil
	}
	var result RuntimeContainer
	if status != http.StatusOK || wire.UniqueJSON(raw, &result) != nil {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_MODEL_TASK_RESPONSE_INVALID")
	}
	h := result.HostConfig
	u := c.config.Utility
	expectedEnv, ie := imageEnvironment(image.Config.Env)
	actualEnv, ae := imageEnvironment(result.Config.Env)
	if !containerIdentity.MatchString(result.ID) || result.Name != "/"+t.name() || result.Image != t.Image || result.Platform != "linux" || result.Config.Image != t.Image || result.Config.User != strconv.Itoa(t.UID)+":"+strconv.Itoa(t.GID) || !slices.Equal(result.Config.Entrypoint, []string{modelTaskEntrypoint}) || !slices.Equal(result.Config.Cmd, t.command()) || result.Config.WorkingDir != "/" || len(result.Config.Volumes) > 0 || !t.Owner.matches(result.Config.Labels) || !ie || !ae || !reflect.DeepEqual(expectedEnv, actualEnv) || h.NetworkMode != "none" || h.IpcMode != "private" || h.PidMode != "" || h.UTSMode != "" || h.UsernsMode != "" || h.CgroupnsMode != "private" || h.CgroupParent != "" || h.Runtime != "runc" || !h.ReadonlyRootfs || h.Privileged || h.AutoRemove || h.PublishAllPorts || h.Memory != u.MemoryBytes || h.MemorySwap != u.MemoryBytes || h.NanoCPUs != u.MilliCPU*nanosecondsPerMillicore || h.PIDs != u.PIDs || !slices.Equal(h.CapDrop, []string{"ALL"}) || len(h.CapAdd) > 0 || !slices.Equal(h.SecurityOpt, []string{"no-new-privileges:true"}) || len(h.Binds) > 0 || len(h.VolumesFrom) > 0 || len(h.Devices) > 0 || len(h.DeviceRequests) > 0 || len(h.PortBindings) > 0 || len(h.DNS) > 0 || len(h.ExtraHosts) > 0 || len(h.Links) > 0 || len(h.Tmpfs) > 0 || h.RestartPolicy.Name != "no" || h.RestartPolicy.MaximumRetryCount != 0 || h.LogConfig.Type != "none" || len(h.LogConfig.Config) > 0 || len(h.Mounts) != 1 || len(result.Mounts) != 1 || len(result.NetworkSettings.Networks) != 1 || len(result.NetworkSettings.Ports) > 0 {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_MODEL_TASK_SECURITY_MISMATCH")
	}
	if _, ok := result.NetworkSettings.Networks["none"]; !ok {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_MODEL_TASK_NETWORK_MISMATCH")
	}
	m := h.Mounts[0]
	actual := result.Mounts[0]
	if m.Type != "volume" || m.Source != t.VolumeOwner.VolumeName() || m.Target != ModelTaskDestination || m.ReadOnly || m.VolumeOptions == nil || !m.VolumeOptions.NoCopy || m.VolumeOptions.Subpath != "" || actual.Type != "volume" || actual.Name != t.VolumeOwner.VolumeName() || actual.Destination != ModelTaskDestination || actual.Driver != "local" || !actual.RW {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_MODEL_TASK_MOUNT_MISMATCH")
	}
	state := result.State
	if state.Paused || state.Restarting || state.Dead || (state.Status != "created" && state.Status != "running" && state.Status != "exited") || state.Running != (state.Status == "running") || state.Running != (state.PID > 0) || state.Status == "created" && (state.StartedAt != neverStarted || state.ExitCode != 0 || state.OOMKilled) || state.Status != "created" && (!validTaskTime(state.StartedAt) || state.StartedAt == neverStarted) {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_MODEL_TASK_STATE_INVALID")
	}
	if e = c.noForeignConsumers(ctx, t.VolumeOwner, result.ID); e != nil {
		return RuntimeContainer{}, false, e
	}
	return result, true, nil
}
func validTaskTime(v string) bool { _, e := time.Parse(time.RFC3339Nano, v); return e == nil }
func (c *Client) CreateModelTask(ctx context.Context, t ModelStoreTask) (RuntimeContainer, error) {
	if _, found, e := c.InspectModelTask(ctx, t); e != nil {
		return RuntimeContainer{}, e
	} else if found {
		return RuntimeContainer{}, failure("OFFLINE_ENGINE_EXISTING_MODEL_TASK_PRESERVED")
	}
	if e := c.noForeignConsumers(ctx, t.VolumeOwner, ""); e != nil {
		return RuntimeContainer{}, e
	}
	u := c.config.Utility
	body := map[string]any{"Image": t.Image, "User": strconv.Itoa(t.UID) + ":" + strconv.Itoa(t.GID), "Entrypoint": []string{modelTaskEntrypoint}, "Cmd": t.command(), "WorkingDir": "/", "Labels": t.Owner.labels(), "HostConfig": map[string]any{"NetworkMode": "none", "IpcMode": "private", "CgroupnsMode": "private", "Runtime": "runc", "ReadonlyRootfs": true, "Privileged": false, "AutoRemove": false, "RestartPolicy": map[string]string{"Name": "no"}, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "Memory": u.MemoryBytes, "MemorySwap": u.MemoryBytes, "NanoCpus": u.MilliCPU * nanosecondsPerMillicore, "PidsLimit": u.PIDs, "LogConfig": map[string]any{"Type": "none", "Config": map[string]string{}}, "Mounts": []any{map[string]any{"Type": "volume", "Source": t.VolumeOwner.VolumeName(), "Target": ModelTaskDestination, "ReadOnly": false, "VolumeOptions": map[string]bool{"NoCopy": true}}}}}
	raw, status, e := c.jsonRequest(ctx, http.MethodPost, "/v"+c.policy.APIVersion+"/containers/create", url.Values{"name": {t.name()}, "platform": {"linux/amd64"}}, body)
	var reply struct {
		ID       string `json:"Id"`
		Warnings []string
	}
	if e != nil || status != http.StatusCreated || wire.UniqueJSON(raw, &reply) != nil || !containerIdentity.MatchString(reply.ID) || len(reply.Warnings) > 0 {
		return RuntimeContainer{}, failure("OFFLINE_ENGINE_MODEL_TASK_CREATE_UNCERTAIN")
	}
	result, found, e := c.InspectModelTask(ctx, t)
	if e != nil || !found || result.ID != reply.ID || result.State.Status != "created" {
		return RuntimeContainer{ID: reply.ID}, failure("OFFLINE_ENGINE_MODEL_TASK_CREATE_UNCERTAIN")
	}
	return result, nil
}
func (c *Client) exactModelTask(ctx context.Context, t ModelStoreTask, id string) (RuntimeContainer, error) {
	if !containerIdentity.MatchString(id) {
		return RuntimeContainer{}, failure("OFFLINE_ENGINE_MODEL_TASK_ID_REQUIRED")
	}
	result, found, e := c.InspectModelTask(ctx, t)
	if e != nil {
		return RuntimeContainer{}, e
	}
	if !found || result.ID != id {
		return RuntimeContainer{}, failure("OFFLINE_ENGINE_MODEL_TASK_ID_MISMATCH")
	}
	return result, nil
}
func (c *Client) StartModelTask(ctx context.Context, t ModelStoreTask, id string, reservePercent int) (ResourceAdmission, error) {
	return c.startModelTask(ctx, t, id, reservePercent, c.AdmitRuntimeResources)
}
func (c *Client) startModelTask(ctx context.Context, t ModelStoreTask, id string, reservePercent int, admit func(context.Context, ResourceRequirement, int) (ResourceAdmission, error)) (ResourceAdmission, error) {
	result, e := c.exactModelTask(ctx, t, id)
	if e != nil {
		return ResourceAdmission{}, e
	}
	if result.State.Status != "created" {
		return ResourceAdmission{}, failure("OFFLINE_ENGINE_MODEL_TASK_EXECUTION_PRESERVED")
	}
	u := c.config.Utility
	admission, e := admit(ctx, ResourceRequirement{u.MemoryBytes, u.MilliCPU}, reservePercent)
	if e != nil {
		return admission, e
	}
	current, e := c.exactModelTask(ctx, t, id)
	if e != nil || current.State.Status != "created" {
		return admission, failure("OFFLINE_ENGINE_MODEL_TASK_CHANGED_BEFORE_START")
	}
	_, status, e := c.request(ctx, http.MethodPost, "/v"+c.policy.APIVersion+"/containers/"+id+"/start", nil, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusNoContent {
		return admission, failure("OFFLINE_ENGINE_MODEL_TASK_START_UNCERTAIN")
	}
	return admission, nil
}
func (c *Client) StopModelTask(ctx context.Context, t ModelStoreTask, id string, graceSeconds int) error {
	if graceSeconds < 1 || graceSeconds > 120 {
		return failure("OFFLINE_ENGINE_MODEL_TASK_STOP_BOUND_INVALID")
	}
	result, e := c.exactModelTask(ctx, t, id)
	if e != nil {
		return e
	}
	if result.State.Status != "running" {
		return failure("OFFLINE_ENGINE_MODEL_TASK_NOT_RUNNING")
	}
	_, status, e := c.request(ctx, http.MethodPost, "/v"+c.policy.APIVersion+"/containers/"+id+"/stop", url.Values{"t": {strconv.Itoa(graceSeconds)}}, nil, graceSeconds+c.config.RequestSeconds)
	if e != nil || status != http.StatusNoContent {
		return failure("OFFLINE_ENGINE_MODEL_TASK_STOP_UNCERTAIN")
	}
	return nil
}
func (c *Client) RemoveModelTask(ctx context.Context, t ModelStoreTask, id string) error {
	result, e := c.exactModelTask(ctx, t, id)
	if e != nil {
		return e
	}
	if result.State.Running {
		return failure("OFFLINE_ENGINE_RUNNING_MODEL_TASK_PRESERVED")
	}
	_, status, e := c.request(ctx, http.MethodDelete, "/v"+c.policy.APIVersion+"/containers/"+id, url.Values{"force": {"0"}, "v": {"0"}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusNoContent {
		return failure("OFFLINE_ENGINE_MODEL_TASK_REMOVE_UNCERTAIN")
	}
	if _, found, e := c.InspectModelTask(ctx, t); e != nil || found {
		return failure("OFFLINE_ENGINE_MODEL_TASK_REMOVE_UNCERTAIN")
	}
	return nil
}

// InspectModelTaskStorage observes the private result through the exited task's
// existing mount. It creates no helper, writes no archive and exposes no contents.
func (c *Client) InspectModelTaskStorage(ctx context.Context, t ModelStoreTask, id string, files []ConfigurationFile) (ConfigurationObservation, error) {
	if e := c.ValidateConfigurationFiles(files, t.UID, t.GID); e != nil {
		return ConfigurationObservation{}, e
	}
	before, e := c.exactModelTask(ctx, t, id)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	if before.State.Status != "exited" {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_EXITED_MODEL_TASK_REQUIRED")
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+id+"/archive", url.Values{"path": {ModelTaskDestination}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusOK {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_MODEL_TASK_RESULT_READBACK_FAILED")
	}
	result, e := inspectPrivateArchive(raw, files, t.UID, t.GID, path.Base(ModelTaskDestination))
	if e != nil {
		return result, e
	}
	if result.Status != "verified_private_volume_payload" {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_MODEL_TASK_PRIVATE_RESULT_REQUIRED")
	}
	after, e := c.exactModelTask(ctx, t, id)
	if e != nil || before.State != after.State {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_MODEL_TASK_CHANGED_DURING_READBACK")
	}
	result.Qualification = "verified_exited_task_private_bytes_only_not_successful_initialization_application_or_target"
	return result, nil
}
