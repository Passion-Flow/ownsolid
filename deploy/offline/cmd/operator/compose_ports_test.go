package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const syntheticComposePortJSON = `{"format_version":1,"bind_address":"127.0.0.1","published_port":8443,"target_port":8443}`

func composePortPrivateDirectory(t *testing.T) string {
	t.Helper()
	directory, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(directory, 0700); e != nil {
		t.Fatal(e)
	}
	return directory
}

func composePortPrivateInput(t *testing.T, directory, name string, raw []byte) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	return path
}

func composePortFixedFailure(t *testing.T, e error, expected string, output *bytes.Buffer) {
	t.Helper()
	if e == nil || e.Error() != expected || errors.Unwrap(e) != nil {
		t.Fatalf("expected fixed redacted %s, observed %v", expected, e)
	}
	if output != nil && output.Len() != 0 {
		t.Fatal("refused public projection wrote output")
	}
}

func composePortDispatchOutput(t *testing.T, args []string) ([]byte, error) {
	t.Helper()
	read, write, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	previous := os.Stdout
	defer func() {
		os.Stdout = previous
		read.Close()
		write.Close()
	}()
	os.Stdout = write
	e = run(args)
	if closeError := write.Close(); closeError != nil {
		t.Fatal(closeError)
	}
	os.Stdout = previous
	raw, readError := io.ReadAll(read)
	if readError != nil {
		t.Fatal(readError)
	}
	return raw, e
}

func TestComposePortValuesCLIEarlyPublicProjection(t *testing.T) {
	directory := composePortPrivateDirectory(t)
	configuration := composePortPrivateInput(t, directory, "port-values.json", []byte(syntheticComposePortJSON))
	environment := composePortPrivateInput(t, directory, "port-values.env", []byte("PROJECT_ONE_BIND_ADDRESS=\"::1\"\nPROJECT_ONE_HTTPS_PORT=\"18444\"\nPROJECT_ONE_CONTROL_PORT=9555\n"))
	// An early renderer must not interpret any ambient port/Engine input or
	// demand the unrelated signed-release/preparation configuration shape.
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(directory, "unavailable-engine.socket"))
	t.Setenv("PROJECT_ONE_BIND_ADDRESS", "not-an-address")
	t.Setenv("PROJECT_ONE_HTTPS_PORT", "false")
	t.Setenv("PROJECT_ONE_CONTROL_PORT", "65536")
	for _, item := range []struct {
		name string
		args []string
		want string
	}{
		{"json_only", []string{"--config", configuration}, "services:\n  control:\n    ports: !override\n      - target: 8443\n        published: \"8443\"\n        host_ip: \"127.0.0.1\"\n        protocol: \"tcp\"\n"},
		{"explicit_overlay_ipv6", []string{"--config=" + configuration, "--env-file=" + environment}, "services:\n  control:\n    ports: !override\n      - target: 9555\n        published: \"18444\"\n        host_ip: \"::1\"\n        protocol: \"tcp\"\n"},
	} {
		t.Run(item.name, func(t *testing.T) {
			raw, e := composePortDispatchOutput(t, append([]string{composePortValuesCommand}, item.args...))
			if e != nil || string(raw) != item.want {
				t.Fatalf("early public-only port YAML mismatch: %v %q", e, raw)
			}
			var direct bytes.Buffer
			if e = runComposePortValuesCommand(item.args, &direct); e != nil || !bytes.Equal(raw, direct.Bytes()) {
				t.Fatal("early dispatcher did not preserve exact typed public projection", e)
			}
		})
	}
	if raw, e := os.ReadFile(configuration); e != nil || string(raw) != syntheticComposePortJSON {
		t.Fatal("read-only renderer changed its protected configuration", e)
	}
	if _, e := os.Lstat(filepath.Join(directory, "unavailable-engine.socket")); !os.IsNotExist(e) {
		t.Fatal("public renderer created an Engine socket path")
	}
}

