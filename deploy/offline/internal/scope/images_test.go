package scope

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This is an explicit test-only Engine double. Real Unix/CLI coverage is separately tagged.
type imageDouble struct {
	observation engine.Observation
	image       engine.Image
	found       bool
	loads       int
	fault       string
	cancel      context.CancelFunc
}

func (d *imageDouble) Preflight(context.Context) (engine.Observation, error) {
	return d.observation, nil
}
func (d *imageDouble) Inspect(context.Context, string) (engine.Image, bool, error) {
	return d.image, d.found, nil
}
func (d *imageDouble) LoadTagless(_ context.Context, r io.Reader) error {
	d.loads++
	if d.fault == "early-return" {
		return nil
	}
	if _, e := io.Copy(io.Discard, r); e != nil {
		return e
	}
	if d.fault == "absent" {
		return errors.New("EXPLICIT_TEST_STREAM_FAILURE")
	}
	d.found = true
	if d.fault == "after-effect" {
		return errors.New("EXPLICIT_TEST_RESPONSE_LOST")
	}
	if d.fault == "drift" {
		d.observation.EngineSHA256 = strings.Repeat("e", 64)
	}
	if d.fault == "cancel" {
		d.cancel()
	}
	return nil
}
func syntheticImage(t *testing.T) (wire.Asset, []byte, string) {
	t.Helper()
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	value := []byte("explicit synthetic layer bytes")
	if tw.WriteHeader(&tar.Header{Name: "fixture", Size: int64(len(value)), Mode: 0644, Typeflag: tar.TypeReg}) != nil {
		t.Fatal("layer header")
	}
	tw.Write(value)
	tw.Close()
	diff := "sha256:" + wire.Digest(layer.Bytes())
	config := encode(map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"type": "layers", "diff_ids": []string{diff}}})
	configID := "sha256:" + wire.Digest(config)
	descriptor := func(media, digest string, size int) map[string]any {
		return map[string]any{"mediaType": media, "digest": digest, "size": size}
	}
	manifest := encode(map[string]any{"schemaVersion": 2, "config": descriptor("application/vnd.oci.image.config.v1+json", configID, len(config)), "layers": []any{descriptor("application/vnd.oci.image.layer.v1.tar", "sha256:"+wire.Digest(layer.Bytes()), layer.Len())}})
	manifestID := "sha256:" + wire.Digest(manifest)
	d := descriptor("application/vnd.oci.image.manifest.v1+json", manifestID, len(manifest))
	d["platform"] = map[string]string{"os": "linux", "architecture": "amd64"}
	d["annotations"] = map[string]string{"io.containerd.image.name": "synthetic/foreign:preserve"}
	index := encode(map[string]any{"schemaVersion": 2, "manifests": []any{d}})
	var output bytes.Buffer
	archive := tar.NewWriter(&output)
	for _, file := range []struct {
		name string
		raw  []byte
	}{{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)}, {"index.json", index}, {"blobs/sha256/" + wire.Digest(config), config}, {"blobs/sha256/" + wire.Digest(manifest), manifest}, {"blobs/sha256/" + wire.Digest(layer.Bytes()), layer.Bytes()}} {
		if archive.WriteHeader(&tar.Header{Name: file.name, Size: int64(len(file.raw)), Typeflag: tar.TypeReg, Mode: 0644, Format: tar.FormatUSTAR}) != nil {
			t.Fatal("image header")
		}
		archive.Write(file.raw)
	}
	archive.Close()
	a := wire.Asset{ID: "image", Path: "images/synthetic.tar", Kind: "image_archive", Size: int64(output.Len()), SHA256: wire.Digest(output.Bytes()), ImageID: manifestID, ImageConfigDigest: configID, Architecture: "amd64", Licenses: []string{"license"}}
	return a, output.Bytes(), diff
}
func imageFixture(t *testing.T) (configuration.Config, *imageDouble) {
	return imageFixtureConfigured(t, nil)
}
func imageFixtureConfigured(t *testing.T, configure func(*configuration.Config)) (configuration.Config, *imageDouble) {
	t.Helper()
	a, raw, diff := syntheticImage(t)
	c := fixtureWithAssets(t, []wire.Asset{a}, [][]byte{raw})
	c.Engine = &configuration.Engine{SocketFile: "/explicit/test-only/engine.sock", Profile: "local-macos-amd64-emulation", DialSeconds: 2, RequestSeconds: 5, LoadSeconds: 30, ResponseBytes: 65536, MaximumExpandedImageBytes: 64 << 20}
	if configure != nil {
		configure(&c)
	}
	if _, e := Create(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	d := &imageDouble{observation: engine.Observation{Status: "passed_local_engine_protocol_preflight_only", Profile: c.Engine.Profile, EngineSHA256: strings.Repeat("a", 64), EndpointSHA256: strings.Repeat("b", 64), EngineVersion: "29.8.2", APIVersion: "1.49", HostArchitecture: "arm64", DeclaredPlatform: "linux/amd64", Qualification: engine.LocalQualification}}
	d.image.ID = a.ImageID
	d.image.OS = "linux"
	d.image.Architecture = "amd64"
	d.image.RootFS.Type = "layers"
	d.image.RootFS.Layers = []string{diff}
	return c, d
}
func TestScopedImagesPersistReuseAndReadonlyStatus(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{true: "preexisting", false: "import"}[existing], func(t *testing.T) {
			c, d := imageFixture(t)
			d.found = existing
			s, e := images(context.Background(), c, d, false)
			if e != nil || s.ImagesRecorded != 1 || s.ImagesPending != 0 || s.Events != 6 || s.Status != "isolated_scope_images_recorded" {
				t.Fatal(s, e)
			}
			if d.loads != map[bool]int{true: 0, false: 1}[existing] {
				t.Fatal("unexpected load", d.loads)
			}
			before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
			if _, e = images(context.Background(), c, d, false); e != nil {
				t.Fatal(e)
			}
			after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
			if !bytes.Equal(before, after) {
				t.Fatal("successful repeat wrote/retried")
			}
			if s, e = Read(context.Background(), c); e != nil || s.ImagesRecorded != 1 || !strings.Contains(s.Qualification, "not_live_engine") {
				t.Fatal(s, e)
			}
			d.found = false
			if _, e = images(context.Background(), c, d, false); e == nil || d.loads > 1 {
				t.Fatal("missing recorded image blindly reloaded", e, d.loads)
			}
		})
	}
}
func TestScopedImportUncertainRequiresExplicitReconciliation(t *testing.T) {
	for _, fault := range []string{"after-effect", "absent", "early-return", "drift", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			c, d := imageFixture(t)
			d.fault = fault
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d.cancel = cancel
			s, e := images(ctx, c, d, false)
			if e == nil || s.ImagesPending != 1 || d.loads != 1 {
				t.Fatal(s, e, d.loads)
			}
			if _, e = images(context.Background(), c, d, false); e == nil || d.loads != 1 {
				t.Fatal("uncertain load retried", e)
			}
			if _, e = Read(context.Background(), c); e != nil {
				t.Fatal(e)
			}
			if fault == "drift" {
				if _, e = images(context.Background(), c, d, true); e == nil {
					t.Fatal("different engine adopted")
				}
				d.observation.EngineSHA256 = strings.Repeat("a", 64)
			}
			if !d.found {
				s, e = images(context.Background(), c, d, true)
				if e == nil || s.ImagesPending != 1 || s.ImagesRecorded != 0 || d.loads != 1 {
					t.Fatal("absence falsely resolved", s, e)
				}
				d.found = true // Explicit test-only later observed outcome, not a product resend.
			}
			s, e = images(context.Background(), c, d, true)
			if e != nil || s.ImagesPending != 0 || s.ImagesRecorded != 1 || d.loads != 1 {
				t.Fatal(s, e, d.loads)
			}
			if _, e = images(context.Background(), c, d, false); e != nil || d.loads != 1 {
				t.Fatal(e, d.loads)
			}
		})
	}
}
func TestScopedImageAdmissionAndBindingFailBeforeMutation(t *testing.T) {
	for _, fault := range []string{"wrong-rootfs", "wrong-image", "capacity", "payload", "configuration", "context", "unknown-engine"} {
		t.Run(fault, func(t *testing.T) {
			c, d := imageFixture(t)
			switch fault {
			case "wrong-rootfs":
				d.found = true
				d.image.RootFS.Layers = []string{"sha256:" + strings.Repeat("c", 64)}
			case "wrong-image":
				d.found = true
				d.image.ID = "sha256:" + strings.Repeat("d", 64)
			case "capacity":
				a, raw, _ := syntheticImage(t)
				assets := []wire.Asset{}
				for _, id := range []string{"one", "two", "three"} {
					copy := a
					copy.ID = id
					copy.Path = "images/" + id + ".tar"
					assets = append(assets, copy)
				}
				engineConfig := c.Engine
				c = fixtureWithAssets(t, assets, [][]byte{raw, raw, raw})
				c.Engine = engineConfig
				c.MaximumJournalBytes = 16384
				if _, e := Create(context.Background(), c); e != nil {
					t.Fatal(e)
				}
			case "payload":
				put(t, filepath.Join(c.ScopeRoot, "release/images/synthetic.tar"), []byte("corrupt"))
			case "configuration":
				c.Engine.LoadSeconds++
			case "unknown-engine":
				d.observation.EngineVersion = "unknown"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if fault == "context" {
				cancel()
			}
			_, e := images(ctx, c, d, false)
			if e == nil || d.loads != 0 {
				t.Fatal("unsafe admission mutated", e, d.loads)
			}
		})
	}
}

func TestScopeRejectsForgedImageTransitionsEvenWithRehashedJournal(t *testing.T) {
	for _, fault := range []string{"reference", "record", "terminal", "engine-rebind", "duplicate", "asset"} {
		t.Run(fault, func(t *testing.T) {
			c, d := imageFixture(t)
			d.fault = "after-effect"
			if _, e := images(context.Background(), c, d, false); e == nil {
				t.Fatal("expected uncertainty")
			}
			l, e := open(c, true)
			if e != nil {
				t.Fatal(e)
			}
			events := append([]Event(nil), l.events...)
			l.close()
			switch fault {
			case "reference":
				events[5].Action = "image_reconcile"
				events[5].ReferenceOperationID = strings.Repeat("f", 32)
				events[5].Outcome = "completed"
				events[5].Code = ""
			case "record":
				copy := *events[5].Image
				copy.TransportSHA256 = strings.Repeat("d", 64)
				events[5].Image = &copy
			case "terminal":
				events[5].Outcome = "failed"
			case "engine-rebind":
				events[5].Action = "engine_bind"
				events[5].Engine = events[3].Engine
			case "duplicate":
				events = append(events, events[4])
			case "asset":
				for i := 4; i < len(events); i++ {
					copy := *events[i].Image
					copy.AssetID = "unlisted"
					events[i].Image = &copy
				}
			}
			var raw []byte
			previous := ""
			for i, event := range events {
				event.Sequence = i + 1
				event.PreviousSHA256 = previous
				line := encode(event)
				previous = wire.Digest(bytes.TrimSuffix(line, []byte("\n")))
				raw = append(raw, line...)
			}
			put(t, filepath.Join(c.ScopeRoot, journalName), raw)
			put(t, filepath.Join(c.ScopeRoot, headName), encode(journalHead{FormatVersion: 1, ScopeID: events[0].ScopeID, ConfigurationSHA256: c.Fingerprint(), Sequence: len(events), EventSHA256: previous, JournalBytes: int64(len(raw))}))
			if _, e := Read(context.Background(), c); e == nil {
				t.Fatal("forged transition accepted")
			}
		})
	}
}
