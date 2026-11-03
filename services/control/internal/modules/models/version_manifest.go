package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func decodeVersionManifest(version *Version, raw []byte, tenant string) error {
	switch version.Kind {
	case "approved_base":
		var c agentwire.ModelCatalog
		if wire.DecodeStrict(raw, &c) != nil {
			return fail("MODEL_FILE_INVALID")
		}
		canonical, e := json.Marshal(c)
		if e != nil {
			return fail("MODEL_FILE_INVALID")
		}
		sum := sha256.Sum256(canonical)
		if version.CatalogDigest != agentwire.ModelCatalogDigest() || hex.EncodeToString(sum[:]) != version.CatalogDigest {
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
