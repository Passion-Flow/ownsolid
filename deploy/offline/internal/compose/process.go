package compose

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

// Process methods require an owning scope's durable intent and recorded IDs.
// They perform no retry of an uncertain effect and do not establish application health.
func (c *Client) StartRuntime(ctx context.Context, reader RuntimeReader, scope RenderContext, model Model, recorded RuntimeStatus) (RuntimeStatus, error) {
	return c.orderedProcess(ctx, reader, scope, model, recorded, true)
}
func (c *Client) StopRuntime(ctx context.Context, reader RuntimeReader, scope RenderContext, model Model, recorded RuntimeStatus) (RuntimeStatus, error) {
	return c.orderedProcess(ctx, reader, scope, model, recorded, false)
}
func (c *Client) StartDatabaseRuntime(ctx context.Context, reader RuntimeReader, scope RenderContext, model Model, recorded RuntimeStatus) (RuntimeStatus, error) {
	return c.processSelected(ctx, reader, scope, model, recorded, true, []string{"postgres"})
}
func (c *Client) StartControlRuntime(ctx context.Context, reader RuntimeReader, scope RenderContext, model Model, recorded RuntimeStatus) (RuntimeStatus, error) {
	return c.processSelected(ctx, reader, scope, model, recorded, true, []string{"control"})
}
func processIdentity(recorded, observed RuntimeStatus) bool {
	if recorded.NetworkID == "" || recorded.NetworkID != observed.NetworkID || recorded.IngressNetworkID != observed.IngressNetworkID || len(recorded.Services) == 0 || len(recorded.Services) != len(observed.Services) {
		return false
	}
	for name, service := range recorded.Services {
		if observed.Services[name].ID != service.ID {
			return false
		}
	}
	return true
}
func processDesired(status RuntimeStatus, start bool) bool {
	for _, service := range status.Services {
		if start {
			if service.State != "running" {
				return false
			}
		} else if service.State != "created" && service.State != "exited" {
			return false
		}
	}
	return len(status.Services) > 0
}
func (c *Client) process(ctx context.Context, reader RuntimeReader, scope RenderContext, model Model, recorded RuntimeStatus, start bool) (RuntimeStatus, error) {
	return c.processSelected(ctx, reader, scope, model, recorded, start, nil)
}
func selectedRuntimeDesired(status RuntimeStatus, start bool, selected []string) bool {
	if len(selected) == 0 {
		return processDesired(status, start)
	}
	if len(selected) != 1 {
		return false
	}
	service, found := status.Services[selected[0]]
	if !found {
		return false
	}
	return start && service.State == "running" || !start && (service.State == "created" || service.State == "exited")
}
func nonselectedRuntimeUnchanged(before, after RuntimeStatus, selected []string) bool {
	if len(selected) == 0 {
		return true
	}
	for name, value := range before.Services {
		if len(selected) != 1 || name != selected[0] && after.Services[name] != value {
			return false
		}
	}
	return true
}
func (c *Client) processSelected(ctx context.Context, reader RuntimeReader, scope RenderContext, model Model, recorded RuntimeStatus, start bool, selected []string) (result RuntimeStatus, resultError error) {
	return c.processService(ctx, reader, scope, model, recorded, start, selected, false)
}
func (c *Client) processService(ctx context.Context, reader RuntimeReader, scope RenderContext, model Model, recorded RuntimeStatus, start bool, selected []string, ordered bool) (result RuntimeStatus, resultError error) {
	submitted := false
	submissionContext := processSubmissionContext(scope, model, recorded, start, selected)
	defer func() {
		if resultError != nil && !submitted {
			resultError = unsubmittedProcessFailure(resultError, submissionContext)
		}
	}()
	if len(selected) > 0 {
		if len(selected) != 1 || !ordered && (!start || selected[0] != "postgres" && selected[0] != "control") || ordered && selected[0] != "postgres" && selected[0] != "control" && selected[0] != "gateway" {
			return result, failure("OFFLINE_COMPOSE_PROCESS_SELECTION_INVALID")
		}
		if _, ok := model.Services[selected[0]]; !ok {
			if selected[0] == "postgres" {
				return result, failure("OFFLINE_COMPOSE_POSTGRES_SERVICE_REQUIRED")
			}
			return result, failure("OFFLINE_COMPOSE_CONTROL_SERVICE_REQUIRED")
		}
	}
	if c.settings.Process == nil {
		return result, failure("OFFLINE_COMPOSE_PROCESS_CONFIGURATION_REQUIRED")
	}
	if e := c.settings.Validate(); e != nil {
		return result, e
	}
	if _, e := c.Version(ctx); e != nil {
		return result, e
	}
	result, e := c.ObserveRuntime(ctx, reader, scope, model)
	if e != nil {
		return result, e
	}
	if result.Status != "observed_complete_compose_resources" || !processIdentity(recorded, result) || !nonselectedRuntimeUnchanged(recorded, result, selected) {
		return result, failure("OFFLINE_COMPOSE_RECORDED_PROCESS_IDENTITY_MISMATCH")
	}
	qualify := func() (RuntimeStatus, error) {
		if start {
			result.Status = "observed_runtime_processes_running"
		} else {
			result.Status = "observed_runtime_processes_stopped"
		}
		if len(selected) > 0 {
			result.Status = "observed_selected_database_process_running"
			if selected[0] == "control" {
				result.Status = "observed_selected_control_process_running"
			}
		}
		result.Qualification = "observed_process_state_only_not_application_readiness_or_target"
		return result, nil
	}
	if selectedRuntimeDesired(result, start, selected) {
		return qualify()
	}
	document, e := c.document(scope, model)
	if e != nil {
		return result, e
	}
	var literal any
	if json.Unmarshal(document, &literal) != nil {
		return result, failure("OFFLINE_COMPOSE_MODEL_INPUT_INVALID")
	}
	document, _ = json.Marshal(escapeInterpolation(literal))
	args := []string{"--project-name", engine.RuntimeProject(scope.ScopeID), "--project-directory", scope.ScopeRoot, "-f", "-"}
	if start {
		args = append(args, "start")
	} else {
		args = append(args, "stop", "--timeout", strconv.Itoa(c.settings.Process.StopSeconds))
	}
	args = append(args, selected...)
	if _, submitted, e = c.executeSubmission(ctx, args, document); e != nil {
		return result, failure("OFFLINE_COMPOSE_PROCESS_ACTION_UNCERTAIN")
	}
	observedContext, cancel := context.WithTimeout(ctx, time.Duration(c.settings.Process.ObserveSeconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Duration(c.settings.Process.PollMilliseconds) * time.Millisecond)
	defer ticker.Stop()
	for {
		result, e = c.ObserveRuntime(observedContext, reader, scope, model)
		if e != nil {
			return result, e
		}
		if result.Status != "observed_complete_compose_resources" || !processIdentity(recorded, result) {
			return result, failure("OFFLINE_COMPOSE_RECORDED_PROCESS_IDENTITY_MISMATCH")
		}
		if !nonselectedRuntimeUnchanged(recorded, result, selected) {
			return result, failure("OFFLINE_COMPOSE_NONSELECTED_PROCESS_CHANGED")
		}
		if selectedRuntimeDesired(result, start, selected) {
			return qualify()
		}
		if start {
			for name, service := range result.Services {
				if len(selected) > 0 && name != selected[0] {
					continue
				}
				if service.State == "exited" {
					return result, failure("OFFLINE_COMPOSE_STARTED_PROCESS_EXITED")
				}
			}
		}
		select {
		case <-observedContext.Done():
			return result, failure("OFFLINE_COMPOSE_PROCESS_OUTCOME_UNRESOLVED")
		case <-ticker.C:
		}
	}
}
