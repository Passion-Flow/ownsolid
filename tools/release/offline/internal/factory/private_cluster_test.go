package factory

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestPrivateClusterFactoryRefusesOldSchemaAndMissingTargetBeforePackaging(t *testing.T) {
	for _, kind := range []string{"schema26", "missing-target", "local-only", "wrong-kind"} {
		t.Run(kind, func(t *testing.T) {
			c, m, _ := setup(t)
			m.Profile = wire.PlatformProfile
			m.SchemaVersion, m.MaximumPredecessor = wire.PrivateClusterSchemaVersion, wire.PrivateClusterSchemaVersion
			m.Prerequisites = []string{"Synthetic negative metadata only; no real M28 release assets"}
			m.Capabilities = nil
			for i := 1; i <= 19; i++ {
				m.Capabilities = append(m.Capabilities, fmt.Sprintf("M%02d", i))
			}
			m.Capabilities = append(m.Capabilities, wire.PrivateClusterCapability)
			m.Assets = nil
			for id, assetKind := range m.RequiredAssets() {
				// Explicit synthetic metadata reaches only the early refusal; no
				// image/trust/license bytes or successful archive are manufactured.
				a := wire.Asset{ID: id, Path: id, Kind: assetKind, Size: 1, SHA256: strings.Repeat("a", 64), Licenses: []string{}}
				if assetKind != "license" {
					a.Licenses = []string{"platform-license"}
				}
				if assetKind == "image_archive" {
					a.ImageID, a.ImageConfigDigest, a.Architecture = "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64), "amd64"
				}
				if id == wire.PrivateClusterProbeAMD64AssetID {
					switch kind {
					case "missing-target":
						continue
					case "local-only":
						a.ID, a.Architecture = wire.PrivateClusterProbeARM64AssetID, "arm64"
					case "wrong-kind":
						a.Kind, a.ImageID, a.ImageConfigDigest, a.Architecture = "operator", "", "", ""
					}
				}
				m.Assets = append(m.Assets, a)
			}
			expected, issues := "RELEASE_REQUIRED_ASSET_MISSING", 1
			if kind == "schema26" {
				m.SchemaVersion, m.MaximumPredecessor = 26, 26
				expected, issues = "RELEASE_PRIVATE_CLUSTER_SCHEMA_REQUIRED", 0
			}
			put(t, c.ManifestFile, Encode(m))
			validation, e := Validate(context.Background(), c)
			if e == nil || e.Error() != expected || len(validation.Issues) != issues {
				t.Fatal("incomplete release not refused", e, validation.Issues)
			}
			if issues == 1 && (validation.Issues[0].AssetID != wire.PrivateClusterProbeAMD64AssetID || validation.Issues[0].Code != "RELEASE_REQUIRED_ASSET_MISSING_OR_WRONG_KIND") {
				t.Fatal("missing target asset identity hidden", validation.Issues)
			}
			if _, e = Build(context.Background(), c); e == nil || e.Error() != expected {
				t.Fatal("invalid release packaged", e)
			}
			if _, e = os.Lstat(c.OutputFile); !os.IsNotExist(e) {
				t.Fatal("invalid release created a final archive")
			}
		})
	}
}
