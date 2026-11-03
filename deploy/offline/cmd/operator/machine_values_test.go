package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const syntheticMachineProfileJSON = `{"format_version":1,"browser_binding":{"bind_address":"127.0.0.1","published_port":18443,"target_port":8443},"machine_binding":{"bind_address":"127.0.0.1","published_port":19443,"target_port":9443},"policy_environment_basename":"runtime-policy.env","probe_ca_basename":"machine-ca.pem","probe_dial_address":"127.0.0.1:9443","probe_timeout_seconds":5,"health_interval_seconds":30,"health_timeout_seconds":10,"health_start_period_seconds":0,"health_retries":3}`

const syntheticMachineProfileEnvironment = "# Complete synthetic public-profile override, not application runtime policy.\r\n" +
	"PROJECT_ONE_BIND_ADDRESS=\"::\"\r\n" +
	"PROJECT_ONE_HTTPS_PORT=28443\r\n" +
	"PROJECT_ONE_CONTROL_PORT=8543\r\n" +
	"PROJECT_ONE_COMPOSE_MACHINE_BIND_ADDRESS=\"::1\"\r\n" +
	"PROJECT_ONE_COMPOSE_MACHINE_PUBLISHED_PORT=29443\r\n" +
	"PROJECT_ONE_COMPOSE_MACHINE_TARGET_PORT=9543\r\n" +
	"PROJECT_ONE_POLICY_ENVIRONMENT_BASENAME=runtime-v2.env\r\n" +
	"PROJECT_ONE_COMPOSE_MACHINE_CA_BASENAME=ca-v2.pem\r\n" +
	"PROJECT_ONE_COMPOSE_MACHINE_DIAL_ADDRESS=\"[::1]:9543\"\r\n" +
	"PROJECT_ONE_COMPOSE_MACHINE_PROBE_TIMEOUT_SECONDS=7\r\n" +
	"PROJECT_ONE_COMPOSE_HEALTH_INTERVAL_SECONDS=45\r\n" +
	"PROJECT_ONE_COMPOSE_HEALTH_TIMEOUT_SECONDS=15\r\n" +
	"PROJECT_ONE_COMPOSE_HEALTH_START_PERIOD_SECONDS=0\r\n" +
	"PROJECT_ONE_COMPOSE_HEALTH_RETRIES=4\r\n"

// This assertion reads only the documented public YAML representation. It does
// not call the producer to manufacture expected output or contact an Engine.
func machinePublicProjection(t *testing.T, raw []byte, overlay bool) {
	t.Helper()
	text := string(raw)
	policy, ca, dial, probe := "runtime-policy.env", "machine-ca.pem", "127.0.0.1:9443", "5"
	bindings := "      - target: 8443\n        published: \"18443\"\n        host_ip: \"127.0.0.1\"\n        protocol: \"tcp\"\n      - target: 9443\n        published: \"19443\"\n        host_ip: \"127.0.0.1\"\n        protocol: \"tcp\"\n"
	scheduler := []string{"interval: \"30s\"", "timeout: \"10s\"", "start_period: \"0s\"", "retries: 3"}
	if overlay {
		policy, ca, dial, probe = "runtime-v2.env", "ca-v2.pem", "[::1]:9543", "7"
		bindings = "      - target: 8543\n        published: \"28443\"\n        host_ip: \"::\"\n        protocol: \"tcp\"\n      - target: 9543\n        published: \"29443\"\n        host_ip: \"::1\"\n        protocol: \"tcp\"\n"
		scheduler = []string{"interval: \"45s\"", "timeout: \"15s\"", "start_period: \"0s\"", "retries: 4"}
	}
	if !strings.HasPrefix(text, "services:\n  control:\n") || strings.Count(text, "ports: !override") != 1 || strings.Count(text, "- target:") != 2 || strings.Count(text, "protocol:") != 2 {
		t.Fatal("public machine projection changed the one-service/two-binding override shape")
	}
	if !strings.Contains(text, bindings) {
		t.Fatal("public machine projection changed an exact address/published/target binding")
	}
	for _, token := range scheduler {
		if !strings.Contains(text, token) {
			t.Fatalf("public machine projection omitted expected field %q", token)
		}
	}
	command := []string{"serve", "--config", "/run/projectone/control.yaml", "--env-file", "/run/projectone/" + policy}
	probeCommand := []string{"CMD", "/usr/local/bin/control", "health-probe", "--config", "/run/projectone/control.yaml", "--env-file", "/run/projectone/" + policy, "--audience", "machine", "--kind", "ready", "--dial-address", dial, "--timeout-seconds", probe, "--ca-file", "/run/projectone/" + ca}
	for _, item := range []struct {
		field string
		argv  []string
	}{{"command", command}, {"test", probeCommand}} {
		found := 0
		for _, line := range strings.Split(text, "\n") {
			prefix := item.field + ": "
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			found++
			var actual []string
			if json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &actual) != nil || len(actual) != len(item.argv) {
				t.Fatal("public machine projection did not retain exact exec argv", item.field)
			}
			for index := range actual {
				if actual[index] != item.argv[index] {
					t.Fatalf("public machine projection changed %s argument %d", item.field, index)
				}
			}
		}
		if found != 1 {
			t.Fatal("public machine projection duplicated or omitted exec argv", item.field)
		}
	}
	for _, forbidden := range []string{"image:", "volumes:", "environment:", "entrypoint:", "postgres:", "synthetic-sensitive"} {
		if strings.Contains(text, forbidden) {
			t.Fatal("public machine projection included an unrelated runtime owner or sensitive source")
		}
	}
}

