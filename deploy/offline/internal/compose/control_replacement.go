package compose

import (
	"context"
	"encoding/json"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// ControlConfigurationModel deep-copies the original model and changes only its
// unique read-only configuration source. The ordinary renderer remains authoritative.
func ControlConfigurationModel(original Model, generation string) (Model, error) {
	bad := func() (Model, error) { return Model{}, failure("OFFLINE_COMPOSE_CONTROL_CONFIGURATION_MODEL_INVALID") }
	if !scopeIDPattern.MatchString(generation) {
		return bad()
	}
	raw, e := json.Marshal(original)
	if e != nil {
		return bad()
	}
	model, e := DecodeModel(raw, int64(len(raw)))
	if e != nil {
		return bad()
	}
	next := engine.TLSConfigurationRole + "-" + generation
	declared, mounted := 0, 0
	for i, role := range model.Volumes {
		if role == next {
			return bad()
		}
		if role == "control-config" {
			declared++
			model.Volumes[i] = next
		}
	}
	for name, service := range model.Services {
		for i, mount := range service.Mounts {
			if mount.Source != "control-config" {
				continue
			}
			if name != "control" || mount.Type != "volume" || !mount.ReadOnly || !containerTarget(mount.Target) {
				return bad()
			}
			mounted++
			service.Mounts[i].Source = next
		}
		model.Services[name] = service
	}
	if declared != 1 || mounted != 1 {
		return bad()
	}
	return model, nil
}
func replacementPeersMatch(before, after RuntimeStatus, controlRequired bool) bool {
	if before.Status != "observed_complete_compose_resources" || !wire.ValidSHA256(before.NetworkID) || before.NetworkID != after.NetworkID || before.IngressNetworkID != after.IngressNetworkID || len(before.Services) < 1 {
		return false
	}
	control, ok := before.Services["control"]
	if !ok || !wire.ValidSHA256(control.ID) {
		return false
	}
	for name, service := range before.Services {
		if !wire.ValidSHA256(service.ID) || service.State != "created" && service.State != "exited" {
			return false
		}
		if name == "control" {
			continue
		}
		if actual, ok := after.Services[name]; !ok || actual != service {
			return false
		}
	}
	current, exists := after.Services["control"]
	if controlRequired {
		return exists && after.Status == "observed_complete_compose_resources" && len(after.Services) == len(before.Services) && current.ID != control.ID && wire.ValidSHA256(current.ID) && current.State == "created" && current.ExitCode == 0
	}
	return !exists && after.Status == "observed_partial_compose_resources" && len(after.Services) == len(before.Services)-1
}

// ObserveControlReplacement only inspects. The scope chooses whether an absent
// or complete result resolves its own pending create/remove intent.
func (c *Client) ObserveControlReplacement(ctx context.Context, r RuntimeReader, scope RenderContext, model Model, before RuntimeStatus, controlRequired bool) (RuntimeStatus, error) {
	actual, e := c.ObserveRuntime(ctx, r, scope, model)
	if e != nil {
		return actual, e
	}
	if !replacementPeersMatch(before, actual, controlRequired) {
		return actual, failure("OFFLINE_COMPOSE_CONTROL_REPLACEMENT_CONTEXT_CHANGED")
	}
	return actual, nil
}

// CreateReplacementControl submits one selected create after durable scope
// intent. Existing/changed peers or an existing control always refuse resubmission.
func (c *Client) CreateReplacementControl(ctx context.Context, r RuntimeReader, scope RenderContext, model Model, before RuntimeStatus) (RuntimeStatus, error) {
	if _, e := c.Version(ctx); e != nil {
		return RuntimeStatus{}, e
	}
	actual, e := c.ObserveControlReplacement(ctx, r, scope, model, before, false)
	if e != nil {
		return actual, e
	}
	document, e := c.document(scope, model)
	if e != nil {
		return actual, e
	}
	var literal any
	if json.Unmarshal(document, &literal) != nil {
		return actual, failure("OFFLINE_COMPOSE_MODEL_INPUT_INVALID")
	}
	document, _ = json.Marshal(escapeInterpolation(literal))
	args := []string{"--project-name", engine.RuntimeProject(scope.ScopeID), "--project-directory", scope.ScopeRoot, "-f", "-", "create", "--no-build", "--pull", "never", "--no-recreate", "control"}
	if _, e = c.execute(ctx, args, document); e != nil {
		return actual, failure("OFFLINE_COMPOSE_CONTROL_REPLACEMENT_CREATE_UNCERTAIN")
	}
	return c.ObserveControlReplacement(ctx, r, scope, model, before, true)
}