func TestComposePortValuesCLIRejectsArgumentsBeforeIO(t *testing.T) {
	directory := composePortPrivateDirectory(t)
	configuration := composePortPrivateInput(t, directory, "must-not-read.json", []byte("synthetic sensitive invalid config must remain unprinted"))
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
		{"unknown_flag", []string{"--config", configuration, "--unknown", "synthetic-secret"}},
		{"missing_config_value", []string{"--config"}},
		{"empty_config", []string{"--config="}},
		{"missing_config_with_environment", []string{"--env-file", environment}},
		{"duplicate_config", []string{"--config", configuration, "--config", configuration}},
		{"mixed_duplicate_config", []string{"--config=" + configuration, "--config", configuration}},
		{"duplicate_environment", []string{"--config", configuration, "--env-file", environment, "--env-file", environment}},
		{"mixed_duplicate_environment", []string{"--config", configuration, "--env-file=" + environment, "--env-file", environment}},
		{"empty_environment", []string{"--config", configuration, "--env-file="}},
		{"missing_environment_value", []string{"--config", configuration, "--env-file"}},
		{"relative_configuration", []string{"--config", "private-values.json"}},
		{"unclean_configuration", []string{"--config", directory + "/./port-values.json"}},
		{"root_configuration", []string{"--config", "/"}},
		{"control_configuration", []string{"--config", directory + "/sensitive\nvalues.json"}},
		{"relative_environment", []string{"--config", configuration, "--env-file", "port.env"}},
		{"unclean_environment", []string{"--config", configuration, "--env-file", directory + "/../port.env"}},
		{"root_environment", []string{"--config", configuration, "--env-file", "/"}},
		{"control_environment", []string{"--config", configuration, "--env-file", directory + "/sensitive\tenv"}},
		{"positional_before", []string{"unexpected", "--config", configuration}},
		{"positional_after", []string{"--config", configuration, "unexpected"}},
		{"operation_owner_flag", []string{"--config", configuration, "--operation-file", "synthetic-secret"}},
		{"help", []string{"--help"}},
		{"help_after_configuration", []string{"--config", configuration, "--help"}},
		{"fifo_not_opened_before_unknown_flag", []string{"--config", fifo, "--unknown"}},
	} {
		t.Run(item.name, func(t *testing.T) {
			var output bytes.Buffer
			composePortFixedFailure(t, runComposePortValuesCommand(item.args, &output), composePortArgumentInvalid, &output)
		})
	}
	t.Run("nil_writer_before_input", func(t *testing.T) {
		composePortFixedFailure(t, runComposePortValuesCommand([]string{"--config", configuration}, nil), composePortArgumentInvalid, nil)
	})
	t.Run("old_dispatch_refusals_unchanged", func(t *testing.T) {
		composePortFixedFailure(t, run(nil), "OFFLINE_OPERATOR_COMMAND_REQUIRED", nil)
		composePortFixedFailure(t, run([]string{"unknown-command", "--config", configuration}), "OFFLINE_OPERATOR_COMMAND_INVALID", nil)
	})
	if raw, e := os.ReadFile(configuration); e != nil || string(raw) != "synthetic sensitive invalid config must remain unprinted" {
		t.Fatal("argument refusal changed the unrelated protected input", e)
	}
}

type composePortBrokenWriter struct {
	short bool
}

func (w composePortBrokenWriter) Write(raw []byte) (int, error) {
	if w.short {
		return len(raw) - 1, nil
	}
	return 0, errors.New("synthetic-sensitive-writer-path-or-token")
}

