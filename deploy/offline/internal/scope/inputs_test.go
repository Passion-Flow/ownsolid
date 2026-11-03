package scope

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This bypasses only the separately integrated source/Engine collector, constructing
// an explicit synthetic immutable plan to exercise real protected files and journal.
func preparedInputFixture(t *testing.T) (configuration.Config, *locked, preparedInputs) {
	return preparedInputFixtureConfigured(t, nil)
}
func preparedInputFixtureConfigured(t *testing.T, configure func(*configuration.Config)) (configuration.Config, *locked, preparedInputs) {
	t.Helper()
	c, _, _ := planFixtureConfigured(t, configure)
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	model, e := os.ReadFile(c.Compose.ModelFile)
	if e != nil {
		t.Fatal(e)
	}
	catalog := encode(SourceCatalog{FormatVersion: 1, StorageProfile: namedVolumeStorage, ImageAssetID: "image", Roles: []SourceRole{{Key: "control-config", Files: []SourceFile{{Name: "control.yaml", SourceFile: "/explicit/test/source"}}}}})
	data := []byte("Explicit synthetic runtime configuration")
	files := []InputFile{{Name: "control.yaml", Bytes: int64(len(data)), SHA256: wire.Digest(data)}}
	image := imageState(l.events).completed["image"]
	d := InputsDescriptor{FormatVersion: 1, ScopeID: l.marker.ScopeID, ConfigurationSHA256: c.Fingerprint(), ModelSHA256: wire.Digest(model), CatalogSHA256: wire.Digest(catalog), ImageAssetID: "image", ImageID: image.ManifestDigest, ImageConfigDigest: image.ConfigDigest, StorageProfile: namedVolumeStorage, Roles: []InputRole{{Key: "control-config", UID: 10033, GID: 10034, Files: files, PayloadSHA256: wire.Digest(encode(files))}}}
	r := InputsRecord{DescriptorSHA256: wire.Digest(encode(d)), ModelSHA256: d.ModelSHA256, CatalogSHA256: d.CatalogSHA256, ImageAssetID: d.ImageAssetID, ImageID: d.ImageID, ImageConfigDigest: d.ImageConfigDigest}
	p := preparedInputs{descriptor: d, record: r, model: model, catalog: catalog, files: map[string][]engine.ConfigurationFile{"control-config": {{Name: "control.yaml", Contents: data}}}}
	return c, l, p
}
func TestImmutableInputSnapshotSurvivesOriginalDeletionAndRestart(t *testing.T) {
	c, l, p := preparedInputFixture(t)
	result, e := prepareInputs(context.Background(), l, p)
	l.close()
	if e != nil || result.Roles != 1 || result.Status != "prepared_immutable_storage_inputs_only" {
		t.Fatal(result, e)
	}
	os.Remove(c.Compose.ModelFile)
	before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	again, e := PrepareStorageInputs(context.Background(), c, false)
	if e != nil || again.ScopeID != result.ScopeID {
		t.Fatal(again, e)
	}
	after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(before, after) {
		t.Fatal("repeat changed immutable snapshot journal")
	}
	status, e := Read(context.Background(), c)
	if e != nil || status.Status != "isolated_scope_storage_inputs_prepared" {
		t.Fatal(status, e)
	}
}
func TestInputSnapshotCorruptionLinksModesAndUnknownContentRefused(t *testing.T) {
	for _, fault := range []string{"descriptor", "model", "payload", "extra-file", "extra-role", "missing-file", "mode", "symlink", "hardlink"} {
		t.Run(fault, func(t *testing.T) {
			c, l, p := preparedInputFixture(t)
			if _, e := prepareInputs(context.Background(), l, p); e != nil {
				l.close()
				t.Fatal(e)
			}
			l.close()
			payload := filepath.Join(c.ScopeRoot, "inputs/payloads/control-config/control.yaml")
			switch fault {
			case "descriptor":
				put(t, filepath.Join(c.ScopeRoot, "inputs/descriptor.json"), []byte("{}"))
			case "model":
				put(t, filepath.Join(c.ScopeRoot, "inputs/model.json"), []byte("{}"))
			case "payload":
				put(t, payload, []byte("corrupt"))
			case "extra-file":
				put(t, filepath.Join(c.ScopeRoot, "inputs/payloads/control-config/extra"), []byte("preserve"))
			case "extra-role":
				os.Mkdir(filepath.Join(c.ScopeRoot, "inputs/payloads/extra"), 0700)
			case "missing-file":
				os.Remove(payload)
			case "mode":
				os.Chmod(payload, 0644)
			case "symlink":
				os.Remove(payload)
				os.Symlink(filepath.Join(c.ScopeRoot, "inputs/model.json"), payload)
			case "hardlink":
				os.Link(payload, filepath.Join(filepath.Dir(c.ScopeRoot), "extra-link"))
			}
			if _, e := Read(context.Background(), c); e == nil {
				t.Fatal("unsafe snapshot accepted")
			}
			if _, e := os.Lstat(c.ScopeRoot); e != nil {
				t.Fatal("failed verification removed scope")
			}
		})
	}
}
func TestInputSnapshotStartedCompleteFilesExplicitlyReconciledWithoutReplay(t *testing.T) {
	c, l, p := preparedInputFixture(t)
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	if e = l.appendEvent(Event{OperationID: id, Action: "inputs_prepare", Outcome: "started", Inputs: &p.record}); e != nil {
		t.Fatal(e)
	}
	if e = l.snapshotInputs(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	l.close()
	if _, e = PrepareStorageInputs(context.Background(), c, false); e == nil {
		t.Fatal("unfinished snapshot silently reused")
	}
	result, e := PrepareStorageInputs(context.Background(), c, true)
	if e != nil || result.Roles != 1 {
		t.Fatal(result, e)
	}
	status, e := Read(context.Background(), c)
	if e != nil || status.Status != "isolated_scope_storage_inputs_prepared" {
		t.Fatal(status, e)
	}
	journal, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Contains(journal, []byte(`"action":"inputs_reconcile"`)) {
		t.Fatal("resolution not audited")
	}
}
func TestInputSnapshotPartialPreservedAndCancelledBeforeIntent(t *testing.T) {
	for _, fault := range []string{"partial", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			c, l, p := preparedInputFixture(t)
			if fault == "cancelled" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				before := len(l.events)
				if _, e := prepareInputs(ctx, l, p); e == nil || len(l.events) != before {
					t.Fatal("cancelled operation wrote an intent")
				}
				l.close()
				return
			}
			id, _ := nonce()
			if e := l.appendEvent(Event{OperationID: id, Action: "inputs_prepare", Outcome: "started", Inputs: &p.record}); e != nil {
				t.Fatal(e)
			}
			l.root.Mkdir(inputsDirectory, 0700)
			writeNew(l.root, "inputs/descriptor.json", p.descriptorBytes())
			l.close()
			if _, e := PrepareStorageInputs(context.Background(), c, true); e == nil {
				t.Fatal("partial snapshot completed")
			}
			if _, e := os.Stat(filepath.Join(c.ScopeRoot, "inputs/descriptor.json")); e != nil {
				t.Fatal("partial snapshot was destroyed")
			}
		})
	}
}
