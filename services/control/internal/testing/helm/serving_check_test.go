//go:build helm_integration

package helm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/servingcheck"
)

func servingIntent(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	private := at(t, v, "configuration", "privateMountPath").(string) + "/private"
	intent := map[string]any{"format_version": 1, "transport_mode": "direct_https", "listen_address": "0.0.0.0:8443",
		"portal_origin": "https://portal.example.invalid", "admin_origin": "https://admin.example.invalid",
		"tls_certificate_file": private + "/certificate.pem", "tls_private_key_file": private + "/private-key.pem", "trusted_proxy_cidrs": []any{}}
	set(v, "ownership.installationID", strings.Repeat("a", 32))
	set(v, "configuration.expectedPortalOrigin", intent["portal_origin"])
	set(v, "configuration.expectedAdminOrigin", intent["admin_origin"])
	set(v, "configuration.servingExpectation", intent)
	set(v, "probes.dialAddress", "127.0.0.1:8443")
	set(v, "probes.caFile", private+"/certificate.pem")
	return intent
}

func servingPolicyArgs(t *testing.T, v map[string]any) []any {
	t.Helper()
	private := at(t, v, "configuration", "privateMountPath").(string) + "/private"
	args := []any{"--config", private + "/control.yaml"}
	if name := at(t, v, "configuration", "policyEnvironmentFile").(string); name != "" {
		args = append(args, "--env-file", private+"/"+name)
	}
	return args
}

func TestHelmServingVerifierExactNativeInitAndPolicy(t *testing.T) {
	for _, policy := range []bool{false, true} {
		for _, knowledge := range []bool{false, true} {
			name := "base"
			if policy {
				name += "_policy"
			}
			if knowledge {
				name += "_knowledge"
			}
			t.Run(name, func(t *testing.T) {
				v := values(t)
				intent := servingIntent(t, v)
				if policy {
					set(v, "configuration.policyEnvironmentFile", "serving.env")
					set(v, "secrets.files", append(array(t, at(t, v, "secrets", "files")), "serving.env"))
				}
				if knowledge {
					set(v, "knowledge.enabled", true)
					set(v, "resources.limitMemoryMiB", 4096)
				}
				docs := documents(t, run(t, v, true, "template"))
				validateOfficial(t, docs)
				pod := at(t, docs["Deployment"], "spec", "template", "spec")
				inits := array(t, at(t, pod, "initContainers"))
				count := 2
				if knowledge {
					count++
				}
				if len(inits) != count || at(t, inits[0], "name") != "private-configuration" || at(t, inits[1], "name") != servingcheck.Command || knowledge && at(t, inits[2], "name") != "knowledge-scratch" {
					t.Fatal("effective verifier init order changed")
				}
				verifier := object(t, inits[1])
				main := object(t, array(t, at(t, pod, "containers"))[0])
				if !reflect.DeepEqual(verifier["command"], []any{"/usr/local/bin/control"}) || verifier["image"] != main["image"] || verifier["imagePullPolicy"] != "Never" || !reflect.DeepEqual(verifier["resources"], main["resources"]) || !reflect.DeepEqual(verifier["env"], main["env"]) {
					t.Fatal("native verifier binary/image/resource policy changed")
				}
				security := object(t, verifier["securityContext"])
				if at(t, pod, "securityContext", "runAsUser") != at(t, v, "runtime", "uid") || at(t, pod, "securityContext", "runAsGroup") != at(t, v, "runtime", "gid") || at(t, pod, "securityContext", "runAsNonRoot") != true || security["allowPrivilegeEscalation"] != false || security["readOnlyRootFilesystem"] != true || !reflect.DeepEqual(security["capabilities"], map[string]any{"drop": []any{"ALL"}}) || len(security) != 3 {
					t.Fatal("native verifier acquired privilege")
				}
				mount := map[string]any{"name": "runtime-private", "mountPath": at(t, v, "configuration", "privateMountPath"), "readOnly": true}
				if !reflect.DeepEqual(verifier["volumeMounts"], []any{mount}) {
					t.Fatal("verifier mounted writable or unrelated state")
				}
				encoded, e := json.Marshal(intent)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = servingcheck.ParseExpected(encoded); e != nil {
					t.Fatal("chart intent disagrees with actual Go owner", e)
				}
				args := append([]any{servingcheck.Command}, servingPolicyArgs(t, v)...)
				args = append(args, "--expected-server-json", string(encoded))
				if !reflect.DeepEqual(verifier["args"], args) || !reflect.DeepEqual(main["args"], append([]any{"serve"}, servingPolicyArgs(t, v)...)) {
					t.Fatal("serve/verifier effective policy arguments differ")
				}
			})
		}
	}
}

