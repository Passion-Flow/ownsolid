//go:build helm_integration

package helm_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestM18PrivateEntrancesShareOneActualListenerAndExplicitArchitecture(t *testing.T) {
	for _, architecture := range []string{"amd64", "arm64"} {
		t.Run(architecture, func(t *testing.T) {
			v := values(t)
			set(v, "architecture", architecture)
			set(v, "ownership.installationID", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			set(v, "http.containerPort", 9443)
			set(v, "http.servicePort", 10443)
			set(v, "http.adminServicePort", 10444)
			set(v, "configuration.expectedPortalOrigin", "https://portal.example.invalid:55443")
			set(v, "configuration.expectedAdminOrigin", "https://admin.example.invalid:55444")
			docs := documents(t, run(t, v, true, "template"))
			validateOfficial(t, docs)
			service := at(t, docs["Service"], "spec")
			ports := array(t, at(t, service, "ports"))
			if len(ports) != 2 || at(t, service, "type") != "ClusterIP" || at(t, ports[0], "port") != 10443 || at(t, ports[1], "port") != 10444 || at(t, ports[0], "targetPort") != "https" || at(t, ports[1], "targetPort") != "https" {
				t.Fatal("two private entrances do not target the real listener")
			}
			pod := at(t, docs["Deployment"], "spec", "template", "spec")
			if at(t, pod, "nodeSelector", "kubernetes.io/arch") != architecture {
				t.Fatal("explicit architecture changed")
			}
			container := array(t, at(t, pod, "containers"))[0]
			listeners := array(t, at(t, container, "ports"))
			if len(listeners) != 1 || at(t, listeners[0], "containerPort") != 9443 {
				t.Fatal("unused second container listener introduced")
			}
			init := array(t, at(t, pod, "initContainers"))[0]
			args := array(t, at(t, init, "args"))
			found := false
			for i, arg := range args {
				if arg == "--expected-server-json" && i+1 < len(args) {
					var expected map[string]any
					if json.Unmarshal([]byte(args[i+1].(string)), &expected) != nil || expected["container_port"] != float64(9443) || expected["portal_origin"] != "https://portal.example.invalid:55443" || expected["admin_origin"] != "https://admin.example.invalid:55444" {
						t.Fatal("serving expectation lost")
					}
					found = true
				}
			}
			if !found {
				t.Fatal("private serving crosscheck omitted")
			}
		})
	}
}

func TestM18HelmRefusesCollidingServicesAndPartialOrEqualOrigins(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"colliding-service-port": func(v map[string]any) { set(v, "http.adminServicePort", 8443) },
		"partial-origin":         func(v map[string]any) { set(v, "configuration.expectedPortalOrigin", "https://portal.example.invalid") },
		"equal-origin": func(v map[string]any) {
			set(v, "configuration.expectedPortalOrigin", "https://portal.example.invalid")
			set(v, "configuration.expectedAdminOrigin", "https://portal.example.invalid")
		},
		"plaintext-origin": func(v map[string]any) {
			set(v, "configuration.expectedPortalOrigin", "http://portal.example.invalid")
			set(v, "configuration.expectedAdminOrigin", "https://admin.example.invalid")
		},
	} {
		t.Run(name, func(t *testing.T) { v := values(t); change(v); run(t, v, false, "template") })
	}
}

func TestM18ChartAddressDefaultsMatchOwnedRendererCatalog(t *testing.T) {
	raw, e := os.ReadFile(filepath.Join(repository(t), "api/kubernetes/v1/address-defaults.json"))
	var defaults map[string]float64
	if e != nil || json.Unmarshal(raw, &defaults) != nil {
		t.Fatal("owned address defaults unavailable", e)
	}
	v := values(t)
	for field, chartField := range map[string]string{"container_port": "containerPort", "portal_service_port": "servicePort", "admin_service_port": "adminServicePort"} {
		if float64(at(t, v, "http", chartField).(int)) != defaults[field] {
			t.Fatal("chart address default drift", field)
		}
	}
}