func TestComposeMachineValuesCLIEarlyPublicProjection(t *testing.T) {
	directory := composePortPrivateDirectory(t)
	configuration := composePortPrivateInput(t, directory, "machine-profile.json", []byte(syntheticMachineProfileJSON))
	environment := composePortPrivateInput(t, directory, "machine-profile.env", []byte(syntheticMachineProfileEnvironment))
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(directory, "unavailable-machine-engine.socket"))
	for _, line := range strings.Split(syntheticMachineProfileEnvironment, "\n") {
		key, _, present := strings.Cut(strings.TrimSuffix(line, "\r"), "=")
		if present {
			t.Setenv(key, "synthetic-invalid-ambient")
		}
	}
	for _, item := range []struct {
		name    string
		args    []string
		overlay bool
	}{
		{"json_only", []string{"--config", configuration}, false},
		{"explicit_fourteen_key_ipv6_overlay", []string{"--config=" + configuration, "--env-file=" + environment}, true},
	} {
		t.Run(item.name, func(t *testing.T) {
			raw, e := composePortDispatchOutput(t, append([]string{composeMachineValuesCommand}, item.args...))
			if e != nil {
				t.Fatal("early public machine dispatch reached unrelated signed configuration", e)
			}
			machinePublicProjection(t, raw, item.overlay)
			var direct bytes.Buffer
			if e = runComposeMachineValuesCommand(item.args, &direct); e != nil || !bytes.Equal(raw, direct.Bytes()) {
				t.Fatal("early dispatcher changed the exact typed public machine projection", e)
			}
		})
	}
	if raw, e := os.ReadFile(configuration); e != nil || string(raw) != syntheticMachineProfileJSON {
		t.Fatal("read-only public renderer changed its protected profile", e)
	}
	if raw, e := os.ReadFile(environment); e != nil || string(raw) != syntheticMachineProfileEnvironment {
		t.Fatal("read-only public renderer changed its explicit overlay", e)
	}
	if _, e := os.Lstat(filepath.Join(directory, "unavailable-machine-engine.socket")); !os.IsNotExist(e) {
		t.Fatal("public renderer created an Engine socket path")
	}
}

