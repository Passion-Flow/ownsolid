//go:build offlineintegration

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

func TestActualReadonlyRuntimeResourceAdmission(t *testing.T) {
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_RESOURCE_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		t.Fatal("explicit resource profile required", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, e := New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	engineObservation, e := client.Preflight(ctx)
	if e != nil {
		t.Fatal(e)
	}
	private := t.TempDir()
	if os.Chmod(private, 0700) != nil || os.WriteFile(filepath.Join(private, "config.json"), []byte("{}\n"), 0600) != nil {
		t.Fatal("private metadata client")
	}
	inventory := func() []byte {
		values := map[string][]string{}
		for _, kind := range []string{"container", "image", "network", "volume"} {
			args := []string{"--config", private, "--host", "unix://" + c.Engine.SocketFile, kind, "ls", "--quiet"}
			if kind == "container" || kind == "image" {
				args = append(args, "--all", "--no-trunc")
			}
			if kind == "network" {
				args = append(args, "--no-trunc")
			}
			command := exec.CommandContext(ctx, c.Compose.DockerBinary, args...)
			command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "DOCKER_API_VERSION=1.49"}
			raw, e := command.Output()
			if e != nil {
				t.Fatal("metadata inventory", e)
			}
			ids := strings.Fields(string(raw))
			sort.Strings(ids)
			values[kind] = ids
		}
		raw, _ := json.Marshal(values)
		return raw
	}
	before := inventory()
	admitted, e := client.AdmitRuntimeResources(ctx, ResourceRequirement{MemoryBytes: 64 << 20, MilliCPU: 100}, c.Compose.Process.ReservePercent)
	if e != nil || admitted.Status != "passed_point_in_time_resource_admission" {
		t.Fatal("actual admission", admitted, e)
	}
	refused, e := client.AdmitRuntimeResources(ctx, ResourceRequirement{MemoryBytes: math.MaxInt64, MilliCPU: 100}, c.Compose.Process.ReservePercent)
	if e == nil || refused.Status != "resource_admission_refused" {
		t.Fatal("oversized requirement admitted")
	}
	after := inventory()
	if !bytes.Equal(before, after) {
		t.Fatal("readonly admission changed Docker inventory")
	}
	evidence := map[string]any{"status": "passed_actual_readonly_host_Engine_admission_and_oversize_refusal", "engine": engineObservation, "admission": admitted, "refused": refused, "same_inventory": true, "mutation_performed": false}
	file, e := os.OpenFile(os.Getenv("PROJECT_ONE_TEST_RESOURCE_EVIDENCE"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	if json.NewEncoder(file).Encode(evidence) != nil || file.Sync() != nil {
		t.Fatal("evidence not durable")
	}
}
