//go:build helm_integration

package helm_test

import (
	"reflect"
	"testing"
)

func TestM14HelmCumulativeUpgradeUsesExplicitSeparateRuntimeConfiguration(t *testing.T) {
	for _, command := range []string{"upgrade-knowledge", "upgrade-workflows", "upgrade-plugins"} {
		t.Run(command, func(t *testing.T) {
			v := operatorValues(t, command)
			arguments := []any{"--runtime-config", "/run/projectone/private/runtime.yaml"}
			set(v, "operator.arguments", arguments)
			set(v, "secrets.files", append(array(t, at(t, v, "secrets", "files")), "runtime.yaml"))
			docs := documents(t, run(t, v, true, "template"))
			validateOfficial(t, docs)
			pod := at(t, docs["Job"], "spec", "template", "spec")
			runtime := object(t, array(t, at(t, pod, "containers"))[0])
			if !reflect.DeepEqual(array(t, runtime["args"]), []any{command, "--config", "/run/projectone/private/control.yaml", "--runtime-config", "/run/projectone/private/runtime.yaml"}) {
				t.Fatal("explicit cumulative operator inputs changed")
			}
			if at(t, docs["Deployment"], "spec", "replicas") != 0 || at(t, docs["Job"], "spec", "backoffLimit") != 0 {
				t.Fatal("cumulative operator acquired live serving/retry authority")
			}
		})
	}
}

func TestM14HelmPluginRequiresExplicitWorkflowCPUPrerequisites(t *testing.T) {
	v := values(t)
	set(v, "plugins.enabled", true)
	run(t, v, false, "template")
	set(v, "workflows.enabled", true)
	docs := documents(t, run(t, v, true, "template"))
	validateOfficial(t, docs)
	pod := at(t, docs["Deployment"], "spec", "template", "spec")
	for _, name := range []string{"initContainers", "volumes"} {
		for _, item := range array(t, at(t, pod, name)) {
			if at(t, item, "name") == "knowledge-scratch" {
				t.Fatal("tool-only profile acquired knowledge scratch", name)
			}
		}
	}
	set(v, "knowledge.enabled", true)
	run(t, v, false, "template") // An enabled encoder still needs its memory budget.
	set(v, "resources.limitMemoryMiB", 3072)
	set(v, "lifecycle.terminationGraceSeconds", 45)
	docs = documents(t, run(t, v, true, "template"))
	validateOfficial(t, docs)
	pod = at(t, docs["Deployment"], "spec", "template", "spec")
	if at(t, pod, "terminationGracePeriodSeconds") != 45 {
		t.Fatal("plugin configured shutdown grace ignored")
	}
	set(v, "plugins.enabled", "true")
	run(t, v, false, "template")
}