func TestComposeMachineValuesCLIArgumentsBeforeIO(t *testing.T) {
	directory := composePortPrivateDirectory(t)
	configuration := composePortPrivateInput(t, directory, "must-not-read.json", []byte("synthetic-sensitive-invalid-profile"))
	environment := filepath.Join(directory, "must-not-read.env")
	fifo := filepath.Join(directory, "must-not-open.fifo")
	if e := syscall.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	for _, item := range []struct {
		name string
		args []string
	}{
		{"missing_all", nil},
		{"unknown_flag", []string{"--config", configuration, "--unknown", "synthetic-sensitive"}},
		{"missing_config_value", []string{"--config"}},
		{"empty_config", []string{"--config="}},
		{"missing_config_with_environment", []string{"--env-file", environment}},
		{"duplicate_config", []string{"--config", configuration, "--config", configuration}},
		{"mixed_duplicate_config", []string{"--config=" + configuration, "--config", configuration}},
		{"duplicate_environment", []string{"--config", configuration, "--env-file", environment, "--env-file", environment}},
		{"mixed_duplicate_environment", []string{"--config", configuration, "--env-file=" + environment, "--env-file", environment}},
		{"empty_environment", []string{"--config", configuration, "--env-file="}},
		{"missing_environment_value", []string{"--config", configuration, "--env-file"}},
		{"relative_configuration", []string{"--config", "profile.json"}},
		{"unclean_configuration", []string{"--config", directory + "/./profile.json"}},
		{"root_configuration", []string{"--config", "/"}},
		{"control_configuration", []string{"--config", directory + "/sensitive\nprofile.json"}},
		{"relative_environment", []string{"--config", configuration, "--env-file", "profile.env"}},
		{"unclean_environment", []string{"--config", configuration, "--env-file", directory + "/../profile.env"}},
		{"root_environment", []string{"--config", configuration, "--env-file", "/"}},
		{"control_environment", []string{"--config", configuration, "--env-file", directory + "/sensitive\tenv"}},
		{"positional_before", []string{"unexpected", "--config", configuration}},
		{"positional_after", []string{"--config", configuration, "unexpected"}},
		{"operation_owner_flag", []string{"--config", configuration, "--operation-file", "synthetic-sensitive"}},
		{"help", []string{"--help"}},
		{"help_after_configuration", []string{"--config", configuration, "--help"}},
		{"fifo_not_opened_before_unknown_flag", []string{"--config", fifo, "--unknown"}},
	} {
		t.Run(item.name, func(t *testing.T) {
			var output bytes.Buffer
			composePortFixedFailure(t, runComposeMachineValuesCommand(item.args, &output), composeMachineArgumentInvalid, &output)
		})
	}
	t.Run("nil_writer_before_input", func(t *testing.T) {
		composePortFixedFailure(t, runComposeMachineValuesCommand([]string{"--config", configuration}, nil), composeMachineArgumentInvalid, nil)
	})
}

