//go:build helm_integration

package helm_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/servingcheck"
)

func transportChartIntent(t *testing.T, mode string) map[string]any {
	t.Helper()
	v := values(t)
	machineChartIntent(t, v)
	browser := object(t, at(t, v, "configuration", "servingExpectation"))
	browser["transport_mode"] = mode
	if mode != "direct_https" {
		browser["tls_certificate_file"], browser["tls_private_key_file"] = "", ""
	}
	if mode == "http" {
		browser["portal_origin"], browser["admin_origin"] = "http://[::1]:18080", "http://[::1]:18081"
		browser["listen_address"] = "[::]:8443"
		set(v, "probes.dialAddress", "[::1]:8443")
		set(v, "probes.caFile", "")
	}
	if mode == "trusted_proxy_https" {
		browser["trusted_proxy_cidrs"] = []any{"192.0.2.0/24"}
		set(v, "probes.dialAddress", "127.0.0.1:9843")
		set(v, "probes.caFile", "/run/projectone/private/machine-certificate.pem")
	}
	set(v, "configuration.expectedPortalOrigin", browser["portal_origin"])
	set(v, "configuration.expectedAdminOrigin", browser["admin_origin"])
	set(v, "configuration.policyEnvironmentFile", "serving.env")
	set(v, "secrets.files", append(array(t, at(t, v, "secrets", "files")), "serving.env"))
	set(v, "probes.timeoutSeconds", 7)
	return v
}

func TestHelmTransportModesUseExactNativeProbes(t *testing.T) {
	for _, mode := range []string{"http", "direct_https", "trusted_proxy_https"} {
		t.Run(mode, func(t *testing.T) {
			v := transportChartIntent(t, mode)
			before, _ := json.Marshal(v)
			docs := documents(t, run(t, v, true, "template"))
			validateOfficial(t, docs)
			pod := at(t, docs["Deployment"], "spec", "template", "spec")
			main := array(t, at(t, pod, "containers"))[0]
			b := at(t, v, "configuration", "servingExpectation")
			raw, _ := json.Marshal(b)
			if _, e := servingcheck.ParseExpected(raw); e != nil {
				t.Fatal("chart disagrees with native serving authority", e)
			}
			audience := "portal"
			if mode == "trusted_proxy_https" {
				audience = "machine"
			}
			for _, item := range []struct{ name, kind string }{{"startupProbe", "live"}, {"livenessProbe", "live"}, {"readinessProbe", "ready"}} {
				want := append([]any{"/usr/local/bin/control", "health-probe"}, servingPolicyArgs(t, v)...)
				want = append(want, "--audience", audience, "--kind", item.kind, "--dial-address", at(t, v, "probes", "dialAddress"), "--timeout-seconds", "6")
				if ca := at(t, v, "probes", "caFile").(string); ca != "" {
					want = append(want, "--ca-file", ca)
				}
				probe := object(t, at(t, main, item.name))
				if !reflect.DeepEqual(probe["exec"], map[string]any{"command": want}) || probe["httpGet"] != nil || probe["timeoutSeconds"] != 7 {
					t.Fatal("probe has wrong identity, trust or deadline")
				}
			}
			after, _ := json.Marshal(v)
			if string(before) != string(after) {
				t.Fatal("render changed selected input")
			}
		})
	}
	for name, change := range map[string]func(map[string]any){
		"proxy-no-machine": func(v map[string]any) {
			set(v, "configuration.machineServingExpectation", nil)
			set(v, "machine.containerPort", nil)
			set(v, "machine.servicePort", nil)
		},
		"no-ca": func(v map[string]any) { set(v, "probes.caFile", "") }, "undeclared-ca": func(v map[string]any) { set(v, "probes.caFile", "/run/projectone/private/absent.pem") },
		"key-as-ca": func(v map[string]any) { set(v, "probes.caFile", "/run/projectone/private/machine-key.pem") }, "false-ca": func(v map[string]any) { set(v, "probes.caFile", false) },
		"false-timeout": func(v map[string]any) { set(v, "probes.timeoutSeconds", false) }, "no-margin": func(v map[string]any) { set(v, "probes.timeoutSeconds", 1) },
		"wrong-port": func(v map[string]any) { set(v, "probes.dialAddress", "127.0.0.1:9844") }, "wrong-family": func(v map[string]any) { set(v, "probes.dialAddress", "[::1]:9843") },
		"remote-probe": func(v map[string]any) { set(v, "probes.dialAddress", "192.0.2.10:9843") }, "missing-proxies": func(v map[string]any) { set(v, "configuration.servingExpectation.trusted_proxy_cidrs", []any{}) },
	} {
		t.Run(name, func(t *testing.T) {
			v := transportChartIntent(t, "trusted_proxy_https")
			change(v)
			run(t, v, false, "template")
		})
	}
	t.Run("http-rejects-ca", func(t *testing.T) {
		v := transportChartIntent(t, "http")
		set(v, "probes.caFile", "/run/projectone/private/certificate.pem")
		run(t, v, false, "template")
	})
}
