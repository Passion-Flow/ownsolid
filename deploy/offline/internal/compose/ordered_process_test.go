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

// This is an explicit stateful CLI/Engine double. Real PG/Control acceptance
// must separately exercise the delivered operator and images.
type orderedReader struct {
	*runtimeDouble
	postgres engine.RuntimeContainer
	root     string
}

func (d *orderedReader) InspectRuntime(_ context.Context, _ string, name string) (engine.RuntimeContainer, bool, error) {
	raw, _ := json.Marshal(d.container)
	if name == "postgres" {
		raw, _ = json.Marshal(d.postgres)
	}
	var object engine.RuntimeContainer
	json.Unmarshal(raw, &object)
	state, err := os.ReadFile(filepath.Join(d.root, name))
	if err == nil {
		object.State.Status = string(state)
		object.State.StartedAt = "2026-10-27T12:00:00Z"
		object.State.Running = string(state) == "running"
		if object.State.Running {
			object.State.PID = 123
			for key, endpoint := range object.NetworkSettings.Networks {
				endpoint.NetworkID = d.network.ID
				object.NetworkSettings.Networks[key] = endpoint
			}
		}
	}
	return object, true, nil
}
func (d *orderedReader) InspectRuntimeNetwork(ctx context.Context, id string) (engine.RuntimeNetwork, bool, error) {
	raw, _ := json.Marshal(d.network)
	var network engine.RuntimeNetwork
	json.Unmarshal(raw, &network)
	peers := map[string]any{}
	for _, name := range []string{"control", "postgres"} {
		object, _, _ := d.InspectRuntime(ctx, "", name)
		if object.State.Running {
			peers[object.ID] = map[string]string{"Name": strings.TrimPrefix(object.Name, "/")}
		}
	}
	raw, _ = json.Marshal(map[string]any{"Containers": peers})
	json.Unmarshal(raw, &network)
	return network, true, nil
}
func (d *orderedReader) RuntimeCandidates(_ context.Context, _ string, networks bool) ([]engine.RuntimeObject, error) {
	if networks {
		return []engine.RuntimeObject{{ID: d.network.ID}}, nil
	}
	return []engine.RuntimeObject{{ID: d.container.ID}, {ID: d.postgres.ID}}, nil
}

func orderedFixture(t *testing.T, fault string) (*Client, RenderContext, Model, *orderedReader) {
	t.Helper()
	_, scope, model, base := runtimeFixture(t)
	root := t.TempDir()
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }
	_, c := fixture(t, fmt.Sprintf(`if [ "${10}" = version ]; then printf '{"version":"v5.5.1"}'; exit; fi
root=%s
fault=%s
case "${16}" in
 start)
  name=${17}; [ "$#" -eq 17 ] || exit 71
  [ "$name" != control ] || [ -f "$root/ready" ] || exit 72
  printf running > "$root/$name"
  printf 'start %%s\n' "$name" >> "$root/actions"
  [ "$fault" != start-loss ] || [ "$name" != postgres ] || exit 75
  ;;
 stop)
  name=${19}; [ "$#" -eq 19 ] && [ "${17}" = --timeout ] || exit 73
  [ "$name" != postgres ] || [ "$(cat "$root/control")" = exited ] || exit 74
  printf exited > "$root/$name"
  printf 'stop %%s\n' "$name" >> "$root/actions"
  ;;
 exec)
  [ "$#" -eq 26 ] && [ "${17}" = --no-tty ] && [ "${18}" = --interactive=false ] && [ "${19}" = postgres ] && [ "${20}" = pg_isready ] && [ "${21}" = --quiet ] && [ "${22}" = --host=127.0.0.1 ] && [ "${23}" = --port=5432 ] && [ "${24}" = --username=synthetic ] && [ "${25}" = --dbname=synthetic ] && [ "${26}" = --timeout=2 ] || exit 76
  [ "$fault" != never-ready ] || exit 1
  if [ ! -f "$root/probed" ]; then : > "$root/probed"; exit 1; fi
  : > "$root/ready"
  ;;
 *) exit 77 ;;
esac`, quote(root), quote(fault)))
	c.settings.Process = &wire.ProcessSettings{ReservePercent: 30, StopSeconds: 1, ObserveSeconds: 2, PollMilliseconds: 50}
	pg := model.Services["control"]
	pg.Environment = map[string]string{"POSTGRES_USER": "synthetic", "POSTGRES_DB": "synthetic"}
	model.Services["postgres"] = pg
	raw, _ := json.Marshal(base.container)
	var object engine.RuntimeContainer
	json.Unmarshal(raw, &object)
	object.ID = strings.Repeat("e", 64)
	object.Name = "/" + engine.RuntimeContainerName(scope.ScopeID, "postgres")
	object.Config.Labels["com.docker.compose.service"] = "postgres"
	object.Config.Env = append(object.Config.Env, "POSTGRES_USER=synthetic", "POSTGRES_DB=synthetic")
	return c, scope, model, &orderedReader{runtimeDouble: base, postgres: object, root: root}
}

func TestOrderedProcessStartsReadyDatabaseFirstAndStopsItLast(t *testing.T) {
	c, scope, model, d := orderedFixture(t, "")
	before, err := c.ObserveRuntime(context.Background(), d, scope, model)
	if err != nil {
		t.Fatal(err)
	}
	after, err := c.StartRuntime(context.Background(), d, scope, model, before)
	if err != nil || !processDesired(after, true) {
		t.Fatal(after, err)
	}
	if _, err = c.StartRuntime(context.Background(), d, scope, model, after); err != nil {
		t.Fatal(err)
	}
	after, err = c.StopRuntime(context.Background(), d, scope, model, after)
	if err != nil || !processDesired(after, false) {
		t.Fatal(after, err)
	}
	if _, err = c.StopRuntime(context.Background(), d, scope, model, after); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(filepath.Join(d.root, "actions"))
	if err != nil || string(log) != "start postgres\nstart control\nstop control\nstop postgres\n" {
		t.Fatal(string(log), err)
	}
}

func TestOrderedDatabaseFailureNeverStartsControlOrGrantsReplay(t *testing.T) {
	for _, fault := range []string{"never-ready", "start-loss"} {
		t.Run(fault, func(t *testing.T) {
			c, scope, model, d := orderedFixture(t, fault)
			before, err := c.ObserveRuntime(context.Background(), d, scope, model)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.StartRuntime(context.Background(), d, scope, model, before)
			if err == nil || ProcessNotSubmitted(err, scope, model, before, true, nil) {
				t.Fatal("partial start granted whole-sequence replay", err)
			}
			log, _ := os.ReadFile(filepath.Join(d.root, "actions"))
			if string(log) != "start postgres\n" {
				t.Fatal(string(log))
			}
		})
	}
}
