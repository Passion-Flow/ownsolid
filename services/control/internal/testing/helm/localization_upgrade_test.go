//go:build helm_integration

package helm_test

import (
	"reflect"
	"testing"
)

func TestM16HelmLocalizationUpgradeUsesExplicitRuntimeConfiguration(t *testing.T) {
	v := operatorValues(t, "upgrade-localization")
	set(v, "operator.arguments", []any{"--runtime-config", "/run/projectone/private/runtime.yaml", "--env-file", "/run/projectone/private/localization.env"})
	set(v, "secrets.files", append(array(t, at(t, v, "secrets", "files")), "runtime.yaml", "localization.env"))
	docs := documents(t, run(t, v, true, "template"))
	validateOfficial(t, docs)
	pod := at(t, docs["Job"], "spec", "template", "spec")
	container := object(t, array(t, at(t, pod, "containers"))[0])
	if !reflect.DeepEqual(array(t, container["args"]), []any{"upgrade-localization", "--config", "/run/projectone/private/control.yaml", "--runtime-config", "/run/projectone/private/runtime.yaml", "--env-file", "/run/projectone/private/localization.env"}) {
		t.Fatal("Localization protected configuration projection")
	}
	if at(t, docs["Deployment"], "spec", "replicas") != 0 || at(t, docs["Job"], "spec", "backoffLimit") != 0 {
		t.Fatal("operator gained live/retry authority")
	}
}
