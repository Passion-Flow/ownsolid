//go:build helm_integration

package helm_test

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTaskEventsHelmExistingProtectedProjectionAndNondefaultPolicy(t *testing.T) {
	for _, selected := range []bool{false, true} {
		name := "omitted"
		if selected {
			name = "selected"
		}
		t.Run(name, func(t *testing.T) {
			v := values(t)
			set(v, "configuration.privateMountPath", "/run/selected-task-events")
			expected := []any{"serve", "--config", "/run/selected-task-events/private/control.yaml"}
			files := array(t, at(t, v, "secrets", "files"))
			if selected {
				files = append(files, "task-policy.env", "task-ca.pem", "task-client.pem", "task-client.key", "task-publisher.nk", "task-consumer.nk")
				set(v, "configuration.policyEnvironmentFile", "task-policy.env")
				set(v, "secrets.files", files)
				expected = append(expected, "--env-file", "/run/selected-task-events/private/task-policy.env")
			}
			run(t, v, true, "lint")
			docs := documents(t, run(t, v, true, "template"))
			validateOfficial(t, docs)
			pod := at(t, docs["Deployment"], "spec", "template", "spec")
			control := array(t, at(t, pod, "containers"))[0]
			if !reflect.DeepEqual(at(t, control, "args"), expected) {
				t.Fatal("private root task-event policy mapping changed")
			}
			init := array(t, at(t, pod, "initContainers"))[0]
			encoded, e := json.Marshal(files)
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, value := range array(t, at(t, init, "args")) {
				if value == string(encoded) {
					found = true
				}
			}
			if !found {
				t.Fatal("actual projection omitted credential/policy names")
			}
		})
	}
}
