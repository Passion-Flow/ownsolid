//go:build helm_integration

package helm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/servingcheck"
)

func machineChartIntent(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	servingIntent(t, v)
	private := at(t, v, "configuration", "privateMountPath").(string) + "/private/"
	intent := map[string]any{"listen_address": "0.0.0.0:9843", "origin": "https://machine.example.invalid:19443",
		"tls_certificate_file": private + "machine-certificate.pem", "tls_private_key_file": private + "machine-key.pem"}
	set(v, "configuration.machineServingExpectation", intent)
	set(v, "machine.containerPort", 9843)
	set(v, "machine.servicePort", 29443)
	set(v, "secrets.files", append(array(t, at(t, v, "secrets", "files")), "machine-certificate.pem", "machine-key.pem"))
	return intent
}

func TestHelmMachineServingExactProjectionAndPorts(t *testing.T) {
	t.Run("historical_values_omit_machine_overrides", func(t *testing.T) {
		v := values(t)
		servingIntent(t, v)
		delete(v, "machine")
		delete(object(t, at(t, v, "configuration")), "machineServingExpectation")
		docs := documents(t, run(t, v, true, "template"))
		validateOfficial(t, docs)
		main := array(t, at(t, docs["Deployment"], "spec", "template", "spec", "containers"))[0]
		if len(array(t, at(t, main, "ports"))) != 1 || len(array(t, at(t, docs["Service"], "spec", "ports"))) != 2 {
			t.Fatal("historical values acquired a machine port")
		}
	})
	for _, present := range []bool{false, true} {
		for _, policy := range []bool{false, true} {
			for _, knowledge := range []bool{false, true} {
				name := "absent"
				if present {
					name = "dedicated"
				}
				if policy {
					name += "_policy"
				}
				if knowledge {
					name += "_knowledge"
				}
				t.Run(name, func(t *testing.T) {
					v := values(t)
					set(v, "configuration.privateMountPath", "/run/machine-acceptance")
					browser := servingIntent(t, v)
					var machine map[string]any
					if present {
						machine = machineChartIntent(t, v)
					}
					if policy {
						set(v, "configuration.policyEnvironmentFile", "serving.env")
						set(v, "secrets.files", append(array(t, at(t, v, "secrets", "files")), "serving.env"))
					}
					if knowledge {
						set(v, "knowledge.enabled", true)
						set(v, "resources.limitMemoryMiB", 4096)
					}
					set(v, "configuration.expectedProjectionSHA256", strings.Repeat("b", 64))
					docs := documents(t, run(t, v, true, "template"))
					validateOfficial(t, docs)
					pod := at(t, docs["Deployment"], "spec", "template", "spec")
					inits := array(t, at(t, pod, "initContainers"))
					main := object(t, array(t, at(t, pod, "containers"))[0])
					count := 2
					if knowledge {
						count++
					}
					if len(inits) != count || at(t, inits[0], "name") != "private-configuration" || at(t, inits[1], "name") != servingcheck.Command {
						t.Fatal("machine changed init sequence")
					}
					browserJSON, e := json.Marshal(browser)
					if e != nil {
						t.Fatal(e)
					}
					args := append([]any{servingcheck.Command}, servingPolicyArgs(t, v)...)
					args = append(args, "--expected-server-json", string(browserJSON))
					wantPorts := []any{map[string]any{"name": "https", "containerPort": at(t, v, "http", "containerPort"), "protocol": "TCP"}}
					service := []any{map[string]any{"name": "https", "port": at(t, v, "http", "servicePort"), "targetPort": "https", "protocol": "TCP"}, map[string]any{"name": "admin", "port": at(t, v, "http", "adminServicePort"), "targetPort": "https", "protocol": "TCP"}}
					projection := array(t, at(t, inits[0], "args"))
					if present {
						machineJSON, e := json.Marshal(machine)
						if e != nil {
							t.Fatal(e)
						}
						if _, e = servingcheck.ParseExpectedMachine(machineJSON); e != nil {
							t.Fatal("chart machine disagrees with typed owner", e)
						}
						args = append(args, "--expected-machine-json", string(machineJSON))
						wantPorts = append(wantPorts, map[string]any{"name": "machine-https", "containerPort": 9843, "protocol": "TCP"})
						service = append(service, map[string]any{"name": "machine-https", "port": 29443, "targetPort": "machine-https", "protocol": "TCP"})
						if len(projection) < 4 || projection[len(projection)-4] != "--expected-machine-json" || projection[len(projection)-3] != string(machineJSON) {
							t.Fatal("projector machine intent/order mismatch")
						}
					} else {
						for _, arg := range projection {
							if arg == "--expected-machine-json" {
								t.Fatal("absent group acquired intent")
							}
						}
					}
					if projection[len(projection)-2] != "--expected-projection-sha256" || projection[len(projection)-1] != strings.Repeat("b", 64) {
						t.Fatal("projection digest order changed")
					}
					if !reflect.DeepEqual(at(t, inits[1], "args"), args) || !reflect.DeepEqual(main["args"], append([]any{"serve"}, servingPolicyArgs(t, v)...)) {
						t.Fatal("machine verifier/policy argv mismatch")
					}
					if !reflect.DeepEqual(main["ports"], wantPorts) || !reflect.DeepEqual(at(t, docs["Service"], "spec", "ports"), service) {
						t.Fatal("machine container/Service port identity mismatch")
					}
					if !reflect.DeepEqual(at(t, inits[1], "resources"), main["resources"]) || !reflect.DeepEqual(at(t, inits[1], "securityContext"), main["securityContext"]) || !reflect.DeepEqual(at(t, inits[1], "env"), main["env"]) {
						t.Fatal("machine verifier changed resources/security/environment")
					}
					mount := map[string]any{"name": "runtime-private", "mountPath": at(t, v, "configuration", "privateMountPath"), "readOnly": true}
					if !reflect.DeepEqual(at(t, inits[1], "volumeMounts"), []any{mount}) {
						t.Fatal("machine verifier acquired unrelated writable state")
					}
					for _, kind := range []string{"startupProbe", "livenessProbe", "readinessProbe"} {
						if command := array(t, at(t, main, kind, "exec", "command")); !strings.Contains(fmt.Sprint(command), "--audience portal") {
							t.Fatal("browser probe was relabeled machine readiness")
						}
					}
				})
			}
		}
	}
	for _, origin := range []string{"https://localhost:19443", "https://127.0.0.1:19443", "https://[::1]:19443"} {
		t.Run(origin, func(t *testing.T) {
			v := values(t)
			intent := machineChartIntent(t, v)
			intent["origin"] = origin
			docs := documents(t, run(t, v, true, "template"))
			validateOfficial(t, docs)
			raw, _ := json.Marshal(intent)
			if _, e := servingcheck.ParseExpectedMachine(raw); e != nil {
				t.Fatal("selected identity changed", e)
			}
		})
	}
}

