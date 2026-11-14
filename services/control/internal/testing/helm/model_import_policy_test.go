//go:build helm_integration

package helm_test

import (
	"reflect"
	"strings"
	"testing"
)

func TestModelImportPolicyHelmProjectionAndRefusal(t *testing.T) {
	const name = "custom-model-import-policy.env"
	v := values(t)
	set(v, "configuration.privateMountPath", "/run/model-policy-check")
	set(v, "configuration.policyEnvironmentFile", name)
	run(t, v, false, "template")
	set(v, "secrets.files", append(array(t, at(t, v, "secrets", "files")), name))
	run(t, v, true, "lint")
	docs := documents(t, run(t, v, true, "template"))
	validateOfficial(t, docs)
	pod := at(t, docs["Deployment"], "spec", "template", "spec")
	control := array(t, at(t, pod, "containers"))[0]
	if !reflect.DeepEqual(at(t, control, "args"), []any{"serve", "--config", "/run/model-policy-check/private/control.yaml", "--env-file", "/run/model-policy-check/private/" + name}) {
		t.Fatal("model policy file projection differs")
	}
	for _, item := range array(t, at(t, control, "env")) {
		if strings.HasPrefix(at(t, item, "name").(string), "PROJECT_ONE_MODELS_") {
			t.Fatal("model policy leaked into ambient environment")
		}
	}
}
