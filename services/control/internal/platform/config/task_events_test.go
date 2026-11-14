package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

func taskEventsRootInput(t *testing.T, component string) []byte {
	t.Helper()
	raw, e := yaml.Marshal(validConfig())
	if e != nil {
		t.Fatal(e)
	}
	if component != "" {
		raw = append(raw, []byte("\ntask_events:\n  "+strings.ReplaceAll(strings.TrimSpace(component), "\n", "\n  ")+"\n")...)
	}
	return raw
}

func taskEventsExample(t *testing.T) ([]byte, taskbus.Configuration) {
	t.Helper()
	raw, e := os.ReadFile("../../../config/examples/task-events/control.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := taskbus.DecodeYAMLConfiguration(raw)
	if e != nil {
		t.Fatal("actual component example invalid", e)
	}
	return raw, c
}

func TestTaskEventsRootOmittedFalseAndCompleteNondefault(t *testing.T) {
	t.Setenv(taskbus.EnvironmentPrefix+"ENABLED", "true")
	for _, disabled := range []string{"", "enabled: false"} {
		c, e := Decode(taskEventsRootInput(t, disabled))
		if e != nil || !reflect.DeepEqual(c.TaskEvents, taskbus.Configuration{}) {
			t.Fatal("omitted/explicit false read ambient or changed disabled state", e)
		}
	}
	raw, expected := taskEventsExample(t)
	c, e := Decode(taskEventsRootInput(t, string(raw)))
	if e != nil || !reflect.DeepEqual(c.TaskEvents, expected) || !c.TaskEvents.Enabled {
		t.Fatal("full actual nondefault example changed", e)
	}
}

func TestTaskEventsRootProtectedEnvironmentGroupedOverrides(t *testing.T) {
	raw, expected := taskEventsExample(t)
	values := "PROJECT_ONE_TASK_EVENTS_REQUEST_SECONDS=8\nPROJECT_ONE_TASK_EVENTS_ACK_WAIT_SECONDS=30\nPROJECT_ONE_TASK_EVENTS_SHUTDOWN_SECONDS=32\nPROJECT_ONE_TASK_EVENTS_DIAL_ADDRESSES='[\"127.0.0.2\"]'\nPROJECT_ONE_TASK_EVENTS_ENDPOINT=tls://nondefault-private-marker.invalid:11442\n"
	environment := workspaceEnvironmentFixture(t, values)
	root := filepath.Dir(environment)
	path := filepath.Join(root, "control.yaml")
	if e := os.WriteFile(path, taskEventsRootInput(t, string(raw)), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := LoadWithEnvironment(path, environment)
	expected.RequestSeconds, expected.AckWaitSeconds, expected.ShutdownSeconds = 8, 30, 32
	expected.DialAddresses, expected.Endpoint = []string{"127.0.0.2"}, "tls://nondefault-private-marker.invalid:11442"
	if e != nil || !reflect.DeepEqual(c.TaskEvents, expected) {
		t.Fatal("protected grouped overrides not applied", e)
	}
	encoded, e := json.Marshal(c)
	if e != nil || strings.Contains(string(encoded), "nondefault-private-marker") || strings.Contains(string(encoded), "publisher_seed_file") || strings.Contains(fmt.Sprintf("%+v", c), "nondefault-private-marker") {
		t.Fatal("private component entered JSON or format diagnostics", e)
	}
	c, e = decodeWithEnvironment(taskEventsRootInput(t, ""), workspaceEnvironmentFixture(t, "PROJECT_ONE_TASK_EVENTS_ENABLED=false\n"))
	if e != nil || c.TaskEvents.Enabled {
		t.Fatal("explicit false environment became enabled", e)
	}
}

func TestTaskEventsRootRejectsInvalidBaseAndAmbiguousEnvironment(t *testing.T) {
	raw, _ := taskEventsExample(t)
	base := taskEventsRootInput(t, string(raw))
	for _, invalid := range []string{"PROJECT_ONE_TASK_EVENTS_UNKNOWN=false\n", "PROJECT_ONE_TASK_EVENTS_ENABLED=False\n", "PROJECT_ONE_TASK_EVENTS_ENABLED=1\n", "PROJECT_ONE_TASK_EVENTS_ENABLED=\n", "PROJECT_ONE_TASK_EVENTS_ENABLED=false\n", "PROJECT_ONE_TASK_EVENTS_RETRY_SECONDS=1\nPROJECT_ONE_TASK_EVENTS_RETRY_SECONDS=2\n", "PROJECT_ONE_TASK_EVENTS_REQUEST_SECONDS=300\n", "PROJECT_ONE_TASK_EVENTS_DIAL_ADDRESSES='[\"8.8.8.8\"]'\n", "PROJECT_ONE_TASK_EVENTS_CA_FILE=relative\n"} {
		if _, e := decodeWithEnvironment(base, workspaceEnvironmentFixture(t, invalid)); e == nil {
			t.Fatal("invalid/contradictory environment accepted", invalid)
		}
	}
	fix := workspaceEnvironmentFixture(t, "PROJECT_ONE_TASK_EVENTS_ENABLED=false\n")
	for _, invalid := range []string{"null", "{}", "enabled: False", "enabled: 'false'", "enabled: false\nunknown: 1", "enabled: false\nenabled: false", "enabled: true", "enabled: &flag false\ndriver: *flag"} {
		if _, e := decodeWithEnvironment(taskEventsRootInput(t, invalid), fix); e == nil {
			t.Fatal("invalid YAML repaired by environment", invalid)
		}
	}
}

func TestTaskEventsRootAllDocumentedEnvironmentFields(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/task-events/deployment.env.example")
	if e != nil {
		t.Fatal(e)
	}
	c, e := decodeWithEnvironment(taskEventsRootInput(t, ""), workspaceEnvironmentFixture(t, string(raw)))
	if e != nil || !c.TaskEvents.Enabled {
		t.Fatal("full documented env cannot enable from omitted base", e)
	}
	_, expected := taskEventsExample(t)
	if !reflect.DeepEqual(c.TaskEvents, expected) {
		t.Fatal("documented YAML/environment mapping differs")
	}
}
