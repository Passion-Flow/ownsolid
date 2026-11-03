//go:build offlineintegration

package compose

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

func TestActualBoundedSyntheticProcessStartStop(t *testing.T) {
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_PROCESS_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		t.Fatal("explicit process profile required", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	reader, e := engine.New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	if _, e = reader.Preflight(ctx); e != nil {
		t.Fatal(e)
	}
	image := os.Getenv("PROJECT_ONE_TEST_PROCESS_IMAGE")
	imageValue, found, e := reader.Inspect(ctx, image)
	if e != nil || !found || imageValue.Config == nil || len(imageValue.Config.Volumes) > 0 {
		t.Fatal("explicit synthetic image required", e)
	}
	random := make([]byte, 16)
	if _, e = rand.Read(random); e != nil {
		t.Fatal(e)
	}
	scope := RenderContext{ScopeID: hex.EncodeToString(random), ScopeRoot: t.TempDir(), AllowedImages: map[string]bool{image: true}}
	if os.Chmod(scope.ScopeRoot, 0700) != nil {
		t.Fatal("private scope")
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	model := Model{Services: map[string]Service{"control": {Image: image, User: "10033:10034", Command: []string{"--listen", "0.0.0.0:8080", "--lifetime", "60s", "--shutdown-timeout", "1s"}, MemoryBytes: 64 << 20, MilliCPU: 100, PIDs: 16, Restart: "no", ReadOnly: true, Ports: []Port{{HostIP: "127.0.0.1", Published: port, Target: 8080}}}}}
	evidencePath := os.Getenv("PROJECT_ONE_TEST_PROCESS_EVIDENCE")
	intent, e := os.OpenFile(evidencePath+"-intent.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if json.NewEncoder(intent).Encode(map[string]any{"scope_id": scope.ScopeID, "model": model, "profile": "explicit_synthetic_process_interface_test"}) != nil || intent.Sync() != nil {
		t.Fatal("test intent not durable")
	}
	intent.Close()
	// Cleanup has its own bounded context and preserves mismatched/foreign resources.
	cleanupClient := scope.ScopeRoot + "/cleanup-client"
	if os.Mkdir(cleanupClient, 0700) != nil || os.WriteFile(cleanupClient+"/config.json", []byte("{}\n"), 0600) != nil {
		t.Fatal("isolated cleanup client unavailable")
	}
	cleaned := false
	clean := func() {
		cleanupContext, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		observed, e := client.ObserveRuntime(cleanupContext, reader, scope, model)
		if e != nil {
			t.Error("cleanup identity mismatch; preserved", e)
			return
		}
		if len(observed.Services) > 0 {
			if _, e = client.StopRuntime(cleanupContext, reader, scope, model, observed); e != nil {
				t.Error("owned process stop unconfirmed", e)
				return
			}
		}
		run := func(args ...string) bool {
			command := exec.CommandContext(cleanupContext, c.Compose.DockerBinary, append([]string{"--config", cleanupClient, "--host", "unix://" + c.Engine.SocketFile}, args...)...)
			command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "DOCKER_API_VERSION=1.49"}
			if _, e := command.CombinedOutput(); e != nil {
				t.Error("exact fixture cleanup failed", e)
				return false
			}
			return true
		}
		for _, service := range observed.Services {
			if !run("container", "rm", service.ID) {
				return
			}
		}
		observed, e = client.ObserveRuntime(cleanupContext, reader, scope, model)
		if e != nil || len(observed.Services) > 0 {
			t.Error("container cleanup unconfirmed", e)
			return
		}
		if observed.NetworkID != "" && !run("network", "rm", observed.NetworkID) {
			return
		}
		if observed.IngressNetworkID != "" && !run("network", "rm", observed.IngressNetworkID) {
			return
		}
		observed, e = client.ObserveRuntime(cleanupContext, reader, scope, model)
		if e != nil || observed.Status != "observed_absent_compose_resources" {
			t.Error("resource cleanup unconfirmed", e)
			return
		}
		cleaned = true
	}
	t.Cleanup(func() {
		if !cleaned {
			clean()
		}
	})
	created, e := client.CreateRuntime(ctx, reader, scope, model)
	if e != nil {
		t.Fatal("create", e)
	}
	started, e := client.StartRuntime(ctx, reader, scope, model, created)
	if e != nil || started.Status != "observed_runtime_processes_running" {
		t.Fatal("start", started, e)
	}
	httpClient := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	address := "http://127.0.0.1:" + strconv.Itoa(port) + "/fixture-health"
	healthy := false
	failures := []string{}
	httpDeadline := time.Now().Add(time.Duration(c.Compose.Process.ObserveSeconds) * time.Second)
	for time.Now().Before(httpDeadline) {
		response, e := httpClient.Get(address)
		if e != nil {
			failures = append(failures, e.Error())
		}
		if e == nil {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
			response.Body.Close()
			failures = append(failures, strconv.Itoa(response.StatusCode)+":"+string(body))
			if response.StatusCode == 200 && string(body) == "{\"fixture\":\"explicit_synthetic_process\"}\n" {
				healthy = true
				break
			}
		}
		time.Sleep(time.Duration(c.Compose.Process.PollMilliseconds) * time.Millisecond)
	}
	if !healthy {
		current, _, observationError := reader.InspectRuntime(ctx, scope.ScopeID, "control")
		diagnostic := map[string]any{"scope_id": scope.ScopeID, "started": started, "requests": failures, "state": current.State, "ports": current.HostConfig.PortBindings, "observation_error": observationError != nil}
		diagnosticRaw, _ := json.MarshalIndent(diagnostic, "", "  ")
		if os.WriteFile(evidencePath+"-failed.json", diagnosticRaw, 0600) != nil {
			t.Error("failure diagnostic unavailable")
		}
		t.Fatal("actual synthetic HTTP response unavailable")
	}
	again, e := client.StartRuntime(ctx, reader, scope, model, created)
	if e != nil || !processIdentity(started, again) {
		t.Fatal("running repeat changed identity", e)
	}
	stopped, e := client.StopRuntime(ctx, reader, scope, model, created)
	if e != nil || stopped.Status != "observed_runtime_processes_stopped" {
		t.Fatal("stop", stopped, e)
	}
	if _, e = client.StopRuntime(ctx, reader, scope, model, created); e != nil {
		t.Fatal("stopped repeat", e)
	}
	clean()
	if !cleaned {
		t.Fatal("owned fixture retained")
	}
	file, e := os.OpenFile(evidencePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	if json.NewEncoder(file).Encode(map[string]any{"status": "passed_actual_synthetic_process_start_HTTP_stop_repeat_and_cleanup", "scope_id": scope.ScopeID, "created": created, "started": started, "stopped": stopped, "synthetic_HTTP": healthy, "cleaned": cleaned, "qualification": engine.LocalQualification}) != nil || file.Sync() != nil {
		t.Fatal("evidence not durable")
	}
}
