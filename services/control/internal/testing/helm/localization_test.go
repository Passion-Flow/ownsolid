//go:build helm_integration

package helm_test

import "testing"

func TestM16HelmLocalizationReadOnlyPrivateExistingClaimAndExplicitFalse(t *testing.T) {
	v := values(t)
	set(v, "localization.enabled", false)
	docs := documents(t, run(t, v, true, "template"))
	pod := at(t, docs["Deployment"], "spec", "template", "spec")
	for _, item := range array(t, at(t, pod, "volumes")) {
		if at(t, item, "name") == "localization" {
			t.Fatal("false enabled locale mount")
		}
	}
	set(v, "localization.enabled", true)
	set(v, "localization.existingClaim", "synthetic-locale-claim")
	set(v, "localization.mountPath", "/srv/isolated/locale-assets")
	set(v, "runtime.uid", 10033)
	set(v, "runtime.gid", 10034)
	docs = documents(t, run(t, v, true, "template"))
	validateOfficial(t, docs)
	run(t, v, true, "lint")
	pod = at(t, docs["Deployment"], "spec", "template", "spec")
	found := false
	for _, item := range array(t, at(t, pod, "volumes")) {
		if at(t, item, "name") == "localization" {
			found = true
			if at(t, item, "persistentVolumeClaim", "readOnly") != true || at(t, item, "persistentVolumeClaim", "claimName") != "synthetic-locale-claim" {
				t.Fatal("mutable or wrong locale claim")
			}
		}
	}
	if !found {
		t.Fatal("missing locale claim")
	}
	runtime := array(t, at(t, pod, "containers"))[0]
	found = false
	for _, mount := range array(t, at(t, runtime, "volumeMounts")) {
		if at(t, mount, "name") == "localization" {
			found = true
			if at(t, mount, "readOnly") != true || at(t, mount, "mountPath") != "/srv/isolated/locale-assets" {
				t.Fatal("locale mount not immutable")
			}
		}
	}
	if !found || at(t, pod, "securityContext", "runAsUser") != 10033 {
		t.Fatal("locale/runtime owner mapping")
	}
}
func TestM16HelmLocalizationRejectsInvalidTypesClaimsAndOverlaps(t *testing.T) {
	for _, change := range []struct {
		path  string
		value any
	}{{"localization.enabled", "false"}, {"localization.existingClaim", ""}, {"localization.existingClaim", "synthetic-models"}, {"localization.mountPath", "/run/projectone/child"}, {"localization.mountPath", "/var/lib/projectone"}, {"localization.mountPath", "/safe/./locale"}, {"localization.mountPath", "/safe/../locale"}, {"localization.mountPath", "/"}} {
		v := values(t)
		set(v, "localization.enabled", true)
		set(v, "localization.existingClaim", "synthetic-locale-claim")
		set(v, "localization.mountPath", "/srv/locales")
		set(v, change.path, change.value)
		run(t, v, false, "template")
	}
	v := values(t)
	set(v, "localization.enabled", true)
	set(v, "localization.existingClaim", "synthetic-locales")
	set(v, "localization.mountPath", "/srv/locales")
	set(v, "mode", "operator")
	set(v, "operator.acknowledgeNamespace", "projectone-test-local")
	set(v, "operator.jobID", "locale-test")
	set(v, "operator.existingConfigMap", "synthetic-operator-config")
	set(v, "operator.existingSecret", "synthetic-operator-secret")
	set(v, "operator.command", "export-models")
	set(v, "operator.snapshot.existingClaim", "synthetic-locales")
	run(t, v, false, "template")
}