func TestHelmMachineServingRefusesUnsafeIntent(t *testing.T) {
	for _, field := range []string{"listen_address", "origin", "tls_certificate_file", "tls_private_key_file"} {
		for _, kind := range []string{"missing", "null", "wrong_type"} {
			t.Run(field+"_"+kind, func(t *testing.T) {
				v := values(t)
				intent := machineChartIntent(t, v)
				switch kind {
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
	cases := map[string]func(map[string]any, map[string]any){
		"unknown_intent":  func(_ map[string]any, m map[string]any) { m["unknown"] = true },
		"alias_key":       func(_ map[string]any, m map[string]any) { m["tls_private_key_file"] = m["tls_certificate_file"] },
		"outside_private": func(_ map[string]any, m map[string]any) { m["tls_certificate_file"] = "/outside/cert.pem" },
		"nested_private": func(_ map[string]any, m map[string]any) {
			m["tls_certificate_file"] = "/run/projectone/private/nested/cert.pem"
		},
		"undeclared": func(_ map[string]any, m map[string]any) {
			m["tls_certificate_file"] = "/run/projectone/private/unowned.pem"
		},
		"reserved": func(_ map[string]any, m map[string]any) {
			m["tls_certificate_file"] = "/run/projectone/private/control.yaml"
		},
		"origin_browser": func(v map[string]any, m map[string]any) {
			m["origin"] = at(t, v, "configuration", "expectedAdminOrigin")
		},
		"bind_browser": func(v map[string]any, m map[string]any) {
			m["listen_address"] = at(t, v, "configuration", "servingExpectation", "listen_address")
		},
		"wrong_listener_port":    func(_ map[string]any, m map[string]any) { m["listen_address"] = "0.0.0.0:9844" },
		"http_origin":            func(_ map[string]any, m map[string]any) { m["origin"] = "http://machine.example.invalid:19443" },
		"missing_browser_intent": func(v map[string]any, _ map[string]any) { set(v, "configuration.servingExpectation", nil) },
		"port_without_intent":    func(v map[string]any, _ map[string]any) { set(v, "configuration.machineServingExpectation", nil) },
		"null_container":         func(v map[string]any, _ map[string]any) { set(v, "machine.containerPort", nil) },
		"null_service":           func(v map[string]any, _ map[string]any) { set(v, "machine.servicePort", nil) },
		"container_collision": func(v map[string]any, _ map[string]any) {
			set(v, "machine.containerPort", at(t, v, "http", "containerPort"))
		},
		"service_portal_collision": func(v map[string]any, _ map[string]any) {
			set(v, "machine.servicePort", at(t, v, "http", "servicePort"))
		},
		"service_admin_collision": func(v map[string]any, _ map[string]any) {
			set(v, "machine.servicePort", at(t, v, "http", "adminServicePort"))
		},
		"unknown_port":    func(v map[string]any, _ map[string]any) { object(t, at(t, v, "machine"))["unknown"] = false },
		"container_false": func(v map[string]any, _ map[string]any) { set(v, "machine.containerPort", false) },
		"service_zero":    func(v map[string]any, _ map[string]any) { set(v, "machine.servicePort", 0) },
		"policy_alias": func(v map[string]any, _ map[string]any) {
			set(v, "configuration.policyEnvironmentFile", "machine-key.pem")
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			v := values(t)
			m := machineChartIntent(t, v)
			change(v, m)
			run(t, v, false, "template")
		})
	}
}

func TestHelmMachineServingPureProjection(t *testing.T) {
	binary := os.Getenv("PROJECT_ONE_TEST_PYTHON_BINARY")
	canonical, e := filepath.EvalSymlinks(binary)
	if e != nil || !filepath.IsAbs(binary) || canonical != binary {
		t.Fatal("explicit pinned Python required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-I", "-B", filepath.Join(repository(t), "tests/integration/kubernetes/machine_serving_projection_test.py"), "--asset", filepath.Join(repository(t), "deploy/helm/projectone/files/materialize.py"))
	command.WaitDelay = time.Second
	var output, diagnostic bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostic
	if e = command.Run(); e != nil || ctx.Err() != nil {
		t.Fatal("machine pure projector controls", e, diagnostic.String())
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
	if output.Len() > 32768 || json.Unmarshal(output.Bytes(), &result) != nil || result.Failures != 0 || result.Errors != 0 || result.Skips != 0 {
		t.Fatal("incomplete machine pure control result")
	}
	// Exact pure helper identities were frozen before their first execution.
	expected := []string{
		"MachineServingProjectionHelpersTests.test_raw_and_final_inventory_preserves_input_bytes",
		"MachineServingProjectionHelpersTests.test_absent_machine_preserves_browser_only_helper",
		"MachineServingProjectionHelpersTests.test_explicit_intent_allows_absent_raw_operator_group",
		"MachineServingProjectionHelpersTests.test_expected_machine_requires_exact_shape_and_nonempty_strings",
		"MachineServingProjectionHelpersTests.test_hidden_machine_group_refuses_without_intent",
		"MachineServingProjectionHelpersTests.test_raw_machine_requires_exact_shape_and_nonempty_strings",
		"MachineServingProjectionHelpersTests.test_raw_and_final_machine_tls_references_require_declared_private_pair",
		"MachineServingProjectionHelpersTests.test_browser_machine_pair_reuse_is_explicitly_allowed",
		"MachineServingProjectionHelpersTests.test_license_references_cannot_alias_any_machine_tls_input",
		"MachineServingProjectionHelpersTests.test_machine_json_duplicate_and_invalid_refusals",
		"MachineServingProjectionHelpersTests.test_pure_admission_does_not_read_overlay_or_validate_effective_transport",
		"MachineServingProjectionHelpersTests.test_legacy_positional_materializer_and_serving_signatures_remain",
	}
	if result.Status != "passed_pure_machine_serving_projection_helpers_only" || len(expected) == 0 || result.TestsRun != len(expected) || len(result.Tests) != len(expected) {
		t.Fatal("machine pure discovery/execution closure mismatch")
	}
	actual := map[string]string{}
	for _, test := range result.Tests {
		if _, exists := actual[test.ID]; exists {
			t.Fatal("duplicate machine pure case")
		}
		actual[test.ID] = test.Status
	}
	for _, name := range expected {
		t.Run(name, func(t *testing.T) {
			if actual[name] != "passed" {
				t.Fatal("missing actual machine pure case")
			}
		})
	}
	t.Log("PROJECT_ONE_HELM_MACHINE_PURE_PROJECTION_RESULT=" + strings.TrimSpace(output.String()))
}
