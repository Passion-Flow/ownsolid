package release

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Only signed metadata doubles: no synthetic asset is a manufactured runtime.
func mvpManifestDouble() Manifest {
	m := fixtureManifest()
	m.Profile = MVPPostgreSQLCPUProfile
	m.SchemaVersion, m.MinimumPredecessor, m.MaximumPredecessor = MVPPostgreSQLCPUV1SchemaVersion, 22, MVPPostgreSQLCPUV1SchemaVersion
	m.Prerequisites = []string{"Explicit installed local Engine/Compose and private DNS/NTP/CA; no runtime downloads"}
	m.Capabilities = nil
	for i := 1; i <= 30; i++ {
		m.Capabilities = append(m.Capabilities, fmt.Sprintf("M%02d", i))
	}
	m.Cumulative = &CumulativeSelection{ApplicationProfile: MVPPostgreSQLCPUApplicationProfile, SchemaLineage: MVPPostgreSQLCPUApplicationProfile, SourceCommit: strings.Repeat("a", 40), ComponentVersions: map[string]string{}, LocaleSourceSHA256: strings.Repeat("b", 64), InstalledLocales: []string{"ar", "en", "fr"}, LocaleTarget: 202, PendingLocales: 199, EmbeddedSeed: "zh-CN", LocaleInventory: "locales"}
	for _, owner := range []string{"control", "gateway", "agent", "python", "postgres", "pgvector", "training", "preparation", "knowledge", "compose", "operator", "kubernetes", "private-cluster-probe"} {
		m.Cumulative.ComponentVersions[owner] = "v1"
	}
	m.Cumulative.ComponentVersions["control"] = m.Cumulative.SourceCommit
	m.Cumulative.ComponentVersions["postgres"], m.Cumulative.ComponentVersions["pgvector"], m.Cumulative.ComponentVersions["compose"] = "18.6", "0.8.7", "5.5.1"
	m.Cumulative.ComponentVersions["inference"], m.Cumulative.ComponentVersions["native-postgres"] = "0.31.0", "18.6"
	required := RequiredAssets(m.Profile)
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
		if id == MVPPostgreSQLCPULineageAssetID {
			a.SHA256 = MVPPostgreSQLCPULineageSHA256()
		}
		m.Assets = append(m.Assets, a)
	}
	return m
}

func TestMVPSelectionRequiresEveryCapabilityAssetAndExactIndependentLineage(t *testing.T) {
	check := func(m Manifest) error { return m.Validate(100, 100, 10000, false) }
	if err := check(mvpManifestDouble()); err != nil {
		t.Fatal(err)
	}
	for _, a := range mvpManifestDouble().Assets {
		t.Run("missing-"+a.ID, func(t *testing.T) {
			m := mvpManifestDouble()
			for i, asset := range m.Assets {
				if asset.ID == a.ID {
					m.Assets = append(m.Assets[:i], m.Assets[i+1:]...)
					break
				}
			}
			if check(m) == nil {
				t.Fatal("indispensable final product asset omitted")
			}
		})
	}
	for name, change := range map[string]func(*Manifest){
		"old-account-lineage":       func(m *Manifest) { m.Cumulative.SchemaLineage = "account22" },
		"old-platform-lineage":      func(m *Manifest) { m.Cumulative.SchemaLineage = "platform27" },
		"old-platform-schema":       func(m *Manifest) { m.SchemaVersion, m.MaximumPredecessor = 27, 27 },
		"old-runtime":               func(m *Manifest) { m.Cumulative.ApplicationProfile = "releases" },
		"old-predecessor":           func(m *Manifest) { m.MinimumPredecessor = 21 },
		"future-predecessor":        func(m *Manifest) { m.MaximumPredecessor = 33 },
		"partial-capabilities":      func(m *Manifest) { m.Capabilities = m.Capabilities[:29] },
		"missing-middle-capability": func(m *Manifest) { m.Capabilities[22] = "M22" },
		"wrong-native-client":       func(m *Manifest) { m.Cumulative.ComponentVersions["native-postgres"] = "18.5" },
		"wrong-inference":           func(m *Manifest) { m.Cumulative.ComponentVersions["inference"] = "latest" },
		"wrong-sql-bytes": func(m *Manifest) {
			for i := range m.Assets {
				if m.Assets[i].ID == MVPPostgreSQLCPULineageAssetID {
					m.Assets[i].SHA256 = strings.Repeat("f", 64)
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := mvpManifestDouble()
			change(&m)
			if check(m) == nil {
				t.Fatal("invalid final selection admitted")
			}
		})
	}
}

func TestMVPLineageDescriptorIsCompletePrivateCopy(t *testing.T) {
	var descriptor struct {
		Application   string `json:"application_profile"`
		Schema        int    `json:"schema_version"`
		Tables        int    `json:"table_count_including_ledger"`
		Prefix        string `json:"prefix_lineage"`
		PrefixVersion int    `json:"prefix_schema_version"`
		Entries       []struct {
			Version int    `json:"version"`
			Path    string `json:"source_path"`
			SHA     string `json:"sha256"`
		} `json:"entries"`
	}
	raw := MVPPostgreSQLCPULineage()
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.Application != MVPPostgreSQLCPUApplicationProfile || descriptor.Schema != 32 || descriptor.Tables != 111 || descriptor.Prefix != "account22" || descriptor.PrefixVersion != 22 || len(descriptor.Entries) != 32 {
		t.Fatal("lineage identity or count differs")
	}
	for i, entry := range descriptor.Entries {
		if entry.Version != i+1 || !SafePath(entry.Path) || !ValidSHA256(entry.SHA) {
			t.Fatal("lineage entry invalid", i)
		}
	}
	digest := MVPPostgreSQLCPULineageSHA256()
	raw[0] ^= 1
	if MVPPostgreSQLCPULineageSHA256() != digest || MVPPostgreSQLCPULineage()[0] == raw[0] {
		t.Fatal("caller mutated the canonical lineage")
	}
	required := RequiredAssets(MVPPostgreSQLCPUProfile)
	delete(required, "control-image")
	if RequiredAssets(MVPPostgreSQLCPUProfile)["control-image"] != "image_archive" {
		t.Fatal("caller mutated the canonical requirements")
	}
}
