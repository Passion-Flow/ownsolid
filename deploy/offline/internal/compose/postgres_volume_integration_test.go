//go:build offlineintegration

package compose

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This uses the shared approved cached image but creates only disposable never-started resources.
func TestActualApprovedPostgresImageExactNamedVolumeCoverage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_POSTGRES_VOLUME_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil {
		t.Fatal("explicit bounded profile required", e)
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	metadata, e := client.Preflight(ctx)
	if e != nil {
		t.Fatal(e)
	}
	worker, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	raw, e = os.ReadFile(os.Getenv("PROJECT_ONE_TEST_POSTGRES_IMAGE_CATALOG"))
	if e != nil {
		t.Fatal(e)
	}
	var catalog struct {
		Repository string
		Platforms  map[string]struct {
			Digest string `json:"manifest_digest"`
		}
	}
	if wire.UniqueJSON(raw, &catalog) != nil || catalog.Repository != "pgvector/pgvector" {
		t.Fatal("approved catalog required")
	}
	id := catalog.Platforms["linux-amd64"].Digest
	image, found, e := client.Inspect(ctx, id)
	if e != nil || !found || image.Config == nil || image.ID != id || image.Architecture != "amd64" || image.OS != "linux" || len(image.Config.Volumes) != 1 {
		t.Fatal("actual cached declared-platform image required", e)
	}
	target := "/var/lib/postgresql"
	if _, ok := image.Config.Volumes[target]; !ok {
		t.Fatal("approved image declaration changed")
	}
	root := t.TempDir()
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	anonymous := filepath.Join(root, "docker-client")
	if e = os.Mkdir(anonymous, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(anonymous, "config.json"), []byte("{}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	docker := func(commandContext context.Context, args ...string) ([]byte, error) {
		command := exec.CommandContext(commandContext, c.Compose.DockerBinary, append([]string{"--config", anonymous, "--host", "unix://" + c.Engine.SocketFile}, args...)...)
		command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "DOCKER_API_VERSION=1.49"}
		return command.CombinedOutput()
	}
	inventory := func() string {
		values := map[string][]string{}
		for _, kind := range []string{"container", "network", "volume"} {
			args := []string{kind, "ls", "--quiet"}
			if kind == "container" {
				args = append(args, "--all", "--no-trunc")
			}
			if kind == "network" {
				args = append(args, "--no-trunc")
			}
			out, e := docker(ctx, args...)
			if e != nil {
				t.Fatal("inventory", e)
			}
			values[kind] = strings.Fields(string(out))
			sort.Strings(values[kind])
		}
		b, _ := json.Marshal(values)
		return wire.Digest(b)
	}
	before := inventory()
	var random [32]byte
	if _, e = rand.Read(random[:]); e != nil {
		t.Fatal(e)
	}
	owner := engine.Ownership{ScopeID: hex.EncodeToString(random[:16]), OperationID: hex.EncodeToString(random[16:]), Role: "postgres"}
	model := Model{Services: map[string]Service{"postgres": {Image: id, User: "999:999", MemoryBytes: 64 << 20, MilliCPU: 100, PIDs: 16, Restart: "no", ReadOnly: true, Mounts: []Mount{{Type: "volume", Source: "postgres", Target: target}}}}, Volumes: []string{"postgres"}}
	scope := RenderContext{ScopeID: owner.ScopeID, ScopeRoot: root, AllowedImages: map[string]bool{id: true}}
	output := os.Getenv("PROJECT_ONE_TEST_POSTGRES_VOLUME_EVIDENCE")
	intent, e := os.OpenFile(output+".intent.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if json.NewEncoder(intent).Encode(map[string]any{"owner": owner, "image": id, "model": model, "status": "before_any_disposable_effect"}) != nil || intent.Sync() != nil || intent.Close() != nil {
		t.Fatal("durable intent")
	}
	volumeCreated := false
	cleaned := false
	cleanup := func() error {
		cleanupContext, done := context.WithTimeout(context.Background(), 25*time.Second)
		defer done()
		resources, e := worker.ObserveRuntime(cleanupContext, client, scope, model)
		if e != nil {
			return e
		}
		for _, resource := range resources.Services {
			if resource.State != "created" {
				return failure("POSTGRES_COVERAGE_TEST_RUNNING_OR_CHANGED_RESOURCE_PRESERVED")
			}
			if _, e = docker(cleanupContext, "container", "rm", resource.ID); e != nil {
				return failure("POSTGRES_COVERAGE_TEST_CONTAINER_CLEANUP_FAILED")
			}
		}
		if resources.NetworkID != "" {
			if _, e = docker(cleanupContext, "network", "rm", resources.NetworkID); e != nil {
				return failure("POSTGRES_COVERAGE_TEST_NETWORK_CLEANUP_FAILED")
			}
		}
		if volumeCreated {
			if _, found, e := client.InspectVolume(cleanupContext, owner); e != nil || !found {
				return failure("POSTGRES_COVERAGE_TEST_VOLUME_CONTEXT_CHANGED")
			}
			if e = client.VerifyVolumeConsumers(cleanupContext, owner, nil); e != nil {
				return e
			}
			if _, e = docker(cleanupContext, "volume", "rm", owner.VolumeName()); e != nil {
				return failure("POSTGRES_COVERAGE_TEST_VOLUME_CLEANUP_FAILED")
			}
			volumeCreated = false
		}
		cleaned = true
		return nil
	}
	t.Cleanup(func() {
		if !cleaned {
			if e := cleanup(); e != nil {
				t.Error(e)
			}
		}
	})
	if _, e = client.CreateVolume(ctx, owner); e != nil {
		_, volumeCreated, _ = client.InspectVolume(context.Background(), owner)
		t.Fatal(e)
	}
	volumeCreated = true
	created, e := worker.CreateRuntime(ctx, client, scope, model)
	if e != nil || created.Status != "observed_complete_compose_resources" {
		t.Fatal("actual declared-volume creation", created, e)
	}
	observed, found, e := client.InspectRuntime(ctx, owner.ScopeID, "postgres")
	if e != nil || !found || observed.State.Status != "created" || observed.State.Running || observed.State.PID != 0 || observed.State.StartedAt != "0001-01-01T00:00:00Z" || len(observed.Mounts) != 1 || observed.Mounts[0].Name != owner.VolumeName() || !observed.HostConfig.Mounts[0].VolumeOptions.NoCopy {
		t.Fatal("actual named/no-copy/never-started proof", e)
	}
	if _, e = worker.CreateRuntime(ctx, client, scope, model); e == nil {
		t.Fatal("existing PostgreSQL container recreated")
	}
	if e = cleanup(); e != nil {
		t.Fatal(e)
	}
	if after := inventory(); after != before {
		t.Fatal("anonymous volume or other resource leak")
	}
	if retained, found, e := client.Inspect(ctx, id); e != nil || !found || retained.ID != image.ID {
		t.Fatal("shared approved image changed", e)
	}
	f, e := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if json.NewEncoder(f).Encode(map[string]any{"status": "passed_actual_approved_amd64_postgres_exact_named_volume_creation", "engine": metadata, "image_id": id, "owner": owner, "resources": created, "container_started": false, "volume_nocopy": true, "exact_final_inventory_unchanged": true, "shared_approved_image_retained": true, "qualification": "never_started_runtime_creation_only_not_database_version_SQL_extension_or_native_qualification"}) != nil || f.Sync() != nil {
		t.Fatal("result evidence")
	}
}
