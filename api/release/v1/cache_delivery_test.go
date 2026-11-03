package release

import (
	"reflect"
	"testing"
)

// One-byte metadata declarations only; these are not manufactured runtime assets.
func cacheDeliveryDouble() Manifest {
	m := cumulativeManifestDouble()
	m.Profile = CumulativeCacheCPUProfile
	m.Cumulative.ApplicationProfile = CumulativeCacheApplicationProfile
	m.Capabilities = append(m.Capabilities, ModelCacheCapability)
	return m
}

func TestCacheCPUSelectionRequiresExactM17AndM23Account22Closure(t *testing.T) {
	check := func(m Manifest) error { return m.Validate(100, 100, 10000, false) }
	if e := check(cacheDeliveryDouble()); e != nil {
		t.Fatal(e)
	}
	for _, required := range cacheDeliveryDouble().Assets {
		t.Run("missing-"+required.ID, func(t *testing.T) {
			m := cacheDeliveryDouble()
			for i, a := range m.Assets {
				if a.ID == required.ID {
					m.Assets = append(m.Assets[:i], m.Assets[i+1:]...)
					break
				}
			}
			if check(m) == nil {
				t.Fatal("required M17 owner/asset omitted")
			}
		})
	}
	for name, change := range map[string]func(*Manifest){
		"missing-cache":   func(m *Manifest) { m.Capabilities = m.Capabilities[:17] },
		"missing-owner":   func(m *Manifest) { m.Capabilities = m.Capabilities[1:] },
		"old-runtime":     func(m *Manifest) { m.Cumulative.ApplicationProfile = "releases" },
		"broad-runtime":   func(m *Manifest) { m.Cumulative.ApplicationProfile = "platform" },
		"old-predecessor": func(m *Manifest) { m.MinimumPredecessor = 21 },
		"future-schema":   func(m *Manifest) { m.SchemaVersion = 23 },
		"extra-license":   func(m *Manifest) { m.Capabilities = append(m.Capabilities, "M19") },
		"duplicate":       func(m *Manifest) { m.Capabilities[0] = ModelCacheCapability },
		"missing-source":  func(m *Manifest) { m.Cumulative.SourceCommit = "" },
	} {
		t.Run(name, func(t *testing.T) {
			m := cacheDeliveryDouble()
			change(&m)
			if check(m) == nil {
				t.Fatal("invalid cache selection admitted")
			}
		})
	}
	for _, foreign := range []string{"M18", "M19", "M20", "M21", "M22", "M24", "M30"} {
		t.Run("foreign-"+foreign, func(t *testing.T) {
			m := cacheDeliveryDouble()
			m.Capabilities[17] = foreign
			if check(m) == nil {
				t.Fatal("cache profile activated an unrelated owner")
			}
		})
	}
}

func TestCacheSelectionCannotBroadenHistoricalProfilesOrRequiredMaps(t *testing.T) {
	before := map[string]map[string]string{}
	for _, profile := range []string{PlatformProfile, CumulativePredecessorCPUProfile, CumulativeCPUProfile, CumulativeKubernetesCPUProfile} {
		before[profile] = RequiredAssets(profile)
		m := cacheDeliveryDouble()
		m.Profile = profile
		if m.Validate(100, 100, 10000, false) == nil {
			t.Fatal("new runtime silently admitted by old profile", profile)
		}
	}
	required := RequiredAssets(CumulativeCacheCPUProfile)
	if !reflect.DeepEqual(required, RequiredAssets(CumulativeCPUProfile)) || required[KubernetesChartAssetID] != "" || required["docker-engine"] != "" {
		t.Fatal("cache CPU profile changed delivery prerequisites")
	}
	delete(required, "control-image")
	if RequiredAssets(CumulativeCacheCPUProfile)["control-image"] != "image_archive" {
		t.Fatal("shared cache asset requirements were mutated")
	}
	for profile, expected := range before {
		if !reflect.DeepEqual(expected, RequiredAssets(profile)) {
			t.Fatal("historical requirement map changed", profile)
		}
	}
}
