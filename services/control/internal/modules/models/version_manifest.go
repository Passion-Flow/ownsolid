package models

import (
	"encoding/json"

	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func decodeVersionManifest(version *Version, raw []byte, tenant string) error {
	switch version.Kind {
	case "approved_base":
		manifest := resolveImportManifest(version.CatalogDigest)
		if manifest == nil || !manifest.matchesStored(raw) {
			return fail("MODEL_FILE_INVALID")
		}
	case "merged_lora":
		c, e := wire.DecodeDerivedCatalog(raw)
		if e != nil {
			return fail("MODEL_DERIVED_CATALOG_INVALID")
		}
		digest, e := c.Digest()
		if e != nil || digest != version.CatalogDigest || c.ModelID != version.ID || c.Scope.TenantID != tenant || c.Scope.WorkspaceID != version.WorkspaceID {
			return fail("MODEL_DERIVED_CATALOG_INVALID")
		}
	default:
		return fail("MODEL_FILE_INVALID")
	}
	version.Manifest = append(json.RawMessage(nil), raw...)
	return nil
}
