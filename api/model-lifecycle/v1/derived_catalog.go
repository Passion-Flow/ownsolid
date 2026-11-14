package v1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const MaximumDerivedCatalogBytes = 16384

// DerivedCatalog describes an actual local derivative and preserves explicit base provenance.
// It does not claim an unmodified remote revision, human approval, or serving readiness.
type DerivedCatalog struct {
	SchemaVersion        int             `json:"schema_version"`
	Kind                 string          `json:"kind"`
	Scope                Scope           `json:"scope"`
	ModelID              string          `json:"model_id"`
	BaseModelID          string          `json:"base_model_id"`
	BaseCatalogSHA256    string          `json:"base_catalog_sha256"`
	AttemptID            string          `json:"attempt_id"`
	ArtifactID           string          `json:"artifact_id"`
	ReportSHA256         string          `json:"report_sha256"`
	RecipeSHA256         string          `json:"recipe_sha256"`
	BaseRepository       string          `json:"base_repository"`
	BaseRevision         string          `json:"base_revision"`
	License              string          `json:"license"`
	Format               string          `json:"format"`
	Architecture         string          `json:"architecture"`
	ModelType            string          `json:"model_type"`
	Precision            string          `json:"precision"`
	MaximumContextTokens int64           `json:"maximum_context_tokens"`
	Files                []dataset.Asset `json:"files"`
}

func (c DerivedCatalog) Validate() error {
	bad := errors.New("MODEL_DERIVED_CATALOG_INVALID")
	if c.SchemaVersion != 1 || c.Kind != "merged_lora" || c.Scope.Validate() != nil || c.ModelID != c.ArtifactID || c.BaseCatalogSHA256 != ApprovedModelCatalogSHA256 || c.RecipeSHA256 != RecipeDigest() || c.BaseRepository != ApprovedBaseRepository || c.BaseRevision != ApprovedBaseRevision || c.License != ApprovedBaseLicense || c.Format != "safetensors" || c.Architecture != ApprovedBaseArchitecture || c.ModelType != ApprovedBaseModelType || c.Precision != "BF16" || c.MaximumContextTokens != MaximumContextTokens || !dataset.ValidDigest(c.ReportSHA256) {
		return bad
	}
	for _, id := range []string{c.ModelID, c.BaseModelID, c.AttemptID, c.ArtifactID} {
		if !dataset.ValidID(id) {
			return bad
		}
	}
	if c.BaseModelID == c.ModelID {
		return bad
	}
	base := ApprovedBaseFiles()
	if len(c.Files) != len(base) {
		return bad
	}
	var total int64
	for i, asset := range c.Files {
		original := base[i]
		if asset.Path != original.Path || asset.SizeBytes < 1 || asset.SizeBytes > MaximumModelBytes || !dataset.ValidDigest(asset.SHA256) {
			return bad
		}
		total += asset.SizeBytes
		if asset.Path == "model.safetensors" {
			if asset.SHA256 == original.SHA256 {
				return bad
			}
		} else if asset != original {
			return bad
		}
	}
	if total > MaximumModelBytes {
		return bad
	}
	return nil
}

// CanonicalBytes sorts object keys recursively and preserves bounded integer values.
func (c DerivedCatalog) CanonicalBytes() ([]byte, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(c)
	if e != nil {
		return nil, e
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object any
	if decoder.Decode(&object) != nil {
		return nil, errors.New("MODEL_DERIVED_CATALOG_INVALID")
	}
	raw, e = json.Marshal(object)
	if e != nil || len(raw) > MaximumDerivedCatalogBytes {
		return nil, errors.New("MODEL_DERIVED_CATALOG_INVALID")
	}
	return raw, nil
}
func (c DerivedCatalog) Digest() (string, error) {
	raw, e := c.CanonicalBytes()
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func DecodeDerivedCatalog(raw []byte) (DerivedCatalog, error) {
	var c DerivedCatalog
	if len(raw) > MaximumDerivedCatalogBytes || DecodeStrict(raw, &c) != nil {
		return c, errors.New("MODEL_DERIVED_CATALOG_INVALID")
	}
	return c, c.Validate()
}