func TestHelmServingVerifierRefusesIncompleteIntent(t *testing.T) {
	for _, field := range []string{"format_version", "transport_mode", "listen_address", "portal_origin", "admin_origin", "tls_certificate_file", "tls_private_key_file", "trusted_proxy_cidrs"} {
		for _, action := range []string{"missing", "null", "wrong_type"} {
			t.Run("required_"+field+"_"+action, func(t *testing.T) {
				v := values(t)
				intent := servingIntent(t, v)
				switch action {
				case "missing":
					delete(intent, field)
				case "null":
					intent[field] = nil
				case "wrong_type":
					intent[field] = false
				}
				run(t, v, false, "template")
			})
		}
	}
	for name, change := range map[string]func(map[string]any, map[string]any){
		"unknown_field":               func(_ map[string]any, e map[string]any) { e["unknown"] = "synthetic" },
		"missing_field":               func(_ map[string]any, e map[string]any) { delete(e, "tls_private_key_file") },
		"null_field":                  func(_ map[string]any, e map[string]any) { e["tls_private_key_file"] = nil },
		"wrong_version":               func(_ map[string]any, e map[string]any) { e["format_version"] = 2 },
		"wrong_type":                  func(_ map[string]any, e map[string]any) { e["format_version"] = "1" },
		"contradictory_http_profile":  func(_ map[string]any, e map[string]any) { e["transport_mode"] = "http" },
		"contradictory_proxy_profile": func(_ map[string]any, e map[string]any) { e["transport_mode"] = "trusted_proxy_https" },
		"origin_alias": func(v map[string]any, _ map[string]any) {
			set(v, "configuration.expectedPortalOrigin", "https://other.example.invalid")
		},
		"listener_port":   func(_ map[string]any, e map[string]any) { e["listen_address"] = "0.0.0.0:19443" },
		"outside_private": func(_ map[string]any, e map[string]any) { e["tls_certificate_file"] = "/synthetic/unowned/cert.pem" },
		"undeclared_file": func(_ map[string]any, e map[string]any) {
			e["tls_certificate_file"] = "/run/projectone/private/missing.pem"
		},
		"alias_pair":     func(_ map[string]any, e map[string]any) { e["tls_private_key_file"] = e["tls_certificate_file"] },
		"no_owner":       func(v map[string]any, _ map[string]any) { set(v, "ownership.installationID", "") },
		"unknown_policy": func(v map[string]any, _ map[string]any) { set(v, "configuration.policyEnvironmentFile", "unowned.env") },
	} {
		t.Run(name, func(t *testing.T) {
			v := values(t)
			e := servingIntent(t, v)
			change(v, e)
			run(t, v, false, "template")
		})
	}
}

func TestHelmServingProjectionPureControls(t *testing.T) {
	binary := os.Getenv("PROJECT_ONE_TEST_PYTHON_BINARY")
	canonical, e := filepath.EvalSymlinks(binary)
	if e != nil || !filepath.IsAbs(binary) || canonical != binary {
		t.Fatal("explicit pinned canonical Python required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-I", "-B", filepath.Join(repository(t), "tests/integration/kubernetes/serving_projection_test.py"))
	command.WaitDelay = time.Second
	var output, diagnostics bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostics
	if e = command.Run(); e != nil || ctx.Err() != nil {
		t.Fatal("pure synthetic projection helper failed", e, diagnostics.String())
	}
	var result struct {
		Status   string `json:"status"`
		TestsRun int    `json:"tests_run"`
		Tests    []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"tests"`
		Failures int `json:"failures"`
		Errors   int `json:"errors"`
		Skips    int `json:"skips"`
	}
	if output.Len() > 16384 || json.Unmarshal(output.Bytes(), &result) != nil || result.Status != "passed_pure_serving_projection_helpers_only" || result.Failures != 0 || result.Errors != 0 || result.Skips != 0 {
		t.Fatal("incomplete actual Python helper result")
	}
	expected := []string{"test_full_expectation_reserves_raw_and_final_tls_without_policy_overlay", "test_full_expectation_requires_exact_version_shape_and_types", "test_full_expectation_requires_declared_private_tls_inventory", "test_legacy_three_field_origin_listener_and_tls_checks_remain", "test_license_trust_and_environment_aliases_remain_refused", "test_license_environment_keeps_three_key_and_syntax_guards", "test_raw_base_cannot_hide_duplicate_or_missing_license_configuration", "test_public_helper_does_not_perform_io_or_claim_effective_policy"}
	actual := map[string]string{}
	for _, test := range result.Tests {
		if _, exists := actual[test.ID]; exists {
			t.Fatal("duplicate Python case")
		}
		actual[test.ID] = test.Status
	}
	if result.TestsRun != len(expected) || len(actual) != len(expected) {
		t.Fatal("Python discovery/execution closure mismatch")
	}
	for _, name := range expected {
		t.Run(name, func(t *testing.T) {
			if actual["ServingProjectionHelpersTests."+name] != "passed" {
				t.Fatal("required actual Python case missing or failed")
			}
		})
	}
	t.Log("PROJECT_ONE_HELM_PURE_PROJECTION_RESULT=" + strings.TrimSpace(output.String()))
}
