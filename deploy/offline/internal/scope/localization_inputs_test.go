package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestLocalizationConfigurationIsControlOwnedReadonlyAndClosed(t *testing.T) {
	model := compose.Model{Services: map[string]compose.Service{"control": {User: "14001:14002"}}}
	uid, gid, err := roleUser(model, localizationConfigurationRole)
	if err != nil || uid != 14001 || gid != 14002 {
		t.Fatal("locale files must use the selected runtime identity", uid, gid, err)
	}
	for _, service := range []string{"control", "postgres", "gateway", "unknown"} {
		for _, kind := range []string{"volume", "bind"} {
			for _, readonly := range []bool{false, true} {
				mount := compose.Mount{Type: kind, Source: localizationConfigurationRole, Target: "/explicit/locales", ReadOnly: readonly}
				want := service == "control" && kind == "volume" && readonly
				if serviceVolumeMountAllowed(service, mount) != want {
					t.Fatal("locale mount gained another service/type/write authority", service, kind, readonly)
				}
			}
		}
	}
	if serviceVolumeMountAllowed("control", compose.Mount{Type: "volume", Source: "unknown-config", ReadOnly: true}) {
		t.Fatal("arbitrary configuration role accepted")
	}
}

// This exercises the actual immutable filesystem snapshot and hash journal.
// Its tiny locale bytes are explicit inventory doubles, not runtime registry acceptance.
func TestLocalizationConfigurationSnapshotRetainsExactPrivateInventory(t *testing.T) {
	c, l, p := preparedInputFixture(t)
	var model compose.Model
	if json.Unmarshal(p.model, &model) != nil {
		t.Fatal("fixture model")
	}
	model.Volumes = append(model.Volumes, localizationConfigurationRole)
	p.model = encode(model)
	p.descriptor.ModelSHA256 = wire.Digest(p.model)
	p.record.ModelSHA256 = p.descriptor.ModelSHA256
	files := []engine.ConfigurationFile{{Name: "inventory.json", Contents: []byte(`{"synthetic_inventory":true}`)}, {Name: "fr.json", Contents: []byte(`{"synthetic_locale":"fr"}`)}}
	index := cumulativeIndex(files)
	uid, gid, err := roleUser(model, localizationConfigurationRole)
	if err != nil {
		t.Fatal(err)
	}
	var catalog SourceCatalog
	if json.Unmarshal(p.catalog, &catalog) != nil {
		t.Fatal("fixture catalog")
	}
	catalog.Roles = append(catalog.Roles, SourceRole{Key: localizationConfigurationRole, Files: []SourceFile{{Name: "inventory.json", SourceFile: "/explicit/test/inventory"}, {Name: "fr.json", SourceFile: "/explicit/test/fr"}}})
	p.catalog = encode(catalog)
	p.descriptor.CatalogSHA256 = wire.Digest(p.catalog)
	p.record.CatalogSHA256 = p.descriptor.CatalogSHA256
	p.files[localizationConfigurationRole] = files
	p.descriptor.Roles = append(p.descriptor.Roles, InputRole{Key: localizationConfigurationRole, UID: uid, GID: gid, Files: index, PayloadSHA256: wire.Digest(encode(index))})
	p.record.DescriptorSHA256 = wire.Digest(encode(p.descriptor))
	if _, err = prepareInputs(context.Background(), l, p); err != nil {
		l.close()
		t.Fatal(err)
	}
	got, err := l.readInputs(context.Background(), p.record)
	if err != nil || len(got.files[localizationConfigurationRole]) != len(files) {
		l.close()
		t.Fatal("exact private locale snapshot", err)
	}
	for _, want := range files {
		found := false
		for _, actual := range got.files[localizationConfigurationRole] {
			if want.Name == actual.Name {
				found = bytes.Equal(want.Contents, actual.Contents)
			}
		}
		if !found {
			l.close()
			t.Fatal("locale file bytes changed", want.Name)
		}
	}
	l.close()
	before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if err = os.Chmod(filepath.Join(c.ScopeRoot, "inputs/payloads", localizationConfigurationRole, "fr.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = Read(context.Background(), c); err == nil {
		t.Fatal("publicly readable locale snapshot accepted")
	}
	after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(before, after) {
		t.Fatal("refusal changed journal")
	}
}
