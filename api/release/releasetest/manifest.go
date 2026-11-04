// Package releasetest builds synthetic release manifests for tests. A manifest it fills
// in describes metadata only: no asset behind it was manufactured.
package releasetest

import (
	"fmt"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Select makes m a valid selection of the release profile, without its assets.
func Select(m *wire.Manifest) {
	m.Profile = wire.MVPPostgreSQLCPUProfile
	m.SchemaVersion, m.MinimumPredecessor, m.MaximumPredecessor = wire.MVPPostgreSQLCPUV1SchemaVersion, 22, wire.MVPPostgreSQLCPUV1SchemaVersion
	m.Prerequisites = []string{"Synthetic metadata only; no actual assets exist"}
	m.Capabilities = nil
	for i := 1; i <= 30; i++ {
		m.Capabilities = append(m.Capabilities, fmt.Sprintf("M%02d", i))
	}
	source := strings.Repeat("a", 40)
	m.Cumulative = &wire.CumulativeSelection{
		ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, SchemaLineage: wire.MVPPostgreSQLCPUApplicationProfile,
		SourceCommit: source, LocaleSourceSHA256: strings.Repeat("b", 64), InstalledLocales: []string{"en"}, LocaleTarget: 202,
		PendingLocales: 201, EmbeddedSeed: "zh-CN", LocaleInventory: "locales",
		ComponentVersions: map[string]string{
			"control": source, "gateway": "v1", "agent": "v1", "python": "v1", "postgres": "18.6", "pgvector": "0.8.7", "training": "v1",
			"preparation": "v1", "knowledge": "v1", "compose": "5.5.1", "operator": "v1", "inference": "0.31.0", "kubernetes": "v1",
			"native-postgres": "18.6", "private-cluster-probe": "v1",
		},
	}
}

// Pin gives the assets whose digest the profile fixes that digest.
func Pin(m *wire.Manifest) {
	for i := range m.Assets {
		switch m.Assets[i].ID {
		case "source-evidence":
			m.Assets[i].SHA256 = m.SourceSHA256
		case wire.MVPPostgreSQLCPULineageAssetID:
			m.Assets[i].SHA256 = wire.MVPPostgreSQLCPULineageSHA256()
		}
	}
}
