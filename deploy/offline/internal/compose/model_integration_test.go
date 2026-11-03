//go:build offlineintegration

package compose

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

func TestActualPinnedComposeConstrainedRender(t *testing.T) {
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_COMPOSE_CONFIG"))
	if e != nil {
		t.Fatal("explicit protected config required", e)
	}
	env, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_COMPOSE_ENV"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, env)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, e = Preflight(ctx, c); e != nil {
		t.Fatal(e)
	}
	client, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("scope root")
	}
	// Rendering does not load/inspect/run this exact retained synthetic manifest.
	id := os.Getenv("PROJECT_ONE_TEST_IMAGE_MANIFEST")
	model := Model{Services: map[string]Service{"control": {Image: id, User: "10033:10034", MemoryBytes: 64 << 20, MilliCPU: 250, PIDs: 32, Restart: "no", ReadOnly: true, Ports: []Port{{HostIP: "127.0.0.1", Published: 18443, Target: 8443}, {HostIP: "::1", Published: 18444, Target: 8443}}, Tmpfs: []Tmpfs{{Target: "/tmp", SizeBytes: 1048576, UID: 10033, GID: 10034}}}}}
	scope := RenderContext{ScopeID: strings.Repeat("c", 32), ScopeRoot: root, AllowedImages: map[string]bool{id: true}}
	result, e := client.Render(ctx, scope, model)
	if e != nil || result.Services != 1 || len(result.Document) < 1 {
		t.Fatal(result, e)
	}
	var document map[string]any
	if json.Unmarshal(result.Document, &document) != nil {
		t.Fatal("canonical JSON")
	}
	service := document["services"].(map[string]any)["control"].(map[string]any)
	if service["user"] != "10033:10034" || service["read_only"] != true || service["mem_limit"] != float64(64<<20) || service["cpus"] != 0.25 {
		t.Fatal("actual model changed typed resources")
	}
	file, e := os.OpenFile(os.Getenv("PROJECT_ONE_TEST_COMPOSE_RENDER_EVIDENCE"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	if json.NewEncoder(file).Encode(map[string]any{"status": "passed_actual_pinned_constrained_Compose_render_only", "result": result, "canonical_synthetic_model": document, "container_created": false, "qualification": "Real canonical rendering; no image load/container/app/native target qualification"}) != nil || file.Sync() != nil {
		t.Fatal("evidence")
	}
}
