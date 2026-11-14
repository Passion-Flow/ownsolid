package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// An import manifest binds the canonical metadata to an installed verifier.
// Only entries whose database and Agent paths can consume them belong here.
type importManifest struct {
	digest              string
	catalog             wire.ModelCatalog
	canonical           string
	minimumArchiveBytes int64
	extractArchive      func(context.Context, io.Reader, string) error
	verifyDirectory     func(context.Context, string) error
}

var qwenImportManifest = func() importManifest {
	catalog := wire.ApprovedModel()
	canonical, err := json.Marshal(catalog)
	if err != nil {
		panic("COMPILED_MODEL_CATALOG_INVALID")
	}
	digest := sha256.Sum256(canonical)
	return importManifest{
		digest:              hex.EncodeToString(digest[:]),
		catalog:             catalog,
		canonical:           string(canonical),
		minimumArchiveBytes: modelartifact.MinimumArchiveBytes(),
		extractArchive:      modelartifact.ExtractArchive,
		verifyDirectory:     modelartifact.VerifyDirectory,
	}
}()

// The digest in a durable import resolves to the same entry after a restart.
func resolveImportManifest(digest string) *importManifest {
	if digest == qwenImportManifest.digest {
		return &qwenImportManifest
	}
	return nil
}

func (m *importManifest) admitsArchiveSize(size int64) bool {
	return size >= m.minimumArchiveBytes && size <= MaximumBundleBytes
}

func (m *importManifest) file(name string) (wire.ModelFile, bool) {
	for _, file := range m.catalog.Files {
		if file.Path == name {
			return file, true
		}
	}
	return wire.ModelFile{}, false
}

func (m *importManifest) matchesStored(raw []byte) bool {
	var stored wire.ModelCatalog
	if lifecycle.DecodeStrict(raw, &stored) != nil {
		return false
	}
	canonical, err := json.Marshal(stored)
	return err == nil && string(canonical) == m.canonical
}
