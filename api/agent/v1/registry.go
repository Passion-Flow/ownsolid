package v1

import (
	"encoding/json"
	"errors"
	"time"
)

// RegistryManifest carries immutable facts derived from actual release/OCI bytes.
// It carries no backend address, repository credential or remote fetch URL.
const RegistryMaximumProofBytes = 4 << 20

type RegistryManifest struct {
	ReleaseProof         json.RawMessage `json:"release_proof"`
	PinID                string          `json:"pin_id"`
	GrantID              string          `json:"grant_id"`
	DeploymentID         string          `json:"deployment_id"`
	Revision             int64           `json:"revision"`
	ImageManifestDigest  string          `json:"image_manifest_digest"`
	ImageConfigDigest    string          `json:"image_config_digest"`
	ArchiveSHA256        string          `json:"archive_sha256"`
	ArchiveBytes         int64           `json:"archive_bytes"`
	ExpandedBytes        int64           `json:"expanded_bytes"`
	SourceArchiveSHA256  string          `json:"source_archive_sha256"`
	SourceManifestSHA256 string          `json:"source_manifest_sha256"`
	OS                   string          `json:"os"`
	Architecture         string          `json:"architecture"`
	ExpiresAt            time.Time       `json:"expires_at"`
}
type RegistryRange struct {
	GrantID string `json:"grant_id"`
	Offset  int64  `json:"offset"`
	Length  int64  `json:"length"`
}

const RegistryMaximumRangeBytes int64 = 8 << 20

func (m RegistryManifest) Validate(c InferenceCommand) error {
	recipe := ApprovedInferenceRecipe()
	if len(m.ReleaseProof) == 0 || len(m.ReleaseProof) > RegistryMaximumProofBytes || !json.Valid(m.ReleaseProof) || c.Validate(c.Operation()) != nil || c.Action != "deploy" || !inferenceUUID.MatchString(m.PinID) || !inferenceUUID.MatchString(m.GrantID) || (c.RegistryPinID != "" && m.PinID != c.RegistryPinID) || m.DeploymentID != c.DeploymentID || m.Revision != c.Revision || m.ImageManifestDigest != recipe.ImageManifestDigest || m.ImageConfigDigest != recipe.ImageConfigDigest || m.OS != recipe.OS || m.Architecture != recipe.Architecture || !sha256Pattern.MatchString(m.ArchiveSHA256) || !sha256Pattern.MatchString(m.SourceArchiveSHA256) || !sha256Pattern.MatchString(m.SourceManifestSHA256) || m.ArchiveBytes < 1 || m.ExpandedBytes < 1 || !inferenceTime(m.ExpiresAt) {
		return errors.New("REGISTRY_MANIFEST_INVALID")
	}
	return nil
}
func (r RegistryRange) Validate() error {
	if !inferenceUUID.MatchString(r.GrantID) || r.Offset < 0 || r.Length < 1 || r.Length > RegistryMaximumRangeBytes || r.Offset > 9223372036854775807-r.Length {
		return errors.New("REGISTRY_RANGE_INVALID")
	}
	return nil
}

// RegistryImageIdentity is also used by explicit offline holds. It grants no task
// execution authority and must be checked against the existing approved recipe.
type RegistryImageIdentity struct {
	ReleaseProof         json.RawMessage `json:"release_proof"`
	PinID                string          `json:"pin_id"`
	ImageManifestDigest  string          `json:"image_manifest_digest"`
	ImageConfigDigest    string          `json:"image_config_digest"`
	ArchiveSHA256        string          `json:"archive_sha256"`
	ArchiveBytes         int64           `json:"archive_bytes"`
	ExpandedBytes        int64           `json:"expanded_bytes"`
	SourceArchiveSHA256  string          `json:"source_archive_sha256"`
	SourceManifestSHA256 string          `json:"source_manifest_sha256"`
	OS                   string          `json:"os"`
	Architecture         string          `json:"architecture"`
}
type RegistryOfflineManifest struct {
	ReferenceID string                `json:"reference_id"`
	Image       RegistryImageIdentity `json:"image"`
}

func (m RegistryManifest) ImageIdentity() RegistryImageIdentity {
	return RegistryImageIdentity{m.ReleaseProof, m.PinID, m.ImageManifestDigest, m.ImageConfigDigest, m.ArchiveSHA256, m.ArchiveBytes, m.ExpandedBytes, m.SourceArchiveSHA256, m.SourceManifestSHA256, m.OS, m.Architecture}
}
func (m RegistryImageIdentity) Validate() error {
	recipe := ApprovedInferenceRecipe()
	if len(m.ReleaseProof) == 0 || len(m.ReleaseProof) > RegistryMaximumProofBytes || !json.Valid(m.ReleaseProof) || !inferenceUUID.MatchString(m.PinID) || m.ImageManifestDigest != recipe.ImageManifestDigest || m.ImageConfigDigest != recipe.ImageConfigDigest || m.OS != recipe.OS || m.Architecture != recipe.Architecture || !sha256Pattern.MatchString(m.ArchiveSHA256) || !sha256Pattern.MatchString(m.SourceArchiveSHA256) || !sha256Pattern.MatchString(m.SourceManifestSHA256) || m.ArchiveBytes < 1 || m.ExpandedBytes < 1 {
		return errors.New("REGISTRY_MANIFEST_INVALID")
	}
	return nil
}
func (m RegistryOfflineManifest) Validate() error {
	if !inferenceUUID.MatchString(m.ReferenceID) {
		return errors.New("REGISTRY_MANIFEST_INVALID")
	}
	return m.Image.Validate()
}
