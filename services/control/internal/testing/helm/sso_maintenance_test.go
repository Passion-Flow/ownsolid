//go:build helm_integration

package helm_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSSOMaintenanceHelmPrivateFileAndOmission(t *testing.T) {
	for _, selected := range []bool{false, true} {
		v := values(t)
		set(v, "configuration.privateMountPath", "/run/sso-maintenance-check")
		expected := []any{"serve", "--config", "/run/sso-maintenance-check/private/control.yaml"}
		const name = "custom-sso-maintenance.env"
		if selected {
			set(v, "configuration.policyEnvironmentFile", name)
			set(v, "secrets.files", append(array(t, at(t, v, "secrets", "files")), name))
			expected = append(expected, "--env-file", "/run/sso-maintenance-check/private/"+name)
		}
		run(t, v, true, "lint")
		docs := documents(t, run(t, v, true, "template"))
		validateOfficial(t, docs)
		pod := at(t, docs["Deployment"], "spec", "template", "spec")
		control := array(t, at(t, pod, "containers"))[0]
		if !reflect.DeepEqual(at(t, control, "args"), expected) {
			t.Fatal("SSO maintenance private file projection or omission changed")
		}
		for _, value := range array(t, at(t, control, "env")) {
			if strings.HasPrefix(at(t, value, "name").(string), "PROJECT_ONE_SSO_") {
				t.Fatal("private policy was replaced by ambient container environment")
			}
		}
		init := array(t, at(t, pod, "initContainers"))[0]
		args := array(t, at(t, init, "args"))
		found := false
		for index, arg := range args {
			if arg != "--secret-files-json" {
				continue
			}
			var files []string
			if index+1 >= len(args) || json.Unmarshal([]byte(args[index+1].(string)), &files) != nil {
				t.Fatal("actual protected projection file declaration missing")
			}
			for _, file := range files {
				found = found || file == name
			}
		}
		if found != selected {
			t.Fatal("selected maintenance policy was not bound to the private init projection")
		}
	}
}

func TestSSOMaintenanceHelmRejectsUndeclaredOrUnsafeFile(t *testing.T) {
	for _, name := range []string{"custom-sso-maintenance.env", "../custom-sso-maintenance.env", "folder/custom-sso-maintenance.env", "control.yaml"} {
		v := values(t)
		set(v, "configuration.policyEnvironmentFile", name)
		run(t, v, false, "template")
	}
}
