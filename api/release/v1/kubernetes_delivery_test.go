package release

import (
	"reflect"
	"testing"
)

// Metadata doubles only; real chart/ELF/image/license bytes require the factory
// and independent consumer checks. No fixture flag admits this production profile.
func kubernetesDeliveryDouble() Manifest {
	m := cumulativeManifestDouble()
	m.Profile = CumulativeKubernetesCPUProfile
	m.MinimumPredecessor = 21
	m.Cumulative.ApplicationProfile = "localization"
	m.Capabilities = append(m.Capabilities, "M18")
	for id, kind := range kubernetesRequiredAssets() {
		found := false
		for _, asset := range m.Assets {
			found = found || asset.ID == id
		}
		if !found {
			m.Assets = append(m.Assets, Asset{ID: id, Path: "kubernetes/" + id, Kind: kind, Size: 1, SHA256: m.SourceSHA256, Licenses: []string{"platform-license"}})
		}
	}
	return m
}

func TestPreparedKubernetesProductionProfileRequiresCompleteCumulativeAndTools(t *testing.T) {
	check := func(m Manifest) error { return m.Validate(100, 100, 10000, false) }
	if err := check(kubernetesDeliveryDouble()); err != nil {
		t.Fatal(err)
	}
	for _, asset := range kubernetesDeliveryDouble().Assets {
		t.Run("missing-"+asset.ID, func(t *testing.T) {
			m := kubernetesDeliveryDouble()
			for index, current := range m.Assets {
				if current.ID == asset.ID {
					m.Assets = append(m.Assets[:index], m.Assets[index+1:]...)
					break
				}
			}
			if check(m) == nil {
				t.Fatal("required cumulative/tool asset omitted")
			}
		})
	}
	for name, change := range map[string]func(*Manifest){
		"missing-selection":  func(m *Manifest) { m.Cumulative = nil },
		"wrong-runtime":      func(m *Manifest) { m.Cumulative.ApplicationProfile = "releases" },
		"wrong-schema":       func(m *Manifest) { m.SchemaVersion = 23 },
		"wrong-lineage":      func(m *Manifest) { m.Cumulative.SchemaLineage = "account21" },
		"wrong-predecessor":  func(m *Manifest) { m.MinimumPredecessor = 20 },
		"missing-capability": func(m *Manifest) { m.Capabilities = m.Capabilities[:17] },
		"future-capability":  func(m *Manifest) { m.Capabilities = append(m.Capabilities, "M19") },
		"image-architecture": func(m *Manifest) {
			for index := range m.Assets {
				if m.Assets[index].ID == "control-image" {
					m.Assets[index].Architecture = "arm64"
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := kubernetesDeliveryDouble()
			change(&m)
			if check(m) == nil {
				t.Fatal("invalid prepared-cluster production declaration admitted")
			}
		})
	}
}

func TestPreparedKubernetesRequirementsCannotRelaxHistoricalProfiles(t *testing.T) {
	legacy := RequiredAssets(PlatformProfile)
	prepared := RequiredAssets(CumulativeKubernetesCPUProfile)
	if legacy["docker-engine"] != "deb" || prepared["docker-engine"] != "" || prepared[KubernetesChartAssetID] != "operator" {
		t.Fatal("prepared-cluster and host-installer requirements conflated")
	}
	delete(prepared, "control-image")
	if RequiredAssets(CumulativeKubernetesCPUProfile)["control-image"] != "image_archive" || !reflect.DeepEqual(legacy, RequiredAssets(PlatformProfile)) {
		t.Fatal("caller mutated another requirement map")
	}
	for _, profile := range []string{CumulativeCPUProfile, CumulativePredecessorCPUProfile, PlatformProfile} {
		if RequiredAssets(profile)[KubernetesChartAssetID] != "" {
			t.Fatal("legacy profile relabeled")
		}
	}
	m := cumulativeManifestDouble()
	m.Capabilities = append(m.Capabilities, "M18")
	if m.Validate(100, 100, 10000, false) == nil {
		t.Fatal("M17 profile silently expanded to M18")
	}
}
