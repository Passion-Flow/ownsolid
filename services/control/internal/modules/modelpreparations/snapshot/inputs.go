// Package snapshot owns the retained version 1 preparation input encoding.
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

var invalid = errors.New("MODEL_PREPARATION_DATA_INVALID")

// Adapter preserves the exact version 1 training artifact descriptor encoding.
type Adapter struct {
	SchemaVersion  int                `json:"schema_version"`
	Scope          trainingwire.Scope `json:"scope"`
	UploadID       string             `json:"upload_id"`
	AttemptID      string             `json:"attempt_id"`
	Kind           string             `json:"kind"`
	GlobalStep     int64              `json:"global_step"`
	ManifestSHA256 string             `json:"manifest_sha256"`
	Files          []datawire.Asset   `json:"files"`
}

// LabelCounts is the frozen holdout label distribution.
type LabelCounts struct {
	Account   int64 `json:"account"`
	Billing   int64 `json:"billing"`
	Technical int64 `json:"technical"`
}

// Increment counts one supported label.
func (c *LabelCounts) Increment(label string) {
	switch label {
	case "account":
		c.Account++
	case "billing":
		c.Billing++
	case "technical":
		c.Technical++
	}
}

// ValidForRows checks coverage and the total against the holdout row count.
func (c LabelCounts) ValidForRows(rows int64) bool {
	return c.Account > 0 && c.Billing > 0 && c.Technical > 0 && c.Account+c.Billing+c.Technical == rows
}

// Holdout is the version 1 snapshot of the preparation evaluation input.
type Holdout struct {
	ID                    string      `json:"id"`
	WorkspaceID           string      `json:"workspace_id"`
	DatasetID             string      `json:"dataset_id"`
	DatasetVersionID      string      `json:"dataset_version_id"`
	Name                  string      `json:"name"`
	ManifestSHA256        string      `json:"manifest_sha256"`
	EffectiveSourceSHA256 string      `json:"effective_source_sha256"`
	RowMappingSHA256      string      `json:"row_mapping_sha256"`
	SourceBytes           int64       `json:"source_bytes"`
	RowCount              int64       `json:"row_count"`
	Labels                LabelCounts `json:"label_counts"`
	RecipeID              string      `json:"recipe_id"`
	RecipeSHA256          string      `json:"recipe_sha256"`
	InstructionSHA256     string      `json:"instruction_sha256"`
	FingerprintAlgorithm  string      `json:"fingerprint_algorithm"`
	CreatedBy             string      `json:"created_by"`
	Audience              string      `json:"audience"`
	Reason                string      `json:"reason"`
	CreatedAt             time.Time   `json:"created_at"`
}

// Inputs preserves the field order used by existing preparation snapshot digests.
type Inputs struct {
	SchemaVersion          int                  `json:"schema_version"`
	TrainingCommand        trainingwire.Command `json:"training_command"`
	Adapter                Adapter              `json:"adapter"`
	TrainingIdentitySHA256 string               `json:"training_identity_sha256"`
	Heldout                Holdout              `json:"heldout"`
	DatasetAttemptID       string               `json:"dataset_attempt_id"`
}

func digest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Digest returns the canonical version 1 preparation input digest.
func (i Inputs) Digest() string { return digest(i) }

// Decode verifies the canonical retained input digest and validates its contents.
func Decode(raw []byte, expected string) (Inputs, error) {
	var i Inputs
	if wire.DecodeStrict(raw, &i) != nil || i.Validate() != nil || i.Digest() != expected {
		return Inputs{}, invalid
	}
	return i, nil
}

func (d Adapter) validFor(c trainingwire.Command) bool {
	if d.SchemaVersion != 1 || d.Scope != c.Scope || d.AttemptID != c.AttemptID ||
		!datawire.ValidID(d.UploadID) || !datawire.ValidDigest(d.ManifestSHA256) ||
		d.Kind != "artifact" || d.GlobalStep < 1 || d.GlobalStep > trainingwire.MaximumStepsCeiling ||
		len(d.Files) != 3 || int64(len(d.Files)) > c.Recipe.Resources.MaximumOutputFiles {
		return false
	}
	seen := make(map[string]bool, len(d.Files))
	var total int64
	for _, file := range d.Files {
		if !trainingwire.AdapterAssetAllowed(file.Path) || seen[file.Path] || file.SizeBytes < 1 ||
			file.SizeBytes > c.Recipe.Resources.MaximumOutputBytes || !datawire.ValidDigest(file.SHA256) {
			return false
		}
		seen[file.Path] = true
		total += file.SizeBytes
	}
	return total <= c.Recipe.Resources.MaximumOutputBytes
}

