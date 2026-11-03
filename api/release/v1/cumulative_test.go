package release

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Deliberately one-byte declaration doubles: these tests validate only the wire
// contract; they do not claim actual images, locale resources or a product bundle.
func cumulativeManifestDouble() Manifest {
	m := fixtureManifest()
	m.Profile = CumulativeCPUProfile
	m.SchemaVersion = 22
	m.MinimumPredecessor = 22
	m.MaximumPredecessor = 22
	m.Prerequisites = []string{"Explicit installed local Engine/Compose and private DNS/NTP/CA; no runtime downloads"}
	m.Capabilities = nil
	for i := 1; i <= 17; i++ {
		m.Capabilities = append(m.Capabilities, fmt.Sprintf("M%02d", i))
	}
	m.Cumulative = &CumulativeSelection{ApplicationProfile: "releases", SchemaLineage: "account22", SourceCommit: strings.Repeat("a", 40), ComponentVersions: map[string]string{}, LocaleSourceSHA256: strings.Repeat("b", 64), InstalledLocales: []string{"ar", "en", "fr"}, LocaleTarget: 202, PendingLocales: 199, EmbeddedSeed: "zh-CN", LocaleInventory: "locales"}
	for _, owner := range []string{"control", "gateway", "agent", "python", "postgres", "pgvector", "training", "preparation", "knowledge", "compose", "operator"} {
		m.Cumulative.ComponentVersions[owner] = "v1"
	}
	m.Cumulative.ComponentVersions["control"] = m.Cumulative.SourceCommit
	m.Cumulative.ComponentVersions["postgres"] = "18.6"
	m.Cumulative.ComponentVersions["pgvector"] = "0.8.7"
	m.Cumulative.ComponentVersions["compose"] = "5.5.1"
	required := RequiredAssets(CumulativeCPUProfile)
	ids := []string{}
	for id := range required {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	m.Assets = nil
	for _, id := range ids {
		a := Asset{ID: id, Path: id, Kind: required[id], Size: 1, SHA256: strings.Repeat("c", 64), Licenses: []string{"platform-license"}}
		if a.Kind == "license" {
			a.Licenses = nil
		}
		if a.Kind == "image_archive" {
			a.ImageID = "sha256:" + strings.Repeat("d", 64)
			a.ImageConfigDigest = "sha256:" + strings.Repeat("e", 64)
			a.Architecture = "amd64"
		}
		if id == "source-evidence" {
			a.SHA256 = m.SourceSHA256
		}
		m.Assets = append(m.Assets, a)
	}
	return m
}
func TestM17CumulativeInventoryRequiresExactSelectionAndEveryAsset(t *testing.T) {
	check := func(m Manifest) error { return m.Validate(100, 100, 10000, false) }
	if e := check(cumulativeManifestDouble()); e != nil {
		t.Fatal(e)
	}
	for _, asset := range cumulativeManifestDouble().Assets {
		t.Run("missing-"+asset.ID, func(t *testing.T) {
			m := cumulativeManifestDouble()
			for i, a := range m.Assets {
				if a.ID == asset.ID {
					m.Assets = append(m.Assets[:i], m.Assets[i+1:]...)
					break
				}
			}
			if check(m) == nil {
				t.Fatal("indispensable selected asset omitted")
			}
		})
	}
	for name, change := range map[string]func(*Manifest){
		"missing-selection": func(m *Manifest) { m.Cumulative = nil }, "future-schema": func(m *Manifest) { m.SchemaVersion = 23 }, "global-schema": func(m *Manifest) { m.Cumulative.SchemaLineage = "platform" }, "broad-runtime": func(m *Manifest) { m.Cumulative.ApplicationProfile = "platform" }, "future-capability": func(m *Manifest) { m.Capabilities = append(m.Capabilities, "M18") }, "wrong-predecessor": func(m *Manifest) { m.MinimumPredecessor = 21 }, "source-byte-mismatch": func(m *Manifest) { m.SourceSHA256 = strings.Repeat("f", 64) }, "missing-component": func(m *Manifest) { delete(m.Cumulative.ComponentVersions, "training") }, "extra-component": func(m *Manifest) { m.Cumulative.ComponentVersions["future"] = "v1" }, "wrong-pg": func(m *Manifest) { m.Cumulative.ComponentVersions["postgres"] = "18.5" }, "full202-from-partial": func(m *Manifest) { m.Cumulative.PendingLocales = 0 }, "duplicate-locale": func(m *Manifest) { m.Cumulative.InstalledLocales = []string{"en", "en", "fr"} }, "unknown-locale": func(m *Manifest) { m.Cumulative.InstalledLocales = []string{"EN", "en", "fr"} }, "invented-reviews": func(m *Manifest) { m.Cumulative.HumanApprovals = 202 }, "unqualified-architecture": func(m *Manifest) { m.Assets[0].Architecture = "arm64" },
	} {
		t.Run(name, func(t *testing.T) {
			m := cumulativeManifestDouble()
			change(&m)
			if check(m) == nil {
				t.Fatal("invalid cumulative selection admitted")
			}
		})
	}
	legacy := licenseReleaseManifest(false)
	legacy.Cumulative = cumulativeManifestDouble().Cumulative
	if check(legacy) == nil {
		t.Fatal("historical profile silently changed")
	}
}

func TestM17PredecessorWrapperRetainsDeliveredM16ProfileAndSameLedger(t *testing.T) {
	m := cumulativeManifestDouble()
	m.Profile = CumulativePredecessorCPUProfile
	m.Cumulative.ApplicationProfile = "localization"
	m.Capabilities = m.Capabilities[:16]
	if err := m.Validate(100, 100, 10000, false); err != nil {
		t.Fatal(err)
	}
	m.Cumulative.ApplicationProfile = "releases"
	if m.Validate(100, 100, 10000, false) == nil {
		t.Fatal("old M16 runtime relabeled as new release graph")
	}
	m.Cumulative.ApplicationProfile = "localization"
	m.Cumulative.ComponentVersions["control"] = "other-source"
	if m.Validate(100, 100, 10000, false) == nil {
		t.Fatal("component source version disagrees with selected source")
	}
}
