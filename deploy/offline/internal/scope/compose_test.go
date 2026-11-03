package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// The renderer/engine here are explicit doubles; the tagged CLI uses real tools.
type renderDouble struct {
	calls int
	scope compose.RenderContext
}

func (r *renderDouble) Render(_ context.Context, s compose.RenderContext, m compose.Model) (compose.Rendered, error) {
	r.calls++
	r.scope = s
	return compose.Rendered{Status: "passed_constrained_Compose_render_only", Services: len(m.Services), Document: []byte("explicit-test-rendered-document"), DocumentSHA256: wire.Digest([]byte("explicit-test-rendered-document"))}, nil
}
func planFixture(t *testing.T) (configuration.Config, *imageDouble, *renderDouble) {
	return planFixtureConfigured(t, nil)
}
func planFixtureConfigured(t *testing.T, configure func(*configuration.Config)) (configuration.Config, *imageDouble, *renderDouble) {
	t.Helper()
	c, d := imageFixtureConfigured(t, func(c *configuration.Config) {
		c.Compose = &configuration.Compose{DockerBinary: "/explicit/test/docker", PluginDirectory: "/explicit/test/plugins", CommandSeconds: 7, OutputBytes: 8192, ModelFile: filepath.Join(filepath.Dir(c.ScopeRoot), "model.json")}
		if configure != nil {
			configure(c)
		}
	})
	model := compose.Model{Services: map[string]compose.Service{"control": {Image: d.image.ID, User: "10033:10034", MemoryBytes: 64 << 20, MilliCPU: 250, PIDs: 32, Restart: "no", ReadOnly: true}}}
	raw, _ := json.Marshal(model)
	put(t, c.Compose.ModelFile, raw)
	if _, e := images(context.Background(), c, d, false); e != nil {
		t.Fatal(e)
	}
	return c, d, &renderDouble{}
}
func TestScopeComposePlanBindsCurrentImagesAndPreservesJournal(t *testing.T) {
	c, d, r := planFixture(t)
	before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	result, e := planCompose(context.Background(), c, d, r)
	if e != nil || result.ScopeID != r.scope.ScopeID || result.Rendered.Services != 1 || !wire.ValidSHA256(result.ModelSHA256) || r.scope.ScopeRoot != c.ScopeRoot || !r.scope.AllowedImages[d.image.ID] || d.loads != 1 {
		t.Fatal(result, e)
	}
	after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(before, after) {
		t.Fatal("readonly plan modified ledger")
	}
	raw, _ := json.Marshal(result)
	if bytes.Contains(raw, []byte("explicit-test-rendered-document")) {
		t.Fatal("private document leaked into output")
	}
}
func TestScopeComposePlanRefusesUnsafeInputsAndUncertainImages(t *testing.T) {
	for _, fault := range []string{"missing-image", "wrong-rootfs", "engine-drift", "configuration", "symlink-model", "hardlink-model", "extra-field", "oversize", "cancel", "busy", "pending"} {
		t.Run(fault, func(t *testing.T) {
			c, d, r := planFixture(t)
			switch fault {
			case "missing-image":
				d.found = false
			case "wrong-rootfs":
				d.image.RootFS.Layers = nil
			case "engine-drift":
				d.observation.EngineSHA256 = wire.Digest([]byte("another-engine"))
			case "configuration":
				c.Compose.CommandSeconds++
			case "symlink-model":
				original := c.Compose.ModelFile + "-preserved"
				os.Rename(c.Compose.ModelFile, original)
				os.Symlink(original, c.Compose.ModelFile)
			case "hardlink-model":
				os.Link(c.Compose.ModelFile, c.Compose.ModelFile+"-link")
			case "extra-field":
				put(t, c.Compose.ModelFile, []byte(`{"include":"https://invalid.example/unsafe"}`))
			case "oversize":
				put(t, c.Compose.ModelFile, bytes.Repeat([]byte("x"), int(c.Verification.MaximumMetadataBytes+1)))
			case "pending":
				// Preserve a genuine validated started intent, simulating process loss before its terminal.
				l, e := open(c, true)
				if e != nil {
					t.Fatal(e)
				}
				events := l.events
				last := events[len(events)-2]
				l.close()
				var raw []byte
				for _, event := range events[:len(events)-1] {
					raw = append(raw, encode(event)...)
				}
				put(t, filepath.Join(c.ScopeRoot, journalName), raw)
				put(t, filepath.Join(c.ScopeRoot, headName), encode(journalHead{FormatVersion: 1, ScopeID: last.ScopeID, ConfigurationSHA256: c.Fingerprint(), Sequence: last.Sequence, EventSHA256: wire.Digest(bytes.TrimSuffix(encode(last), []byte("\n"))), JournalBytes: int64(len(raw))}))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if fault == "cancel" {
				cancel()
			}
			if fault == "busy" {
				held, e := open(c, true)
				if e != nil {
					t.Fatal(e)
				}
				defer held.close()
			}
			_, e := planCompose(ctx, c, d, r)
			if e == nil || r.calls != 0 {
				t.Fatal("unsafe scope reached renderer", e, r.calls)
			}
		})
	}
}
