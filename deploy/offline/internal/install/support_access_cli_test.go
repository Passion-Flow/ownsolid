package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This black-box flow is selected only with the exact native installer prepared outside Git.
func TestSupportAccessCLI(t *testing.T) {
	binary := os.Getenv("OWNSOLID_SUPPORT_INSTALLER")
	if binary == "" {
		t.Skip("Set OWNSOLID_SUPPORT_INSTALLER to the admitted native installer")
	}
	for _, kind := range []string{"local", "managed_cluster"} {
		t.Run(kind, func(t *testing.T) {
			directory := shortTempDir(t)
			var source []byte
			var input, operator string
			var requests atomic.Int64
			var server *httptest.Server
			if kind == "local" {
				input = filepath.Join(directory, "install.yaml")
				source = []byte(validYAML(t, filepath.Join(directory, "installation")))
				c, err := Decode(source)
				if err != nil {
					t.Fatal(err)
				}
				operator = c.Layout().Operator
			} else {
				operator = directory
				input = filepath.Join(directory, "cluster.json")
				server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					events := supportAccessEvents(t, operator)
					if len(events) == 0 || events[len(events)-1].Outcome != "admitted" {
						t.Error("CLI performed HTTPS read without durable admission")
					}
					if r.Header.Get("Authorization") != "Bearer "+supportTestBearer {
						t.Error("CLI omitted source authorization")
					}
					w.WriteHeader(http.StatusForbidden)
				}))
				defer server.Close()
				c := clusterSupportInput{Version: 1, TimeoutSeconds: 2,
					Helm: &helmSupportInput{writeClusterSupportEndpoint(t, server, supportTestBearer), supportTestNamespace, supportTestRelease}}
				var err error
				source, err = json.Marshal(c)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(input, source, 0o600); err != nil {
				t.Fatal(err)
			}
			run := func(output string) (string, error) {
				flag := "--config"
				if kind == "managed_cluster" {
					flag = "--cluster-config"
				}
				command := exec.CommandContext(t.Context(), binary, "support-bundle", flag, input, "--output", output)
				command.Env = append(os.Environ(), "DOCKER_HOST=unix://"+filepath.Join(directory, "missing.sock"))
				raw, err := command.CombinedOutput()
				return string(raw), err
			}
			output := filepath.Join(directory, "first.tar.gz")
			if raw, err := run(output); err != nil {
				t.Fatalf("native support collection failed: %v %s", err, raw)
			}
			first := checkSupportCompletion(t, operator, output, kind)
			path := filepath.Join(operator, operationLogName)
			if err := os.Chmod(path, 0o400); err != nil {
				t.Fatal(err)
			}
			before := requests.Load()
			refused := filepath.Join(directory, "refused.tar.gz")
			raw, err := run(refused)
			if err == nil || !strings.Contains(raw, "INSTALL_SUPPORT_ACCESS_UNAVAILABLE") ||
				strings.Contains(raw, "INSTALL_SUPPORT_COMPLETE") {
				t.Fatal("native CLI did not refuse an unwritable receipt")
			}
			if _, err := os.Lstat(refused); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("refused CLI attempt published an archive")
			}
			if requests.Load() != before {
				t.Fatal("refused CLI attempt reached HTTPS")
			}
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			retry := filepath.Join(directory, "retry.tar.gz")
			if raw, err := run(retry); err != nil {
				t.Fatalf("native explicit retry failed: %v %s", err, raw)
			}
			events := supportAccessEvents(t, operator)
			if len(events) != 4 || events[2].Attempt == first[0].Attempt ||
				events[2].Outcome != "admitted" || events[3].Outcome != "complete" {
				t.Fatal("native restart/retry did not preserve distinct attempts")
			}
			_, report := readSupportArchive(t, retry)
			for _, sentinel := range []string{directory, supportTestBearer} {
				if bytes.Contains(report, []byte(sentinel)) {
					t.Fatal("native report leaked private values")
				}
			}
		})
	}
}

func TestSupportAccessKilledCLILeavesOriginalAdmission(t *testing.T) {
	binary := os.Getenv("OWNSOLID_SUPPORT_INSTALLER")
	if binary == "" {
		t.Skip("Set OWNSOLID_SUPPORT_INSTALLER to the admitted native installer")
	}
	directory := shortTempDir(t)
	started := make(chan struct{}, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	c := clusterSupportInput{Version: 1, TimeoutSeconds: 10,
		Helm: &helmSupportInput{writeClusterSupportEndpoint(t, server, supportTestBearer), supportTestNamespace, supportTestRelease}}
	source, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	input, output := filepath.Join(directory, "cluster.json"), filepath.Join(directory, "killed.tar.gz")
	if err := os.WriteFile(input, source, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), binary, "support-bundle", "--cluster-config", input, "--output", output)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			command.Process.Kill()
			command.Wait()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("native collector did not reach its selected private source")
	}
	events := supportAccessEvents(t, directory)
	if len(events) != 1 || events[0].Outcome != "admitted" {
		t.Fatal("native protected read lacked durable admission")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("killed native CLI returned success")
	}
	after := supportAccessEvents(t, directory)
	if len(after) != 1 || after[0].Attempt != events[0].Attempt {
		t.Fatal("killed CLI fabricated a terminal outcome")
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("killed CLI published an archive")
	}
}
