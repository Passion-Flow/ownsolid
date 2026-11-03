package engine

import (
	"context"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// InspectStoppedControlConfiguration reads a current, never-started control's
// read-only mounted TLS generation. It returns only verified inventory facts;
// private certificate/key/runtime payloads never leave this boundary.
func (c *Client) InspectStoppedControlConfiguration(ctx context.Context, o Ownership, id, destination string, uid, gid int, files []ConfigurationFile) (ConfigurationObservation, error) {
	return c.inspectControlConfiguration(ctx, o, id, destination, uid, gid, files, true, false)
}

// InspectCurrentControlConfiguration verifies current bytes without authorizing
// a process action. The scope must separately establish trusted activation history.
func (c *Client) InspectCurrentControlConfiguration(ctx context.Context, o Ownership, id, destination string, uid, gid int, files []ConfigurationFile) (ConfigurationObservation, error) {
	return c.inspectControlConfiguration(ctx, o, id, destination, uid, gid, files, false, false)
}

// InspectFailedControlConfiguration is a fixed read-only entrance. The scope
// separately proves a recorded failed start and current retry authorization.
func (c *Client) InspectFailedControlConfiguration(ctx context.Context, o Ownership, id, destination string, uid, gid int, files []ConfigurationFile) (ConfigurationObservation, error) {
	return c.inspectControlConfiguration(ctx, o, id, destination, uid, gid, files, false, true)
}

func (c *Client) inspectControlConfiguration(ctx context.Context, o Ownership, id, destination string, uid, gid int, files []ConfigurationFile, neverStarted, failedExit bool) (ConfigurationObservation, error) {
	bad := func() (ConfigurationObservation, error) {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_STOPPED_CONTROL_CONFIGURATION_CONTEXT_INVALID")
	}
	if !currentConfigurationOwner(o) || !containerIdentity.MatchString(id) || len(files) == 0 || path.Clean(destination) != destination || !strings.HasPrefix(destination, "/run/projectone/") || strings.ContainsAny(destination, "\\\x00\r\n") {
		return bad()
	}
	if e := c.ValidateConfigurationFiles(files, uid, gid); e != nil {
		return ConfigurationObservation{}, e
	}
	before, found, e := c.InspectRuntime(ctx, o.ScopeID, "control")
	if e != nil {
		return ConfigurationObservation{}, e
	}
	owned := currentOwnedControl(o.ScopeID, before)
	if failedExit {
		owned = failedOwnedControl(o.ScopeID, before)
	}
	if !found || before.ID != id || !owned || neverStarted && before.State.Status != "created" || before.Config.User != strconv.Itoa(uid)+":"+strconv.Itoa(gid) {
		return bad()
	}
	configured, actual := 0, 0
	for _, m := range before.HostConfig.Mounts {
		if m.Target != destination {
			continue
		}
		if m.Type != "volume" || m.Source != o.VolumeName() || !m.ReadOnly || m.VolumeOptions == nil || !m.VolumeOptions.NoCopy || m.VolumeOptions.Subpath != "" {
			return bad()
		}
		configured++
	}
	for _, m := range before.Mounts {
		if m.Destination != destination {
			continue
		}
		if m.Type != "volume" || m.Name != o.VolumeName() || m.Driver != "local" || m.RW {
			return bad()
		}
		actual++
	}
	if configured != 1 || actual != 1 {
		return bad()
	}
	if e = c.VerifyVolumeConsumers(ctx, o, []string{id}); e != nil {
		return ConfigurationObservation{}, e
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+id+"/archive", url.Values{"path": {destination}}, nil, c.config.RequestSeconds)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	if status != http.StatusOK {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_STOPPED_CONTROL_CONFIGURATION_READBACK_FAILED")
	}
	observed, e := inspectPrivateArchive(raw, files, uid, gid, path.Base(destination))
	if e != nil {
		return ConfigurationObservation{}, e
	}
	if observed.Status != ConfigurationPayloadVerified {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_STOPPED_CONTROL_CONFIGURATION_PAYLOAD_REQUIRED")
	}
	after, found, e := c.InspectRuntime(ctx, o.ScopeID, "control")
	if e != nil || !found {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_STOPPED_CONTROL_CONFIGURATION_CHANGED")
	}
	first, fe := RuntimeContainerDigest(before)
	last, le := RuntimeContainerDigest(after)
	if fe != nil || le != nil || first != last {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_STOPPED_CONTROL_CONFIGURATION_CHANGED")
	}
	observed.Qualification = "stopped_control_private_payload_only_not_serving_activation_or_target"
	if !neverStarted {
		observed.Qualification = "current_control_private_payload_only_not_activation_authorization_readiness_or_target"
	}
	return observed, nil
}
