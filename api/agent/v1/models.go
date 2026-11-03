package v1

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
)

//go:embed model-catalog.json
var approvedModelCatalog []byte

type ModelFile struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}
type ModelCatalog struct {
	SchemaVersion int         `json:"schema_version"`
	ModelKey      string      `json:"model_key"`
	Repository    string      `json:"repository"`
	Revision      string      `json:"revision"`
	License       string      `json:"license"`
	Format        string      `json:"format"`
	Architecture  string      `json:"architecture"`
	ModelType     string      `json:"model_type"`
	MaxContext    int         `json:"max_context"`
	Files         []ModelFile `json:"files"`
}

func ApprovedModel() ModelCatalog {
	var value ModelCatalog
	if json.Unmarshal(approvedModelCatalog, &value) != nil {
		panic("COMPILED_MODEL_CATALOG_INVALID")
	}
	return value
}
func ModelCatalogDigest() string {
	encoded, _ := json.Marshal(ApprovedModel())
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
func ModelFileForName(name string) (ModelFile, bool) {
	for _, file := range ApprovedModel().Files {
		if file.Path == name {
			return file, true
		}
	}
	return ModelFile{}, false
}
func ModelBytes() int64 {
	var total int64
	for _, file := range ApprovedModel().Files {
		total += file.SizeBytes
	}
	return total
}
