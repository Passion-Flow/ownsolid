//go:build helm_integration

package helm_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestM19ActualHelmLicensePrivateGeneration(t *testing.T) {
	for _, overlay := range []bool{false, true} {
		v := values(t)
		set(v, "ownership.installationID", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		set(v, "configuration.privateMountPath", "/run/license-acceptance")
		set(v, "configuration.expectedPortalOrigin", "https://portal.example.invalid:9447")
		set(v, "configuration.expectedAdminOrigin", "https://admin.example.invalid:9448")
		set(v, "http.containerPort", 9447)
		set(v, "http.servicePort", 9447)
		set(v, "http.adminServicePort", 9448)
		set(v, "runtime.uid", 10031)
		set(v, "runtime.gid", 10032)
		files := array(t, at(t, v, "secrets", "files"))
		found := 0
		for _, file := range files {
			if file == "license-trust.json" {
				found++
			}
		}
		if found != 1 {
			t.Fatal("default public License trust selection required exactly once")
		}
		if overlay {
			files = append(files, "license.env")
			set(v, "secrets.files", files)
		}
		run(t, v, true, "lint")
		docs := documents(t, run(t, v, true, "template"))
		validateOfficial(t, docs)
		pod := object(t, at(t, docs["Deployment"], "spec", "template", "spec"))
		init := object(t, array(t, pod["initContainers"])[0])
		args := array(t, init["args"])
		arguments := map[string]any{}
		for i := 0; i+1 < len(args); i += 2 {
			arguments[args[i].(string)] = args[i+1]
		}
		var selected []any
		if json.Unmarshal([]byte(arguments["--secret-files-json"].(string)), &selected) != nil || !reflect.DeepEqual(selected, files) {
			t.Fatal("selected generation drift")
		}
		if arguments["--target"] != "/run/license-acceptance/private" || arguments["--owner-uid"] != "10031" || arguments["--owner-gid"] != "10032" || arguments["--expected-server-json"] == nil {
			t.Fatal("License generation runtime boundary drift")
		}
		asset, e := os.ReadFile(filepath.Join(repository(t), "deploy/helm/projectone/files/materialize.py"))
		if e != nil || at(t, docs["ConfigMap"], "data", "materialize.py") != string(asset) {
			t.Fatal("rendered projector differs from verified owning source")
		}
	}
}
