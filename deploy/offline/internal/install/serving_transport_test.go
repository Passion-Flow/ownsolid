package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestServingIntentUsesOnlyThePublicContract(t *testing.T) {
	c := validConfig(t)
	args, err := c.servingArguments(containerConfigDir + "/control.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if len(args) != 5 || args[0] != "verify-serving-transport" || args[3] != "--expected-server-json" || json.Unmarshal([]byte(args[4]), &fields) != nil {
		t.Fatalf("unexpected public arguments: %v", args)
	}
	if len(fields) != 8 || fields["transport_mode"] != "direct_https" || fields["listen_address"] != containerListen || fields["portal_origin"] != c.Origins.Portal || fields["admin_origin"] != c.Origins.Admin || len(fields["trusted_proxy_cidrs"].([]any)) != 0 {
		t.Fatalf("public intent differs from the installed transport: %v", fields)
	}
	machine := map[string]any{"listen_address": "0.0.0.0:9443", "origin": "https://machine.example.invalid:9443", "tls_certificate_file": "/run/machine/cert.pem", "tls_private_key_file": "/run/machine/key.pem"}
	c.ControlSettings = map[string]any{"machine_server": machine}
	args, err = c.servingArguments(containerConfigDir + "/control.yaml")
	if err != nil || len(args) != 7 || args[5] != "--expected-machine-json" {
		t.Fatalf("machine intent lost: %v %v", args, err)
	}
	fields = nil
	if err := json.Unmarshal([]byte(args[6]), &fields); err != nil || !reflect.DeepEqual(fields, machine) {
		t.Fatalf("machine intent changed: %v %v", fields, err)
	}
	for name, value := range map[string]any{"null": nil, "scalar": "hidden-value", "missing": map[string]any{}, "secret": map[string]any{"listen_address": "a", "origin": "b", "tls_certificate_file": "c", "password": "hidden-value"}} {
		t.Run(name, func(t *testing.T) {
			c.ControlSettings["machine_server"] = value
			if args, err := c.servingArguments("unused"); err == nil || args != nil || strings.Contains(err.Error(), "hidden-value") {
				t.Fatalf("invalid machine intent escaped admission: %v %v", args, err)
			}
		})
	}
}

func TestApplyServingRefusalPreservesTheWriterAndCanRetry(t *testing.T) {
	for _, failure := range []struct {
		name, output string
		err          error
	}{
		{"rejected", "SERVING_TRANSPORT_MISMATCH\n", errors.New("exit 1")},
		{"missing-marker", "task finished\n", nil},
		{"embedded-marker", "warning CONTROL_SERVING_TRANSPORT_OK details\n", nil},
	} {
		t.Run(failure.name, func(t *testing.T) {
			c := validConfig(t)
			runner := &fakeRunner{answer: func(line string) (string, error) {
				if strings.Contains(line, "verify-serving-transport") {
					return failure.output, failure.err
				}
				return successOutput(line), nil
			}}
			var out bytes.Buffer
			if err := testInstaller(c, runner, &out).apply(t.Context()); err == nil || !strings.Contains(err.Error(), "serving-transport") {
				t.Fatalf("serving refusal was ignored: %v", err)
			}
			for _, command := range runner.commands {
				if strings.Contains(command, "stop control") || strings.Contains(command, "up --detach") || strings.Contains(command, " migrate ") {
					t.Fatalf("writer or database changed after refusal: %s", command)
				}
			}
			keyPath := filepath.Join(c.Layout().Config, "auth-hmac")
			before, err := os.ReadFile(keyPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := testInstaller(c, &fakeRunner{}, &out).apply(t.Context()); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(keyPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("retry changed the installation key: %v", err)
			}
		})
	}
}

type servingImageRunner struct {
	t     *testing.T
	image string
	pass  bool
	file  string
}

func (r *servingImageRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.t.Helper()
	if name != "docker" || !strings.Contains(strings.Join(args, " "), "--no-deps --no-TTY control-operator verify-serving-transport") {
		r.t.Fatalf("unexpected serving image command: %s %v", name, args)
	}
	for index, arg := range args {
		if arg == "-f" {
			r.file = args[index+1]
		}
	}
	raw, err := os.ReadFile(r.file)
	if err != nil {
		r.t.Fatal(err)
	}
	want, _ := json.Marshal(map[string]any{"services": map[string]any{"control-operator": map[string]string{"image": r.image}}})
	if !bytes.Equal(bytes.TrimSpace(raw), want) {
		r.t.Fatalf("override changes more than the operator image: %s", raw)
	}
	if r.pass {
		return servingTransportSuccess + "\n", nil
	}
	return "SERVING_TRANSPORT_MISMATCH\n", errors.New("exit 1")
}

func TestServingImageCheckKeepsInstalledFilesAndRemovesItsOverride(t *testing.T) {
	c := validConfig(t)
	if err := Materialize(c); err != nil {
		t.Fatal(err)
	}
	paths := []string{c.Layout().ComposeFile, c.Layout().ComposeEnv, filepath.Join(c.Layout().Config, "control.yaml"), filepath.Join(c.Layout().Operator, "operator.yaml")}
	before := make([][]byte, len(paths))
	for n, path := range paths {
		var err error
		before[n], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, pass := range []bool{false, true} {
		runner := &servingImageRunner{t: t, image: "sha256:" + strings.Repeat("a", 64), pass: pass}
		i := &installer{config: c, runner: runner}
		if err := i.verifyServingImage(t.Context(), runner.image); (err == nil) != pass {
			t.Fatalf("pass=%t: %v", pass, err)
		}
		if _, err := os.Lstat(runner.file); !os.IsNotExist(err) {
			t.Fatalf("temporary override retained: %v", err)
		}
		for n, path := range paths {
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before[n], after) {
				t.Fatalf("installed file changed: %s %v", path, err)
			}
		}
	}
}
