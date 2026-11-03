package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// RuntimeContainerDigest binds private metadata without publishing environment
// or mount values. Callers must independently admit the expected runtime/model.
func RuntimeContainerDigest(object RuntimeContainer) (string, error) {
	raw, e := json.Marshal(object)
	if e != nil {
		return "", failure("OFFLINE_ENGINE_RUNTIME_METADATA_INVALID")
	}
	return wire.Digest(raw), nil
}
func ownedControlIdentity(scope string, object RuntimeContainer) bool {
	parts := strings.Split(object.Config.User, ":")
	if len(parts) != 2 {
		return false
	}
	uid, e := strconv.Atoi(parts[0])
	gid, ge := strconv.Atoi(parts[1])
	if e != nil || ge != nil || uid < 1 || gid < 1 || strconv.Itoa(uid) != parts[0] || strconv.Itoa(gid) != parts[1] {
		return false
	}
	labels := object.Config.Labels
	return objectIdentity.MatchString(scope) && containerIdentity.MatchString(object.ID) && object.Name == "/"+RuntimeContainerName(scope, "control") && object.Platform == "linux" && strings.HasPrefix(object.Image, "sha256:") && wire.ValidSHA256(strings.TrimPrefix(object.Image, "sha256:")) && object.Config.Image == object.Image && object.HostConfig.NetworkMode == RuntimeNetworkName(scope) && object.HostConfig.ReadonlyRootfs && !object.HostConfig.Privileged && !object.HostConfig.AutoRemove && labels["io.projectone.isolated-scope"] == scope && labels["io.projectone.purpose"] == "isolated-validation" && labels["com.docker.compose.project"] == RuntimeProject(scope) && labels["com.docker.compose.service"] == "control" && labels["com.docker.compose.container-number"] == "1" && labels["com.docker.compose.oneoff"] == "False"
}

func currentOwnedControl(scope string, object RuntimeContainer) bool {
	return ownedControlProcess(scope, object, false)
}

func failedOwnedControl(scope string, object RuntimeContainer) bool {
	return object.State.Status == "exited" && object.State.ExitCode > 0 && object.State.ExitCode <= 255 && ownedControlProcess(scope, object, true)
}

func ownedControlProcess(scope string, object RuntimeContainer, failedExit bool) bool {
	started, e := time.Parse(time.RFC3339Nano, object.State.StartedAt)
	if e != nil || !ownedControlIdentity(scope, object) || object.State.ExitCode != 0 && !failedExit || object.State.Restarting || object.State.Dead || object.State.Paused || object.State.OOMKilled {
		return false
	}
	if object.State.Status == "running" {
		return object.State.Running && object.State.PID > 0 && !started.IsZero()
	}
	return (object.State.Status == "created" || object.State.Status == "exited") && (object.State.Status == "created") == started.IsZero() && !object.State.Running && object.State.PID == 0
}

func stoppedOwnedControl(scope string, object RuntimeContainer) bool {
	return currentOwnedControl(scope, object) && object.State.Status != "running"
}

// RemoveStoppedControl is a low-level actual effect. The owning scope must sync
// intent first. It never kills a process or removes a volume, even on failure.
func (c *Client) RemoveStoppedControl(ctx context.Context, scope string, expected RuntimeContainer) error {
	if !stoppedOwnedControl(scope, expected) {
		return failure("OFFLINE_ENGINE_STOPPED_CONTROL_CONTEXT_INVALID")
	}
	actual, found, e := c.InspectRuntime(ctx, scope, "control")
	if e != nil {
		return e
	}
	if !found || !stoppedOwnedControl(scope, actual) || !reflect.DeepEqual(actual, expected) {
		return failure("OFFLINE_ENGINE_STOPPED_CONTROL_MISSING_OR_CHANGED")
	}
	_, status, e := c.request(ctx, http.MethodDelete, "/v"+c.policy.APIVersion+"/containers/"+url.PathEscape(expected.ID), url.Values{"force": {"false"}, "v": {"false"}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusNoContent {
		return failure("OFFLINE_ENGINE_STOPPED_CONTROL_REMOVAL_UNCERTAIN")
	}
	if _, found, e = c.InspectRuntime(ctx, scope, "control"); e != nil || found {
		return failure("OFFLINE_ENGINE_STOPPED_CONTROL_REMOVAL_UNCERTAIN")
	}
	return nil
}