func TestComposePortValuesCLIProtectedInputAndRedaction(t *testing.T) {
	for _, name := range []string{
		"missing_configuration", "public_configuration", "symlink_configuration", "hardlink_configuration", "directory_configuration", "fifo_configuration", "empty_configuration", "reader_limit_configuration",
		"missing_environment", "public_environment", "symlink_environment", "hardlink_environment", "fifo_environment",
		"malformed_json", "invalid_port", "json_logical_limit", "unknown_environment", "duplicate_environment", "environment_expansion", "environment_logical_limit",
	} {
		t.Run(name, func(t *testing.T) {
			directory := composePortPrivateDirectory(t)
			configuration := composePortPrivateInput(t, directory, "sensitive-port-values.json", []byte(syntheticComposePortJSON))
			environment := ""
			want := composePortInputInvalid
			change := func(path string, body []byte) {
				t.Helper()
				if e := os.WriteFile(path, body, 0600); e != nil {
					t.Fatal(e)
				}
			}
			switch name {
			case "missing_configuration":
				configuration = filepath.Join(directory, "missing-sensitive-values.json")
			case "public_configuration":
				if e := os.Chmod(configuration, 0644); e != nil {
					t.Fatal(e)
				}
			case "symlink_configuration", "hardlink_configuration":
				link := filepath.Join(directory, "linked-sensitive-values.json")
				var e error
				if name == "symlink_configuration" {
					e = os.Symlink(configuration, link)
				} else {
					e = os.Link(configuration, link)
				}
				if e != nil {
					t.Fatal(e)
				}
				configuration = link
			case "directory_configuration":
				configuration = directory
			case "fifo_configuration":
				configuration = filepath.Join(directory, "sensitive-values.fifo")
				if e := syscall.Mkfifo(configuration, 0600); e != nil {
					t.Fatal(e)
				}
			case "empty_configuration":
				change(configuration, nil)
			case "reader_limit_configuration":
				change(configuration, bytes.Repeat([]byte("x"), (1<<20)+1))
			case "missing_environment":
				environment = filepath.Join(directory, "missing-sensitive.env")
			case "public_environment", "symlink_environment", "hardlink_environment", "fifo_environment":
				environment = composePortPrivateInput(t, directory, "sensitive-port.env", []byte("PROJECT_ONE_HTTPS_PORT=19443\n"))
				if name == "public_environment" {
					if e := os.Chmod(environment, 0644); e != nil {
						t.Fatal(e)
					}
				} else {
					link := filepath.Join(directory, "linked-sensitive.env")
					var e error
					switch name {
					case "symlink_environment":
						e = os.Symlink(environment, link)
					case "hardlink_environment":
						e = os.Link(environment, link)
					case "fifo_environment":
						e = syscall.Mkfifo(link, 0600)
					}
					if e != nil {
						t.Fatal(e)
					}
					environment = link
				}
			case "malformed_json":
				change(configuration, []byte(`{"sensitive-token":"unprinted"`))
				want = "COMPOSE_PORT_VALUES_INVALID"
			case "invalid_port":
				change(configuration, []byte(strings.Replace(syntheticComposePortJSON, `"published_port":8443`, `"published_port":65536`, 1)))
				want = "COMPOSE_PORT_VALUES_INVALID"
			case "json_logical_limit":
				change(configuration, []byte(syntheticComposePortJSON+strings.Repeat(" ", (64<<10)+1-len(syntheticComposePortJSON))))
				want = "COMPOSE_PORT_VALUES_INVALID"
			case "unknown_environment", "duplicate_environment", "environment_expansion", "environment_logical_limit":
				body := "PROJECT_ONE_UNKNOWN=sensitive-value\n"
				switch name {
				case "duplicate_environment":
					body = "PROJECT_ONE_HTTPS_PORT=19443\nPROJECT_ONE_HTTPS_PORT=29443\n"
				case "environment_expansion":
					body = "PROJECT_ONE_HTTPS_PORT=$(sensitive-token)\n"
				case "environment_logical_limit":
					body = "#" + strings.Repeat("x", 16<<10)
				}
				environment = composePortPrivateInput(t, directory, "sensitive-invalid.env", []byte(body))
				want = "COMPOSE_PORT_ENVIRONMENT_INVALID"
			}
			args := []string{"--config", configuration}
			if environment != "" {
				args = append(args, "--env-file", environment)
			}
			var output bytes.Buffer
			composePortFixedFailure(t, runComposePortValuesCommand(args, &output), want, &output)
		})
	}
	for _, item := range []struct {
		name   string
		writer composePortBrokenWriter
	}{
		{"writer_error", composePortBrokenWriter{}},
		{"writer_short", composePortBrokenWriter{short: true}},
	} {
		t.Run(item.name, func(t *testing.T) {
			configuration := composePortPrivateInput(t, composePortPrivateDirectory(t), "port-values.json", []byte(syntheticComposePortJSON))
			composePortFixedFailure(t, runComposePortValuesCommand([]string{"--config", configuration}, item.writer), composePortOutputFailed, nil)
		})
	}
}
