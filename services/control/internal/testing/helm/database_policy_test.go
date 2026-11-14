//go:build helm_integration

package helm_test

import (
	"reflect"
	"testing"
)

func TestPostgreSQLLockPolicyHelmPrivateFileAndOmission(t *testing.T) {
	for _, selected := range []bool{false, true} {
		v := values(t)
		set(v, "configuration.privateMountPath", "/run/lock-policy-check")
		expected := []any{"serve", "--config", "/run/lock-policy-check/private/control.yaml"}
		if selected {
			set(v, "configuration.policyEnvironmentFile", "database-policy.env")
			set(v, "secrets.files", append(array(t, at(t, v, "secrets", "files")), "database-policy.env"))
			expected = append(expected, "--env-file", "/run/lock-policy-check/private/database-policy.env")
		}
		run(t, v, true, "lint")
		docs := documents(t, run(t, v, true, "template"))
		validateOfficial(t, docs)
		pod := at(t, docs["Deployment"], "spec", "template", "spec")
		control := array(t, at(t, pod, "containers"))[0]
		if !reflect.DeepEqual(at(t, control, "args"), expected) {
			t.Fatal("selected protected policy reference or omission changed")
		}
	}
}

func TestPostgreSQLLockPolicyHelmRejectsUndeclaredOrUnsafeFile(t *testing.T) {
	for _, name := range []string{"database-policy.env", "../database-policy.env", "folder/database-policy.env"} {
		v := values(t)
		set(v, "configuration.policyEnvironmentFile", name)
		run(t, v, false, "template")
	}
}
