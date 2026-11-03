package compose

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

// One owning scope intent covers this sequence. Partial effects stay uncertain;
// a failed child never grants a whole-sequence not-submitted proof or retry.
func (c *Client) orderedProcess(ctx context.Context, r RuntimeReader, scope RenderContext, model Model, recorded RuntimeStatus, start bool) (RuntimeStatus, error) {
	if _, hasDatabase := model.Services["postgres"]; !hasDatabase || len(model.Services) == 1 {
		return c.process(ctx, r, scope, model, recorded, start)
	}
	if c.settings.Process == nil || c.settings.Validate() != nil {
		return recorded, failure("OFFLINE_COMPOSE_PROCESS_CONFIGURATION_REQUIRED")
	}
	if start && (!modelNamePattern.MatchString(model.Services["postgres"].Environment["POSTGRES_USER"]) || !modelNamePattern.MatchString(model.Services["postgres"].Environment["POSTGRES_DB"])) {
		return recorded, failure("OFFLINE_COMPOSE_DATABASE_READINESS_CONFIGURATION_REQUIRED")
	}
	order := []string{"postgres", "control", "gateway"}
	if !start {
		order = []string{"gateway", "control", "postgres"}
	}
	result := recorded
	for _, name := range order {
		if _, present := model.Services[name]; !present {
			continue
		}
		var err error
		result, err = c.processService(ctx, r, scope, model, result, start, []string{name}, true)
		if err != nil {
			return result, failure("OFFLINE_COMPOSE_ORDERED_PROCESS_INCOMPLETE")
		}
		if start && name == "postgres" {
			if err = c.waitDatabase(ctx, r, scope, model, result); err != nil {
				return result, err
			}
		}
	}
	if !processIdentity(recorded, result) || !processDesired(result, start) {
		return result, failure("OFFLINE_COMPOSE_ORDERED_PROCESS_INCOMPLETE")
	}
	result.Status = "observed_runtime_processes_stopped"
	if start {
		result.Status = "observed_runtime_processes_running"
	}
	result.Qualification = "observed_ordered_process_state_not_application_readiness_or_target"
	return result, nil
}

// Pinned PostgreSQL images expose the existing database-task TCP contract on
// container loopback:5432. This checks server acceptance, not schema/auth health.
// Only read-only probes repeat; deadlines and polling use typed process policy.
func (c *Client) waitDatabase(ctx context.Context, r RuntimeReader, scope RenderContext, model Model, recorded RuntimeStatus) error {
	bad := func() error { return failure("OFFLINE_COMPOSE_DATABASE_READINESS_UNAVAILABLE") }
	before, found, err := r.InspectRuntime(ctx, scope.ScopeID, "postgres")
	if err != nil || !found || before.ID != recorded.Services["postgres"].ID || before.State.PID < 1 || !before.State.Running || before.State.Status != "running" || before.State.OOMKilled || before.State.Restarting || before.State.Dead || before.State.Paused {
		return bad()
	}
	document, err := c.document(scope, model)
	if err != nil {
		return bad()
	}
	var literal any
	if json.Unmarshal(document, &literal) != nil {
		return bad()
	}
	document, _ = json.Marshal(escapeInterpolation(literal))
	service := model.Services["postgres"]
	if !modelNamePattern.MatchString(service.Environment["POSTGRES_USER"]) || !modelNamePattern.MatchString(service.Environment["POSTGRES_DB"]) {
		return bad()
	}
	args := []string{"--project-name", engine.RuntimeProject(scope.ScopeID), "--project-directory", scope.ScopeRoot, "-f", "-", "exec", "--no-tty", "--interactive=false", "postgres", "pg_isready", "--quiet", "--host=127.0.0.1", "--port=5432", "--username=" + service.Environment["POSTGRES_USER"], "--dbname=" + service.Environment["POSTGRES_DB"], "--timeout=" + strconv.Itoa(c.settings.Process.ObserveSeconds)}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.settings.Process.ObserveSeconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Duration(c.settings.Process.PollMilliseconds) * time.Millisecond)
	defer ticker.Stop()
	for {
		_, probeError := c.execute(ctx, args, document)
		after, found, err := r.InspectRuntime(ctx, scope.ScopeID, "postgres")
		if err != nil || !found || before.ID != after.ID || before.State != after.State {
			return bad()
		}
		observed, err := c.ObserveRuntime(ctx, r, scope, model)
		if err != nil || !processIdentity(recorded, observed) || !nonselectedRuntimeUnchanged(recorded, observed, []string{"postgres"}) || observed.Services["postgres"].State != "running" {
			return bad()
		}
		if probeError == nil && ctx.Err() == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return bad()
		case <-ticker.C:
		}
	}
}
