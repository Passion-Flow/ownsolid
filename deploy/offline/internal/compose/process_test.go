package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestProcessRequiresConfigurationAndRecordedIdentity(t *testing.T) {
	c, scope, model, d := runtimeFixture(t)
	recorded, e := c.ObserveRuntime(context.Background(), d, scope, model)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.StartRuntime(context.Background(), d, scope, model, recorded); e == nil {
		t.Fatal("missing process settings accepted")
	}
	c.settings.Process = &wire.ProcessSettings{ReservePercent: 30, StopSeconds: 1, ObserveSeconds: 1, PollMilliseconds: 50}
	changed := recorded
	changed.NetworkID = "foreign"
	if _, e = c.StartRuntime(context.Background(), d, scope, model, changed); e == nil {
		t.Fatal("foreign recorded network accepted")
	}
	status, e := c.StopRuntime(context.Background(), d, scope, model, recorded)
	if e != nil || status.Status != "observed_runtime_processes_stopped" {
		t.Fatal(status, e)
	}
}

// This is a metadata/process double only. The marker is written by an explicit fake CLI.
type databaseRuntimeReader struct {
	base     *runtimeDouble
	postgres engine.RuntimeContainer
	marker   string
	selected string
}

func (d *databaseRuntimeReader) selectedService() string {
	if d.selected == "" {
		return "postgres"
	}
	return d.selected
}

func (d *databaseRuntimeReader) Inspect(ctx context.Context, id string) (engine.Image, bool, error) {
	return d.base.Inspect(ctx, id)
}
func (d *databaseRuntimeReader) InspectRuntime(_ context.Context, _ string, name string) (engine.RuntimeContainer, bool, error) {
	r := d.postgres
	if name == "control" {
		r = d.base.container
	}
	if _, e := os.Stat(d.marker); e == nil && name == d.selectedService() {
		r.State.Status = "running"
		r.State.Running = true
		r.State.PID = 123
		r.State.StartedAt = "2026-10-17T00:00:01Z"
		for name, endpoint := range r.NetworkSettings.Networks {
			endpoint.NetworkID = d.base.network.ID
			r.NetworkSettings.Networks[name] = endpoint
		}
	}
	return r, true, nil
}
func (d *databaseRuntimeReader) InspectRuntimeNetwork(context.Context, string) (engine.RuntimeNetwork, bool, error) {
	n := d.base.network
	if _, e := os.Stat(d.marker); e == nil {
		selected := d.postgres
		if d.selectedService() == "control" {
			selected = d.base.container
		}
		raw, _ := json.Marshal(map[string]any{"Containers": map[string]any{selected.ID: map[string]any{"Name": strings.TrimPrefix(selected.Name, "/")}}})
		json.Unmarshal(raw, &n)
	}
	return n, true, nil
}
func (d *databaseRuntimeReader) InspectRuntimeIngress(ctx context.Context, id string) (engine.RuntimeNetwork, bool, error) {
	return d.base.InspectRuntimeIngress(ctx, id)
}
func (d *databaseRuntimeReader) RuntimeCandidates(_ context.Context, _ string, networks bool) ([]engine.RuntimeObject, error) {
	if networks {
		return []engine.RuntimeObject{{ID: d.base.network.ID}}, nil
	}
	return []engine.RuntimeObject{{ID: d.base.container.ID}, {ID: d.postgres.ID}}, nil
}
func TestDatabaseProcessDriverUsesExplicitPostgresArgumentAndNoOtherStart(t *testing.T) {
	testSelectedProcessDriver(t, "postgres")
}
func TestControlProcessDriverUsesExplicitControlArgumentAndNoOtherStart(t *testing.T) {
	testSelectedProcessDriver(t, "control")
}
func testSelectedProcessDriver(t *testing.T, role string) {
	t.Helper()
	_, scope, model, base := runtimeFixture(t)
	marker := filepath.Join(t.TempDir(), "selected-started")
	_, client := fixture(t, fmt.Sprintf(`if [ "${10}" = version ]; then printf '{"version":"v5.5.1"}'; exit; fi
[ "$#" -eq 17 ] && [ "${16}" = start ] && [ "${17}" = %s ] || exit 71
: > '%s'`, role, strings.ReplaceAll(marker, "'", "'\"'\"'")))
	client.settings.Process = &wire.ProcessSettings{ReservePercent: 30, StopSeconds: 1, ObserveSeconds: 1, PollMilliseconds: 50}
	pg := model.Services["control"]
	model.Services["postgres"] = pg
	raw, _ := json.Marshal(base.container)
	var object engine.RuntimeContainer
	json.Unmarshal(raw, &object)
	object.ID = strings.Repeat("e", 64)
	object.Name = "/" + engine.RuntimeContainerName(scope.ScopeID, "postgres")
	object.Config.Labels["com.docker.compose.service"] = "postgres"
	d := &databaseRuntimeReader{base: base, postgres: object, marker: marker, selected: role}
	before, e := client.ObserveRuntime(context.Background(), d, scope, model)
	if e != nil {
		t.Fatal(e)
	}
	start := client.StartDatabaseRuntime
	status, peer := "observed_selected_database_process_running", "control"
	if role == "control" {
		start, status, peer = client.StartControlRuntime, "observed_selected_control_process_running", "postgres"
	}
	after, e := start(context.Background(), d, scope, model, before)
	if e != nil || after.Status != status || after.Services[role].State != "running" || after.Services[peer] != before.Services[peer] {
		t.Fatal(after, e)
	}
	i, e := os.Stat(marker)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = start(context.Background(), d, scope, model, after); e != nil {
		t.Fatal(e)
	}
	current, _ := os.Stat(marker)
	if current.ModTime() != i.ModTime() {
		t.Fatal("completed selective start reissued")
	}
}
func TestStartedEarlyExitAndUnknownOutcomeRemainErrors(t *testing.T) {
	for _, exit := range []bool{false, true} {
		t.Run(map[bool]string{true: "early-exit", false: "unresolved"}[exit], func(t *testing.T) {
			c, scope, model, d := runtimeFixture(t)
			c.settings.Process = &wire.ProcessSettings{ReservePercent: 30, StopSeconds: 1, ObserveSeconds: 1, PollMilliseconds: 50}
			recorded, e := c.ObserveRuntime(context.Background(), d, scope, model)
			if e != nil {
				t.Fatal(e)
			}
			if exit {
				d.container.State.Status = "exited"
			}
			_, e = c.StartRuntime(context.Background(), d, scope, model, recorded)
			expected := "OFFLINE_COMPOSE_PROCESS_OUTCOME_UNRESOLVED"
			if exit {
				expected = "OFFLINE_COMPOSE_STARTED_PROCESS_EXITED"
			}
			if e == nil || e.Error() != expected {
				t.Fatal(e)
			}
		})
	}
}

