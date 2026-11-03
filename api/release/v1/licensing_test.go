package release

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func licenseReleaseManifest(current bool) Manifest {
	m := fixtureManifest()
	m.Profile = PlatformProfile
	m.Prerequisites = []string{"Explicit isolated target and complete declared assets required"}
	m.Assets = []Asset{{ID: "platform-license", Path: "platform-license", Kind: "license", Size: 1, SHA256: strings.Repeat("b", 64)}}
	last := 16
	if current {
		m.SchemaVersion = LicenseSchemaVersion
		m.MaximumPredecessor = LicenseSchemaVersion
		last = 19
	}
	m.Capabilities = nil
	for i := 1; i <= last; i++ {
		m.Capabilities = append(m.Capabilities, fmt.Sprintf("M%02d", i))
	}
	required := RequiredAssets(m.Profile)
	if current {
		required[LicenseTrustAssetID] = LicenseTrustAssetKind
		required[LicenseValidationAssetID] = "operator"
	}
	ids := []string{}
	for id := range required {
		if id != "platform-license" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := Asset{ID: id, Path: id, Kind: required[id], Size: 1, SHA256: strings.Repeat("c", 64), Licenses: []string{"platform-license"}}
		if a.Kind == "image_archive" {
			a.ImageID = "sha256:" + strings.Repeat("d", 64)
			a.ImageConfigDigest = "sha256:" + strings.Repeat("e", 64)
			a.Architecture = "amd64"
		}
		m.Assets = append(m.Assets, a)
	}
	return m
}

func TestM19ReleasePreservesLegacyAndRequiresCumulativeLicenseAssets(t *testing.T) {
	for _, current := range []bool{false, true} {
		if e := licenseReleaseManifest(current).Validate(100, 100, 10000, false); e != nil {
			t.Fatal("valid declared inventory", current, e)
		}
	}
	for _, id := range []string{LicenseTrustAssetID, LicenseValidationAssetID} {
		t.Run("missing-"+id, func(t *testing.T) {
			m := licenseReleaseManifest(true)
			for i, a := range m.Assets {
				if a.ID == id {
					m.Assets = append(m.Assets[:i], m.Assets[i+1:]...)
					break
				}
			}
			if e := m.Validate(100, 100, 10000, false); e == nil || e.Error() != "RELEASE_REQUIRED_ASSET_MISSING" {
				t.Fatal("missing current asset admitted", e)
			}
		})
	}
	for name, change := range map[string]func(*Manifest){
		"schema16":    func(m *Manifest) { m.SchemaVersion = 16; m.MaximumPredecessor = 16 },
		"missing-M17": func(m *Manifest) { m.Capabilities = append(m.Capabilities[:16], m.Capabilities[17:]...) },
		"wrong-trust-kind": func(m *Manifest) {
			for i := range m.Assets {
				if m.Assets[i].ID == LicenseTrustAssetID {
					m.Assets[i].Kind = "config_example"
				}
			}
		},
		"wrong-validation-kind": func(m *Manifest) {
			for i := range m.Assets {
				if m.Assets[i].ID == LicenseValidationAssetID {
					m.Assets[i].Kind = "runbook"
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := licenseReleaseManifest(true)
			change(&m)
			if m.Validate(100, 100, 10000, false) == nil {
				t.Fatal("incomplete current release admitted")
			}
		})
	}
}