// Validate checks the two retained source descriptors and their training identity.
func (i Inputs) Validate() error {
	c, d, h := i.TrainingCommand, i.Adapter, i.Heldout
	if i.SchemaVersion != 1 || c.Validate() != nil || c.ModelCatalogSHA256 != wire.ApprovedModelCatalogSHA256 || !d.validFor(c) {
		return invalid
	}
	identity := digest(struct {
		Command    trainingwire.Command
		Descriptor Adapter
	}{c, d})
	if identity != i.TrainingIdentitySHA256 || !datawire.ValidID(i.DatasetAttemptID) ||
		!datawire.ValidID(h.ID) || h.WorkspaceID != c.Scope.WorkspaceID ||
		!datawire.ValidID(h.DatasetID) || !datawire.ValidID(h.DatasetVersionID) ||
		!datawire.ValidDigest(h.ManifestSHA256) || !datawire.ValidDigest(h.EffectiveSourceSHA256) ||
		!datawire.ValidDigest(h.RowMappingSHA256) || h.SourceBytes < 1 || h.SourceBytes > datawire.MaximumBytesCeiling ||
		h.RowCount < wire.MinimumRows || h.RowCount > wire.MaximumRows || !h.Labels.ValidForRows(h.RowCount) ||
		h.RecipeID != wire.RecipeID || h.RecipeSHA256 != wire.RecipeDigest() ||
		h.InstructionSHA256 != wire.InstructionDigest() || h.FingerprintAlgorithm != wire.PromptFingerprintAlgorithm {
		return invalid
	}
	return nil
}

// Binding holds the immutable preparation row references to its inputs.
type Binding struct {
	TrainingScope      trainingwire.Scope
	TrainingAttemptID  string
	ModelID            string
	TrainingArtifactID string
	HoldoutID          string
}

// Matches checks the retained input references against the scoped preparation row.
func (i Inputs) Matches(b Binding) bool {
	c := i.TrainingCommand
	return c.Scope == b.TrainingScope && c.AttemptID == b.TrainingAttemptID &&
		c.ModelID == b.ModelID && i.Adapter.UploadID == b.TrainingArtifactID && i.Heldout.ID == b.HoldoutID
}

// Inventory returns the adapter and holdout files expected by the preparation worker.
func (i Inputs) Inventory() wire.InputInventory {
	return wire.InputInventory{
		AdapterFiles: append([]datawire.Asset{}, i.Adapter.Files...),
		Heldout:      datawire.Asset{Path: "heldout.jsonl", SizeBytes: i.Heldout.SourceBytes, SHA256: i.Heldout.EffectiveSourceSHA256},
	}
}

// DatasetVersionLineage identifies a frozen dataset version and its manifest.
type DatasetVersionLineage struct {
	DatasetID        string
	DatasetVersionID string
	ManifestSHA256   string
}

// DatasetLineage keeps the original training source distinct from the preparation holdout.
type DatasetLineage struct {
	Training DatasetVersionLineage
	Holdout  DatasetVersionLineage
}

// Lineage projects the original version references from validated inputs.
func (i Inputs) Lineage() DatasetLineage {
	c, h := i.TrainingCommand, i.Heldout
	return DatasetLineage{
		Training: DatasetVersionLineage{DatasetID: c.DatasetID, DatasetVersionID: c.DatasetVersionID, ManifestSHA256: c.DatasetManifestSHA256},
		Holdout:  DatasetVersionLineage{DatasetID: h.DatasetID, DatasetVersionID: h.DatasetVersionID, ManifestSHA256: h.ManifestSHA256},
	}
}