func TestDatabaseSelectionRequiresPostgresAndPreservesOtherServiceStates(t *testing.T) {
	c, scope, model, d := runtimeFixture(t)
	if _, e := c.StartDatabaseRuntime(context.Background(), d, scope, model, RuntimeStatus{}); e == nil || e.Error() != "OFFLINE_COMPOSE_POSTGRES_SERVICE_REQUIRED" {
		t.Fatal(e)
	}
	if _, e := c.processSelected(context.Background(), d, scope, model, RuntimeStatus{}, true, []string{"gateway"}); e == nil {
		t.Fatal("arbitrary selector accepted")
	}
	if _, e := c.processSelected(context.Background(), d, scope, model, RuntimeStatus{}, false, []string{"postgres"}); e == nil {
		t.Fatal("undeclared database-only stop accepted")
	}
	before := RuntimeStatus{Services: map[string]RuntimeServiceStatus{"control": {ID: "control-id", State: "created"}, "postgres": {ID: "postgres-id", State: "created"}}}
	after := RuntimeStatus{Services: map[string]RuntimeServiceStatus{"control": before.Services["control"], "postgres": {ID: "postgres-id", State: "running"}}}
	selected := []string{"postgres"}
	if !selectedRuntimeDesired(after, true, selected) || !nonselectedRuntimeUnchanged(before, after, selected) || selectedRuntimeDesired(after, true, nil) {
		t.Fatal("selected versus whole-runtime outcome")
	}
	changed := after.Services["control"]
	changed.State = "running"
	after.Services["control"] = changed
	if nonselectedRuntimeUnchanged(before, after, selected) {
		t.Fatal("nonselected state change accepted")
	}
}
