//go:build offlineintegration

package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type exitingProcessWorker struct{ *compose.Client }

func (w exitingProcessWorker) StartRuntime(ctx context.Context, r compose.RuntimeReader, s compose.RenderContext, m compose.Model, known compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	status, e := w.Client.StartRuntime(ctx, r, s, m, known)
	if e == nil {
		os.Exit(98)
	}
	return status, e
}
func (w exitingProcessWorker) StopRuntime(ctx context.Context, r compose.RuntimeReader, s compose.RenderContext, m compose.Model, known compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	status, e := w.Client.StopRuntime(ctx, r, s, m, known)
	if e == nil {
		os.Exit(99)
	}
	return status, e
}
func TestActualScopedUnfinishedProcessHelper(t *testing.T) {
	target := os.Getenv("PROJECT_ONE_TEST_PROCESS_LOSS_TARGET")
	if target == "" {
		return
	}
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_PROCESS_SCOPE_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || c.Engine == nil {
		t.Fatal(e)
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, e = processRuntime(ctx, c, client, exitingProcessWorker{worker}, target, false); e != nil {
		t.Fatal(e)
	}
	t.Fatal("controlled process loss did not occur")
}
func TestActualSignedScopedProcessLossHTTPAndStopReconciliation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	receiptRaw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_PROCESS_FIXTURE_MANIFEST"))
	if e != nil {
		t.Fatal(e)
	}
	var receipt struct {
		Status         string `json:"status"`
		Archive        string `json:"archive"`
		ArchiveSHA256  string `json:"archive_sha256"`
		ImageManifest  string `json:"image_manifest"`
		ImageConfig    string `json:"image_config"`
		OperatorBinary string `json:"operator_binary"`
		OperatorSHA256 string `json:"operator_sha256"`
	}
	if wire.UniqueJSON(receiptRaw, &receipt) != nil || receipt.Status != "manufactured_synthetic_fixture_only_pending_scope_test" {
		t.Fatal("actual bounded fixture receipt required")
	}
	imageBytes, e := os.ReadFile(receipt.Archive)
	if e != nil || len(imageBytes) > 32<<20 || wire.Digest(imageBytes) != receipt.ArchiveSHA256 {
		t.Fatal("actual fixture bytes", e)
	}
	binaryBytes, e := os.ReadFile(receipt.OperatorBinary)
	if e != nil || wire.Digest(binaryBytes) != receipt.OperatorSHA256 {
		t.Fatal("actual operator changed", e)
	}
	profileRaw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_PROCESS_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	profile, e := configuration.Load(profileRaw, nil)
	if e != nil || profile.Engine == nil || profile.Compose == nil || profile.Compose.Process == nil {
		t.Fatal(e)
	}
	asset := wire.Asset{ID: "actual-process-image", Path: "images/process.tar", Kind: "image_archive", Size: int64(len(imageBytes)), SHA256: receipt.ArchiveSHA256, ImageID: receipt.ImageManifest, ImageConfigDigest: receipt.ImageConfig, Architecture: "amd64", Licenses: []string{"license"}}
	c := fixtureWithAssets(t, []wire.Asset{asset}, [][]byte{imageBytes})
	c.Engine = profile.Engine
	c.Engine.Utility = &wire.UtilityResources{MemoryBytes: 24 << 20, MilliCPU: 50, PIDs: 12}
	c.Compose = profile.Compose
	parent := filepath.Dir(c.ScopeRoot)
	c.Compose.ModelFile = filepath.Join(parent, "model.json")
	c.Compose.InputsFile = filepath.Join(parent, "sources.json")
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	model := compose.Model{Services: map[string]compose.Service{"control": {Image: asset.ImageID, User: "10033:10034", Command: []string{"--listen", "0.0.0.0:8080", "--lifetime", "60s", "--shutdown-timeout", "1s"}, MemoryBytes: 64 << 20, MilliCPU: 100, PIDs: 16, Restart: "no", ReadOnly: true, Mounts: []compose.Mount{{Type: "volume", Source: "control-config", Target: "/run/projectone", ReadOnly: true}}, Ports: []compose.Port{{HostIP: "127.0.0.1", Published: port, Target: 8080}}}}, Volumes: []string{"control-config", "operator-config"}}
	runtimeSource := filepath.Join(parent, "runtime.txt")
	operatorSource := filepath.Join(parent, "operator.txt")
	put(t, runtimeSource, []byte("Explicit synthetic runtime data"))
	put(t, operatorSource, []byte("Explicit synthetic privileged data"))
	put(t, c.Compose.ModelFile, encode(model))
	put(t, c.Compose.InputsFile, encode(SourceCatalog{FormatVersion: 1, StorageProfile: namedVolumeStorage, ImageAssetID: asset.ID, Roles: []SourceRole{{Key: "control-config", Files: []SourceFile{{Name: "runtime.txt", SourceFile: runtimeSource}}}, {Key: "operator-config", Files: []SourceFile{{Name: "operator.txt", SourceFile: operatorSource}}}}}))
	config := filepath.Join(parent, "operator.json")
	put(t, config, encode(c))
	evidencePath := os.Getenv("PROJECT_ONE_TEST_PROCESS_SCOPE_EVIDENCE")
	client, e := engine.New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		t.Fatal(e)
	}
	if _, found, e := client.Inspect(ctx, asset.ImageID); e != nil || found {
		t.Fatal("exact disposable process image must initially be absent", e)
	}
	cli := func(command string, success bool) []byte {
		out, e := exec.CommandContext(ctx, receipt.OperatorBinary, command, "--config", config).CombinedOutput()
		if (e == nil) != success {
			t.Fatalf("actual CLI %s %v %s", command, e, out)
		}
		return out
	}
	var initial Status
	if json.Unmarshal(cli("init-scope", true), &initial) != nil {
		t.Fatal("actual scope")
	}
	render := compose.RenderContext{ScopeID: initial.ScopeID, ScopeRoot: c.ScopeRoot, AllowedImages: map[string]bool{asset.ImageID: true}}
	cleaned := false
	cleanup := func() {
		cleanupContext, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		l, e := open(c, false)
		if e != nil {
			t.Error("cleanup ownership unavailable", e)
			return
		}
		events := append([]Event(nil), l.events...)
		l.close()
		raw := encodeJournal(appendJournal(events))
		if os.WriteFile(evidencePath+"-journal.json", raw, 0600) != nil {
			t.Error("cleanup journal evidence unavailable")
			return
		}
		observed, e := worker.ObserveRuntime(cleanupContext, client, render, model)
		if e != nil {
			t.Error("foreign/changed resources preserved", e)
			return
		}
		cleanupClient := filepath.Join(parent, "cleanup-client")
		if os.Mkdir(cleanupClient, 0700) != nil || os.WriteFile(filepath.Join(cleanupClient, "config.json"), []byte("{}\n"), 0600) != nil {
			t.Error("private cleanup client unavailable")
			return
		}
		docker := func(args ...string) bool {
			cmd := exec.CommandContext(cleanupContext, c.Compose.DockerBinary, append([]string{"--config", cleanupClient, "--host", "unix://" + c.Engine.SocketFile}, args...)...)
			cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "DOCKER_API_VERSION=1.49"}
			if _, e := cmd.CombinedOutput(); e != nil {
				t.Error("exact test cleanup failed", e)
				return false
			}
			return true
		}
		if len(observed.Services) > 0 {
			if _, e = worker.StopRuntime(cleanupContext, client, render, model, observed); e != nil {
				t.Error("owned cleanup stop unconfirmed", e)
				return
			}
			for _, service := range observed.Services {
				if !docker("container", "rm", service.ID) {
					return
				}
			}
		}
		observed, e = worker.ObserveRuntime(cleanupContext, client, render, model)
		if e != nil || len(observed.Services) > 0 {
			t.Error("container removal unconfirmed", e)
			return
		}
		for _, id := range []string{observed.NetworkID, observed.IngressNetworkID} {
			if id != "" && !docker("network", "rm", id) {
				return
			}
		}
		for _, r := range volumeState(events).roles {
			owner := engine.Ownership{ScopeID: initial.ScopeID, OperationID: r.ResourceOperationID, Role: r.Key}
			if _, found, e := client.InspectUtility(cleanupContext, owner, r.ImageID, r.UID, r.GID); e != nil {
				t.Error(e)
				return
			} else if found {
				if e = client.RemoveUtility(cleanupContext, owner, r.ImageID, r.UID, r.GID); e != nil {
					t.Error(e)
					return
				}
			}
			if _, found, e := client.InspectVolume(cleanupContext, owner); e != nil {
				t.Error(e)
				return
			} else if found {
				if e = client.VerifyVolumeConsumers(cleanupContext, owner, nil); e != nil {
					t.Error(e)
					return
				}
				if !docker("volume", "rm", owner.VolumeName()) {
					return
				}
			}
		}
		if image, found, e := client.Inspect(cleanupContext, asset.ImageID); e != nil {
			t.Error(e)
			return
		} else if found {
			if len(image.RepoTags) > 0 {
				t.Error("newly tagged image preserved")
				return
			}
			if !docker("image", "rm", "--no-prune", asset.ImageID) {
				return
			}
		}
		observed, e = worker.ObserveRuntime(cleanupContext, client, render, model)
		if e != nil || observed.Status != "observed_absent_compose_resources" {
			t.Error("final cleanup unconfirmed", e)
			return
		}
		cleaned = true
	}
	t.Cleanup(func() {
		if !cleaned {
			cleanup()
		}
	})
	for _, command := range []string{"import-images", "prepare-storage-inputs", "initialize-storage", "create-runtime"} {
		cli(command, true)
	}
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	crash := func(target string, code int) {
		child := exec.CommandContext(ctx, self, "-test.run=^TestActualScopedUnfinishedProcessHelper$", "-test.count=1")
		child.Env = append(os.Environ(), "PROJECT_ONE_TEST_PROCESS_LOSS_TARGET="+target, "PROJECT_ONE_TEST_PROCESS_SCOPE_CONFIG="+config)
		out, e := child.CombinedOutput()
		var exit *exec.ExitError
		if !errorAsExit(e, &exit) || exit.ExitCode() != code {
			t.Fatalf("actual controlled %s exit %v %s", target, e, out)
		}
	}
	crash("running", 98)
	startJournal, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Contains(cli("start-runtime", false), []byte("OFFLINE_SCOPE_PROCESS_RECONCILIATION_REQUIRED")) {
		t.Fatal("pending start resent")
	}
	refused, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(startJournal, refused) {
		t.Fatal("refusal rewrote start history")
	}
	var running ProcessResult
	if json.Unmarshal(cli("reconcile-process", true), &running) != nil || running.Pending || running.Status != "recorded_process_completed" {
		t.Fatal("actual start reconciliation", running)
	}
	httpClient := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	probeHTTP := func() bool {
		healthy := false
		deadline := time.Now().Add(time.Duration(c.Compose.Process.ObserveSeconds) * time.Second)
		for time.Now().Before(deadline) {
			response, e := httpClient.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/fixture-health")
			if e == nil {
				body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
				response.Body.Close()
				if response.StatusCode == 200 && strings.Contains(string(body), "explicit_synthetic_process") {
					healthy = true
					break
				}
			}
			time.Sleep(time.Duration(c.Compose.Process.PollMilliseconds) * time.Millisecond)
		}
		return healthy
	}
	healthy := probeHTTP()
	if !healthy {
		t.Fatal("actual signed synthetic process HTTP unavailable")
	}
	committed, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	cli("start-runtime", true)
	unchanged, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(committed, unchanged) {
		t.Fatal("running repeat emitted action")
	}
	var restarted RestartResult
	if json.Unmarshal(cli("restart-runtime", true), &restarted) != nil || restarted.Status != "recorded_restart_sequence_completed" || restarted.Started == nil || restarted.Stopped.ScopeID != initial.ScopeID || restarted.Started.ScopeID != initial.ScopeID {
		t.Fatal("actual restart sequence", restarted)
	}
	if !runtimeIDsMatch(RuntimeRecord{NetworkID: running.Resources.NetworkID, IngressNetworkID: running.Resources.IngressNetworkID, Services: running.Resources.Services}, restarted.Started.Resources) {
		t.Fatal("restart recreated resources")
	}
	if !probeHTTP() {
		t.Fatal("synthetic HTTP unavailable after restart")
	}
	crash("stopped", 99)
	if !bytes.Contains(cli("stop-runtime", false), []byte("OFFLINE_SCOPE_PROCESS_RECONCILIATION_REQUIRED")) {
		t.Fatal("pending stop resent")
	}
	var stopped ProcessResult
	if json.Unmarshal(cli("reconcile-process", true), &stopped) != nil || stopped.Pending || stopped.Status != "recorded_process_completed" {
		t.Fatal("actual stop reconciliation", stopped)
	}
	cli("stop-runtime", true)
	cli("reconcile-process", true)
	cli("status", true)
	cleanup()
	if !cleaned {
		t.Fatal("owned fixture retained")
	}
	file, e := os.OpenFile(evidencePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	if json.NewEncoder(file).Encode(map[string]any{"status": "passed_actual_signed_scope_process_loss_HTTP_and_stop_reconciliation", "scope_id": initial.ScopeID, "running": running, "stopped": stopped, "controlled_start_exit": 98, "controlled_stop_exit": 99, "restarted": restarted, "synthetic_HTTP": healthy, "exact_cleanup": cleaned, "qualification": engine.LocalQualification, "production_database_mutated": false}) != nil || file.Sync() != nil {
		t.Fatal("evidence not durable")
	}
}
func appendJournal(events []Event) []byte {
	var raw []byte
	for _, event := range events {
		raw = append(raw, encode(event)...)
	}
	return raw
}
