package release

import (
	"reflect"
	"strings"
	"testing"
)

// These are synthetic metadata contracts, never real OCI or release approvals.
func privateClusterReleaseManifest() Manifest {
	m := licenseReleaseManifest(true)
	m.SchemaVersion = PrivateClusterSchemaVersion
	m.MaximumPredecessor = PrivateClusterSchemaVersion
	m.Capabilities = append(m.Capabilities, PrivateClusterCapability)
	m.Assets = append(m.Assets, Asset{ID: PrivateClusterProbeAMD64AssetID, Path: "images/synthetic-probe.tar", Kind: "image_archive", Size: 1, SHA256: strings.Repeat("1", 64), ImageID: "sha256:" + strings.Repeat("2", 64), ImageConfigDigest: "sha256:" + strings.Repeat("3", 64), Architecture: "amd64", Licenses: []string{"platform-license"}})
	return m
}

func TestPrivateClusterReleaseRequiresSchemaAndPreservesLegacy(t *testing.T) {
	for _, fixture := range []bool{false, true} {
		for _, version := range []int{26, 27, 28} {
			m := privateClusterReleaseManifest()
			if fixture {
				m.Profile = FixtureProfile
			}
			m.SchemaVersion, m.MaximumPredecessor = version, version
			e := m.Validate(100, 100, 10000, fixture)
			if version < PrivateClusterSchemaVersion {
				if e == nil || e.Error() != "RELEASE_PRIVATE_CLUSTER_SCHEMA_REQUIRED" {
					t.Fatal("old M28 schema admitted", fixture, version, e)
				}
			} else if e != nil {
				t.Fatal("valid metadata refused", fixture, version, e)
			}
		}
	}
	for _, m := range []Manifest{fixtureManifest(), licenseReleaseManifest(false), licenseReleaseManifest(true)} {
		if e := m.Validate(100, 100, 10000, m.Profile == FixtureProfile); e != nil {
			t.Fatal("legacy admission changed", e)
		}
	}
}

func TestPrivateClusterReleaseRequiresTargetProbeAsset(t *testing.T) {
	for _, wrongKind := range []bool{false, true} {
		m := privateClusterReleaseManifest()
		probe := &m.Assets[len(m.Assets)-1]
		if wrongKind {
			probe.Kind = "operator"
			probe.Architecture, probe.ImageID, probe.ImageConfigDigest = "", "", ""
		} else {
			probe.ID, probe.Architecture = PrivateClusterProbeARM64AssetID, "arm64"
		}
		if e := m.Validate(100, 100, 10000, false); e == nil || e.Error() != "RELEASE_REQUIRED_ASSET_MISSING" {
			t.Fatal("missing target probe admitted", wrongKind, e)
		}
	}
	m := privateClusterReleaseManifest()
	additional := m.Assets[len(m.Assets)-1]
	additional.ID, additional.Path, additional.Architecture = PrivateClusterProbeARM64AssetID, "images/synthetic-local-probe.tar", "arm64"
	m.Assets = append(m.Assets, additional)
	if e := m.Validate(100, 100, 10000, false); e != nil {
		t.Fatal("valid extra local probe refused", e)
	}
}

func TestPrivateClusterReleaseArchitectureAndRequiredMapIsolation(t *testing.T) {
	for _, id := range []string{"control-image", "gateway-image", "agent-image", PrivateClusterProbeAMD64AssetID} {
		if validImageArchitecture(Asset{ID: id, Architecture: "arm64"}) || !validImageArchitecture(Asset{ID: id, Architecture: "amd64"}) {
			t.Fatal("original target architecture changed", id)
		}
	}
	if validImageArchitecture(Asset{ID: PrivateClusterProbeARM64AssetID, Architecture: "amd64"}) || !validImageArchitecture(Asset{ID: PrivateClusterProbeARM64AssetID, Architecture: "arm64"}) {
		t.Fatal("local role identity does not match architecture")
	}
	m := privateClusterReleaseManifest()
	before := RequiredAssets(PlatformProfile)
	actual := m.RequiredAssets()
	if actual[PrivateClusterProbeAMD64AssetID] != "image_archive" {
		t.Fatal("target role absent")
	}
	delete(actual, "control-image")
	actual["foreign"] = "operator"
	if !reflect.DeepEqual(before, RequiredAssets(PlatformProfile)) || m.RequiredAssets()["control-image"] != "image_archive" || m.RequiredAssets()["foreign"] != "" || licenseReleaseManifest(true).RequiredAssets()[PrivateClusterProbeAMD64AssetID] != "" {
		t.Fatal("mutable requirement map leaked into a different release")
	}
	for _, architecture := range []string{"arm64", "", "riscv64"} {
		m := privateClusterReleaseManifest()
		m.Assets[len(m.Assets)-1].Architecture = architecture
		if e := m.Validate(100, 100, 10000, false); e == nil || e.Error() != "RELEASE_IMAGE_IDENTITY_INVALID" {
			t.Fatal("wrong target image architecture admitted", architecture, e)
		}
	}
}
