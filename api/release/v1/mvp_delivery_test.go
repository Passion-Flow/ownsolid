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
	m := cumulativeManifestDouble()
	m.Profile = MVPPostgreSQLCPUProfile
	m.SchemaVersion, m.MaximumPredecessor = MVPPostgreSQLCPUV1SchemaVersion, MVPPostgreSQLCPUV1SchemaVersion
	m.Cumulative.ApplicationProfile, m.Cumulative.SchemaLineage = MVPPostgreSQLCPUApplicationProfile, MVPPostgreSQLCPUApplicationProfile
	for _, owner := range []string{"inference", "kubernetes", "native-postgres", "private-cluster-probe"} {
		m.Cumulative.ComponentVersions[owner] = "v1"
	}
	m.Cumulative.ComponentVersions["inference"], m.Cumulative.ComponentVersions["native-postgres"] = "0.31.0", "18.6"
	m.Capabilities = nil
	for i := 1; i <= 30; i++ {
		m.Capabilities = append(m.Capabilities, fmt.Sprintf("M%02d", i))
	}
	for id, kind := range RequiredAssets(m.Profile) {
		found := false
		for _, asset := range m.Assets {
			found = found || asset.ID == id
		}
		if found {
			continue
		}
		a := Asset{ID: id, Kind: kind, Path: id, Size: 1, SHA256: strings.Repeat("c", 64), Licenses: []string{"platform-license"}}
		if kind == "license" {
			a.Licenses = nil
		}
		if kind == "image_archive" {
			a.ImageID, a.ImageConfigDigest, a.Architecture = "sha256:"+strings.Repeat("d", 64), "sha256:"+strings.Repeat("e", 64), "amd64"
		}
		if id == MVPPostgreSQLCPULineageAssetID {
			a.SHA256 = MVPPostgreSQLCPULineageSHA256()
		}
		m.Assets = append(m.Assets, a)
	}
	sort.Slice(m.Assets, func(i, j int) bool { return m.Assets[i].ID < m.Assets[j].ID })
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
		"old-cache-runtime":         func(m *Manifest) { m.Cumulative.ApplicationProfile = CumulativeCacheApplicationProfile },
		"old-predecessor":           func(m *Manifest) { m.MinimumPredecessor = 21 },
		"future-predecessor":        func(m *Manifest) { m.MaximumPredecessor = 30 },
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
	for _, legacy := range []Manifest{cumulativeManifestDouble(), cacheDeliveryDouble()} {
		if err := check(legacy); err != nil {
			t.Fatal("legacy profile changed", err)
		}
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
	if descriptor.Application != MVPPostgreSQLCPUApplicationProfile || descriptor.Schema != 29 || descriptor.Tables != 108 || descriptor.Prefix != "account22" || descriptor.PrefixVersion != 22 || len(descriptor.Entries) != 29 {
		t.Fatal("lineage identity or count differs")
	}
	for i, entry := range descriptor.Entries {
		if entry.Version != i+1 || !SafePath(entry.Path) || !ValidSHA256(entry.SHA) || i >= 22 && !strings.Contains(entry.Path, "/mvp-postgresql-cpu-v1/") {
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
