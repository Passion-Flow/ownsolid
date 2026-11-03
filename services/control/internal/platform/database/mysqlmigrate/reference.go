package mysqlmigrate

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// CanonicalizeMeasuredEvolution prepares metadata,never executes or rewrites SQL.
// Preserve the original measured artifact separately. The same bounded metadata
// canonicalizer used by actual restore/readiness computes the accepted hashes.
func CanonicalizeMeasuredEvolution(raw []byte) ([]byte, error) {
	var document evolutionDocument
	if len(raw) == 0 || len(raw) > maximumCatalogBytes || strict.DecodeStrict(raw, &document) != nil || document.Format != evolutionFormat || !checksum.MatchString(document.BaseReferenceSHA256) || len(document.Steps) == 0 || len(document.Steps) > maximumCatalogSteps {
		return nil, invalidCatalog()
	}
	for i := range document.Steps {
		item := &document.Steps[i]
		if !identifier.MatchString(item.Object) || !assetFile.MatchString(item.File) || !checksum.MatchString(item.SourceSHA256) || item.Step < 1 || item.Step > maximumCatalogSteps || i > 0 && item.Step != document.Steps[i-1].Step+1 || !utf8.ValidString(item.Definition) || len(item.Definition) > maximumStatementBytes || item.DefinitionSHA256 != hash(item.Definition) || item.Kind != createTable && item.Kind != alterTable && item.Kind != createTrigger && item.Kind != installationSeed {
			return nil, invalidCatalog()
		}
		if item.Kind == alterTable && !checksum.MatchString(item.PreviousDefinitionSHA256) || item.Kind != alterTable && item.PreviousDefinitionSHA256 != "" {
			return nil, invalidCatalog()
		}
		if item.Kind == createTrigger {
			definition, canonical, e := canonicalTrigger(item.Definition)
			if e != nil || definition.Name != item.Object {
				return nil, invalidCatalog()
			}
			item.Definition = canonical
		} else if item.Kind == installationSeed {
			membership := item.Object == membershipAuthoritySeedObject && item.Definition == membershipAuthoritySeedDefinition
			licenseState := item.Object == licenseStateSeedObject && item.Definition == licenseStateSeedDefinition
			preferences := item.Object == userPreferencesSeedObject && item.Definition == userPreferencesSeedDefinition
			if !membership && !licenseState && !preferences {
				return nil, invalidCatalog()
			}
		} else {
			if !strings.HasPrefix(item.Definition, "CREATE TABLE `"+item.Object+"` (") {
				return nil, invalidCatalog()
			}
			item.Definition = canonicalDefinition(item.Definition)
		}
		item.DefinitionSHA256 = hash(item.Definition)
	}
	encoded, e := json.MarshalIndent(document, "", "  ")
	if e != nil {
		return nil, invalidCatalog()
	}
	return append(encoded, '\n'), nil
}