func TestComposeMachineValuesCLIProtectedInputsAndRedaction(t *testing.T) {
	for _, source := range []string{"configuration", "environment"} {
		for _, fault := range []string{"missing", "public", "symlink", "hardlink", "fifo", "empty"} {
			t.Run(source+"_"+fault, func(t *testing.T) {
				directory := composePortPrivateDirectory(t)
				configuration := composePortPrivateInput(t, directory, "sensitive-machine-profile.json", []byte(syntheticMachineProfileJSON))
				environment := composePortPrivateInput(t, directory, "sensitive-machine-profile.env", []byte("# explicit protected overlay\n"))
				selected := &configuration
				if source == "environment" {
					selected = &environment
				}
				var e error
				switch fault {
				case "missing":
					*selected = filepath.Join(directory, "missing-sensitive-input")
				case "public":
					e = os.Chmod(*selected, 0644)
				case "symlink", "hardlink", "fifo":
					link := filepath.Join(directory, "linked-sensitive-input")
					switch fault {
					case "symlink":
						e = os.Symlink(*selected, link)
					case "hardlink":
						e = os.Link(*selected, link)
					case "fifo":
						e = syscall.Mkfifo(link, 0600)
					}
					*selected = link
				case "empty":
					e = os.WriteFile(*selected, nil, 0600)
				}
				if e != nil {
					t.Fatal(e)
				}
				var output bytes.Buffer
				composePortFixedFailure(t, runComposeMachineValuesCommand([]string{"--config", configuration, "--env-file", environment}, &output), composeMachineInputInvalid, &output)
			})
		}
	}
	for _, fault := range []string{"directory_configuration", "reader_limit_configuration"} {
		t.Run(fault, func(t *testing.T) {
			directory := composePortPrivateDirectory(t)
			configuration := directory
			if fault == "reader_limit_configuration" {
				configuration = composePortPrivateInput(t, directory, "sensitive-too-large.json", bytes.Repeat([]byte("x"), (1<<20)+1))
			}
			var output bytes.Buffer
			composePortFixedFailure(t, runComposeMachineValuesCommand([]string{"--config", configuration}, &output), composeMachineInputInvalid, &output)
		})
	}
	for _, item := range []struct {
		name, configuration, environment, want string
	}{
		{"malformed_json", `{"sensitive-token":"unprinted"`, "", "COMPOSE_MACHINE_VALUES_INVALID"},
		{"invalid_boolean", strings.Replace(syntheticMachineProfileJSON, `"health_retries":3`, `"health_retries":false`, 1), "", "COMPOSE_MACHINE_VALUES_INVALID"},
		{"json_logical_limit", syntheticMachineProfileJSON + strings.Repeat(" ", (64<<10)+1-len(syntheticMachineProfileJSON)), "", "COMPOSE_MACHINE_VALUES_INVALID"},
		{"unknown_environment", syntheticMachineProfileJSON, "PROJECT_ONE_UNKNOWN=sensitive-value\n", "COMPOSE_MACHINE_ENVIRONMENT_INVALID"},
		{"duplicate_environment", syntheticMachineProfileJSON, "PROJECT_ONE_COMPOSE_HEALTH_RETRIES=4\nPROJECT_ONE_COMPOSE_HEALTH_RETRIES=5\n", "COMPOSE_MACHINE_ENVIRONMENT_INVALID"},
		{"environment_expansion", syntheticMachineProfileJSON, "PROJECT_ONE_COMPOSE_HEALTH_RETRIES=$(sensitive-token)\n", "COMPOSE_MACHINE_ENVIRONMENT_INVALID"},
		{"environment_logical_limit", syntheticMachineProfileJSON, "#" + strings.Repeat("x", 16<<10), "COMPOSE_MACHINE_ENVIRONMENT_INVALID"},
		{"environment_invalid_utf8", syntheticMachineProfileJSON, "#\xff\n", "COMPOSE_MACHINE_ENVIRONMENT_INVALID"},
	} {
		t.Run(item.name, func(t *testing.T) {
			directory := composePortPrivateDirectory(t)
			configuration := composePortPrivateInput(t, directory, "sensitive-machine-values.json", []byte(item.configuration))
			args := []string{"--config", configuration}
			if item.environment != "" {
				environment := composePortPrivateInput(t, directory, "sensitive-machine-values.env", []byte(item.environment))
				args = append(args, "--env-file", environment)
			}
			var output bytes.Buffer
			composePortFixedFailure(t, runComposeMachineValuesCommand(args, &output), item.want, &output)
		})
	}
	for _, item := range []struct {
		name   string
		writer composePortBrokenWriter
	}{{"writer_error", composePortBrokenWriter{}}, {"writer_short", composePortBrokenWriter{short: true}}} {
		t.Run(item.name, func(t *testing.T) {
			configuration := composePortPrivateInput(t, composePortPrivateDirectory(t), "profile.json", []byte(syntheticMachineProfileJSON))
			composePortFixedFailure(t, runComposeMachineValuesCommand([]string{"--config", configuration}, item.writer), composeMachineOutputFailed, nil)
		})
	}
	t.Run("changed_input_is_reloaded_between_explicit_invocations", func(t *testing.T) {
		directory := composePortPrivateDirectory(t)
		configuration := composePortPrivateInput(t, directory, "profile.json", []byte(syntheticMachineProfileJSON))
		var before, after bytes.Buffer
		args := []string{"--config", configuration}
		if e := runComposeMachineValuesCommand(args, &before); e != nil {
			t.Fatal(e)
		}
		changed := strings.Replace(syntheticMachineProfileJSON, `"published_port":19443`, `"published_port":29444`, 1)
		if e := os.WriteFile(configuration, []byte(changed), 0600); e != nil {
			t.Fatal(e)
		}
		if e := runComposeMachineValuesCommand(args, &after); e != nil || bytes.Equal(before.Bytes(), after.Bytes()) || !strings.Contains(after.String(), `published: "29444"`) || strings.Contains(after.String(), `published: "19443"`) {
			t.Fatal("explicit rerender reused a stale private snapshot", e)
		}
		// This tests fresh reads between invocations, not an injected race inside
		// the shared release reader's independently covered pinned snapshot.
		if raw, e := os.ReadFile(configuration); e != nil || string(raw) != changed {
			t.Fatal("renderer rewrote changed public intent", e)
		}
	})
}
