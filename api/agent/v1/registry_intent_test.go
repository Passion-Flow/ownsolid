package v1

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRegistryDeployIntentShapeAndSchema(t *testing.T) {
	c := InferenceCommand{Action: "deploy", DeploymentID: "12345678-1234-1234-1234-123456789abc", ModelID: "22345678-1234-1234-1234-123456789abc", CatalogDigest: ModelCatalogDigest(), GPUUUID: "GPU-12345678-1234-1234-1234-123456789abc", Revision: 1}
	if c.Validate(c.Operation()) != nil {
		t.Fatal("ordinary command changed")
	}
	c.RegistryPinID = "32345678-1234-1234-1234-123456789abc"
	if c.Validate(c.Operation()) != nil {
		t.Fatal("pinned deploy refused")
	}
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	var roundtrip InferenceCommand
	if json.Unmarshal(raw, &roundtrip) != nil || roundtrip.RegistryPinID != c.RegistryPinID {
		t.Fatal("pin not persisted")
	}
	for _, value := range []any{nil, "", false, "invalid"} {
		var object map[string]any
		json.Unmarshal(raw, &object)
		object["image_registry_pin_id"] = value
		changed, _ := json.Marshal(object)
		if json.Unmarshal(changed, &roundtrip) == nil {
			t.Fatal("invalid explicit pin accepted", value)
		}
	}
	c.Action = "stop"
	if c.Validate(c.Operation()) == nil {
		t.Fatal("cleanup acquired image-loading intent")
	}
	var schema struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	data, e := os.ReadFile("schema.json")
	if e != nil || json.Unmarshal(data, &schema) != nil {
		t.Fatal("schema unreadable", e)
	}
	var derived struct {
		Properties map[string]any `json:"properties"`
	}
	if json.Unmarshal(schema.Components.Schemas["DerivedInferenceCommand"], &derived) != nil || derived.Properties["image_registry_pin_id"] == nil {
		t.Fatal("derived schema lost marker")
	}
}

// These are structural wire facts only, not signed image/release evidence.
func TestRegistryManifestMatchesPersistedDeployPin(t *testing.T) {
	c := InferenceCommand{Action: "deploy", DeploymentID: "12345678-1234-1234-1234-123456789abc", ModelID: "22345678-1234-1234-1234-123456789abc", CatalogDigest: ModelCatalogDigest(), GPUUUID: "GPU-12345678-1234-1234-1234-123456789abc", Revision: 1, RegistryPinID: "32345678-1234-1234-1234-123456789abc"}
	r := ApprovedInferenceRecipe()
	m := RegistryManifest{ReleaseProof: json.RawMessage(`{}`), PinID: c.RegistryPinID, GrantID: "42345678-1234-1234-1234-123456789abc", DeploymentID: c.DeploymentID, Revision: c.Revision, ImageManifestDigest: r.ImageManifestDigest, ImageConfigDigest: r.ImageConfigDigest, OS: r.OS, Architecture: r.Architecture, ArchiveSHA256: strings.Repeat("a", 64), SourceArchiveSHA256: strings.Repeat("b", 64), SourceManifestSHA256: strings.Repeat("c", 64), ArchiveBytes: 1, ExpandedBytes: 1, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if e := m.Validate(c); e != nil {
		t.Fatal("valid structural intent refused", e)
	}
	m.PinID = m.GrantID
	if m.Validate(c) == nil {
		t.Fatal("different pin accepted for persisted command")
	}
}
